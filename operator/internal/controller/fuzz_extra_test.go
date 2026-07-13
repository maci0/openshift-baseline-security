package controller

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// FuzzNotIn: failure-name set difference (newlyFailed/fixed). Sorted, ⊆ a, disjoint from b.
func FuzzNotIn(f *testing.F) {
	f.Add("a,b,c", "b,d")
	f.Add("", "")
	f.Add("a,a,b", "a")
	f.Add("z,y,x", "")
	f.Add("", "x,y")
	f.Fuzz(func(t *testing.T, aCSV, bCSV string) {
		if len(aCSV) > 4096 {
			aCSV = aCSV[:4096]
		}
		if len(bCSV) > 4096 {
			bCSV = bCSV[:4096]
		}
		a := splitCSV(aCSV)
		b := map[string]bool{}
		for _, x := range splitCSV(bCSV) {
			b[x] = true
		}
		got := notIn(a, b)
		seen := map[string]bool{}
		for i, x := range got {
			if b[x] {
				t.Fatalf("result %q still in b", x)
			}
			if seen[x] {
				t.Fatalf("duplicate %q in result %v", x, got)
			}
			seen[x] = true
			inA := false
			for _, y := range a {
				if y == x {
					inA = true
					break
				}
			}
			if !inA {
				t.Fatalf("result %q not in a", x)
			}
			if i > 0 && got[i-1] > x {
				t.Fatalf("unsorted: %v", got)
			}
		}
	})
}

// FuzzSetCond: empty Reason -> Unknown; Message <=1024; ObservedGeneration = CR gen.
func FuzzSetCond(f *testing.F) {
	f.Add("ScanConfigured", "InvalidSchedule", "bad", int64(1))
	f.Add("Degraded", "", "pending", int64(0))
	f.Add("Available", "AsExpected", "", int64(3))
	f.Add("ComplianceOperatorReady", "CSVNotReady", strings.Repeat("p", 2000), int64(9))
	f.Fuzz(func(t *testing.T, typ, reason, msg string, gen int64) {
		if len(typ) > 128 {
			typ = typ[:128]
		}
		if len(reason) > 256 {
			reason = reason[:256]
		}
		if len(msg) > 2048 {
			msg = msg[:2048]
		}
		if gen < 0 {
			gen = -gen
		}
		gen %= 1 << 20
		if typ == "" {
			typ = "Available"
		}
		cb := &baselinev1alpha1.ClusterBaseline{}
		cb.Generation = gen
		setCond(cb, typ, metav1.ConditionFalse, reason, msg)
		c := meta.FindStatusCondition(cb.Status.Conditions, typ)
		if c == nil {
			t.Fatal("condition missing after setCond")
		}
		wantReason := reason
		if reason == "" {
			wantReason = "Unknown"
		}
		if c.Reason != wantReason {
			t.Fatalf("reason %q want %q", c.Reason, wantReason)
		}
		if len(c.Message) > 1024 {
			t.Fatalf("message len %d > 1024", len(c.Message))
		}
		if c.ObservedGeneration != gen {
			t.Fatalf("ObservedGeneration %d want %d", c.ObservedGeneration, gen)
		}
	})
}

// FuzzPickComplianceOperatorCSV: untrusted CSV name/ns/phase; winner must match filter.
func FuzzPickComplianceOperatorCSV(f *testing.F) {
	f.Add("compliance-operator.v1.0.0", "Succeeded", "openshift-compliance", true)
	f.Add("compliance-operator.v1.9.1", "Failed", "openshift-compliance", false)
	f.Add("not-a-co-csv", "Succeeded", "", true)
	f.Fuzz(func(t *testing.T, name, phase, ns string, succeededOnly bool) {
		const max = 256
		if len(name) > max {
			name = name[:max]
		}
		if len(phase) > max {
			phase = phase[:max]
		}
		if len(ns) > max {
			ns = ns[:max]
		}
		items := []unstructured.Unstructured{
			{Object: map[string]any{
				"metadata": map[string]any{"name": name, "namespace": ns},
				"status":   map[string]any{"phase": phase},
			}},
			{Object: map[string]any{
				"metadata": map[string]any{"name": "compliance-operator.v0.1.0", "namespace": ns},
				"status":   map[string]any{"phase": "Succeeded"},
			}},
			{Object: map[string]any{"metadata": "bad"}},
			{Object: map[string]any{}},
		}
		got := pickComplianceOperatorCSV(items, ns, succeededOnly)
		if got == nil {
			return
		}
		if !strings.HasPrefix(got.GetName(), "compliance-operator.v") {
			t.Fatalf("winner name %q lacks prefix", got.GetName())
		}
		if ns != "" && got.GetNamespace() != ns {
			t.Fatalf("winner ns %q != filter %q", got.GetNamespace(), ns)
		}
		p, _, _ := unstructured.NestedString(got.Object, "status", "phase")
		if succeededOnly && p != "Succeeded" {
			t.Fatalf("succeededOnly winner phase=%q", p)
		}
		if !succeededOnly && p == "Succeeded" {
			t.Fatalf("non-succeededOnly winner is Succeeded")
		}
	})
}

