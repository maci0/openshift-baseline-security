package main

import (
	"bytes"
	"errors"
	"flag"
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

// --help is pipeable, so it writes to the caller's writer and reports
// flag.ErrHelp; a bad invocation writes nothing there so a script capturing
// stdout on the exit-2 path sees no usage text mixed into its data.
func TestParseArgsWriters(t *testing.T) {
	var out bytes.Buffer
	if err := parseArgs([]string{"--help"}, &out); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("--help: got %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Errorf("--help wrote no usage to the help writer:\n%s", out.String())
	}

	for _, args := range [][]string{{"--not-a-flag"}, {"--leader-elect", "false"}} {
		out.Reset()
		err := parseArgs(args, &out)
		if err == nil {
			t.Fatalf("%v: want a usage error", args)
		}
		if errors.Is(err, flag.ErrHelp) {
			t.Errorf("%v: got flag.ErrHelp, want a usage error", args)
		}
		if out.Len() != 0 {
			t.Errorf("%v: wrote %q to the help writer, want nothing", args, out.String())
		}
	}
}
