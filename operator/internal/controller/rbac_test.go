package controller

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestSubscriptionRBACAllowsUpdate guards the production path that patches an
// existing OLM Subscription when spec.complianceCatalogSource changes
// (syncComplianceSubscriptionSource). create-only RBAC would Forbidden on a
// real cluster while the fake client still passes unit tests. Name-scoped
// get/update/patch (resourceNames=compliance-operator); create unscoped;
// list/watch unused (Get by name only).
func TestSubscriptionRBACAllowsUpdate(t *testing.T) {
	assertRoleResourceUpdate(t, mustReadRoleYAML(t), "subscriptions")
}

// TestOperatorGroupRBACAllowsUpdate guards ensureComplianceOperatorGroup, which
// patches targetNamespaces on an existing empty OperatorGroup.
func TestOperatorGroupRBACAllowsUpdate(t *testing.T) {
	assertRoleResourceUpdate(t, mustReadRoleYAML(t), "operatorgroups")
}

func assertRoleResourceUpdate(t *testing.T, text, resource string) {
	t.Helper()
	rules := rulesGrantingResource(t, mustParseClusterRole(t, text), resource)
	if len(rules) == 0 {
		t.Fatalf("role.yaml has no rule for resource %q", resource)
	}
	if !ruleGrantsVerb(rules, "create") {
		t.Fatalf("%s RBAC missing create", resource)
	}
	for _, verb := range []string{"get", "update", "patch"} {
		if !ruleGrantsVerb(rules, verb) {
			t.Fatalf("%s RBAC missing verb %q", resource, verb)
		}
	}
	// Name-scoped get/update/patch must pin the CO object so a compromised SA
	// cannot rewrite arbitrary Subscriptions / OperatorGroups cluster-wide.
	// The scope is per rule: a create-only rule elsewhere in the file must not
	// satisfy this, and dropping resourceNames from the rule that does carry
	// update must fail even if some other rule mentions the name.
	if !ruleGrantsNameScopedWrite(rules) {
		t.Fatalf("%s RBAC missing name-scoped get/update/patch (resourceNames: compliance-operator)", resource)
	}
}

// policyRule mirrors one entry of a ClusterRole's rules list. The json tags are
// what sigs.k8s.io/yaml decodes, because it routes YAML through JSON.
type policyRule struct {
	APIGroups     []string `json:"apiGroups"`
	Resources     []string `json:"resources"`
	Verbs         []string `json:"verbs"`
	ResourceNames []string `json:"resourceNames"`
}

type clusterRole struct {
	Rules []policyRule `json:"rules"`
}

type multiDocRole struct {
	Items []clusterRole `json:"items"`
}

func mustParseClusterRole(t *testing.T, text string) clusterRole {
	t.Helper()
	var role clusterRole
	if err := yaml.Unmarshal([]byte(text), &role); err != nil {
		t.Fatalf("parse ClusterRole: %v", err)
	}
	var list multiDocRole
	if err := yaml.Unmarshal([]byte(text), &list); err == nil && list.Items != nil {
		role.Rules = append(role.Rules, list.Items[0].Rules...)
	}
	return role
}

// rulesGrantingResource returns every rule naming resource (or the wildcard),
// regardless of verb, so a caller can check the full verb set for it.
func rulesGrantingResource(t *testing.T, role clusterRole, resource string) []policyRule {
	t.Helper()
	var out []policyRule
	for _, rule := range role.Rules {
		for _, r := range rule.Resources {
			if r == resource || r == "*" {
				out = append(out, rule)
				break
			}
		}
	}
	return out
}

func ruleGrantsVerb(rules []policyRule, verb string) bool {
	for _, rule := range rules {
		for _, v := range rule.Verbs {
			if v == verb || v == "*" {
				return true
			}
		}
	}
	return false
}