// FuzzSetComplianceOperatorReadyFromCSV: untrusted CSV name/phase -> status + conditions.
func FuzzSetComplianceOperatorReadyFromCSV(f *testing.F) {
	f.Add("compliance-operator.v1.9.1", "Succeeded")
	f.Add("compliance-operator.v1.0.0", "Failed")
	f.Add("compliance-operator.v1.0.0", "Installing")
	f.Add("compliance-operator.v", "")
	f.Add("x", strings.Repeat("Z", 2000))
	f.Fuzz(func(t *testing.T, name, phase string) {
		if len(name) > 256 {
			name = name[:256]
		}
		if len(phase) > 2048 {
			phase = phase[:2048]
		}
		csv := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": name},
			"status":   map[string]any{"phase": phase},
		}}
		if phase == "TYPECONFUSE" {
			_ = unstructured.SetNestedField(csv.Object, 42, "status", "phase")
		}
		cb := &baselinev1alpha1.ClusterBaseline{}
		setComplianceOperatorReadyFromCSV(cb, csv)
		c := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
		if c == nil {
			t.Fatal("ComplianceOperatorReady missing")
		}
		if len(c.Message) > 1024 || c.Reason == "" {
			t.Fatalf("unsafe condition: reason=%q msgLen=%d", c.Reason, len(c.Message))
		}
		effPhase := phase
		if phase == "TYPECONFUSE" || phase == "" {
			effPhase = "unknown"
		}
		switch effPhase {
		case "Succeeded":
			if c.Status != metav1.ConditionTrue || c.Reason != "CSVSucceeded" {
				t.Fatalf("Succeeded: status=%s reason=%s", c.Status, c.Reason)
			}
			wantVer := strings.TrimPrefix(name, "compliance-operator.v")
			if cb.Status.ComplianceOperatorVersion != wantVer {
				t.Fatalf("version %q want %q", cb.Status.ComplianceOperatorVersion, wantVer)
			}
		case "Failed":
			if c.Status != metav1.ConditionFalse || c.Reason != "CSVFailed" {
				t.Fatalf("Failed: status=%s reason=%s", c.Status, c.Reason)
			}
			if cb.Status.ComplianceOperatorVersion != "" {
				t.Fatalf("Failed must clear version, got %q", cb.Status.ComplianceOperatorVersion)
			}
		default:
			if c.Status != metav1.ConditionFalse || c.Reason != "CSVNotReady" {
				t.Fatalf("other: status=%s reason=%s", c.Status, c.Reason)
			}
			if cb.Status.ComplianceOperatorVersion != "" {
				t.Fatalf("non-Succeeded must clear version, got %q", cb.Status.ComplianceOperatorVersion)
			}
		}
	})
}

// FuzzCheckSeverityTypeConfusion: non-string CCR.severity falls back to label.
func FuzzCheckSeverityTypeConfusion(f *testing.F) {
	f.Add(byte(0), "high")
	f.Add(byte(1), "low")
	f.Add(byte(2), "")
	f.Fuzz(func(t *testing.T, shape byte, label string) {
		if len(label) > 256 {
			label = label[:256]
		}
		item := &unstructured.Unstructured{Object: map[string]any{}}
		if label != "" {
			item.SetLabels(map[string]string{checkSeverityLabel: label})
		}
		switch shape % 5 {
		case 0:
			item.Object["severity"] = 7
		case 1:
			item.Object["severity"] = true
		case 2:
			item.Object["severity"] = []any{"high"}
		case 3:
			item.Object["severity"] = map[string]any{"v": "high"}
		default:
			item.Object["severity"] = nil
		}
		want := label
		if want == "" {
			want = "unknown"
		}
		if got := checkSeverity(item); got != want {
			t.Fatalf("got %q want %q (shape=%d)", got, want, shape)
		}
	})
}

