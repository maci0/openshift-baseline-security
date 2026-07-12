package controller

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSubscriptionRBACAllowsUpdate guards the production path that patches an
// existing OLM Subscription when spec.complianceCatalogSource changes
// (syncComplianceSubscriptionSource). create-only RBAC would Forbidden on a
// real cluster while the fake client still passes unit tests.
func TestSubscriptionRBACAllowsUpdate(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/controller -> operator/config/rbac/role.yaml
	rolePath := filepath.Join(filepath.Dir(thisFile), "..", "..", "config", "rbac", "role.yaml")
	raw, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("read role.yaml: %v", err)
	}
	text := string(raw)
	// Find the subscriptions rule block and require update + patch verbs.
	idx := strings.Index(text, "- subscriptions\n")
	if idx < 0 {
		// controller-gen may emit without leading dash spacing variants
		idx = strings.Index(text, "subscriptions\n")
	}
	if idx < 0 {
		t.Fatal("role.yaml has no subscriptions resource entry")
	}
	// Take a window after the resource name covering its verbs list.
	window := text[idx:]
	if end := strings.Index(window[1:], "\n- apiGroups:"); end > 0 {
		window = window[:end+1]
	}
	// Require a YAML list entry for each verb. Bare strings.Contains(verb) is
	// false confidence ("get" matches "target", "update" matches comments).
	for _, verb := range []string{"update", "patch", "create", "get", "list", "watch"} {
		if !rbacVerbListed(window, verb) {
			t.Fatalf("subscriptions RBAC missing verb %q in block:\n%s", verb, window)
		}
	}
}

// TestOperatorGroupRBACAllowsUpdate guards ensureComplianceOperatorGroup, which
// patches targetNamespaces on an existing empty OperatorGroup.
func TestOperatorGroupRBACAllowsUpdate(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	rolePath := filepath.Join(filepath.Dir(thisFile), "..", "..", "config", "rbac", "role.yaml")
	raw, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("read role.yaml: %v", err)
	}
	text := string(raw)
	idx := strings.Index(text, "- operatorgroups\n")
	if idx < 0 {
		idx = strings.Index(text, "operatorgroups\n")
	}
	if idx < 0 {
		t.Fatal("role.yaml has no operatorgroups resource entry")
	}
	window := text[idx:]
	if end := strings.Index(window[1:], "\n- apiGroups:"); end > 0 {
		window = window[:end+1]
	}
	for _, verb := range []string{"update", "patch", "create", "get", "list", "watch"} {
		if !rbacVerbListed(window, verb) {
			t.Fatalf("operatorgroups RBAC missing verb %q in block:\n%s", verb, window)
		}
	}
}

// rbacVerbListed reports whether block contains a YAML list item for verb
// ("- update" as its own list entry), not a bare substring match.
func rbacVerbListed(block, verb string) bool {
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == "- "+verb {
			return true
		}
	}
	return false
}

// TestCSVOperatorGroupRBACAllowsUpdate keeps the OLM CSV permissions in sync
// with role.yaml for OperatorGroup targetNamespaces repair.
func TestCSVOperatorGroupRBACAllowsUpdate(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	csvPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "bundle", "manifests",
		"baseline-security-operator.clusterserviceversion.yaml")
	raw, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read CSV: %v", err)
	}
	text := string(raw)
	idx := strings.Index(text, "resources: [operatorgroups]")
	if idx < 0 {
		t.Fatal("CSV has no operatorgroups permission entry")
	}
	window := text[idx:]
	if len(window) > 200 {
		window = window[:200]
	}
	// CSV rules use "verbs: [get, list, ..., update, patch]" form.
	if !csvVerbsInclude(window, "update") || !csvVerbsInclude(window, "patch") {
		t.Fatalf("CSV operatorgroups rule missing update/patch:\n%s", window)
	}
}

// TestCSVSubscriptionRBACAllowsUpdate keeps the OLM CSV permissions in sync
// with role.yaml for the catalog-source sync path.
func TestCSVSubscriptionRBACAllowsUpdate(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	csvPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "bundle", "manifests",
		"baseline-security-operator.clusterserviceversion.yaml")
	raw, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read CSV: %v", err)
	}
	text := string(raw)
	// The subscriptions rule must list update and patch (catalog source sync).
	idx := strings.Index(text, "resources: [subscriptions]")
	if idx < 0 {
		t.Fatal("CSV has no subscriptions permission entry")
	}
	// Next line(s) should carry verbs including update and patch.
	window := text[idx:]
	if len(window) > 200 {
		window = window[:200]
	}
	if !csvVerbsInclude(window, "update") || !csvVerbsInclude(window, "patch") {
		t.Fatalf("CSV subscriptions rule missing update/patch:\n%s", window)
	}
}

// csvVerbsInclude matches a verb as a list token in "verbs: [a, b, c]" form
// (not a bare substring of another word such as "updated").
func csvVerbsInclude(block, verb string) bool {
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, "verbs:")
		if i < 0 {
			continue
		}
		list := line[i+len("verbs:"):]
		for _, tok := range strings.FieldsFunc(list, func(r rune) bool {
			return r == '[' || r == ']' || r == ',' || r == ' ' || r == '\t'
		}) {
			if tok == verb {
				return true
			}
		}
	}
	return false
}
