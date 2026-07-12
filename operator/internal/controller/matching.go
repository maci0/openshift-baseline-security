package controller

import (
	"slices"
	"strings"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

func bindingName(key baselinev1alpha1.ProfileKey) string { return "baseline-" + string(key) }

// tailoredBindingName names the binding for a TailoredProfile. The "tp-" infix
// keeps its suite label distinct from a built-in profile's "baseline-<key>".
func tailoredBindingName(name string) string { return "baseline-tp-" + name }

// tailoredNameFromSuite returns the TailoredProfile name for a tailored suite
// label ("baseline-tp-<name>"), or ("", false) otherwise.
func tailoredNameFromSuite(suite string) (string, bool) {
	n, ok := strings.CutPrefix(suite, "baseline-tp-")
	if !ok || n == "" {
		return "", false
	}
	return n, true
}

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
// Comma walk (no strings.Split) so batch annotation lists up to 256 names do not
// allocate an intermediate slice of every segment including empties.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for start <= len(s) {
		comma := strings.IndexByte(s[start:], ',')
		end := len(s)
		if comma >= 0 {
			end = start + comma
		}
		if p := strings.TrimSpace(s[start:end]); p != "" {
			out = append(out, p)
		}
		if comma < 0 {
			break
		}
		start = end + 1
	}
	return out
}

// notIn returns the sorted unique members of a that are absent from set b.
// Set semantics match sortedDiff (duplicate names in a count once). When a is
// already sorted (production failure lists always are), filtering preserves
// order and the Sort is skipped (IsSorted is O(n), Sort is O(n log n)).
func notIn(a []string, b map[string]bool) []string {
	// Pre-size for worst case (all of a missing from b) so failure-diff on
	// multi-thousand FAIL sets does not thrash append.
	out := make([]string, 0, len(a))
	seen := make(map[string]bool, len(a))
	for _, x := range a {
		if b[x] || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	if !slices.IsSorted(out) {
		slices.Sort(out)
	}
	return out
}

// sortedDiff returns unique members of sorted a that are absent from sorted b.
// Both inputs must be ascending; the result is sorted. Set semantics match
// map-based notIn (duplicate names in either list count once). Prefer this
// over notIn on multi-thousand FAIL lists (one linear pass, no maps).
func sortedDiff(a, b []string) []string {
	if len(a) == 0 {
		return nil
	}
	if len(b) == 0 {
		// Unique copy of a (production lists are unique; fuzz may not be).
		out := make([]string, 0, len(a))
		var prev string
		for i, x := range a {
			if i == 0 || x != prev {
				out = append(out, x)
				prev = x
			}
		}
		return out
	}
	// Typical newlyFailed/fixed are a small fraction of the fail set.
	out := make([]string, 0, len(a)/8+1)
	i, j := 0, 0
	for i < len(a) {
		if j >= len(b) {
			// Remainder of a, unique.
			var prev string
			havePrev := len(out) > 0
			if havePrev {
				prev = out[len(out)-1]
			}
			for ; i < len(a); i++ {
				if !havePrev || a[i] != prev {
					out = append(out, a[i])
					prev = a[i]
					havePrev = true
				}
			}
			break
		}
		switch {
		case a[i] == b[j]:
			// Skip the whole equal run in both (set membership).
			v := a[i]
			for i < len(a) && a[i] == v {
				i++
			}
			for j < len(b) && b[j] == v {
				j++
			}
		case a[i] < b[j]:
			v := a[i]
			out = append(out, v)
			for i < len(a) && a[i] == v {
				i++
			}
		default:
			v := b[j]
			for j < len(b) && b[j] == v {
				j++
			}
		}
	}
	return out
}