// FuzzPoolFromRemediationTypeConfusion: wrong-typed nested rem objects must not panic.
func FuzzPoolFromRemediationTypeConfusion(f *testing.F) {
	f.Add(byte(0), "ocp4-cis-node-worker")
	f.Add(byte(1), "ocp4-cis-node-master")
	f.Add(byte(2), "")
	f.Fuzz(func(t *testing.T, shape byte, scan string) {
		if len(scan) > 256 {
			scan = scan[:256]
		}
		rem := &unstructured.Unstructured{Object: map[string]any{}}
		if scan != "" {
			rem.SetLabels(map[string]string{"compliance.openshift.io/scan-name": scan})
		}
		switch shape % 6 {
		case 0:
			rem.Object["spec"] = "not-a-map"
		case 1:
			rem.Object["spec"] = map[string]any{"current": "not-a-map"}
		case 2:
			rem.Object["spec"] = map[string]any{"current": map[string]any{"object": "string-object"}}
		case 3: // non-JSON int kind: NestedMap DeepCopy used to panic here
			rem.Object["spec"] = map[string]any{"current": map[string]any{"object": map[string]any{
				"kind":     42,
				"metadata": map[string]any{"labels": map[string]any{"machineconfiguration.openshift.io/role": "worker"}},
			}}}
		case 4:
			rem.Object["spec"] = map[string]any{"current": map[string]any{"object": map[string]any{
				"kind": "MachineConfig", "metadata": map[string]any{"labels": "not-a-map"},
			}}}
		default:
			rem.Object["spec"] = map[string]any{"current": map[string]any{"object": []any{map[string]any{"kind": "MachineConfig"}}}}
		}
		got := poolFromRemediation(rem)
		if got != "" && validMCPPoolName(got) != got {
			t.Fatalf("non-DNS1123 pool %q", got)
		}
	})
}

// FuzzProfileBucketScore: flat/weighted profile history score is nil or [0,100].
func FuzzProfileBucketScore(f *testing.F) {
	f.Add(int32(1), int32(1), int64(10), int64(5), true, true)
	f.Add(int32(0), int32(0), int64(0), int64(0), false, false)
	f.Add(int32(-1), int32(1), int64(1), int64(1), true, true)
	f.Fuzz(func(t *testing.T, pass, fail int32, wPass, wFail int64, weighted, haveWeights bool) {
		mode := baselinev1alpha1.ScoringFlat
		if weighted {
			mode = baselinev1alpha1.ScoringSeverityWeighted
		}
		got := profileBucketScore(pass, fail, weightedSum{pass: wPass, fail: wFail}, mode, haveWeights)
		if got != nil && (*got < 0 || *got > 100) {
			t.Fatalf("score %d out of [0,100]", *got)
		}
	})
}

// FuzzHistoryModeMatches: history-scoring-mode annotation is hand-editable CR
// metadata. Empty/missing always allows late refresh; non-empty must equal the
// effective scoring mode (unknown mode collapses to Flat).
func FuzzHistoryModeMatches(f *testing.F) {
	f.Add("", false)
	f.Add("Flat", false)
	f.Add("SeverityWeighted", true)
	f.Add("SeverityWeighted", false)
	f.Add("bogus", true)
	f.Add(strings.Repeat("x", 64), false)
	f.Fuzz(func(t *testing.T, stamp string, weighted bool) {
		if len(stamp) > 256 {
			stamp = stamp[:256]
		}
		cb := &baselinev1alpha1.ClusterBaseline{}
		if weighted {
			cb.Spec.Scoring.Mode = baselinev1alpha1.ScoringSeverityWeighted
		}
		if stamp != "" {
			cb.Annotations = map[string]string{historyScoringModeAnn: stamp}
		}
		got := historyModeMatches(cb)
		wantMode := string(baselinev1alpha1.ScoringFlat)
		if weighted {
			wantMode = string(baselinev1alpha1.ScoringSeverityWeighted)
		}
		want := stamp == "" || stamp == wantMode
		if got != want {
			t.Fatalf("stamp=%q weighted=%v got %v want %v", stamp, weighted, got, want)
		}
	})
}

