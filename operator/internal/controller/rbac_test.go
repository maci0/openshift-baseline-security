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
// real cluster while the fake client still passes unit tests. Name-scoped
// get/update/patch (resourceNames=compliance-operator); create unscoped;
// list/watch unused (Get by name only).
func TestSubscriptionRBACAllowsUpdate(t *testing.T) {
	text := mustReadRoleYAML(t)
	if !strings.Contains(text, "subscriptions") {
		t.Fatal("role.yaml has no subscriptions resource entry")
	}
	if !roleHasResourceVerb(text, "subscriptions", "create") {
		t.Fatal("subscriptions RBAC missing create")
	}
	for _, verb := range []string{"get", "update", "patch"} {
		if !roleHasResourceVerb(text, "subscriptions", verb) {
			t.Fatalf("subscriptions RBAC missing verb %q", verb)
		}
	}
	// Name-scope must pin the CO Subscription so a compromised SA cannot
	// rewrite arbitrary Subscriptions cluster-wide.
	if !strings.Contains(text, "compliance-operator") {
		t.Fatal("subscriptions RBAC missing resourceNames compliance-operator")
	}
}

// TestOperatorGroupRBACAllowsUpdate guards ensureComplianceOperatorGroup, which
// patches targetNamespaces on an existing empty OperatorGroup.
func TestOperatorGroupRBACAllowsUpdate(t *testing.T) {
	text := mustReadRoleYAML(t)
	if !strings.Contains(text, "operatorgroups") {
		t.Fatal("role.yaml has no operatorgroups resource entry")
	}
	if !roleHasResourceVerb(text, "operatorgroups", "create") {
		t.Fatal("operatorgroups RBAC missing create")
	}
	for _, verb := range []string{"get", "update", "patch"} {
		if !roleHasResourceVerb(text, "operatorgroups", verb) {
			t.Fatalf("operatorgroups RBAC missing verb %q", verb)
		}
	}
	if !strings.Contains(text, "compliance-operator") {
		t.Fatal("operatorgroups RBAC missing resourceNames compliance-operator")
	}
}

func mustReadRoleYAML(t *testing.T) string {
	t.Helper()
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
	return string(raw)
}

// roleHasResourceVerb is true when role.yaml lists verb as a YAML list item
// and the resource name appears (create may be on a separate block from
// name-scoped get/update/patch).
func roleHasResourceVerb(roleYAML, resourceName, verb string) bool {
	if !strings.Contains(roleYAML, resourceName) {
		return false
	}
	return rbacVerbListed(roleYAML, verb)
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
	// Create is unscoped; get/update/patch are on the resourceNames block.
	// Scan the full CSV so either form is accepted.
	if !csvVerbsInclude(text, "update") || !csvVerbsInclude(text, "patch") {
		t.Fatalf("CSV operatorgroups rules missing update/patch")
	}
	if !strings.Contains(text, "resourceNames: [compliance-operator]") {
		t.Fatal("CSV operatorgroups missing resourceNames compliance-operator")
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
	idx := strings.Index(text, "resources: [subscriptions]")
	if idx < 0 {
		t.Fatal("CSV has no subscriptions permission entry")
	}
	if !csvVerbsInclude(text, "update") || !csvVerbsInclude(text, "patch") {
		t.Fatalf("CSV subscriptions rules missing update/patch")
	}
	if !strings.Contains(text, "resourceNames: [compliance-operator]") {
		t.Fatal("CSV subscriptions missing resourceNames compliance-operator")
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
