package controller

import (
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

// TestBatchRemediationNames pins the annotation CSV contract with exact outputs.
// The fuzz target below covers arbitrary input; this table catches intentional
// shape regressions (sort order, trim, empty drop) with readable failure text.
func TestBatchRemediationNames(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{",,,", nil},
		{"a", []string{"a"}},
		{"a,b", []string{"a", "b"}},
		{"b,a", []string{"a", "b"}},
		{"a, a, b", []string{"a", "b"}},
		{"  x  , y ", []string{"x", "y"}},
		{"a,b,a", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := batchRemediationNames(c.raw)
		if len(got) != len(c.want) {
			t.Fatalf("batchRemediationNames(%q) = %v, want %v", c.raw, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("batchRemediationNames(%q) = %v, want %v", c.raw, got, c.want)
			}
		}
	}
}

// FuzzBatchRemediationNames: annotation CSV of remediation names is untrusted
// CR/annotation text. Must never panic; drop empties; sort+dedupe; no empty items.
func FuzzBatchRemediationNames(f *testing.F) {
	for _, seed := range []string{
		"", "a", "a,b", "a, a, b", ",,,", "  x  , y ", "a,b,a",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := batchRemediationNames(raw)
		seen := map[string]bool{}
		for _, n := range got {
			if n == "" {
				t.Fatal("empty name in result")
			}
			if seen[n] {
				t.Fatalf("duplicate %q", n)
			}
			seen[n] = true
		}
		// Sorted ascending.
		for i := 1; i < len(got); i++ {
			if got[i-1] > got[i] {
				t.Fatalf("unsorted: %v", got)
			}
		}
		// Every non-empty trimmed CSV field must appear.
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !seen[p] {
				t.Fatalf("missing field %q from %v (raw=%q)", p, got, raw)
			}
		}
	})
}

// FuzzPoolFromRemediation: untrusted remediation object + scan-name label drive
// which MachineConfigPool is paused during batch apply. Must never panic;
// non-MachineConfig kinds yield ""; MachineConfig role label wins over scan.
// TestPoolFromRemediationToleratesNonJSONObject: spec.current.object can hold a
// non-JSON Go value (int, not int64) on a hand-built or partially-converted
// remediation. poolFromRemediation must not panic, which is why it reads via
// NestedFieldNoCopy + type assertions instead of NestedMap (whose deep copy
// DeepCopyJSON-panics on such values). The fuzz builds only string-valued maps,
// so this covers the class the no-copy read exists for.
func TestPoolFromRemediationToleratesNonJSONObject(t *testing.T) {
	rem := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"current": map[string]any{"object": map[string]any{
			"kind": "MachineConfig",
			// int (not int64) is non-JSON: NestedMap over this map would panic.
			"metadata": map[string]any{"labels": map[string]any{"machineconfiguration.openshift.io/role": 42}},
		}}},
	}}
	rem.SetLabels(map[string]string{"compliance.openshift.io/scan-name": "ocp4-cis-node-worker"})
	// Must not panic; a non-string role is ignored and the pool falls back to the
	// scan-name suffix ("worker").
	if got := poolFromRemediation(rem); got != "worker" {
		t.Fatalf("poolFromRemediation with non-JSON role = %q, want worker (scan-name fallback)", got)
	}
}

func FuzzPoolFromRemediation(f *testing.F) {
	f.Add("MachineConfig", "worker", "ocp4-cis-node-master")
	f.Add("ConfigMap", "worker", "ocp4-cis-node-worker")
	f.Add("KubeletConfig", "", "ocp4-cis-node-worker")
	f.Add("APIServer", "", "ocp4-cis-api-server")
	f.Add("", "", "ocp4-cis-node-worker")
	f.Add("", "", "no-node-suffix")
	f.Add("MachineConfig", "", "profile-node-infra")
	f.Add("MachineConfig", "bad role", "profile-node-infra")
	f.Add("MachineConfig", "master", "")
	f.Add("", "ignored", "x-node-")
	f.Fuzz(func(t *testing.T, kind, role, scan string) {
		rem := &unstructured.Unstructured{Object: map[string]any{}}
		if scan != "" {
			rem.SetLabels(map[string]string{"compliance.openshift.io/scan-name": scan})
		}
		if kind != "" || role != "" {
			obj := map[string]any{}
			if kind != "" {
				obj["kind"] = kind
			}
			if role != "" {
				obj["metadata"] = map[string]any{
					"labels": map[string]any{"machineconfiguration.openshift.io/role": role},
				}
			}
			_ = unstructured.SetNestedMap(rem.Object, obj, "spec", "current", "object")
		}
		got := poolFromRemediation(rem)
		// Expected pool: a MachineConfig with a DNS-valid role label names it
		// precisely; otherwise ANY remediation from a "…-node-<pool>" scan
		// (KubeletConfig, partial render, or a MachineConfig with no/invalid role)
		// uses the scan-name pool. A non-node kind on a scan without "-node-"
		// yields "". The rendered kind never short-circuits the scan-name fallback.
		var want string
		if kind == "MachineConfig" && role != "" {
			want = validK8sName(role)
		}
		if want == "" {
			if i := strings.LastIndex(scan, "-node-"); i >= 0 {
				want = validK8sName(scan[i+len("-node-"):])
			}
		}
		if got != want {
			t.Fatalf("poolFromRemediation(kind=%q role=%q scan=%q) = %q, want %q", kind, role, scan, got, want)
		}
	})
}

