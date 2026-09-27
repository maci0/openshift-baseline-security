// Contract tests for backup.sh and restore.sh: the backup/restore round trip
// must preserve the durable fields, and every validation guard must refuse a
// damaged artifact before it writes anything to the cluster.
//
// The scripts are driven for real, with a fake `oc` first on PATH, so these
// tests exercise the shipped entry points rather than a reimplementation of
// them. The fake records every invocation so the tests can assert the restore
// wrote the status subresource, which `oc apply` alone would silently drop.
package hack_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// resealManifest recomputes the sha256 line in a backup directory's MANIFEST
// from the current artifact. Cases that must be refused for a reason other
// than a checksum mismatch need this, otherwise the checksum gate refuses them
// first and the case proves nothing about the guard it names.
func resealManifest(t *testing.T, artifactPath string) {
	t.Helper()
	raw, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(artifactPath)
	manifestPath := filepath.Join(dir, "MANIFEST")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	lines := strings.Split(string(manifest), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "sha256=") {
			lines[i] = fmt.Sprintf("sha256=%x", sum)
		}
	}
	if err := os.WriteFile(manifestPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// baselineYAML is a captured ClusterBaseline/cluster: the user-owned spec
// (waivers with their audit attribution, schedule, scoring mode) and the
// derived status the Compliance Operator rollup produces (score, conditions,
// score history, an in-flight remediation batch). Those annotations are the
// only record of batch progress if the object is lost mid-pause.
const baselineYAML = `apiVersion: baselinesecurity.openshift.io/v1alpha1
kind: ClusterBaseline
metadata:
  name: cluster
  resourceVersion: "41237"
  uid: 6f0b1c2a-7d3e-4a55-9b21-0c8e5f1d4a90
  annotations:
    baselinesecurity.openshift.io/batch-apply: api-check,file-permission
    baselinesecurity.openshift.io/batch-started-at: "2026-09-20T10:04:11Z"
    baselinesecurity.openshift.io/batch-pools: worker
    baselinesecurity.openshift.io/batch-pause-owner: "6f0b1c2a-7d3e-4a55-9b21-0c8e5f1d4a90"
    baselinesecurity.openshift.io/history-scoring-mode: Flat
spec:
  schedule: "0 3 * * *"
  scoringMode: Flat
  waivers:
    - name: legacy-tls
      reason: upstream not migrated
      expiresAt: "2027-01-01T00:00:00Z"
      requestedBy: alice
      approvedBy: bob
status:
  lastScanTime: "2026-09-20T03:02:44Z"
  score: 87
  history:
    - scanTime: "2026-09-20T03:02:44Z"
      score: 87
  conditions:
    - type: Available
      status: "True"
      reason: ScanComplete
      message: score 87 of 100
  remediationBatch:
    startedAt: "2026-09-20T10:04:11Z"
    pools:
      - worker
    pauseOwner: "6f0b1c2a-7d3e-4a55-9b21-0c8e5f1d4a90"
`

// fakeOC installs a stub `oc` on PATH for the duration of the test. get
// returns captured; a get that asks for a jsonpath field returns
// FAKE_OC_RESOURCE_VERSION (empty by default, which reads as "no live object",
// the case a restore onto a recovered cluster is in). Every other subcommand
// is appended to the call log. The stub must tolerate the --request-timeout
// flag both scripts pass on every call, so it drops leading global flags
// before dispatching.
func fakeOC(t *testing.T, dir, captured string) (logfile string) {
	t.Helper()
	stub := filepath.Join(dir, "oc")
	script := "#!/usr/bin/env bash\n" +
		"args=()\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$a\" in --request-timeout=*) continue ;; esac\n" +
		"  args+=(\"$a\")\n" +
		"done\n" +
		"set -- \"${args[@]}\"\n" +
		"case \"${1:-}\" in\n" +
		"  whoami) echo kube:admin; exit 0 ;;\n" +
		"  get)\n" +
		"    for a in \"$@\"; do\n" +
		"      case \"$a\" in jsonpath=*) printf '%s' \"${FAKE_OC_RESOURCE_VERSION:-}\"; exit 0 ;; esac\n" +
		"    done\n" +
		"    cat <<'CAPTURED'\n" + captured + "CAPTURED\nexit 0 ;;\n" +
		"esac\n" +
		"printf '%s\\n' \"$*\" >> " + filepath.Join(dir, "oc.log") + "\n" +
		"exit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatalf("writing fake oc: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return filepath.Join(dir, "oc.log")
}

func ocCalls(t *testing.T, logfile string) string {
	t.Helper()
	b, err := os.ReadFile(logfile)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("reading oc call log: %v", err)
	}
	return string(b)
}

