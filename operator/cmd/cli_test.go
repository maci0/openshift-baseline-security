package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
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

// Every flag the process registers is spelled --long in the help, matching the
// README, the CSV args, and the usage errors. The flag package's own renderer
// would print -long, and a reader who has just been told "--metrics-secure
// takes no separate value" should not then see -metrics-secure in the help.
// TestMain registers the real process flag surface on the test binary's
// FlagSet, so the help assertions below run against the flags the manager
// actually accepts rather than only the ones a package init happens to add.
func TestMain(m *testing.M) {
	registerFlags(flag.CommandLine)
	os.Exit(m.Run())
}

func TestPrintUsageUsesLongFlags(t *testing.T) {
	var buf bytes.Buffer
	if err := printUsage(&buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	lines := strings.Split(got, "\n")
	for _, name := range []string{
		"health-probe-bind-address",
		"kubeconfig",
		"leader-elect",
		"metrics-bind-address",
		"metrics-cert-dir",
		"metrics-secure",
		"version",
		"zap-devel",
		"zap-encoder",
		"zap-log-level",
	} {
		if !hasHelpLine(lines, "  --"+name) {
			t.Errorf("help does not render --%s in long form:\n%s", name, got)
		}
	}
	// A default worth documenting is shown; a zero default is not noise.
	if !strings.Contains(got, "--leader-elect") || !strings.Contains(got, "(default true)") {
		t.Errorf("help hides the leader-elect default:\n%s", got)
	}
	if strings.Contains(got, "(default false)") {
		t.Errorf("help shows a zero default:\n%s", got)
	}
}

// hasHelpLine reports whether any help line starts with the given prefix. The
// flag column is padded, so a short flag is followed by spaces and a long one
// by the flag's own usage text.
// An undefined flag is reported in the long form the help and the docs use,
// with the nearest real flag named when the miss is close enough that the
// guess is worth making.
func TestUnknownFlagError(t *testing.T) {
	for _, tc := range []struct{ given, want string }{
		{"--metrcs-secure", "unknown flag: --metrcs-secure; did you mean --metrics-secure?"},
		{"--leadre-elect", "unknown flag: --leadre-elect; did you mean --leader-elect?"},
		{"--nope", "unknown flag: --nope"},
		{"--zzzzzzzzzz", "unknown flag: --zzzzzzzzzz"},
	} {
		got := unknownFlagError(errors.New("flag provided but not defined: -" + strings.TrimPrefix(tc.given, "--")))
		if got.Error() != tc.want {
			t.Errorf("%s: %q, want %q", tc.given, got, tc.want)
		}
	}
	// An error from somewhere else is passed through untouched.
	other := errors.New("invalid value for flag")
	if got := unknownFlagError(other); !errors.Is(got, other) {
		t.Errorf("non-flag error: %v, want it returned unchanged", got)
	}
}

func hasHelpLine(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// The KUBECONFIG env var is only meaningful if --kubeconfig is a real flag:
// the help documents one precedence, so the flag set must carry both. The flag
// comes from clientconfig, so pin it rather than trusting the import.
func TestKubeconfigFlagIsDefined(t *testing.T) {
	if flag.Lookup(clientconfig.KubeconfigFlagName) == nil {
		t.Fatalf("--%s is not registered; the documented KUBECONFIG precedence is unreachable",
			clientconfig.KubeconfigFlagName)
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