// FuzzValidMCPPoolName: MachineConfig role labels and scan-name suffixes are
// untrusted cluster data. Must never panic; empty or non-DNS1123 -> ""; else identity.
func FuzzValidMCPPoolName(f *testing.F) {
	for _, seed := range []string{
		"", "worker", "master", "control-plane", "infra",
		"UPPER", "has_underscore", "has space", "a",
		strings.Repeat("a", 253), strings.Repeat("a", 254),
		"-leading", "trailing-", ".dot.", "worker.master",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		got := validK8sName(name)
		if name == "" {
			if got != "" {
				t.Fatalf("empty name returned %q", got)
			}
			return
		}
		if got == "" {
			// Rejected: must not be a valid DNS-1123 subdomain.
			if len(utilvalidation.IsDNS1123Subdomain(name)) == 0 {
				t.Fatalf("valid DNS-1123 %q was rejected", name)
			}
			return
		}
		if got != name {
			t.Fatalf("accepted name mutated: in=%q out=%q", name, got)
		}
		if len(utilvalidation.IsDNS1123Subdomain(got)) > 0 {
			t.Fatalf("accepted non-DNS1123 %q", got)
		}
	})
}

// FuzzBatchPastGrace: batch StartedAt from status/annotation is untrusted.
// Zero and far-future must trip the safety valve; modest skew must not.
func FuzzBatchPastGrace(f *testing.F) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	f.Add(int64(0), true) // metav1 zero via flag
	f.Add(now.Unix(), false)
	f.Add(now.Add(-batchResumeGrace-time.Second).Unix(), false)
	f.Add(now.Add(batchResumeGrace+time.Second).Unix(), false)
	f.Add(now.Add(30*time.Second).Unix(), false)
	f.Fuzz(func(t *testing.T, unix int64, forceZero bool) {
		var started metav1.Time
		if forceZero {
			started = metav1.Time{}
		} else {
			// Bound unix so time math stays well-defined.
			if unix < 0 {
				unix = -unix
			}
			unix = unix % (1 << 40)
			started = metav1.NewTime(time.Unix(unix, 0).UTC())
		}
		past := batchPastGrace(started, now)
		if started.IsZero() {
			if !past {
				t.Fatal("zero StartedAt must be past grace")
			}
			return
		}
		if started.After(now.Add(batchResumeGrace)) {
			if !past {
				t.Fatalf("far-future StartedAt %v must be past grace", started.Time)
			}
			return
		}
		want := now.Sub(started.Time) > batchResumeGrace
		if past != want {
			t.Fatalf("batchPastGrace(%v) = %v, want %v", started.Time, past, want)
		}
	})
}

// FuzzValidateBatchTarget: validateBatchTarget is the confused-deputy gate
// before the operator applies spec.apply with its own service account, and
// both fields it reads off a hand-editable ComplianceRemediation are untyped
// in an unstructured object. A wrong type must come back as a permanent
// reject (isPermanentBatchTargetReject) so a corrupt remediation cannot enter
// status.remediationBatch.Remediations and pin the wait path until grace, and
// a well-formed remediation must never be rejected.
func FuzzValidateBatchTarget(f *testing.F) {
	f.Add("Enabled", fuzzFieldString, fuzzFieldBool)
	f.Add("MissingDependencies", fuzzFieldString, fuzzFieldBool)
	f.Add("", fuzzFieldString, fuzzFieldBool)
	f.Add("Enabled", fuzzFieldString, fuzzFieldMissing)
	f.Add("Enabled", fuzzFieldInt, fuzzFieldBool)
	f.Add("Enabled", fuzzFieldString, fuzzFieldInt)
	f.Add("Enabled", fuzzFieldMap, fuzzFieldList)
	f.Add("Enabled", fuzzFieldMissing, fuzzFieldMissing)
	f.Add("Enabled", fuzzFieldNull, fuzzFieldString)
	f.Fuzz(func(t *testing.T, state string, stateField, applyField int) {
		if len(state) > 256 {
			state = state[:256]
		}
		rem := &unstructured.Unstructured{Object: map[string]any{}}
		rem.SetName("remediation")
		rem.Object["status"] = map[string]any{
			"applicationState": fuzzFieldValue(stateField, state),
		}
		spec := map[string]any{}
		if applyField != fuzzFieldMissing {
			spec["apply"] = fuzzFieldValue(applyField, state)
		}
		rem.Object["spec"] = spec

		err := validateBatchTarget(rem)
		stateOK := stateField == fuzzFieldString && state != "MissingDependencies"
		applyOK := applyField == fuzzFieldBool || applyField == fuzzFieldMissing
		if stateOK && applyOK {
			if err != nil {
				t.Fatalf("well-formed remediation rejected: state=%q stateField=%d applyField=%d: %v",
					state, stateField, applyField, err)
			}
			return
		}
		if err == nil {
			t.Fatalf("corrupt remediation accepted: state=%q stateField=%d applyField=%d",
				state, stateField, applyField)
		}
		if !isPermanentBatchTargetReject(err) {
			t.Fatalf("reject is not permanent (it retries and wedges the batch wait): %v", err)
		}
	})
}

const (
	fuzzFieldString = iota
	fuzzFieldBool
	fuzzFieldInt
	fuzzFieldFloat
	fuzzFieldMap
	fuzzFieldList
	fuzzFieldNull
	fuzzFieldMissing
)

// fuzzFieldValue renders one JSON-typed field. A fuzzer mutating a single
// string never reaches the type-confusion branches of NestedString and
// NestedBool, so the field type is an explicit dimension here.
func fuzzFieldValue(kind int, s string) any {
	switch kind {
	case fuzzFieldString:
		return s
	case fuzzFieldBool:
		return s == "true"
	case fuzzFieldInt:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			n = 42
		}
		return n
	case fuzzFieldFloat:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			f = 1.5
		}
		return f
	case fuzzFieldMap:
		return map[string]any{"key": s}
	case fuzzFieldList:
		return []any{s}
	case fuzzFieldNull:
		return nil
	default:
		return nil
	}
}