// sha256Hex digests a file with the standard library. The tests must not
// shell out to a checksum tool: `sha256sum` is GNU coreutils and absent on
// macOS, which is a supported host for `make test`.
func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// runScript runs a hack/ script with a controlled working directory. The
// script path is resolved against the test's own directory first: cmd.Dir
// moves the child off it, so a relative path would not resolve.
func runScript(t *testing.T, name string, workdir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	abs, err := filepath.Abs(scriptPath(t, name))
	if err != nil {
		t.Fatalf("resolving %s: %v", name, err)
	}
	cmd := exec.CommandContext(t.Context(), abs, args...)
	cmd.Dir = workdir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
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

func TestBackupRestoreRoundTripPreservesDurableState(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	// The live object is exactly what the backup holds, so the staleness guard
	// sees matching resourceVersions and lets the restore through.
	t.Setenv("FAKE_OC_RESOURCE_VERSION", "41237")
	work := t.TempDir()

	if _, stderr, code := runScript(t, "backup.sh", work, "bdir"); code != 0 {
		t.Fatalf("backup.sh: exit %d, want 0; stderr=%s", code, stderr)
	}

	artifact := filepath.Join(work, "bdir", "clusterbaseline.yaml")
	got, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("backup produced no artifact: %v", err)
	}
	if string(got) != baselineYAML {
		t.Errorf("captured artifact differs from the object:\n--- got ---\n%s\n--- want ---\n%s", got, baselineYAML)
	}

	// The manifest is what makes the artifact verifiable; a backup with no
	// checksum is the same as no backup, since corruption is silent.
	manifest, err := os.ReadFile(filepath.Join(work, "bdir", "MANIFEST"))
	if err != nil {
		t.Fatalf("no MANIFEST: %v", err)
	}
	for _, key := range []string{"takenAt=", "resourceVersion=41237", "uid=6f0b1c2a", "sha256="} {
		if !strings.Contains(string(manifest), key) {
			t.Errorf("MANIFEST missing %q:\n%s", key, manifest)
		}
	}

	if _, stderr, code := runScript(t, "restore.sh", work, "bdir"); code != 0 {
		t.Fatalf("restore.sh: exit %d, want 0; stderr=%s", code, stderr)
	}

	calls := ocCalls(t, log)
	if !strings.Contains(calls, "apply -f") {
		t.Errorf("restore did not apply the spec; oc calls:\n%s", calls)
	}
	// The load-bearing assertion. `oc apply` ignores the status subresource,
	// so a restore that only applies silently loses the score, the history,
	// and the in-flight batch.
	if !strings.Contains(calls, "replace --subresource=status -f") {
		t.Errorf("restore did not replace the status subresource; oc calls:\n%s", calls)
	}
}

// pathWithoutSHA256Sum symlinks every executable on the current PATH into a
// fresh directory, minus `sha256sum`, and puts that directory on PATH. It
// reproduces a host with no GNU coreutils (macOS, unless Homebrew installed
// them), where the digest has to come from shasum or openssl instead.
func pathWithoutSHA256Sum(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("host has no sha256sum; the fallback is already the only path")
	}
	farm := t.TempDir()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || e.Name() == "sha256sum" {
				continue
			}
			info, err := e.Info()
			if err != nil || info.Mode()&0o111 == 0 {
				continue
			}
			link := filepath.Join(farm, e.Name())
			if _, err := os.Lstat(link); err == nil {
				continue
			}
			if err := os.Symlink(filepath.Join(dir, e.Name()), link); err != nil {
				t.Fatalf("symlinking %s: %v", e.Name(), err)
			}
		}
	}
	for _, fallback := range []string{"shasum", "openssl"} {
		if _, err := os.Stat(filepath.Join(farm, fallback)); err == nil {
			t.Setenv("PATH", farm)
			return
		}
	}
	t.Skip("host has neither shasum nor openssl; no fallback to exercise")
}