// ruleGrantsNameScopedWrite requires one rule to carry both the write verbs
// and the complianceOperatorName resourceNames entry, so a name-scoped rule and
// a write rule cannot come from two different blocks.
func ruleGrantsNameScopedWrite(rules []policyRule) bool {
	for _, rule := range rules {
		writes := false
		for _, v := range rule.Verbs {
			if v == "update" || v == "patch" || v == "*" {
				writes = true
				break
			}
		}
		if !writes {
			continue
		}
		for _, n := range rule.ResourceNames {
			if n == complianceOperatorName || n == "*" {
				return true
			}
		}
	}
	return false
}

// TestRoleRulesAreScopedToTheResource pins the scoping the RBAC guards above
// depend on: verbs and resourceNames are read from the rule that names the
// resource, so a create-only rule elsewhere in the file cannot stand in for a
// missing name-scoped write rule. Each case is a whole role document that must
// satisfy (or not) the same predicates the file-based guards use.
func TestRoleRulesAreScopedToTheResource(t *testing.T) {
	t.Parallel()
	const writeRule = `
- apiGroups: [operators.coreos.com]
  resourceNames: [compliance-operator]
  resources: [%s]
  verbs: [get, patch, update]
- apiGroups: [operators.coreos.com]
  resources: [%s]
  verbs: [create]
`
	role := func(rules string) clusterRole {
		t.Helper()
		return mustParseClusterRole(t, "kind: ClusterRole\nrules:"+rules)
	}
	t.Run("well formed role passes", func(t *testing.T) {
		t.Parallel()
		rules := rulesGrantingResource(t, role(fmt.Sprintf(writeRule, "subscriptions", "subscriptions")), "subscriptions")
		if !ruleGrantsVerb(rules, "create") || !ruleGrantsVerb(rules, "update") {
			t.Fatalf("well formed role rejected: %+v", rules)
		}
		if !ruleGrantsNameScopedWrite(rules) {
			t.Fatal("well formed role lost its name-scoped write rule")
		}
	})
	t.Run("dropping resourceNames from the write rule fails", func(t *testing.T) {
		t.Parallel()
		doc := `
- apiGroups: [operators.coreos.com]
  resources: [subscriptions]
  verbs: [get, patch, update, create]
- apiGroups: [operators.coreos.com]
  resources: [operatorgroups]
  resourceNames: [compliance-operator]
  verbs: [update]
`
		rules := rulesGrantingResource(t, role(doc), "subscriptions")
		if !ruleGrantsVerb(rules, "update") {
			t.Fatal("setup: the unscoped write rule should still grant update")
		}
		if ruleGrantsNameScopedWrite(rules) {
			t.Error("an unscoped write rule was accepted as name-scoped")
		}
	})
	t.Run("resource from another rule is not borrowed", func(t *testing.T) {
		t.Parallel()
		doc := `
- apiGroups: [operators.coreos.com]
  resources: [configmaps]
  resourceNames: [compliance-operator]
  verbs: [update, patch, get, create]
`
		if rules := rulesGrantingResource(t, role(doc), "subscriptions"); len(rules) != 0 {
			t.Errorf("configmaps rule matched subscriptions: %+v", rules)
		}
	})
}

