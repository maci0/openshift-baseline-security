package controller

import (
	"strings"
	"testing"
)

// FuzzUnstructuredMetadataReads: CCR/remediation metadata maps are untrusted
// cluster JSON. Labels/annotations may be map[string]string, map[string]any, or
// the wrong type entirely. Helpers must never panic and must return "" on
// missing/wrong types; string values round-trip when present.
func FuzzUnstructuredMetadataReads(f *testing.F) {
	f.Add("name", "suite-a", "ann-v", "label-v", byte(0))
	f.Add("", "", "", "", byte(1))
	f.Add("x", "baseline-cis", "k", "v", byte(2))
	f.Add(strings.Repeat("n", 300), strings.Repeat("s", 300), "a", "l", byte(3))
	f.Fuzz(func(t *testing.T, name, suite, annVal, labelVal string, shape byte) {
		const max = 512
		if len(name) > max {
			name = name[:max]
		}
		if len(suite) > max {
			suite = suite[:max]
		}
		if len(annVal) > max {
			annVal = annVal[:max]
		}
		if len(labelVal) > max {
			labelVal = labelVal[:max]
		}

		meta := map[string]any{}
		if name != "" {
			meta["name"] = name
		}
		// Exercise typed and untyped maps, plus deliberate type confusion.
		switch shape % 4 {
		case 0:
			meta["labels"] = map[string]string{"compliance.openshift.io/suite": suite, "k": labelVal}
			meta["annotations"] = map[string]string{"a": annVal}
		case 1:
			meta["labels"] = map[string]any{"compliance.openshift.io/suite": suite, "k": labelVal}
			meta["annotations"] = map[string]any{"a": annVal}
		case 2:
			// Wrong types: must not panic; reads return "".
			meta["labels"] = suite
			meta["annotations"] = 42
			meta["name"] = []any{name}
		default:
			// Non-string values inside map[string]any.
			meta["labels"] = map[string]any{"compliance.openshift.io/suite": 7, "k": true}
			meta["annotations"] = map[string]any{"a": map[string]any{"nested": annVal}}
		}

		obj := map[string]any{"metadata": meta}
		// Never panic on any shape.
		gotName := unstructuredName(obj)
		gotLabel := unstructuredLabel(obj, "compliance.openshift.io/suite")
		gotAnn := unstructuredAnnotation(obj, "a")
		gotMissing := unstructuredLabel(obj, "does-not-exist")
		if gotMissing != "" {
			t.Fatalf("missing label returned %q", gotMissing)
		}
		// Empty / nil metadata.
		if unstructuredName(nil) != "" || unstructuredLabel(nil, "k") != "" {
			t.Fatal("nil object must yield empty reads")
		}
		if unstructuredName(map[string]any{}) != "" {
			t.Fatal("empty object must yield empty name")
		}

		switch shape % 4 {
		case 0, 1:
			if name != "" && gotName != name {
				t.Fatalf("name: got %q want %q", gotName, name)
			}
			if gotLabel != suite {
				t.Fatalf("suite label: got %q want %q", gotLabel, suite)
			}
			if gotAnn != annVal {
				t.Fatalf("annotation: got %q want %q", gotAnn, annVal)
			}
			// stringMapValue on typed map[string]string.
			if shape%4 == 0 {
				if v := stringMapValue(meta["labels"], "k"); v != labelVal {
					t.Fatalf("stringMapValue typed: got %q want %q", v, labelVal)
				}
			}
		case 2:
			// name was a non-string; labels/annotations wrong type.
			if gotName != "" || gotLabel != "" || gotAnn != "" {
				t.Fatalf("wrong types must yield empty: name=%q label=%q ann=%q", gotName, gotLabel, gotAnn)
			}
		default:
			// Non-string values inside any-maps: cast fails -> "".
			if gotLabel != "" || gotAnn != "" {
				t.Fatalf("non-string map values must yield empty: label=%q ann=%q", gotLabel, gotAnn)
			}
		}
	})
}