func TestBackupRestoreWithoutGNUCoreutils(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	work := t.TempDir()
	pathWithoutSHA256Sum(t)

	if _, stderr, code := runScript(t, "backup.sh", work, "bdir"); code != 0 {
		t.Fatalf("backup.sh without sha256sum: exit %d, want 0; stderr=%s", code, stderr)
	}
	artifact := filepath.Join(work, "bdir", "clusterbaseline.yaml")
	manifest, err := os.ReadFile(filepath.Join(work, "bdir", "MANIFEST"))
	if err != nil {
		t.Fatalf("no MANIFEST: %v", err)
	}
	want := "sha256=" + sha256Hex(t, artifact) + "\n"
	if !strings.Contains(string(manifest), want) {
		t.Errorf("MANIFEST digest is not the artifact's; want it to contain %q:\n%s", want, manifest)
	}

	// A digest written by the fallback must still verify: restore recomputes
	// it and has to reach the apply.
	if _, stderr, code := runScript(t, "restore.sh", work, "bdir"); code != 0 {
		t.Fatalf("restore.sh without sha256sum: exit %d, want 0; stderr=%s", code, stderr)
	}
	if calls := ocCalls(t, log); !strings.Contains(calls, "apply -f") {
		t.Errorf("restore did not apply the spec; oc calls:\n%s", calls)
	}
}

func TestBackupRefusesEmptyCapture(t *testing.T) {
	bin := t.TempDir()
	fakeOC(t, bin, "")
	work := t.TempDir()

	_, stderr, code := runScript(t, "backup.sh", work, "bdir")
	if code == 0 {
		t.Fatal("backup of a zero-byte capture succeeded; a faked zero must not read as a healthy backup")
	}
	if !strings.Contains(stderr, "empty") {
		t.Errorf("stderr %q, want it to name the empty capture", stderr)
	}
	if _, err := os.Stat(filepath.Join(work, "bdir", "MANIFEST")); err == nil {
		t.Error("a failed backup left a MANIFEST behind")
	}
}

