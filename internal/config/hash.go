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

package config

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

// HashInputs are the materials that determine whether the pod must roll. A
// change to any of these changes the configHash and triggers a Recreate.
type HashInputs struct {
	// ConfigYAML is the rendered config.yaml bytes.
	ConfigYAML []byte
	// Soul is the rendered SOUL.md content.
	Soul string
	// SkillPayloads maps custom skill name -> its rendered SKILL.md content.
	SkillPayloads map[string]string
	// BrewPackages as declared.
	BrewPackages []string
	// SecretVersions maps referenced Secret name -> resourceVersion. The
	// operator never decodes secret values; a rotation rolls the pod via the
	// changed resourceVersion alone.
	SecretVersions map[string]string
}

// ConfigHash computes a stable sha256 over the inputs. Maps are emitted in
// sorted key order so the hash is deterministic across reconciles.
func ConfigHash(in HashInputs) string {
	h := sha256.New()

	writeBlock(h, "config.yaml", in.ConfigYAML)
	writeBlock(h, "SOUL.md", []byte(in.Soul))

	for _, name := range sortedKeys(in.SkillPayloads) {
		writeBlock(h, "skill:"+name, []byte(in.SkillPayloads[name]))
	}

	writeBlock(h, "brew", []byte(joinSorted(in.BrewPackages)))

	for _, name := range sortedKeys(in.SecretVersions) {
		writeBlock(h, "secret:"+name, []byte(in.SecretVersions[name]))
	}

	return "sha256:" + fmt.Sprintf("%x", h.Sum(nil))
}

func writeBlock(h interface{ Write([]byte) (int, error) }, label string, data []byte) {
	// Length-prefix each block so concatenation is unambiguous.
	_, _ = fmt.Fprintf(h, "%s:%d:", label, len(data))
	_, _ = h.Write(data)
	_, _ = h.Write([]byte("\n"))
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func joinSorted(in []string) string {
	cp := append([]string(nil), in...)
	sort.Strings(cp)
	var b strings.Builder
	for _, s := range cp {
		b.WriteString(s)
		b.WriteByte(0)
	}
	return b.String()
}
