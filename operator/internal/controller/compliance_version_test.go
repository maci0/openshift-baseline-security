package controller

import "testing"

// compareComplianceCSVVersion decides which installed CSV is newest. Names
// carrying the csvNamePrefix with an all-numeric core are compared as
// versions; anything else loses to every parseable name, and a tie between two
// names (equal versions, or two unparseable ones) falls back to string order so
// the comparison stays a total order. A negative result means a sorts before b.
func TestCompareComplianceCSVVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{name: "equal", a: "compliance-operator.v1.2.3", b: "compliance-operator.v1.2.3", want: 0},
		{name: "patch", a: "compliance-operator.v1.2.4", b: "compliance-operator.v1.2.3", want: 1},
		{name: "minor", a: "compliance-operator.v1.3.0", b: "compliance-operator.v1.2.9", want: 1},
		{name: "major", a: "compliance-operator.v2.0.0", b: "compliance-operator.v1.99.99", want: 1},
		{name: "not a decimal number", a: "compliance-operator.v1.10.0", b: "compliance-operator.v1.9.0", want: 1},
		{name: "equal part count tie falls back to string order", a: "compliance-operator.v1.2", b: "compliance-operator.v1.2.0", want: -1},
		{name: "missing trailing part sorts first", a: "compliance-operator.v1.2.3", b: "compliance-operator.v1.2.4", want: -1},
		{name: "prerelease sorts before release", a: "compliance-operator.v1.2.3-rc1", b: "compliance-operator.v1.2.3", want: -1},
		{name: "numeric prerelease segment by number", a: "compliance-operator.v1.2.3-alpha.10", b: "compliance-operator.v1.2.3-alpha.2", want: 1},
		{name: "non-numeric prerelease segment after numeric", a: "compliance-operator.v1.2.3-rc.rc", b: "compliance-operator.v1.2.3-rc.1", want: 1},
		{name: "alpha prerelease segment by string order", a: "compliance-operator.v1.2.3-rc10", b: "compliance-operator.v1.2.3-rc2", want: -1},
		{name: "longer prerelease wins on a shared prefix", a: "compliance-operator.v1.2.3-rc.1", b: "compliance-operator.v1.2.3-rc", want: 1},
		{name: "build metadata ties to string order", a: "compliance-operator.v1.2.3+build.7", b: "compliance-operator.v1.2.3", want: 1},
		{name: "build metadata differs", a: "compliance-operator.v1.2.3+build.2", b: "compliance-operator.v1.2.3+build.1", want: 1},
		{name: "prerelease tie with build metadata", a: "compliance-operator.v1.2.3-rc.1+meta", b: "compliance-operator.v1.2.3-rc.1", want: 1},
		{name: "core wins over prerelease", a: "compliance-operator.v1.3.0-rc1", b: "compliance-operator.v1.2.9", want: 1},
		{name: "parseable beats unparseable", a: "compliance-operator.v1.0.0", b: "compliance-operator.v1.0.0-1.2.3.4.5", want: 1},
		{name: "unparseable falls back to string order", a: "compliance-operator.v1.0.0-1.2", b: "compliance-operator.v1.0.0-1.10", want: -1},
		{name: "two unparseable names fall back to string order", a: "compliance-operator.abc", b: "compliance-operator.xyz", want: -1},
		{name: "empty version is unparseable", a: "compliance-operator.v", b: "compliance-operator.v1.0.0", want: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := compareComplianceCSVVersion(tc.a, tc.b)
			if sign(got) != tc.want {
				t.Errorf("compareComplianceCSVVersion(%q, %q) = %d, want sign %d", tc.a, tc.b, got, tc.want)
			}
			if back := compareComplianceCSVVersion(tc.b, tc.a); sign(back) != -tc.want {
				t.Errorf("antisymmetry: compareComplianceCSVVersion(%q, %q) = %d, want sign %d", tc.b, tc.a, back, -tc.want)
			}
		})
	}
}

func TestCompareComplianceCSVVersionTotalOrder(t *testing.T) {
	t.Parallel()
	// Every pair of these names must order the same way whichever way round it
	// is compared; a comparison that is not a strict weak order would let
	// pickComplianceOperatorCSV converge on a different CSV as the list order
	// changes.
	names := []string{
		"compliance-operator.v1.0.0",
		"compliance-operator.v1.0.1",
		"compliance-operator.v1.2.0",
		"compliance-operator.v1.2.0-rc1",
		"compliance-operator.v1.2.0-rc2",
		"compliance-operator.v1.2.0-rc2+build",
		"compliance-operator.v2.0.0",
		"compliance-operator.v1.0.0-x",
	}
	for _, a := range names {
		for _, b := range names {
			ab := sign(compareComplianceCSVVersion(a, b))
			ba := sign(compareComplianceCSVVersion(b, a))
			if ab != -ba {
				t.Errorf("compareComplianceCSVVersion(%q, %q) = %d but reversed = %d", a, b, ab, ba)
			}
			if a == b && ab != 0 {
				t.Errorf("compareComplianceCSVVersion(%q, %q) = %d, want 0", a, b, ab)
			}
		}
	}
	// Strict weak order: if a < b and b < c then a < c.
	for _, a := range names {
		for _, b := range names {
			if sign(compareComplianceCSVVersion(a, b)) != -1 {
				continue
			}
			for _, c := range names {
				if sign(compareComplianceCSVVersion(b, c)) == -1 &&
					sign(compareComplianceCSVVersion(a, c)) != -1 {
					t.Errorf("transitivity broken: %q < %q < %q but %q is not < %q", a, b, c, a, c)
				}
			}
		}
	}
}

func TestComplianceCSVVersionParse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		wantOK  bool
		parts   []int
		pre     string
		comment string
	}{
		{name: "three parts", in: "compliance-operator.v1.2.3", wantOK: true, parts: []int{1, 2, 3}},
		{name: "single part", in: "compliance-operator.v4", wantOK: true, parts: []int{4}},
		{name: "prerelease kept", in: "compliance-operator.v1.2.3-rc.1", wantOK: true, parts: []int{1, 2, 3}, pre: "rc.1"},
		{name: "build metadata dropped", in: "compliance-operator.v1.2.3+b", wantOK: true, parts: []int{1, 2, 3}},
		{name: "missing prefix", in: "other-operator.v1.2.3", wantOK: false},
		{name: "empty version", in: "compliance-operator.v", wantOK: false},
		{name: "empty part", in: "compliance-operator.v1..3", wantOK: false},
		{name: "trailing dot", in: "compliance-operator.v1.2.", wantOK: false},
		{name: "non numeric part", in: "compliance-operator.v1.2.3a", wantOK: false},
		{name: "negative part", in: "compliance-operator.v1.-2", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := complianceCSVVersion(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("complianceCSVVersion(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if len(got.parts) != len(tc.parts) {
				t.Fatalf("complianceCSVVersion(%q) parts = %v, want %v", tc.in, got.parts, tc.parts)
			}
			for i, p := range tc.parts {
				if got.parts[i] != p {
					t.Fatalf("complianceCSVVersion(%q) parts = %v, want %v", tc.in, got.parts, tc.parts)
				}
			}
			if got.prerelease != tc.pre {
				t.Errorf("complianceCSVVersion(%q) prerelease = %q, want %q", tc.in, got.prerelease, tc.pre)
			}
		})
	}
}
