package controller

import (
	"os"
	"strings"
	"testing"
)

func TestValidRelatedImage(t *testing.T) {
	for _, ref := range []string{
		"nginx",
		"quay.io/org/plugin:1.0",
		"registry.example.com:5000/ns/img:tag",
		"example.test/plugin@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		if !ValidRelatedImage(ref) {
			t.Errorf("%q should be valid", ref)
		}
	}
	for _, ref := range []string{
		"",
		"has space",
		"bad!!!",
		"cmd;inject",
		"$(boom)",
		"img%20name",
		"img#frag",
		`img\path`,
		strings.Repeat("a", 1025),
	} {
		if ValidRelatedImage(ref) {
			t.Errorf("%q should be invalid", ref)
		}
	}
}

// relatedImageConsolePlugin trims whitespace so a mis-set env (padding, empty
// quotes) does not create a Deployment with an unpullable image ref.
func TestRelatedImageConsolePluginTrim(t *testing.T) {
	const key = "RELATED_IMAGE_CONSOLE_PLUGIN"
	prev, had := os.LookupEnv(key)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})

	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	if got := relatedImageConsolePlugin(); got != "" {
		t.Fatalf("unset env = %q, want empty", got)
	}

	if err := os.Setenv(key, "   "); err != nil {
		t.Fatal(err)
	}
	if got := relatedImageConsolePlugin(); got != "" {
		t.Fatalf("whitespace-only env = %q, want empty", got)
	}

	if err := os.Setenv(key, "  quay.io/org/plugin:1.0  "); err != nil {
		t.Fatal(err)
	}
	if got := relatedImageConsolePlugin(); got != "quay.io/org/plugin:1.0" {
		t.Fatalf("padded env = %q, want trimmed image", got)
	}
}

// FuzzValidRelatedImage: RELATED_IMAGE_CONSOLE_PLUGIN is untrusted env text.
// Must never panic; rejects empty, oversize, control chars, and shell/URL noise;
// accepts only when at least one alnum is present and no forbidden metachar.
func FuzzValidRelatedImage(f *testing.F) {
	for _, seed := range []string{
		"", "nginx", "quay.io/org/plugin:1.0", "has space", "cmd;inject",
		"$(boom)", "img%20", "img#frag", `img\path`,
		strings.Repeat("a", 1024), strings.Repeat("a", 1025),
		"registry:5000/ns/img@sha256:dead", "!!!", "\x00img", "img\n",
	} {
		f.Add(seed)
	}
	// Bound work: oversize refs are a single reject path.
	const maxSeed = 2048
	f.Fuzz(func(t *testing.T, ref string) {
		if len(ref) > maxSeed {
			ref = ref[:maxSeed]
		}
		got := ValidRelatedImage(ref)
		if ref == "" || len(ref) > 1024 {
			if got {
				t.Fatalf("empty/oversize accepted: len=%d", len(ref))
			}
			return
		}
		for _, r := range ref {
			if r <= 0x20 || r == 0x7f {
				if got {
					t.Fatalf("control/space accepted: %q", ref)
				}
				return
			}
		}
		if strings.ContainsAny(ref, "<>|;&$`\\\"'*?[]{}()!%#\\") {
			if got {
				t.Fatalf("metachar accepted: %q", ref)
			}
			return
		}
		hasAlnum := false
		for _, r := range ref {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				hasAlnum = true
				break
			}
		}
		if got != hasAlnum {
			t.Fatalf("ValidRelatedImage(%q) = %v, want %v", ref, got, hasAlnum)
		}
	})
}

func TestWithoutPlugin(t *testing.T) {
	in := []string{"a", pluginName, "b", pluginName}
	got := withoutPlugin(in, pluginName)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("%v", got)
	}
	// Input must not be mutated.
	if len(in) != 4 {
		t.Fatalf("input mutated: %v", in)
	}
	if len(withoutPlugin([]string{"x"}, pluginName)) != 1 {
		t.Fatal("untouched when absent")
	}
	if len(withoutPlugin(nil, pluginName)) != 0 {
		t.Fatal("nil input")
	}
}

func FuzzWithoutPlugin(f *testing.F) {
	f.Add("a,b,c", "b")
	f.Add("", "x")
	f.Add("x", "x")
	f.Fuzz(func(t *testing.T, csv, drop string) {
		var in []string
		if csv != "" {
			in = strings.Split(csv, ",")
		}
		origLen := len(in)
		got := withoutPlugin(in, drop)
		if len(in) != origLen {
			t.Fatal("input mutated")
		}
		for _, p := range got {
			if p == drop {
				t.Fatalf("drop %q still present in %v", drop, got)
			}
		}
		for _, p := range got {
			found := false
			for _, o := range in {
				if o == p {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("extra element %q", p)
			}
		}
	})
}