func TestBackupRefusesNonBaselineObject(t *testing.T) {
	bin := t.TempDir()
	fakeOC(t, bin, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: not-a-baseline\n")
	work := t.TempDir()

	_, stderr, code := runScript(t, "backup.sh", work, "bdir")
	if code == 0 {
		t.Fatal("backing up a non-ClusterBaseline object succeeded")
	}
	if !strings.Contains(stderr, "ClusterBaseline") {
		t.Errorf("stderr %q, want it to name the wrong kind", stderr)
	}
}

func TestRestoreRejectsTamperedArtifact(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, work string)
		want   string
	}{
		{
			name: "truncated mid-transfer",
			mutate: func(t *testing.T, work string) {
				path := filepath.Join(work, "bdir", "clusterbaseline.yaml")
				if err := os.WriteFile(path, []byte(baselineYAML[:len(baselineYAML)/2]), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "checksum mismatch",
		},
		{
			name: "status edited after the backup",
			mutate: func(t *testing.T, work string) {
				path := filepath.Join(work, "bdir", "clusterbaseline.yaml")
				if err := os.WriteFile(path, []byte(strings.Replace(baselineYAML, "score: 87", "score: 100", 1)), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "checksum mismatch",
		},
		{
			name: "manifest absent",
			mutate: func(t *testing.T, work string) {
				if err := os.Remove(filepath.Join(work, "bdir", "MANIFEST")); err != nil {
					t.Fatal(err)
				}
			},
			want: "unverifiable",
		},
		{
			name: "manifest without a checksum",
			mutate: func(t *testing.T, work string) {
				if err := os.WriteFile(filepath.Join(work, "bdir", "MANIFEST"), []byte("takenAt=x\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "unverifiable",
		},
		{
			name: "artifact is another api group",
			mutate: func(t *testing.T, work string) {
				if err := os.WriteFile(filepath.Join(work, "bdir", "clusterbaseline.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "not a baselinesecurity.openshift.io object",
		},
		{
			// oc apply/replace write EVERY document in a multi-doc YAML, and
			// the MANIFEST checksum is recomputable by anyone holding the
			// directory, so a second document is a privilege escalation the
			// kind checks above cannot see.
			name: "second document smuggled after the ClusterBaseline",
			mutate: func(t *testing.T, work string) {
				path := filepath.Join(work, "bdir", "clusterbaseline.yaml")
				smuggled := baselineYAML +
					"---\napiVersion: rbac.authorization.k8s.io/v1\n" +
					"kind: ClusterRoleBinding\nmetadata:\n  name: escalate\n" +
					"roleRef:\n  apiGroup: rbac.authorization.k8s.io\n  kind: ClusterRole\n  name: cluster-admin\n"
				if err := os.WriteFile(path, []byte(smuggled), 0o600); err != nil {
					t.Fatal(err)
				}
				// Re-sign so the checksum gate passes and the multi-document
				// check is what refuses.
				resealManifest(t, path)
			},
			want: "more than one YAML document",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			log := fakeOC(t, bin, baselineYAML)
			work := t.TempDir()

			if _, stderr, code := runScript(t, "backup.sh", work, "bdir"); code != 0 {
				t.Fatalf("backup.sh: exit %d, want 0; stderr=%s", code, stderr)
			}
			tc.mutate(t, work)

			_, stderr, code := runScript(t, "restore.sh", work, "bdir")
			if code == 0 {
				t.Fatal("restore accepted a damaged artifact")
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr %q, want it to mention %q", stderr, tc.want)
			}
			// The refusal must happen before any write: a partial apply would
			// let the operator reconcile a half-restored object and overwrite
			// the evidence of what was lost.
			if calls := ocCalls(t, log); strings.Contains(calls, "apply -f") {
				t.Errorf("restore wrote to the cluster before validating:\n%s", calls)
			}
		})
	}
}

func TestRestoreWarnsOnFutureLastScanTime(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	work := t.TempDir()

	if _, stderr, code := runScript(t, "backup.sh", work, "bdir"); code != 0 {
		t.Fatalf("backup.sh: exit %d, want 0; stderr=%s", code, stderr)
	}
	// Re-sign the artifact so only the clock case is under test.
	path := filepath.Join(work, "bdir", "clusterbaseline.yaml")
	future := strings.Replace(baselineYAML, `lastScanTime: "2026-09-20T03:02:44Z"`, `lastScanTime: "2099-01-01T00:00:00Z"`, 1)
	if err := os.WriteFile(path, []byte(future), 0o600); err != nil {
		t.Fatal(err)
	}
	resealManifest(t, path)

	_, stderr, code := runScript(t, "restore.sh", work, "bdir")
	if code != 0 {
		t.Fatalf("restore.sh: exit %d, want 0; stderr=%s", code, stderr)
	}
	// A future LastScanTime freezes the score until it is cleared, and the
	// operator is not watching when that happens. It must be called out.
	if !strings.Contains(stderr, "lastScanTime") || !strings.Contains(stderr, "future") {
		t.Errorf("restore did not warn about the frozen-clock case:\n%s", stderr)
	}
	if calls := ocCalls(t, log); !strings.Contains(calls, "replace --subresource=status -f") {
		t.Errorf("a warned-about restore must still proceed; oc calls:\n%s", calls)
	}
}

// backupDir writes a backup directory whose artifact is baselineYAML and
// whose MANIFEST carries the given takenAt, re-signing the artifact so the
// checksum always matches. It is the starting point for the tests that are
// about one specific property of an otherwise good backup.
func backupDir(t *testing.T, work, name, takenAt string) string {
	t.Helper()
	dir := filepath.Join(work, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "clusterbaseline.yaml")
	if err := os.WriteFile(path, []byte(baselineYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, err := exec.CommandContext(t.Context(), "sha256sum", path).Output()
	if err != nil {
		t.Fatalf("sha256sum: %v", err)
	}
	digest := strings.SplitN(string(sum), " ", 2)[0]
	manifest := "takenAt=" + takenAt + "\nresourceVersion=41237\nuid=6f0b1c2a\nsha256=" + digest + "\n"
	if err := os.WriteFile(filepath.Join(dir, "MANIFEST"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRestoreRefusesToRollBackAMovedOnObject(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	// The live object carries waiver edits made after the backup. Applying
	// the artifact discards them, and nothing else records them.
	t.Setenv("FAKE_OC_RESOURCE_VERSION", "41999")
	work := t.TempDir()
	dir := backupDir(t, work, "bdir", "2026-09-20T03:00:00Z")

	_, stderr, code := runScript(t, "restore.sh", work, dir)
	if code == 0 {
		t.Fatal("restore overwrote a live object that had moved on since the backup")
	}
	if !strings.Contains(stderr, "41999") || !strings.Contains(stderr, "41237") {
		t.Errorf("stderr %q, want both resourceVersions named", stderr)
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("stderr %q, want the override named", stderr)
	}
	if calls := ocCalls(t, log); strings.Contains(calls, "apply -f") {
		t.Errorf("the refused restore still wrote to the cluster:\n%s", calls)
	}

	// --force is how an operator says they meant it, and then it proceeds.
	if _, stderr, code := runScript(t, "restore.sh", work, "--force", dir); code != 0 {
		t.Fatalf("restore.sh --force: exit %d, want 0; stderr=%s", code, stderr)
	}
	calls := ocCalls(t, log)
	if !strings.Contains(calls, "apply -f") || !strings.Contains(calls, "replace --subresource=status -f") {
		t.Errorf("--force did not complete the restore; oc calls:\n%s", calls)
	}
}

// The RPO a restore buys is the age of the artifact, so the age has to be on
// screen at restore time rather than in a doc nobody reads mid-incident.
func TestRestoreReportsBackupAge(t *testing.T) {
	bin := t.TempDir()
	fakeOC(t, bin, baselineYAML)
	work := t.TempDir()

	fresh := backupDir(t, work, "fresh", time.Now().UTC().Format(time.RFC3339))
	stdout, stderr, code := runScript(t, "restore.sh", work, fresh)
	if code != 0 {
		t.Fatalf("restore.sh: exit %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "0d old") {
		t.Errorf("stdout %q, want the artifact age reported", stdout)
	}
	if strings.Contains(stderr, "days old") {
		t.Errorf("a backup taken today warned about staleness: %q", stderr)
	}

	stale := backupDir(t, work, "stale", time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339))
	stdout, stderr, code = runScript(t, "restore.sh", work, stale)
	if code != 0 {
		t.Fatalf("restore.sh on a stale backup: exit %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "30 days old") {
		t.Errorf("stderr %q, want the stale age called out", stderr)
	}
	if !strings.Contains(stdout, "30d old") {
		t.Errorf("stdout %q, want the age in the restore summary", stdout)
	}
}

func TestVerifyBackup(t *testing.T) {
	now := time.Now().UTC()

	t.Run("a good backup verifies", func(t *testing.T) {
		work := t.TempDir()
		dir := backupDir(t, work, "bdir", now.Format(time.RFC3339))
		stdout, stderr, code := runScript(t, "verify-backup.sh", work, dir)
		if code != 0 {
			t.Fatalf("exit %d, want 0; stderr=%s", code, stderr)
		}
		if !strings.Contains(stdout, "restorable") || stderr != "" {
			t.Errorf("stdout %q stderr %q, want a clean restorable verdict", stdout, stderr)
		}
	})

	// Everything below is a way a scheduled backup dies without anyone
	// noticing: a copy that never landed, a truncated transfer, a cron whose
	// token expired weeks ago, a host whose clock jumped.
	cases := []struct {
		name   string
		mutate func(t *testing.T, work string) string
		want   string
	}{
		{
			name: "directory never arrived",
			mutate: func(t *testing.T, work string) string {
				return filepath.Join(work, "missing")
			},
			want: "does not exist",
		},
		{
			name: "copy truncated in transit",
			mutate: func(t *testing.T, work string) string {
				dir := backupDir(t, work, "bdir", now.Format(time.RFC3339))
				path := filepath.Join(dir, "clusterbaseline.yaml")
				if err := os.WriteFile(path, []byte(baselineYAML[:200]), 0o600); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: "checksum mismatch",
		},
		{
			name: "manifest lost",
			mutate: func(t *testing.T, work string) string {
				dir := backupDir(t, work, "bdir", now.Format(time.RFC3339))
				if err := os.Remove(filepath.Join(dir, "MANIFEST")); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: "unverifiable",
		},
		{
			name: "zero-byte artifact",
			mutate: func(t *testing.T, work string) string {
				dir := backupDir(t, work, "bdir", now.Format(time.RFC3339))
				if err := os.WriteFile(filepath.Join(dir, "clusterbaseline.yaml"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: "empty",
		},
		{
			name: "artifact is another kind",
			mutate: func(t *testing.T, work string) string {
				dir := backupDir(t, work, "bdir", now.Format(time.RFC3339))
				path := filepath.Join(dir, "clusterbaseline.yaml")
				other := strings.Replace(baselineYAML, "kind: ClusterBaseline", "kind: ConfigMap", 1)
				if err := os.WriteFile(path, []byte(other), 0o600); err != nil {
					t.Fatal(err)
				}
				sum, err := exec.CommandContext(t.Context(), "sha256sum", path).Output()
				if err != nil {
					t.Fatal(err)
				}
				digest := strings.SplitN(string(sum), " ", 2)[0]
				manifest := "takenAt=" + now.Format(time.RFC3339) + "\nsha256=" + digest + "\n"
				if err := os.WriteFile(filepath.Join(dir, "MANIFEST"), []byte(manifest), 0o600); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: "not a ClusterBaseline",
		},
		{
			name: "schedule stopped running",
			mutate: func(t *testing.T, work string) string {
				return backupDir(t, work, "bdir", now.AddDate(0, 0, -9).Format(time.RFC3339))
			},
			want: "past the 7-day limit",
		},
		{
			name: "clock was wrong when it was taken",
			mutate: func(t *testing.T, work string) string {
				return backupDir(t, work, "bdir", now.AddDate(1, 0, 0).Format(time.RFC3339))
			},
			want: "in the future",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			dir := tc.mutate(t, work)
			_, stderr, code := runScript(t, "verify-backup.sh", work, dir)
			if code == 0 {
				t.Fatal("verify-backup.sh passed a backup that cannot be restored")
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr %q, want it to mention %q", stderr, tc.want)
			}
		})
	}

	// The age limit is the admin's call, and a verifier that cannot be tuned
	// to a daily schedule is a verifier nobody runs.
	work := t.TempDir()
	dir := backupDir(t, work, "bdir", now.AddDate(0, 0, -9).Format(time.RFC3339))
	if _, stderr, code := runScript(t, "verify-backup.sh", work, "--max-age-days", "30", dir); code != 0 {
		t.Fatalf("--max-age-days 30 on a 9-day backup: exit %d, want 0; stderr=%s", code, stderr)
	}
}

func TestBackupRestoreUsage(t *testing.T) {
	for _, name := range []string{"backup.sh", "restore.sh", "verify-backup.sh"} {
		script := scriptPath(t, name)
		for _, flag := range []string{"--help", "-h"} {
			stdout, stderr, code := runCmd(t, script, flag)
			if code != 0 {
				t.Errorf("%s %s: exit %d, want 0; stderr=%q", name, flag, code, stderr)
			}
			if stderr != "" {
				t.Errorf("%s %s: stderr %q, want empty", name, flag, stderr)
			}
			if !strings.Contains(stdout, "Usage:") {
				t.Errorf("%s %s: stdout missing usage:\n%s", name, flag, stdout)
			}
		}

		stdout, stderr, code := runCmd(t, script, "--not-a-flag")
		if code != 2 {
			t.Errorf("%s unknown option: exit %d, want 2; stderr=%q", name, code, stderr)
		}
		if stdout != "" {
			t.Errorf("%s unknown option: stdout %q, want empty", name, stdout)
		}
		if !strings.Contains(stderr, "Usage:") {
			t.Errorf("%s unknown option: stderr missing usage:\n%s", name, stderr)
		}
	}

	// An unknown option must not be turned into a directory named after it.
	if _, err := os.Stat("--not-a-flag"); err == nil {
		t.Fatal("a script created a path named after an unknown option")
	}
}
