package controller

import (
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
func FuzzPoolFromRemediation(f *testing.F) {
	f.Add("MachineConfig", "worker", "ocp4-cis-node-master")
	f.Add("ConfigMap", "worker", "ocp4-cis-node-worker")
	f.Add("", "", "ocp4-cis-node-worker")
	f.Add("", "", "no-node-suffix")
	f.Add("MachineConfig", "", "profile-node-infra")
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
		if kind != "" && kind != "MachineConfig" {
			if got != "" {
				t.Fatalf("non-MachineConfig kind %q returned pool %q", kind, got)
			}
			return
		}
		if kind == "MachineConfig" && role != "" {
			// Invalid DNS-1123 role labels are dropped (same as validMCPPoolName).
			want := validMCPPoolName(role)
			if got != want {
				t.Fatalf("MachineConfig role %q, got pool %q want %q", role, got, want)
			}
			return
		}
		// Scan-name fallback: last "-node-" segment when DNS-valid, or empty.
		if i := strings.LastIndex(scan, "-node-"); i >= 0 {
			want := validMCPPoolName(scan[i+len("-node-"):])
			if got != want {
				t.Fatalf("scan fallback: scan=%q got %q want %q", scan, got, want)
			}
		} else if got != "" {
			t.Fatalf("no role/scan pool, got %q", got)
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
		got := validMCPPoolName(name)
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
