package controller

import (
	"slices"
	"strings"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

func bindingName(key baselinev1alpha1.ProfileKey) string { return "baseline-" + string(key) }

func ownedSuites(cb *baselinev1alpha1.ClusterBaseline) map[string]bool {
	s := make(map[string]bool, len(cb.Spec.Profiles)+len(cb.Spec.TailoredProfiles))
	for _, key := range cb.Spec.Profiles {
		s[bindingName(key)] = true
	}
	for _, name := range cb.Spec.TailoredProfiles {
		s[tailoredBindingName(name)] = true
	}
	return s
}

// matchesAnyProfile: name equals a profile/tailored base or a role-suffixed
// variant (ocp4-cis-node -> ocp4-cis-node-master, custom -> custom-worker).
// Only known ScanSetting roles are accepted after the "<base>-" boundary so a
// short or ambiguous base (e.g. tailored "ocp4") cannot prefix-match foreign
// PVCs like "ocp4-cis". name is untrusted cluster data.
func matchesAnyProfile(name string, profiles map[string]bool) bool {
	for p := range profiles {
		if name == p {
			return true
		}
		if rest, ok := strings.CutPrefix(name, p+"-"); ok && scanRoleSuffix(rest) {
			return true
		}
	}
	return false
}

// scanRoleSuffix is true for the role path we may append after a scan/profile
// base name. Matches ScanSetting roles we set (worker/master) and common extras,
// including the "node-<role>" form used by CO node profiles.
func scanRoleSuffix(rest string) bool {
	switch rest {
	case "worker", "master", "control-plane", "infra", "node":
		return true
	}
	if role, ok := strings.CutPrefix(rest, "node-"); ok {
		switch role {
		case "worker", "master", "control-plane", "infra":
			return true
		}
	}
	return false
}

// profileKeyFromSuite inverts bindingName ("baseline-<key>").
// Requires a non-empty key after the prefix so "baseline-" alone is rejected.
// Tailored suites ("baseline-tp-<name>") are excluded so they are only handled
// by tailoredNameFromSuite.
func profileKeyFromSuite(suite string) (baselinev1alpha1.ProfileKey, bool) {
	p, ok := strings.CutPrefix(suite, "baseline-")
	if !ok || p == "" || strings.HasPrefix(p, "tp-") {
		return "", false
	}
	return baselinev1alpha1.ProfileKey(p), true
}

// splitCSV splits a comma-separated list, trimming and dropping empty items.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// notIn returns the sorted members of a that are absent from set b.
func notIn(a []string, b map[string]bool) []string {
	var out []string
	for _, x := range a {
		if !b[x] {
			out = append(out, x)
		}
	}
	slices.Sort(out)
	return out
}

// withoutPlugin returns plugins without name (copy; does not mutate input).
func withoutPlugin(plugins []string, name string) []string {
	return slices.DeleteFunc(slices.Clone(plugins), func(p string) bool { return p == name })
}
