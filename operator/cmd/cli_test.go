package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func hackPath(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("..", "hack", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("missing %s: %v", p, err)
	}
	return p
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

func TestHackMustGatherHelp(t *testing.T) {
	script := hackPath(t, "must-gather.sh")
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

func TestHackMustGatherUnknownOption(t *testing.T) {
	script := hackPath(t, "must-gather.sh")
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

func TestHackMustGatherExtraArgs(t *testing.T) {
	script := hackPath(t, "must-gather.sh")
	_, stderr, code := runCmd(t, script, "outdir", "extra")
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "unexpected arguments") {
		t.Errorf("stderr missing unexpected arguments:\n%s", stderr)
	}
}

func TestHackMustGatherSelfTestRejectsExtraArgs(t *testing.T) {
	script := hackPath(t, "must-gather.sh")
	_, stderr, code := runCmd(t, script, "--self-test", "extra")
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "--self-test takes no arguments") {
		t.Errorf("stderr=%q", stderr)
	}
}

func TestHackVerifyAndTestAlertsHelp(t *testing.T) {
	for _, name := range []string{
		"test-alerts.sh",
		"verify-bundle-static.sh",
		"verify-product-lockstep.sh",
	} {
		script := hackPath(t, name)
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
		if strings.Contains(stdout, "verify-product-lockstep: ok") {
			t.Errorf("%s --help ran the check instead of printing usage", name)
		}
		_, stderr, code = runCmd(t, script, "--not-a-flag")
		if code != 2 {
			t.Errorf("%s --not-a-flag: exit %d, want 2; stderr=%q", name, code, stderr)
		}
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
	script := hackPath(t, "prometheusrule_to_rules.py")
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
	_, stderr, code = runCmd(t, py, script)
	if code != 2 {
		t.Fatalf("no args: exit %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "expected 2 arguments") {
		t.Errorf("no args stderr=%q", stderr)
	}
	_, stderr, code = runCmd(t, py, script, "--bogus")
	if code != 2 {
		t.Fatalf("unknown option: exit %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "unknown option") {
		t.Errorf("unknown option stderr=%q", stderr)
	}
}

func TestPrometheusRuleToRulesExtract(t *testing.T) {
	script := hackPath(t, "prometheusrule_to_rules.py")
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
	script := hackPath(t, "prometheusrule_to_rules.py")
	dst := filepath.Join(t.TempDir(), "rules.yaml")
	_, stderr, code := runCmd(t, python3(t), script, filepath.Join(t.TempDir(), "missing.yaml"), dst)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr=%q", code, stderr)
	}
	if stderr == "" {
		t.Error("missing file should explain the error on stderr")
	}
}
