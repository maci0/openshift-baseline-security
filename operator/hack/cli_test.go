// Contract tests for the scripts in this directory: --help is pipeable and
// exits 0, an unknown option exits 2 with usage on stderr, and extra
// arguments are rejected. resolve-release-version.sh additionally pins how a
// version from the dispatch input or the tag ref is parsed. The manager's own
// flag handling lives in ../cmd.
package hack_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scriptPath returns a runnable path: exec needs an explicit "./" or a
// separator to run a file from the test's working directory.
func scriptPath(t *testing.T, name string) string {
	t.Helper()
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("missing %s: %v", name, err)
	}
	return "./" + name
}

func runCmd(t *testing.T, name string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err == nil {
		return out.String(), errb.String(), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), errb.String(), ee.ExitCode()
	}
	t.Fatalf("%s %v: %v\nstdout:\n%s\nstderr:\n%s", name, args, err, out.String(), errb.String())
	return "", "", -1
}

func TestMustGatherHelp(t *testing.T) {
	script := scriptPath(t, "must-gather.sh")
	for _, flag := range []string{"--help", "-h"} {
		stdout, stderr, code := runCmd(t, script, flag)
		if code != 0 {
			t.Errorf("%s: exit %d, want 0; stderr=%q", flag, code, stderr)
		}
		if stderr != "" {
			t.Errorf("%s: stderr %q, want empty", flag, stderr)
		}
		if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "--self-test") {
			t.Errorf("%s: stdout missing usage:\n%s", flag, stdout)
		}
	}
}

func TestMustGatherUnknownOption(t *testing.T) {
	script := scriptPath(t, "must-gather.sh")
	stdout, stderr, code := runCmd(t, script, "--not-a-flag")
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr=%q", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout %q, want empty (usage errors go to stderr)", stdout)
	}
	if !strings.Contains(stderr, "unknown option") || !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr missing unknown-option usage:\n%s", stderr)
	}
	if _, err := os.Stat("--not-a-flag"); err == nil {
		t.Fatal("unknown option must not mkdir a directory named after the flag")
	}
}

func TestMustGatherExtraArgs(t *testing.T) {
	script := scriptPath(t, "must-gather.sh")
	_, stderr, code := runCmd(t, script, "outdir", "extra")
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "unexpected arguments") {
		t.Errorf("stderr missing unexpected arguments:\n%s", stderr)
	}
}

func TestMustGatherSelfTestRejectsExtraArgs(t *testing.T) {
	script := scriptPath(t, "must-gather.sh")
	_, stderr, code := runCmd(t, script, "--self-test", "extra")
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "--self-test takes no arguments") {
		t.Errorf("stderr=%q", stderr)
	}
}

