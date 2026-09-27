package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"

	clientconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
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

// The common mistake is a boolean flag written with a space, so the leftover
// token is the value; the message has to name the fix.
func TestUnexpectedArgsErrorBooleanHint(t *testing.T) {
	for _, arg := range []string{"false", "true", "0", "1"} {
		err := unexpectedArgsError([]string{arg})
		if err == nil {
			t.Fatalf("%q: want a usage error", arg)
		}
		if !strings.Contains(err.Error(), "--flag="+arg) {
			t.Errorf("%q: message does not name the --flag=value fix: %v", arg, err)
		}
	}
	// A non-boolean leftover keeps the plain positional message.
	if err := unexpectedArgsError([]string{"false", "true"}); strings.Contains(err.Error(), "--flag=") {
		t.Errorf("multiple leftovers: %v", err)
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
		"--kubeconfig",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("printUsage missing %q\n%s", want, got)
		}
	}
}

// main.go calls clientconfig.RegisterFlags(flag.CommandLine) so the flag set is
// not at the mercy of a transitive package init. Two properties make that call
// safe and useful: the flag defaults to empty, so KUBECONFIG stays meaningful,
// and registering a second time on an already-populated FlagSet is a no-op
// rather than a duplicate-definition panic. Asserting on flag.CommandLine
// proves neither, since controller-runtime's own init has already defined the
// flag there before any test runs.
func TestKubeconfigFlagRegistration(t *testing.T) {
	fs := flag.NewFlagSet("fresh", flag.ContinueOnError)
	clientconfig.RegisterFlags(fs)
	f := fs.Lookup(clientconfig.KubeconfigFlagName)
	if f == nil {
		t.Fatalf("--%s is not registered; the documented KUBECONFIG precedence is unreachable",
			clientconfig.KubeconfigFlagName)
	}
	if f.DefValue != "" {
		t.Fatalf("--%s defaults to %q; KUBECONFIG would be shadowed",
			clientconfig.KubeconfigFlagName, f.DefValue)
	}
	// The real binary registers on a FlagSet that controller-runtime's init
	// already populated, so re-registration must not redefine the flag.
	clientconfig.RegisterFlags(fs)
	if again := fs.Lookup(clientconfig.KubeconfigFlagName); again != f {
		t.Fatalf("re-registering --%s replaced the flag; the process flag set would panic at startup",
			clientconfig.KubeconfigFlagName)
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
