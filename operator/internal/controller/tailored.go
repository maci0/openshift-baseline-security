package controller

import "strings"

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