// TestHackScriptUsageContract pins the contract every hack/ script shares:
// --help is pipeable (stdout, exit 0), a bad invocation is a usage error
// (stderr, exit 2, empty stdout) that names the script before the usage text,
// and the Usage line names the script by basename, not by a hack/ path. It
// walks the directory rather than a hand-maintained list, so a new script has
// to satisfy it too.
func TestHackScriptUsageContract(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, e := range entries {
		name := e.Name()
		// lib-*.sh are sourced libraries with no argument parsing, and
		// prometheusrule_to_rules.py has its own test below.
		if e.IsDir() || !strings.HasSuffix(name, ".sh") || strings.HasPrefix(name, "lib-") {
			continue
		}
		ran++
		script := scriptPath(t, name)
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code := runCmd(t, script, "--help")
			if code != 0 || stderr != "" {
				t.Fatalf("--help: exit %d, want 0; stderr=%q", code, stderr)
			}
			usageLine := firstLine(stdout)
			if want := "Usage: " + name; !strings.HasPrefix(usageLine, want) {
				t.Errorf("--help first line %q, want prefix %q", usageLine, want)
			}
			if strings.Contains(usageLine, "/") {
				t.Errorf("--help Usage line %q names a path, want the bare script name", usageLine)
			}
			// Every script accepts -h, so the usage text has to list it: an
			// accepted-but-undocumented spelling is a flag a reader cannot
			// discover, and it reads as a bug on the scripts that do document it.
			if !strings.Contains(stdout, "-h, --help") {
				t.Errorf("--help does not document -h:\n%s", stdout)
			}
			shortStdout, shortStderr, shortCode := runCmd(t, script, "-h")
			if shortCode != 0 || shortStderr != "" || shortStdout != stdout {
				t.Errorf("-h: exit %d, stderr %q, stdout %q; want exit 0, empty stderr, and the --help text", shortCode, shortStderr, shortStdout)
			}

			for _, args := range [][]string{{"--help", "extra"}, {"--not-a-flag"}} {
				stdout, stderr, code := runCmd(t, script, args...)
				if code != 2 {
					t.Errorf("%v: exit %d, want 2; stderr=%q", args, code, stderr)
				}
				if stdout != "" {
					t.Errorf("%v: stdout %q, want empty", args, stdout)
				}
				if want := name + ": "; !strings.HasPrefix(stderr, want) {
					t.Errorf("%v: stderr starts %q, want prefix %q", args, firstLine(stderr), want)
				}
				if !strings.Contains(stderr, "Usage:") {
					t.Errorf("%v: stderr missing the usage text:\n%s", args, stderr)
				}
			}
		})
	}
	if ran == 0 {
		t.Fatal("no scripts found; the contract test would pass vacuously")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func TestHackScriptHelp(t *testing.T) {
	for _, name := range []string{
		"resolve-release-version.sh",
		"test-alerts.sh",
		"verify-bundle-static.sh",
		"verify-csv-deploy.sh",
		"verify-image-metadata.sh",
		"verify-manifests.sh",
		"verify-product-lockstep.sh",
	} {
		script := scriptPath(t, name)
		stdout, stderr, code := runCmd(t, script, "--help")
		if code != 0 {
			t.Errorf("%s --help: exit %d, want 0; stderr=%q", name, code, stderr)
		}
		if stderr != "" {
			t.Errorf("%s --help: stderr %q, want empty", name, stderr)
		}
		if !strings.Contains(stdout, "Usage:") {
			t.Errorf("%s --help: stdout missing Usage:\n%s", name, stdout)
		}
		if strings.Contains(stdout, " ok\n") {
			t.Errorf("%s --help ran the check instead of printing usage", name)
		}
		_, stderr, code = runCmd(t, script, "--not-a-flag")
		if code != 2 {
			t.Errorf("%s --not-a-flag: exit %d, want 2; stderr=%q", name, code, stderr)
		}
	}
}

// runScriptEnv runs a script with env replaced by exactly the given
// key=value pairs, so a case cannot pass or fail on an inherited value.
func runScriptEnv(t *testing.T, name string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err == nil {
		return out.String(), errb.String(), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), errb.String(), ee.ExitCode()
	}
	t.Fatalf("%s %v: %v\nstdout:\n%s\nstderr:\n%s", name, args, err, out.String(), errb.String())
	return "", "", -1
}

// makefileVersion reads the VERSION the release script must agree with, so the
// test does not pin a version of its own.
func makefileVersion(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "Makefile"))
	if err != nil {
		t.Fatalf("reading ../Makefile: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "VERSION ?= "); ok {
			return strings.TrimSpace(v)
		}
	}
	t.Fatal("../Makefile has no VERSION ?= line")
	return ""
}

