package controller

import (
	"strconv"
	"strings"
)

type complianceVersion struct {
	parts      []int
	prerelease string
}

func compareComplianceCSVVersion(a, b string) int {
	av, aok := complianceCSVVersion(a)
	bv, bok := complianceCSVVersion(b)
	switch {
	case aok && bok:
		if cmp := compareComplianceVersions(av, bv); cmp != 0 {
			return cmp
		}
		return strings.Compare(a, b)
	case aok:
		return 1
	case bok:
		return -1
	default:
		return strings.Compare(a, b)
	}
}

func complianceCSVVersion(name string) (complianceVersion, bool) {
	v, ok := strings.CutPrefix(name, "compliance-operator.v")
	if !ok || v == "" {
		return complianceVersion{}, false
	}
	v, _, _ = strings.Cut(v, "+")
	core, _, _ := strings.Cut(v, "-")
	_, prerelease, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		if p == "" {
			return complianceVersion{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return complianceVersion{}, false
		}
		out[i] = n
	}
	return complianceVersion{parts: out, prerelease: prerelease}, true
}

func compareComplianceVersions(a, b complianceVersion) int {
	if cmp := compareVersionParts(a.parts, b.parts); cmp != 0 {
		return cmp
	}
	switch {
	case a.prerelease == "" && b.prerelease != "":
		return 1
	case a.prerelease != "" && b.prerelease == "":
		return -1
	case a.prerelease != "" && b.prerelease != "":
		return comparePrerelease(a.prerelease, b.prerelease)
	default:
		return 0
	}
}

func compareVersionParts(a, b []int) int {
	n := max(len(a), len(b))
	for i := range n {
		var av, bv int
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	return 0
}

func comparePrerelease(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	n := min(len(ap), len(bp))
	for i := range n {
		ai, aNum := parsePrereleaseNumber(ap[i])
		bi, bNum := parsePrereleaseNumber(bp[i])
		switch {
		case aNum && bNum && ai != bi:
			if ai > bi {
				return 1
			}
			return -1
		case aNum && !bNum:
			return -1
		case !aNum && bNum:
			return 1
		case !aNum && !bNum:
			if cmp := strings.Compare(ap[i], bp[i]); cmp != 0 {
				return cmp
			}
		}
	}
	switch {
	case len(ap) > len(bp):
		return 1
	case len(ap) < len(bp):
		return -1
	default:
		return 0
	}
}

func parsePrereleaseNumber(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}
