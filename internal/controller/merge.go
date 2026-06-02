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

package controller

import (
	"encoding/json"
	"maps"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

// mergePreset deep-merges a HermesConfigPreset's defaults UNDER the agent spec
// (the CR wins). The preset's config sections (model/agent/compression/memory/
// skills/extraConfig) fill any unset fields of spec.defaultProfile; preset.packages
// fills pod-level spec.packages. Named profiles inherit the preset because they
// resolve over defaultProfile (see resolveProfileConfig). See spec §3.2.
func mergePreset(spec *hermesv1alpha1.HermesAgentSpec, preset *hermesv1alpha1.HermesConfigPresetSpec) error {
	presetMap, err := toMap(preset)
	if err != nil {
		return err
	}
	// Pod-level packages merge under spec.packages, not defaultProfile.
	var presetPackages any
	if p, ok := presetMap["packages"]; ok {
		presetPackages = p
		delete(presetMap, "packages")
	}

	// Config sections merge under defaultProfile; the profile (overlay) wins.
	dpMap, err := toMap(spec.DefaultProfile)
	if err != nil {
		return err
	}
	mergedDP := deepMergeAny(presetMap, dpMap)
	if err := fromMap(mergedDP, &spec.DefaultProfile); err != nil {
		return err
	}

	if pm, ok := presetPackages.(map[string]any); ok {
		pkgMap, err := toMap(spec.Packages)
		if err != nil {
			return err
		}
		merged := deepMergeAny(pm, pkgMap)
		if err := fromMap(merged, &spec.Packages); err != nil {
			return err
		}
	}
	return nil
}

// resolveProfileConfig overlays a named profile's config over the default profile
// (profile wins; sections the profile leaves unset inherit defaultProfile), using
// the same JSON deep-merge as the preset. Returns the fully-resolved config that
// feeds the renderers.
func resolveProfileConfig(base, profile hermesv1alpha1.ProfileConfig) (hermesv1alpha1.ProfileConfig, error) {
	var out hermesv1alpha1.ProfileConfig
	baseMap, err := toMap(base)
	if err != nil {
		return out, err
	}
	profMap, err := toMap(profile)
	if err != nil {
		return out, err
	}
	merged := deepMergeAny(baseMap, profMap)
	if err := fromMap(merged, &out); err != nil {
		return out, err
	}
	return out, nil
}

func toMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func fromMap(m map[string]any, out any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// deepMergeAny returns base with overlay applied: overlay keys win, nested maps
// merge recursively. Slices are replaced wholesale (overlay wins).
func deepMergeAny(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	maps.Copy(out, base)
	for k, ov := range overlay {
		if bv, ok := out[k]; ok {
			bm, bok := bv.(map[string]any)
			om, ook := ov.(map[string]any)
			if bok && ook {
				out[k] = deepMergeAny(bm, om)
				continue
			}
		}
		out[k] = ov
	}
	return out
}
