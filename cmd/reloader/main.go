/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Command reloader is the in-pod sidecar that applies in-container state for a
// HermesAgent against the shared PVC, as the hermes user: Homebrew prefix init,
// brew install, custom-skill materialization, and status write-back. See spec §8.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

var (
	scheme = runtime.NewScheme()
	log    = ctrl.Log.WithName("reloader")
)

func init() {
	utilruntime.Must(hermesv1alpha1.AddToScheme(scheme))
}

type reloader struct {
	agentName      string
	agentNamespace string
	hermesHome     string
	brewPrefix     string
	brewDist       string
	skillSrcDir    string
	brewPackages   []string
	aptPackages    []string
	customSkills   []string
	k8s            client.Client
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	r := &reloader{
		agentName:      os.Getenv("RELOADER_AGENT_NAME"),
		agentNamespace: os.Getenv("RELOADER_AGENT_NAMESPACE"),
		hermesHome:     envOr("HERMES_HOME", "/opt/data"),
		brewPrefix:     envOr("RELOADER_HOMEBREW_PREFIX", "/home/linuxbrew/.linuxbrew"),
		brewDist:       envOr("RELOADER_HOMEBREW_DIST", "/opt/homebrew-dist"),
		skillSrcDir:    envOr("RELOADER_SKILL_SRC_DIR", "/etc/hermes-skills"),
		brewPackages:   splitNonEmpty(os.Getenv("RELOADER_BREW_PACKAGES"), " "),
		aptPackages:    splitNonEmpty(os.Getenv("RELOADER_APT_PACKAGES"), " "),
		customSkills:   splitNonEmpty(os.Getenv("RELOADER_CUSTOM_SKILLS"), ","),
	}

	// Best-effort API client for status write-back; the reloader still applies
	// in-container state even if the cluster API is unreachable.
	if cfg, err := ctrl.GetConfig(); err == nil {
		if c, err := client.New(cfg, client.Options{Scheme: scheme}); err == nil {
			r.k8s = c
		} else {
			log.Error(err, "no API client; status write-back disabled")
		}
	} else {
		log.Error(err, "no kubeconfig; status write-back disabled")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Reconcile immediately, then on a ticker to pick up ConfigMap changes.
	r.reconcile(ctx)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("reloader shutting down")
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

func (r *reloader) reconcile(ctx context.Context) {
	if err := r.initBrew(ctx); err != nil {
		log.Error(err, "brew prefix init failed")
	}
	installed := r.installBrew(ctx)
	if err := r.materializeSkills(); err != nil {
		log.Error(err, "skill materialization failed")
	}
	synced := r.syncSkills(ctx)

	r.writeStatus(ctx, installed, synced)
}

// initBrew populates the Homebrew prefix from the baked distribution on first
// boot (idempotent). brew bottles work because the prefix is the default path
// on the shared PVC (spec §5.1).
func (r *reloader) initBrew(ctx context.Context) error {
	brewBin := filepath.Join(r.brewPrefix, "bin", "brew")
	if _, err := os.Stat(brewBin); err == nil {
		return nil // already initialized
	}
	if _, err := os.Stat(r.brewDist); err != nil {
		// No baked distribution (e.g. upstream image without brew). Nothing to do.
		log.Info("homebrew distribution absent; skipping brew init", "dist", r.brewDist)
		return nil
	}
	if err := os.MkdirAll(r.brewPrefix, 0o755); err != nil {
		return err
	}
	log.Info("initializing Homebrew prefix from baked distribution", "prefix", r.brewPrefix)
	// Copy the distribution into the (empty) prefix on the PVC.
	cmd := exec.CommandContext(ctx, "sh", "-c",
		fmt.Sprintf("cp -a %s/. %s/", shellQuote(r.brewDist), shellQuote(r.brewPrefix)))
	return runLogged(cmd, "brew-init")
}

// installBrew ensures each declared formula is installed; returns those present.
func (r *reloader) installBrew(ctx context.Context) []string {
	var present []string
	for _, pkg := range r.brewPackages {
		if r.brewHas(ctx, pkg) {
			present = append(present, pkg)
			continue
		}
		log.Info("installing brew formula", "formula", pkg)
		cmd := exec.CommandContext(ctx, "brew", "install", pkg)
		cmd.Env = r.brewEnv()
		if err := runLogged(cmd, "brew-install"); err != nil {
			log.Error(err, "brew install failed", "formula", pkg)
			continue
		}
		present = append(present, pkg)
	}
	return present
}

func (r *reloader) brewHas(ctx context.Context, pkg string) bool {
	cmd := exec.CommandContext(ctx, "brew", "list", "--formula", pkg)
	cmd.Env = r.brewEnv()
	return cmd.Run() == nil
}

func (r *reloader) brewEnv() []string {
	env := os.Environ()
	env = append(env,
		"HOMEBREW_PREFIX="+r.brewPrefix,
		"HOMEBREW_CELLAR="+filepath.Join(r.brewPrefix, "Cellar"),
		"HOMEBREW_REPOSITORY="+filepath.Join(r.brewPrefix, "Homebrew"),
		"HOMEBREW_NO_ANALYTICS=1",
		"PATH="+filepath.Join(r.brewPrefix, "bin")+":"+filepath.Join(r.brewPrefix, "sbin")+":"+os.Getenv("PATH"),
	)
	return env
}

// materializeSkills copies each custom skill's mounted source into the PVC
// skills dir (operator-owned ⇒ refreshed each pass).
func (r *reloader) materializeSkills() error {
	skillsDir := filepath.Join(r.hermesHome, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return err
	}
	for _, name := range r.customSkills {
		src := filepath.Join(r.skillSrcDir, name)
		if _, err := os.Stat(src); err != nil {
			log.Info("custom skill source missing; skipping", "skill", name, "src", src)
			continue
		}
		dst := filepath.Join(skillsDir, name)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		cmd := exec.Command("sh", "-c",
			fmt.Sprintf("cp -aL %s/. %s/", shellQuote(src), shellQuote(dst)))
		if err := runLogged(cmd, "skill-copy"); err != nil {
			return err
		}
	}
	return nil
}

// syncSkills runs the upstream skills_sync.py to seed bundled skills and returns
// the count of skill directories present.
func (r *reloader) syncSkills(ctx context.Context) int {
	syncScript := "/opt/hermes/tools/skills_sync.py"
	if _, err := os.Stat(syncScript); err == nil {
		cmd := exec.CommandContext(ctx, "python3", syncScript)
		cmd.Env = append(os.Environ(), "HERMES_HOME="+r.hermesHome)
		if err := runLogged(cmd, "skills-sync"); err != nil {
			log.Error(err, "skills_sync.py failed")
		}
	}
	entries, err := os.ReadDir(filepath.Join(r.hermesHome, "skills"))
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			count++
		}
	}
	return count
}

func (r *reloader) writeStatus(ctx context.Context, brewInstalled []string, syncedSkills int) {
	if r.k8s == nil || r.agentName == "" {
		return
	}
	agent := &hermesv1alpha1.HermesAgent{}
	if err := r.k8s.Get(ctx, types.NamespacedName{Namespace: r.agentNamespace, Name: r.agentName}, agent); err != nil {
		log.Error(err, "fetch agent for status write-back")
		return
	}
	patch := client.MergeFrom(agent.DeepCopy())
	agent.Status.Skills.Synced = int32(syncedSkills)
	agent.Status.Skills.CustomActive = r.customSkills
	agent.Status.Packages.BrewInstalled = brewInstalled
	agent.Status.Packages.AptApplied = r.aptPackages
	if err := r.k8s.Status().Patch(ctx, agent, patch); err != nil {
		log.Error(err, "status write-back failed")
	}
}

// --- helpers ---

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitNonEmpty(s, sep string) []string {
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func runLogged(cmd *exec.Cmd, label string) error {
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Info(label+" output", "out", string(out))
	}
	return err
}