// FuzzConditionProgressing: only False+Installing/CSVNotReady/WaitingForPods
// is in-flight progress. Hostile/empty reasons must not keep 15s poll forever.
func FuzzConditionProgressing(f *testing.F) {
	f.Add("Installing", "False")
	f.Add("CSVNotReady", "False")
	f.Add("WaitingForPods", "False")
	f.Add("Installing", "True")
	f.Add("NotInstalled", "False")
	f.Add("CRDsMissing", "False")
	f.Add("", "False")
	f.Add(strings.Repeat("R", 300), "False")
	f.Fuzz(func(t *testing.T, reason, status string) {
		if len(reason) > 256 {
			reason = reason[:256]
		}
		if len(status) > 32 {
			status = status[:32]
		}
		// nil condition is steady (not progressing).
		if conditionProgressing(nil) {
			t.Fatal("nil condition must not progress")
		}
		c := &metav1.Condition{Reason: reason, Status: metav1.ConditionStatus(status)}
		got := conditionProgressing(c)
		want := c.Status == metav1.ConditionFalse &&
			(reason == "Installing" || reason == "CSVNotReady" || reason == "WaitingForPods")
		if got != want {
			t.Fatalf("reason=%q status=%q got %v want %v", reason, status, got, want)
		}
	})
}

// FuzzRemediationOwnedByBaseline: suite label is untrusted cluster data; only
// exact membership in the prebuilt ownedSuites map grants apply rights.
func FuzzRemediationOwnedByBaseline(f *testing.F) {
	f.Add("baseline-cis", "baseline-cis", true)
	f.Add("baseline-cis", "baseline-pci-dss", true)
	f.Add("baseline-cis", "", true)
	f.Add("foreign", "baseline-cis", true)
	f.Add("", "baseline-cis", false)
	f.Fuzz(func(t *testing.T, suiteLabelVal, ownedCSV string, includeLabel bool) {
		if len(suiteLabelVal) > 256 {
			suiteLabelVal = suiteLabelVal[:256]
		}
		if len(ownedCSV) > 512 {
			ownedCSV = ownedCSV[:512]
		}
		suites := map[string]bool{}
		for _, s := range splitCSV(ownedCSV) {
			if len(s) > 128 {
				s = s[:128]
			}
			if s != "" {
				suites[s] = true
			}
		}
		rem := &unstructured.Unstructured{Object: map[string]any{}}
		if includeLabel {
			// Type-confused labels must not panic and must not match.
			if len(suiteLabelVal)%5 == 0 {
				rem.Object["metadata"] = map[string]any{
					"labels": map[string]any{suiteLabel: 42},
				}
			} else {
				rem.SetLabels(map[string]string{suiteLabel: suiteLabelVal})
			}
		}
		got := remediationOwnedByBaseline(suites, rem)
		want := false
		if includeLabel && len(suiteLabelVal)%5 != 0 {
			want = suites[suiteLabelVal]
		}
		if got != want {
			t.Fatalf("suite=%q owned=%v include=%v got %v want %v",
				suiteLabelVal, suites, includeLabel, got, want)
		}
	})
}

