package controller

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// boundedForLog exists so a hand-edited annotation cannot flood the log: the
// lines that use it repeat every reconcile while the failure persists, and the
// source lists run to the etcd object limit. It must bound the rendering
// without dropping, reordering, or rewriting the entries it does keep.
func TestBoundedForLog(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		if got := boundedForLog(nil); got != nil {
			t.Fatalf("boundedForLog(nil) = %v, want nil", got)
		}
	})
	t.Run("empty stays empty", func(t *testing.T) {
		if got := boundedForLog([]string{}); len(got) != 0 {
			t.Fatalf("boundedForLog([]) = %v, want empty", got)
		}
	})
	t.Run("under the bound is untouched", func(t *testing.T) {
		in := []string{"master", "worker"}
		got := boundedForLog(in)
		if len(got) != 2 || got[0] != "master" || got[1] != "worker" {
			t.Fatalf("boundedForLog(%v) = %v, want it unchanged", in, got)
		}
	})
	t.Run("over the item bound is marked, not silently dropped", func(t *testing.T) {
		in := make([]string, logListMaxItems+10)
		for i := range in {
			in[i] = "pool"
		}
		got := boundedForLog(in)
		if len(got) != logListMaxItems+1 {
			t.Fatalf("boundedForLog returned %d items, want %d plus a marker", len(got), logListMaxItems)
		}
		if got[len(got)-1] != "..." {
			t.Fatalf("last item = %q, want the truncation marker", got[len(got)-1])
		}
	})
	t.Run("long value is truncated without splitting a rune", func(t *testing.T) {
		got := boundedForLog([]string{strings.Repeat("é", logValueMaxLen*2)})
		if len([]rune(got[0])) != logValueMaxLen {
			t.Fatalf("truncated to %d runes, want %d", len([]rune(got[0])), logValueMaxLen)
		}
		if !utf8.ValidString(got[0]) {
			t.Fatalf("truncation split a multibyte character: %q", got[0])
		}
	})
}
