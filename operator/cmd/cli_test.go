package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUnexpectedArgsError(t *testing.T) {
	if err := unexpectedArgsError(nil); err != nil {
		t.Fatalf("nil args: %v", err)
	}
	if err := unexpectedArgsError([]string{}); err != nil {
		t.Fatalf("empty args: %v", err)
	}
	err := unexpectedArgsError([]string{"false"})
	if err == nil || !strings.Contains(err.Error(), "false") {
		t.Fatalf("leftover bool literal: %v", err)
	}
	err = unexpectedArgsError([]string{"extra", "--help"})
	if err == nil || !strings.Contains(err.Error(), "extra --help") {
		t.Fatalf("multiple leftovers: %v", err)
	}
}

func TestPrintUsageIncludesEnv(t *testing.T) {
	var buf bytes.Buffer
	if err := printUsage(&buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{
		"Usage:",
		"Flags:",
		"Environment:",
		envSkipDefaultCR,
		"RELATED_IMAGE_CONSOLE_PLUGIN",
		"KUBECONFIG",
		"--kubeconfig wins",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("printUsage missing %q\n%s", want, got)
		}
	}
}