// FuzzOwnedSuitesRelatedObjects: profile/tailored names from CR spec drive
// ownership maps and must-gather relatedObjects. Never panic; relatedObjects
// length is fixed core refs + owned suite count; suite names are sorted.
func FuzzOwnedSuitesRelatedObjects(f *testing.F) {
	f.Add("cis", "custom")
	f.Add("", "")
	f.Add("cis,pci-dss", "a,b")
	f.Add("cis,cis", "x,x")
	f.Add(strings.Repeat("p", 64), strings.Repeat("t", 64))
	f.Fuzz(func(t *testing.T, profilesCSV, tailoredCSV string) {
		if len(profilesCSV) > 512 {
			profilesCSV = profilesCSV[:512]
		}
		if len(tailoredCSV) > 512 {
			tailoredCSV = tailoredCSV[:512]
		}
		var profiles []baselinev1alpha1.ProfileKey
		for _, p := range splitCSV(profilesCSV) {
			if len(p) > 64 {
				p = p[:64]
			}
			if p != "" {
				profiles = append(profiles, baselinev1alpha1.ProfileKey(p))
			}
		}
		// Cap counts so the fuzzer cannot thrash on huge CR-shaped inputs.
		if len(profiles) > 16 {
			profiles = profiles[:16]
		}
		var tailored []string
		for _, n := range splitCSV(tailoredCSV) {
			if len(n) > 64 {
				n = n[:64]
			}
			if n != "" {
				tailored = append(tailored, n)
			}
		}
		if len(tailored) > 16 {
			tailored = tailored[:16]
		}
		cb := &baselinev1alpha1.ClusterBaseline{
			Spec: baselinev1alpha1.ClusterBaselineSpec{
				Profiles:         profiles,
				TailoredProfiles: tailored,
			},
		}
		suites := ownedSuites(cb)
		// Deduped map size: unique binding/tp names only.
		if len(suites) > len(profiles)+len(tailored) {
			t.Fatalf("ownedSuites larger than inputs: %d > %d+%d", len(suites), len(profiles), len(tailored))
		}
		for _, key := range profiles {
			if !suites[bindingName(key)] {
				t.Fatalf("missing profile suite for %q", key)
			}
		}
		for _, name := range tailored {
			if !suites[tailoredBindingName(name)] {
				t.Fatalf("missing tailored suite for %q", name)
			}
		}
		refs := relatedObjectsFromSuites(suites)
		// 4 fixed core refs + one ScanSettingBinding per owned suite.
		if len(refs) != 4+len(suites) {
			t.Fatalf("relatedObjectsFromSuites len %d want %d", len(refs), 4+len(suites))
		}
		// Binding names after the fixed prefix must be sorted.
		var names []string
		for _, ref := range refs[4:] {
			if ref.Resource != "scansettingbindings" {
				t.Fatalf("unexpected resource %q", ref.Resource)
			}
			names = append(names, ref.Name)
			if !suites[ref.Name] {
				t.Fatalf("relatedObjectsFromSuites name %q not in ownedSuites", ref.Name)
			}
		}
		for i := 1; i < len(names); i++ {
			if names[i-1] > names[i] {
				t.Fatalf("relatedObjectsFromSuites bindings unsorted: %v", names)
			}
		}
	})
}

// FuzzSetRollupConditions: detail condition reasons/messages are untrusted
// (hand-edit, wrap errors). Rollups must never panic and must keep Reason and
// Message admission-safe (non-empty Reason, Message <= 1024).
func FuzzSetRollupConditions(f *testing.F) {
	f.Add("CSVFailed", "boom", "InvalidSchedule", "bad cron", "Unavailable", "down")
	f.Add("Installing", "", "AsExpected", "", "WaitingForPods", "")
	f.Add("CSVNotReady", strings.Repeat("m", 2000), "InvalidSchedule", "x", "Unavailable", "y")
	f.Add("", "", "", "", "", "")
	f.Fuzz(func(t *testing.T, coReason, coMsg, scanReason, scanMsg, pluginReason, pluginMsg string) {
		clamp := func(s string, n int) string {
			if len(s) > n {
				return s[:n]
			}
			return s
		}
		coReason, coMsg = clamp(coReason, 256), clamp(coMsg, 2048)
		scanReason, scanMsg = clamp(scanReason, 256), clamp(scanMsg, 2048)
		pluginReason, pluginMsg = clamp(pluginReason, 256), clamp(pluginMsg, 2048)

		cb := &baselinev1alpha1.ClusterBaseline{}
		cb.Generation = 1
		// Seed detail conditions as False so rollup logic exercises Degraded paths.
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, coReason, coMsg)
		setCond(cb, "ScanConfigured", metav1.ConditionFalse, scanReason, scanMsg)
		setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, pluginReason, pluginMsg)
		setCond(cb, "ScanStorageReady", metav1.ConditionTrue, "AsExpected", "")
		setRollupConditions(cb)

		for _, typ := range []string{"Available", "Progressing", "Degraded"} {
			c := meta.FindStatusCondition(cb.Status.Conditions, typ)
			if c == nil {
				t.Fatalf("missing rollup %s", typ)
			}
			if c.Reason == "" {
				t.Fatalf("%s Reason empty", typ)
			}
			if len(c.Message) > 1024 {
				t.Fatalf("%s Message len %d > 1024", typ, len(c.Message))
			}
		}
	})
}