// TestResolveReleaseVersionInput: a version that is not MAJOR.MINOR.PATCH is
// rejected by shape, naming the source it came from, and the spellings a
// maintainer types into the dispatch box (leading v, pasted whitespace) resolve
// to the same cut as the bare version instead of reading as a mismatch.
func TestResolveReleaseVersionInput(t *testing.T) {
	script := scriptPath(t, "resolve-release-version.sh")
	ver := makefileVersion(t)

	// A well-formed input is pinned only on not being rejected for its shape
	// and on naming the resolved version. The release workflow runs this test
	// on the tagged commit, where the tag is HEAD and the script exits 0.
	// Every other clone is a provenance failure (exit 1): the tag is missing
	// ("git tag vX.Y.Z missing") or it points elsewhere ("refusing to publish
	// X.Y.Z"). Both messages name the version.
	for _, tc := range []struct{ name, input string }{
		{"bare", ver},
		{"leading-v", "v" + ver},
		{"padded", "  v" + ver + " \t\n"},
	} {
		stdout, stderr, code := runScriptEnv(t, script, []string{"INPUT_VERSION=" + tc.input})
		if strings.Contains(stderr, "invalid release version") {
			t.Errorf("%s: rejected a well-formed version: %q", tc.name, stderr)
		}
		switch code {
		case 0:
			if !strings.Contains(stdout, "VERSION="+ver+"\n") || !strings.Contains(stdout, "TAG=v"+ver+"\n") {
				t.Errorf("%s: exit 0 but stdout does not name %s: %q", tc.name, ver, stdout)
			}
		case 1:
			if !strings.Contains(stderr, ver) {
				t.Errorf("%s: stderr does not name the resolved version %s: %q", tc.name, ver, stderr)
			}
		default:
			t.Errorf("%s: exit %d, want 0 or 1; stderr=%q", tc.name, code, stderr)
		}
	}

	for _, tc := range []struct{ name, env, wantSource string }{
		{"short", "INPUT_VERSION=0.6", "INPUT_VERSION"},
		{"prerelease", "INPUT_VERSION=0.6.1-rc.1", "INPUT_VERSION"},
		{"not-a-version", "INPUT_VERSION=latest", "INPUT_VERSION"},
		{"newline", "INPUT_VERSION=0.6.1\nINJECTED=1", "INPUT_VERSION"},
		{"branch-ref", "GITHUB_REF_NAME=refs/heads/main", "GITHUB_REF_NAME"},
	} {
		stdout, stderr, code := runScriptEnv(t, script, []string{tc.env})
		if code != 2 {
			t.Errorf("%s: exit %d, want 2; stderr=%q", tc.name, code, stderr)
		}
		if !strings.Contains(stderr, "invalid release version from "+tc.wantSource) {
			t.Errorf("%s: stderr does not name the source: %q", tc.name, stderr)
		}
		// A rejected input must not reach the version the image build stamps
		// on stdout, or a caller capturing it would build the wrong version.
		if stdout != "" {
			t.Errorf("%s: rejected input wrote %q to stdout", tc.name, stdout)
		}
	}

	// No version from either source is a resolution failure, not a shape one.
	stdout, stderr, code := runScriptEnv(t, script, []string{"INPUT_VERSION=", "GITHUB_REF_NAME="})
	if code != 1 {
		t.Errorf("unset: exit %d, want 1; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "no release version") {
		t.Errorf("unset: stderr=%q", stderr)
	}
	if stdout != "" {
		t.Errorf("unset: unresolved version wrote %q to stdout", stdout)
	}
}

func python3(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("python3 is required on PATH")
	}
	return p
}

func TestPrometheusRuleToRulesHelp(t *testing.T) {
	script := scriptPath(t, "prometheusrule_to_rules.py")
	py := python3(t)
	stdout, stderr, code := runCmd(t, py, script, "--help")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "prometheusrule.yaml") {
		t.Errorf("stdout missing usage:\n%s", stdout)
	}
	if _, stderr, code = runCmd(t, py, script); code != 2 {
		t.Fatalf("no args: exit %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "expected 2 arguments") {
		t.Errorf("no args stderr=%q", stderr)
	}
	stdout, stderr, code = runCmd(t, py, script, "--bogus")
	if code != 2 {
		t.Fatalf("unknown option: exit %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "unknown option") {
		t.Errorf("unknown option stderr=%q", stderr)
	}
	// The same contract the shell scripts keep: stdout empty, the diagnostic
	// names the script, and the usage follows it rather than preceding it.
	if stdout != "" {
		t.Errorf("unknown option: stdout %q, want empty", stdout)
	}
	const want = "prometheusrule_to_rules.py: "
	if !strings.HasPrefix(stderr, want) {
		t.Errorf("stderr starts %q, want prefix %q", firstLine(stderr), want)
	}
	if i, j := strings.Index(stderr, want), strings.Index(stderr, "Usage:"); i < 0 || j < i {
		t.Errorf("usage must follow the diagnostic:\n%s", stderr)
	}
}

func TestPrometheusRuleToRulesExtract(t *testing.T) {
	script := scriptPath(t, "prometheusrule_to_rules.py")
	src := filepath.Join("..", "config", "prometheus", "prometheusrule.yaml")
	dst := filepath.Join(t.TempDir(), "rules.yaml")
	stdout, stderr, code := runCmd(t, python3(t), script, src, dst)
	if code != 0 {
		t.Fatalf("exit %d; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("extract should be quiet; stdout=%q stderr=%q", stdout, stderr)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.HasPrefix(got, "groups:") {
		t.Errorf("output should start with groups:, got %q", got)
	}
	if !strings.Contains(got, "ComplianceScoreLow") {
		t.Error("output missing ComplianceScoreLow")
	}
}

func TestPrometheusRuleToRulesMissingFile(t *testing.T) {
	script := scriptPath(t, "prometheusrule_to_rules.py")
	dst := filepath.Join(t.TempDir(), "rules.yaml")
	_, stderr, code := runCmd(t, python3(t), script, filepath.Join(t.TempDir(), "missing.yaml"), dst)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr=%q", code, stderr)
	}
	if stderr == "" {
		t.Error("missing file should explain the error on stderr")
	}
}