func mustReadRepoFile(t *testing.T, rel ...string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(append([]string{filepath.Dir(thisFile)}, rel...)...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	return string(raw)
}

func mustReadRoleYAML(t *testing.T) string {
	t.Helper()
	return mustReadRepoFile(t, "..", "..", "config", "rbac", "role.yaml")
}

func mustReadUserRolesYAML(t *testing.T) string {
	t.Helper()
	return mustReadRepoFile(t, "..", "..", "config", "rbac", "user_roles.yaml")
}

func mustReadCSV(t *testing.T) string {
	t.Helper()
	return mustReadRepoFile(t, "..", "..", "bundle", "manifests",
		"baseline-security-operator.clusterserviceversion.yaml")
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
	assertCSVResourceUpdate(t, mustReadCSV(t), "operatorgroups")
}

// TestCSVSubscriptionRBACAllowsUpdate keeps the OLM CSV permissions in sync
// with role.yaml for the catalog-source sync path.
func TestCSVSubscriptionRBACAllowsUpdate(t *testing.T) {
	assertCSVResourceUpdate(t, mustReadCSV(t), "subscriptions")
}

// assertCSVResourceUpdate checks the CSV's own clusterPermissions entry, not
// merely that the resource and its verbs appear somewhere in the document: the
// create rule, the name-scoped write rule and unrelated rules are separate
// blocks, and only the block that names the resource may satisfy the check.
func assertCSVResourceUpdate(t *testing.T, text, resource string) {
	t.Helper()
	rules := csvRulesGrantingResource(t, text, resource)
	if len(rules) == 0 {
		t.Fatalf("CSV has no %s permission entry", resource)
	}
	// Create is unscoped; get/update/patch are on the resourceNames block.
	if !ruleGrantsVerb(rules, "create") {
		t.Fatalf("CSV %s rules missing create", resource)
	}
	for _, verb := range []string{"get", "update", "patch"} {
		if !ruleGrantsVerb(rules, verb) {
			t.Fatalf("CSV %s rules missing %s", resource, verb)
		}
	}
	if !ruleGrantsNameScopedWrite(rules) {
		t.Fatalf("CSV %s missing name-scoped get/update/patch (resourceNames: compliance-operator)", resource)
	}
}

// csvPermission is one entry of the CSV's spec.install.spec.clusterPermissions.
type csvPermission struct {
	ServiceAccountName string       `json:"serviceAccountName"`
	Rules              []policyRule `json:"rules"`
}

type csvSpec struct {
	Spec struct {
		Install struct {
			Spec struct {
				ClusterPermissions []csvPermission `json:"clusterPermissions"`
			} `json:"spec"`
		} `json:"install"`
	} `json:"spec"`
}

func csvRulesGrantingResource(t *testing.T, text, resource string) []policyRule {
	t.Helper()
	var csv csvSpec
	if err := yaml.Unmarshal([]byte(text), &csv); err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	var out []policyRule
	for _, perm := range csv.Spec.Install.Spec.ClusterPermissions {
		out = append(out, rulesGrantingResource(t, clusterRole{Rules: perm.Rules}, resource)...)
	}
	return out
}

// clusterRoleDoc returns the YAML document whose metadata.name is name.
func clusterRoleDoc(rolesYAML, name string) string {
	for _, doc := range strings.Split(rolesYAML, "---") {
		for _, line := range strings.Split(doc, "\n") {
			if strings.TrimSpace(line) == "name: "+name {
				return doc
			}
		}
	}
	return ""
}

func mustUserRoleDoc(t *testing.T, name string) string {
	t.Helper()
	doc := clusterRoleDoc(mustReadUserRolesYAML(t), name)
	if doc == "" {
		t.Fatalf("user_roles.yaml missing %s", name)
	}
	return doc
}

// TestViewerRoleCoversConsoleReads pins the aggregated viewer ClusterRole to
// every compliance.openshift.io kind the console watches on the Profiles tab
// (profiles, tailoredprofiles, rules) in addition to results/scans/suites.
// Omitting tailoredprofiles or rules 403s those watches for view-only users.
func TestViewerRoleCoversConsoleReads(t *testing.T) {
	doc := mustUserRoleDoc(t, "baseline-security-viewer")
	if !strings.Contains(doc, "resources: [clusterbaselines]") {
		t.Fatal("viewer role missing clusterbaselines")
	}
	for _, resource := range []string{
		"compliancecheckresults",
		"compliancescans",
		"compliancesuites",
		"complianceremediations",
		"profiles",
		"tailoredprofiles",
		"rules",
	} {
		if !strings.Contains(doc, "- "+resource) {
			t.Fatalf("viewer role missing resource %q", resource)
		}
	}
	for _, verb := range []string{"create", "update", "patch", "delete"} {
		if roleDocHasVerb(doc, verb) {
			t.Fatalf("viewer role must stay read-only, found verb %q", verb)
		}
	}
}

// TestAdminRoleAllowsTailoredProfileAuthoring pins create/update/patch on
// tailoredprofiles so a user with the aggregated admin role can use the
// console authoring flow (k8sCreate / k8sUpdate) without cluster-admin.
func TestAdminRoleAllowsTailoredProfileAuthoring(t *testing.T) {
	doc := mustUserRoleDoc(t, "baseline-security-admin")
	idx := strings.Index(doc, "resources: [tailoredprofiles]")
	if idx < 0 {
		t.Fatal("admin role missing tailoredprofiles")
	}
	block := doc[idx:]
	for _, verb := range []string{"get", "list", "watch", "create", "update", "patch"} {
		if !csvVerbsInclude(block, verb) {
			t.Fatalf("admin tailoredprofiles missing verb %q", verb)
		}
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

// TestViewerRoleDeniesWrites is the deny side of the human RBAC matrix:
// baseline-security-viewer must not grant create/update/patch/delete.
func TestViewerRoleDeniesWrites(t *testing.T) {
	doc := mustUserRoleDoc(t, "baseline-security-viewer")
	for _, verb := range []string{"create", "update", "patch", "delete"} {
		if roleDocHasVerb(doc, verb) {
			t.Fatalf("viewer ClusterRole must not grant %q", verb)
		}
	}
}

// TestAdminRoleNotAggregatedToAdmin pins the namespace-admin confused-deputy
// fix: aggregating remediations onto the built-in admin ClusterRole would let a
// RoleBinding to admin in openshift-compliance patch ComplianceRemediations
// (node reboots) without cluster-scoped ClusterBaseline access.
func TestAdminRoleNotAggregatedToAdmin(t *testing.T) {
	doc := mustUserRoleDoc(t, "baseline-security-admin")
	if strings.Contains(doc, "aggregate-to-admin") {
		t.Fatal("baseline-security-admin must not aggregate onto the built-in admin ClusterRole")
	}
}

// TestAdminClusterBaselineWritesAreNameScoped: update/patch on clusterbaselines
// is limited to the singleton name so a second object cannot be mutated if
// admission is bypassed. get/list/watch stay unscoped (list ignores resourceNames).
func TestAdminClusterBaselineWritesAreNameScoped(t *testing.T) {
	doc := mustUserRoleDoc(t, "baseline-security-admin")
	if !strings.Contains(doc, "resourceNames: [cluster]") {
		t.Fatal("admin ClusterBaseline writes must set resourceNames: [cluster]")
	}
	if !roleDocHasVerb(doc, "update") || !roleDocHasVerb(doc, "patch") {
		t.Fatal("admin ClusterRole missing update/patch")
	}
}

// TestAdminRoleIncludesViewerReads: binding only baseline-security-admin must
// still list check results (admin is a superset of viewer reads).
func TestAdminRoleIncludesViewerReads(t *testing.T) {
	doc := mustUserRoleDoc(t, "baseline-security-admin")
	for _, res := range []string{
		"clusterbaselines", "compliancecheckresults", "compliancescans",
		"compliancesuites", "complianceremediations", "profiles", "tailoredprofiles",
	} {
		if !strings.Contains(doc, res) {
			t.Fatalf("admin ClusterRole missing read resource %q", res)
		}
	}
	if !roleDocHasVerb(doc, "get") || !roleDocHasVerb(doc, "list") || !roleDocHasVerb(doc, "watch") {
		t.Fatal("admin ClusterRole missing get/list/watch")
	}
}

// roleDocHasVerb is true when a ClusterRole YAML document lists verb either as
// a YAML item ("- patch") or an inline flow list ("verbs: [get, patch]").
func roleDocHasVerb(doc, verb string) bool {
	return rbacVerbListed(doc, verb) || csvVerbsInclude(doc, verb)
}
