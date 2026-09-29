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
	"regexp"
	"strconv"
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

// baselineUID is the uid of the captured object, named so the identity guard's
// tests and the MANIFEST fixtures cannot drift apart.
const baselineUID = "6f0b1c2a-7d3e-4a55-9b21-0c8e5f1d4a90"

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

// liveAfterWaiverEdit is the same object baselineYAML captured, at a later
// resourceVersion, with a second waiver added and a batch annotation moved on.
// It is what the live object looks like when an admin has worked on it since
// the backup: the spec differs, so restoring over it would discard that work.
// A bare resourceVersion bump is not enough to say so, which is why the fake
// serves this rather than only FAKE_OC_RESOURCE_VERSION.
const liveAfterWaiverEdit = `apiVersion: baselinesecurity.openshift.io/v1alpha1
kind: ClusterBaseline
metadata:
  name: cluster
  resourceVersion: "41999"
  uid: 6f0b1c2a-7d3e-4a55-9b21-0c8e5f1d4a90
  annotations:
    baselinesecurity.openshift.io/batch-apply: api-check,file-permission
    baselinesecurity.openshift.io/batch-started-at: "2026-09-21T10:04:11Z"
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
    - name: node-debug
      reason: debugging the CNI outage
      requestedBy: alice
      approvedBy: carol
status:
  lastScanTime: "2026-09-21T03:02:44Z"
  score: 84
  history:
    - scanTime: "2026-09-21T03:02:44Z"
      score: 84
`

// liveAfterThisRestore is the object the first run of a restore leaves behind:
// the artifact's own spec and status, at a higher resourceVersion, because both
// writes bumped it. Nothing has been edited, so the second run is a re-run and
// must converge rather than refuse.
const liveAfterThisRestore = `apiVersion: baselinesecurity.openshift.io/v1alpha1
kind: ClusterBaseline
metadata:
  name: cluster
  resourceVersion: "41999"
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
// returns captured, or FAKE_OC_LIVE_YAML when set (a live object that has
// moved on since the capture, which captured alone can never express); a get
// that asks for a jsonpath field returns
// FAKE_OC_RESOURCE_VERSION and FAKE_OC_UID (both empty by default, which
// reads as "no live object", the case a restore onto a recovered cluster is
// in),
// FAKE_OC_GET_FAIL (makes every jsonpath get fail, the way an expired token or
// an apiserver blip does), FAKE_OC_UID_FAIL (makes only the uid get fail, so
// the read of the live object succeeds and the uid read does not), and
// FAKE_OC_CRD_VERSIONS (the versions the CRD serves, one per line, empty
// when the CRD cannot be read) stand in for
// the reads a restore makes. Every other subcommand is appended to the call
// log. The stub must tolerate the --request-timeout flag both scripts pass on
// every call, so it drops leading global flags before dispatching.
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
		"      case \"$a\" in\n" +
		"        *spec.versions*) printf '%s' \"${FAKE_OC_CRD_VERSIONS:-v1alpha1}\"; exit 0 ;;\n" +
		"        jsonpath=*) if [ -n \"${FAKE_OC_GET_FAIL:-}\" ]; then echo 'Forbidden: token expired' >&2; exit 1; fi\n" +
		"          case \"$a\" in\n" +
		"            *metadata.uid*) if [ -n \"${FAKE_OC_UID_FAIL:-}\" ]; then echo 'Forbidden: token expired' >&2; exit 1; fi; printf '%s' \"${FAKE_OC_UID:-}\"; exit 0 ;;\n" +
		"            *) printf '%s' \"${FAKE_OC_RESOURCE_VERSION:-}\"; exit 0 ;;\n" +
		"          esac ;;\n" +
		"      esac\n" +
		"    done\n" +
		"    if [ -n \"${FAKE_OC_LIVE_YAML:-}\" ]; then printf '%s\\n' \"$FAKE_OC_LIVE_YAML\"; exit 0 ; fi\n" +
		"    cat <<'CAPTURED'\n" + captured + "CAPTURED\nexit 0 ;;\n" +
		"esac\n" +
		"printf '%s\\n' \"$*\" >> " + filepath.Join(dir, "oc.log") + "\n" +
		// FAKE_OC_COPY_SENT records what a -f write actually sent. A restore
		// that stages a temporary copy deletes it on exit, so the caller's
		// own log line names a path that is gone by the time it reads it.
		"prev=''\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$prev\" = '-f' ] || [ \"$prev\" = '--filename' ]; then\n" +
		"    if [ -n \"${FAKE_OC_COPY_SENT:-}\" ]; then printf '=== sent %s\\n' \"$a\" >> " + filepath.Join(dir, "sent.log") + "; cat -- \"$a\" >> " + filepath.Join(dir, "sent.log") + " 2>/dev/null || printf '<unreadable>\\n' >> " + filepath.Join(dir, "sent.log") + "; fi\n" +
		"  fi\n" +
		"  prev=\"$a\"\n" +
		"done\n" +
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

// assertNoClusterWrites fails if the fake oc recorded any verb that mutates the
// cluster. "apply -f" alone is not enough: restore.sh also reaches the API
// through `replace --subresource=status`, and a script that validated late
// could patch, create, or delete its way through the refusal under test and
// still pass a single-verb check.
func assertNoClusterWrites(t *testing.T, logfile string) {
	t.Helper()
	calls := ocCalls(t, logfile)
	for _, verb := range []string{"apply ", "replace ", "patch ", "delete ", "create ", "edit "} {
		if strings.Contains(calls, verb) {
			t.Errorf("a %q call reached the cluster before the refusal took effect:\n%s", strings.TrimSpace(verb), calls)
		}
	}
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
	// Read-only handle fully drained by io.Copy below: a Close error carries
	// no data-loss risk, and the hash failure above has already failed the test
	// if it mattered.
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Logf("closing %s: %v", path, cerr)
		}
	}()
	h := sha256.New()
	// A single drain and a single close: the deferred close above is the one
	// that reports, so the copy path must not close the handle a second time.
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
	// sees matching resourceVersions and the identity guard a matching uid, and
	// the restore goes through.
	t.Setenv("FAKE_OC_RESOURCE_VERSION", "41237")
	t.Setenv("FAKE_OC_UID", baselineUID)
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
			assertNoClusterWrites(t, log)
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
	// sha256Hex, not a `sha256sum` subprocess: coreutils is GNU and macOS
	// ships none, so a shelling-out helper fails the suite on a supported host.
	manifest := "takenAt=" + takenAt + "\nresourceVersion=41237\nuid=" + baselineUID + "\nsha256=" + sha256Hex(t, path) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "MANIFEST"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRestoreRefusesToRollBackAMovedOnObject(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	// The live object carries waiver edits made after the backup. Applying
	// the artifact discards them, and nothing else records them. The spec
	// difference is the signal, so the live object has to differ in more
	// than its resourceVersion: a bare bump is what this script's own write
	// looks like, and treating that as an edit is what used to make a
	// re-run refuse.
	t.Setenv("FAKE_OC_RESOURCE_VERSION", "41999")
	t.Setenv("FAKE_OC_LIVE_YAML", liveAfterWaiverEdit)
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
	assertNoClusterWrites(t, log)

	// --force is how an operator says they meant it, and then it proceeds.
	if _, stderr, code := runScript(t, "restore.sh", work, "--force", dir); code != 0 {
		t.Fatalf("restore.sh --force: exit %d, want 0; stderr=%s", code, stderr)
	}
	calls := ocCalls(t, log)
	if !strings.Contains(calls, "apply -f") || !strings.Contains(calls, "replace --subresource=status -f") {
		t.Errorf("--force did not complete the restore; oc calls:\n%s", calls)
	}
}

// A restore is run twice for one incident as often as it is run once, and the
// ordinary path (no --force) has to converge too, not only the forced one.
//
// It refused. The first run's own apply and status replace bumped the live
// resourceVersion, so the second run's staleness guard fired on the write the
// first run had just made. It printed a warning that was false (the waiver
// edits it offered to protect were the ones that run had just put there) and
// pointed at --force, whose status replace is an unconditional clobber of
// state an operator reads as current.
func TestRestoreConvergesOnRerunWithoutForce(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	// What the first run leaves behind: this artifact's own spec and status,
	// one resourceVersion on. Nothing has been edited since.
	t.Setenv("FAKE_OC_RESOURCE_VERSION", "41999")
	t.Setenv("FAKE_OC_LIVE_YAML", liveAfterThisRestore)
	t.Setenv("FAKE_OC_COPY_SENT", "1")
	work := t.TempDir()
	dir := backupDir(t, work, "bdir", "2026-09-20T03:00:00Z")

	for run := 1; run <= 2; run++ {
		_, stderr, code := runScript(t, "restore.sh", work, dir)
		if code != 0 {
			t.Fatalf("restore.sh run %d: exit %d, want 0; stderr=%s", run, code, stderr)
		}
		if run == 1 && !strings.Contains(stderr, "re-run of a restore that landed") {
			t.Errorf("run 1 did not report a converged re-run:\n%s", stderr)
		}
		// The refusal message must not appear: nothing has moved on.
		if strings.Contains(stderr, "has moved on") {
			t.Errorf("run %d refused an object it had itself just written:\n%s", run, stderr)
		}
	}

	calls := ocCalls(t, log)
	for _, want := range []string{"apply -f", "replace --subresource=status -f"} {
		if got := strings.Count(calls, want); got != 2 {
			t.Errorf("wanted %q twice, one per run, got %d; oc calls:\n%s", want, got, calls)
		}
	}
	// Both writes must have gone out without the captured resourceVersion:
	// the second run's precondition is stale by the first run's own write,
	// and a stale one is refused with a conflict.
	sent, err := os.ReadFile(filepath.Join(bin, "sent.log"))
	if err != nil {
		t.Fatalf("no record of what the restore sent: %v", err)
	}
	if strings.Contains(string(sent), "resourceVersion") {
		t.Errorf("the re-run sent a resourceVersion precondition:\n%s", sent)
	}
	// Stripping it must not cost a field: a spec or status silently dropped
	// is a restore that half happened.
	for _, want := range []string{"score: 87", "schedule:", "requestedBy: alice", "lastScanTime:"} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("the re-run dropped %q from what it sent:\n%s", want, sent)
		}
	}
	// The artifact is the evidence; neither run may consume it.
	artifact, err := os.ReadFile(filepath.Join(dir, "clusterbaseline.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact) != baselineYAML {
		t.Errorf("the restore modified the backup artifact:\n%s", artifact)
	}
}

// A resourceVersion counts writes within one object's lifetime, so it cannot
// tell two objects apart. A ClusterBaseline that was deleted and recreated
// under the same name, a restore onto a different cluster, or an etcd snapshot
// taken from a point before the object existed all put a live object in front
// of a backup whose resourceVersion happens to match, and the rollback guard
// read that as "nothing has moved on" and restored over it. The waivers on
// that unrelated object are the state nothing else records, so the restore
// destroys them with no --force and no message.
//
// Here the live object is at exactly the backup's resourceVersion, which is the
// only way past the resourceVersion guard: the uid is the whole check.
func TestRestoreRefusesADifferentObject(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	// The CR was deleted and recreated. The new object happens to be at the
	// same write count as the captured one.
	t.Setenv("FAKE_OC_RESOURCE_VERSION", "41237")
	t.Setenv("FAKE_OC_UID", "b19e77aa-0000-4000-8000-000000000001")
	work := t.TempDir()
	dir := backupDir(t, work, "bdir", "2026-09-20T03:00:00Z")

	_, stderr, code := runScript(t, "restore.sh", work, dir)
	if code == 0 {
		t.Fatal("restore overwrote a live ClusterBaseline that is a different object from the one the backup was taken from")
	}
	if !strings.Contains(stderr, "b19e77aa-0000-4000-8000-000000000001") ||
		!strings.Contains(stderr, baselineUID) {
		t.Errorf("stderr %q, want both uids named", stderr)
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("stderr %q, want the override named", stderr)
	}
	if calls := ocCalls(t, log); strings.Contains(calls, "apply -f") {
		t.Errorf("the refused restore still wrote to the cluster:\n%s", calls)
	}

	// Once the operator has confirmed the live object holds nothing this
	// backup does not, --force is how they say so.
	if _, stderr, code := runScript(t, "restore.sh", work, "--force", dir); code != 0 {
		t.Fatalf("restore.sh --force: exit %d, want 0; stderr=%s", code, stderr)
	}
	calls := ocCalls(t, log)
	if !strings.Contains(calls, "apply -f") || !strings.Contains(calls, "replace --subresource=status -f") {
		t.Errorf("--force did not complete the restore; oc calls:\n%s", calls)
	}
}

// The uid is the check that survives an object swap, so a read that fails is
// not an absent uid either. Skipping the comparison on a failed read restores
// with the guard silently off, which is the failure this guard exists to stop.
func TestRestoreRefusesWhenLiveUIDCannotBeRead(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	t.Setenv("FAKE_OC_RESOURCE_VERSION", "41237")
	t.Setenv("FAKE_OC_UID_FAIL", "1")
	work := t.TempDir()
	dir := backupDir(t, work, "bdir", "2026-09-20T03:00:00Z")

	_, stderr, code := runScript(t, "restore.sh", work, dir)
	if code == 0 {
		t.Fatal("restore proceeded after failing to read the live object's uid")
	}
	if !strings.Contains(stderr, "uid") {
		t.Errorf("stderr %q, want it to name the uid read that failed", stderr)
	}
	if calls := ocCalls(t, log); strings.Contains(calls, "apply -f") {
		t.Errorf("the refused restore still wrote to the cluster:\n%s", calls)
	}
	// --force does not cover it: there is nothing to compare against, and a
	// clobber of an unknown object is not what an operator is agreeing to.
	if _, _, code := runScript(t, "restore.sh", work, "--force", dir); code == 0 {
		t.Error("restore.sh --force overrode a failed uid read")
	}
}

func TestBackupRefusesACaptureWithNoRecoveryTieBreakers(t *testing.T) {
	// An object whose metadata the capture did not read leaves the MANIFEST
	// without the resourceVersion and uid that restore.sh's guards are keyed
	// on. The artifact would still restore, so nothing downstream would notice
	// the guards were off until the restore destroyed a live object's waivers.
	for _, tc := range []struct {
		name string
		drop string
		want string
	}{
		{"no resourceVersion", "  resourceVersion: \"41237\"\n", "no resourceVersion"},
		{"no uid", "  uid: 6f0b1c2a-7d3e-4a55-9b21-0c8e5f1d4a90\n", "no uid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			fakeOC(t, bin, strings.Replace(baselineYAML, tc.drop, "", 1))
			work := t.TempDir()

			_, stderr, code := runScript(t, "backup.sh", work, "bdir")
			if code == 0 {
				t.Fatal("backup.sh wrote a MANIFEST that cannot guard the restore it exists for")
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr %q, want it to name %q", stderr, tc.want)
			}
			if _, err := os.Stat(filepath.Join(work, "bdir", "MANIFEST")); err == nil {
				t.Error("the failed backup left a MANIFEST behind")
			}
		})
	}
}

// A restore is run twice for one incident as often as it is run once: the
// operator re-runs it after a transient API error, re-runs it to be sure, and
// hands it to a colleague who re-runs it. The second run has to reach the same
// state as the first, not fail.
//
// It did fail. The artifact carries the resourceVersion it was captured at,
// which is a precondition on both writes, and --force exists precisely for the
// case where the live object has moved past it. So every --force write carried
// a precondition the operator had already accepted as unsatisfiable: apply and
// the status replace were both refused, the script exited 1 with the spec half
// restored, and it told the operator to re-run, which hit the identical
// conflict. Forever.
func TestForceRestoreConvergesOnRerun(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	t.Setenv("FAKE_OC_RESOURCE_VERSION", "41999")
	t.Setenv("FAKE_OC_COPY_SENT", "1")
	work := t.TempDir()
	dir := backupDir(t, work, "bdir", "2026-09-20T03:00:00Z")

	for run := 1; run <= 2; run++ {
		if _, stderr, code := runScript(t, "restore.sh", work, "--force", dir); code != 0 {
			t.Fatalf("restore.sh --force run %d: exit %d, want 0; stderr=%s", run, code, stderr)
		}
	}
	calls := ocCalls(t, log)
	for _, want := range []string{"apply -f", "replace --subresource=status -f"} {
		if got := strings.Count(calls, want); got != 2 {
			t.Errorf("wanted %q twice, one per run, got %d; oc calls:\n%s", want, got, calls)
		}
	}

	// Both writes must have gone out without the captured resourceVersion: a
	// stale one is the conflict this test is about, whatever the fake oc
	// tolerates.
	sent, err := os.ReadFile(filepath.Join(bin, "sent.log"))
	if err != nil {
		t.Fatalf("no record of what the restore sent: %v", err)
	}
	if got := strings.Count(string(sent), "=== sent "); got != 4 {
		t.Errorf("recorded %d sent files, want 4 (apply and replace, twice):\n%s", got, sent)
	}
	if strings.Contains(string(sent), "resourceVersion") {
		t.Errorf("the restore sent a resourceVersion precondition:\n%s", sent)
	}
	// Nothing but that line may go missing: a spec or status silently dropped
	// by the strip is a restore that half happened.
	for _, want := range []string{"score: 87", "schedule:", "requestedBy: alice", "lastScanTime:"} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("the strip dropped %q from what the restore sent:\n%s", want, sent)
		}
	}

	// The artifact on disk is the evidence; the script must not consume it.
	artifact, err := os.ReadFile(filepath.Join(dir, "clusterbaseline.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact) != baselineYAML {
		t.Errorf("the restore modified the backup artifact:\n%s", artifact)
	}
	// The temporary copy is not left behind in the backup directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".restore.") {
			t.Errorf("restore left %s behind in the backup directory", e.Name())
		}
	}
}

// A read of the live object that fails is not the same as a live object that
// is gone. Treating the two alike skipped the resourceVersion guard, so a
// backup was applied over an object that had moved on, with no --force and no
// warning: the waiver and batch edits made since were gone.
func TestRestoreRefusesWhenLiveObjectCannotBeRead(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	// Same live resourceVersion as the backup, so only the failed read is
	// under test: the guard has a real disagreement to catch, not a typo.
	t.Setenv("FAKE_OC_RESOURCE_VERSION", "41999")
	t.Setenv("FAKE_OC_GET_FAIL", "1")
	work := t.TempDir()
	dir := backupDir(t, work, "bdir", "2026-09-20T03:00:00Z")

	_, stderr, code := runScript(t, "restore.sh", work, dir)
	if code == 0 {
		t.Fatal("restore applied a backup it could not compare against the live object")
	}
	if !strings.Contains(stderr, "cannot read the live ClusterBaseline") {
		t.Errorf("stderr %q, want the unreadable live object named", stderr)
	}
	assertNoClusterWrites(t, log)
	// --force does not cover this either: the operator cannot mean to clobber
	// an object whose current resourceVersion was never read.
	if _, _, code := runScript(t, "restore.sh", work, "--force", dir); code == 0 {
		t.Fatal("--force restored over a live object it could not read")
	}
}

// A backup written at an apiVersion this cluster no longer serves cannot be
// applied at all, and `oc apply` reports it as "no matches for kind", which
// sends the operator looking at RBAC instead of at the version. Refuse before
// the first write, and say what the real cause is.
func TestRestoreRefusesAnArtifactTheClusterCannotServe(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	work := t.TempDir()
	dir := backupDir(t, work, "bdir", time.Now().UTC().Format(time.RFC3339))
	t.Setenv("FAKE_OC_CRD_VERSIONS", "v1beta1")

	_, stderr, code := runScript(t, "restore.sh", work, dir)
	if code == 0 {
		t.Fatal("restore.sh applied an artifact the cluster's CRD does not serve")
	}
	if !strings.Contains(stderr, "does not serve") || !strings.Contains(stderr, "v1beta1") {
		t.Errorf("stderr %q, want the served versions named", stderr)
	}
	assertNoClusterWrites(t, log)

	// An admin who knows the CRD is coming back still gets a way through.
	_, stderr, code = runScript(t, "restore.sh", work, "--force", dir)
	if code != 0 {
		t.Fatalf("--force on an unserved version: exit %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("stderr %q, want the forced restore announced", stderr)
	}
	if calls := ocCalls(t, log); !strings.Contains(calls, "replace --subresource=status -f") {
		t.Errorf("--force did not complete the restore; oc calls:\n%s", calls)
	}
}

// A cluster recovered without its CRD has nothing to check the artifact
// version against, and the documented order is to restore etcd first and this
// second. That must not turn into a refusal.
func TestRestoreProceedsWhenTheCRDCannotBeRead(t *testing.T) {
	bin := t.TempDir()
	log := fakeOC(t, bin, baselineYAML)
	work := t.TempDir()
	dir := backupDir(t, work, "bdir", time.Now().UTC().Format(time.RFC3339))
	t.Setenv("FAKE_OC_CRD_VERSIONS", "")

	if _, stderr, code := runScript(t, "restore.sh", work, dir); code != 0 {
		t.Fatalf("restore.sh with no readable CRD: exit %d, want 0; stderr=%s", code, stderr)
	}
	if calls := ocCalls(t, log); !strings.Contains(calls, "apply -f") {
		t.Errorf("restore did not apply; oc calls:\n%s", calls)
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

	// An unreadable stamp does not stop the restore, but the operator has to
	// be told the RPO is unknown rather than shown no age at all.
	unreadable := backupDir(t, work, "unreadable", "last tuesday")
	stdout, stderr, code = runScript(t, "restore.sh", work, unreadable)
	if code != 0 {
		t.Fatalf("restore.sh on an unreadable takenAt: exit %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "RPO this restore buys is") {
		t.Errorf("stderr %q, want the unmeasurable RPO called out", stderr)
	}
	if strings.Contains(stdout, "d old") {
		t.Errorf("stdout %q claims an age for a stamp that has none", stdout)
	}
}

// pathWithBSDDate puts a `date` on PATH that has no GNU `-d` flag, the way
// macOS ships it, and symlinks everything else through. A script that parses
// the backup age with `date -d` loses the check entirely on such a host and
// cannot tell that the check is gone; the age has to come out of the stamp
// itself.
func pathWithBSDDate(t *testing.T) {
	t.Helper()
	real, err := exec.LookPath("date")
	if err != nil {
		t.Skip("host has no date")
	}
	farm := t.TempDir()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || e.Name() == "date" {
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
	stub := filepath.Join(farm, "date")
	script := "#!/usr/bin/env bash\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$a\" in -d|--date) echo 'date: illegal option -- d' >&2; exit 1 ;; esac\n" +
		"done\n" +
		"exec " + real + " \"$@\"\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatalf("writing BSD date stub: %v", err)
	}
	t.Setenv("PATH", farm)
}

// The age is the RPO. A host whose `date` cannot parse it must still see the
// age, or a months-old backup reads as a fresh one and nobody looks for a
// newer one.
func TestBackupAgeSurvivesANonGNUDate(t *testing.T) {
	bin := t.TempDir()
	fakeOC(t, bin, baselineYAML)
	pathWithBSDDate(t)
	work := t.TempDir()

	stale := backupDir(t, work, "stale", time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339))

	_, stderr, code := runScript(t, "verify-backup.sh", work, stale)
	if code == 0 {
		t.Fatal("verify-backup.sh passed a 30-day-old backup on a host with no GNU date")
	}
	if !strings.Contains(stderr, "past the 7-day limit") {
		t.Errorf("stderr %q, want the age limit enforced without `date -d`", stderr)
	}

	stdout, stderr, code := runScript(t, "restore.sh", work, stale)
	if code != 0 {
		t.Fatalf("restore.sh: exit %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "30 days old") || !strings.Contains(stdout, "30d old") {
		t.Errorf("stdout %q stderr %q, want the age reported without `date -d`", stdout, stderr)
	}
}

// The digest a verifier recomputes must not depend on GNU coreutils either,
// or the check cannot run on the machine the copy landed on.
func TestVerifyBackupWithoutGNUCoreutils(t *testing.T) {
	work := t.TempDir()
	dir := backupDir(t, work, "bdir", time.Now().UTC().Format(time.RFC3339))
	pathWithoutSHA256Sum(t)

	stdout, stderr, code := runScript(t, "verify-backup.sh", work, dir)
	if code != 0 {
		t.Fatalf("verify-backup.sh without sha256sum: exit %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "restorable") {
		t.Errorf("stdout %q, want a restorable verdict without GNU coreutils", stdout)
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
				manifest := "takenAt=" + now.Format(time.RFC3339) + "\nsha256=" + sha256Hex(t, path) + "\n"
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
		{
			// A MANIFEST missing a tie-breaker passes every other check and
			// then restores with that guard switched off, so the verifier that
			// a schedule alerts on has to refuse it.
			name: "manifest records no uid",
			mutate: func(t *testing.T, work string) string {
				dir := backupDir(t, work, "bdir", now.Format(time.RFC3339))
				manifest, err := os.ReadFile(filepath.Join(dir, "MANIFEST"))
				if err != nil {
					t.Fatal(err)
				}
				kept := []string{}
				for _, line := range strings.Split(string(manifest), "\n") {
					if !strings.HasPrefix(line, "uid=") {
						kept = append(kept, line)
					}
				}
				if err := os.WriteFile(filepath.Join(dir, "MANIFEST"), []byte(strings.Join(kept, "\n")), 0o600); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: "no uid",
		},
		{
			name: "manifest records no resourceVersion",
			mutate: func(t *testing.T, work string) string {
				dir := backupDir(t, work, "bdir", now.Format(time.RFC3339))
				manifest, err := os.ReadFile(filepath.Join(dir, "MANIFEST"))
				if err != nil {
					t.Fatal(err)
				}
				kept := []string{}
				for _, line := range strings.Split(string(manifest), "\n") {
					if !strings.HasPrefix(line, "resourceVersion=") {
						kept = append(kept, line)
					}
				}
				if err := os.WriteFile(filepath.Join(dir, "MANIFEST"), []byte(strings.Join(kept, "\n")), 0o600); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: "no resourceVersion",
		},
		{
			// An unreadable stamp is not a young backup. Passing here would
			// make the alert on this exit status blind to the one failure it
			// exists to catch.
			name: "manifest records no age",
			mutate: func(t *testing.T, work string) string {
				return backupDir(t, work, "bdir", "")
			},
			want: "no takenAt",
		},
		{
			name: "manifest age is not a timestamp",
			mutate: func(t *testing.T, work string) string {
				return backupDir(t, work, "bdir", "last tuesday")
			},
			want: "is not a YYYY-MM-DDTHH:MM:SSZ stamp",
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

// The age limit bounds the RPO, so it is compared in seconds. Truncating to
// whole days first would accept anything up to a full day past the limit, and
// the whole point of a 7-day limit is that a backup 7d23h old already
// discards a week of scan and waiver history.
func TestBackupAgeLimitIsNotTruncatedToWholeDays(t *testing.T) {
	now := time.Now().UTC()
	work := t.TempDir()
	bin := t.TempDir()
	fakeOC(t, bin, baselineYAML)
	// Inside the limit by a minute, past it by an hour. A stamp exactly 7 days
	// old is the limit boundary itself, and the script reads the wall clock at
	// run time, so a stamp taken at -7d and verified a second later is already
	// past it: the in-limit case has to sit clear of the boundary or the test
	// fails on a slow machine and passes on a fast one.
	atLimit := backupDir(t, work, "at", now.Add(-7*24*time.Hour+time.Minute).Format(time.RFC3339))
	if _, stderr, code := runScript(t, "verify-backup.sh", work, atLimit); code != 0 {
		t.Errorf("verify-backup.sh on a backup inside the 7-day limit: exit %d, want 0; stderr=%s", code, stderr)
	}
	overLimit := backupDir(t, work, "over", now.Add(-7*24*time.Hour-time.Hour).Format(time.RFC3339))
	_, stderr, code := runScript(t, "verify-backup.sh", work, overLimit)
	if code == 0 {
		t.Fatal("verify-backup.sh passed a backup an hour past the 7-day limit")
	}
	if !strings.Contains(stderr, "7 days 1 hour old") {
		t.Errorf("stderr %q, want the age reported to the hour", stderr)
	}
	// restore.sh warns rather than refuses, but it must not warn at 7d23h and
	// stay silent.
	_, stderr, code = runScript(t, "restore.sh", bin, overLimit)
	if code != 0 {
		t.Fatalf("restore.sh: exit %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "7 days 1 hour old") {
		t.Errorf("stderr %q, want the stale age called out to the hour", stderr)
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

// defaultOutputDir reads a hack/ script's `OUT="${1:-<default>}"` default.
// The default is relative to operator/, the directory the scripts are
// documented to run from.
var defaultOutputDir = regexp.MustCompile(`OUT="\$\{1:-(?:\./)?([^}"]+)\}"`)

func scriptDefaultOutputDir(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(scriptPath(t, name))
	if err != nil {
		t.Fatal(err)
	}
	m := defaultOutputDir.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s has no OUT default to check against .gitignore", name)
	}
	return string(m[1])
}

// repoRoot walks up from the test's own directory for the marker that only the
// repository root has: hack/ itself, one level under operator/.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "operator", "hack", "backup.sh")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("operator/hack/backup.sh not found above the test directory")
		}
		dir = parent
	}
}

// TestDefaultOutputDirsAreGitignored keeps .gitignore in step with the two
// support scripts that default their output into the working tree. A backup is
// the unredacted ClusterBaseline, so it carries the waiver requestedBy and
// approvedBy identities must-gather.sh strips, and a must-gather carries
// operator logs and cluster dumps. The 0600 file and 0700 directory modes
// stop another local user, not `git add -A`, so an unignored default is a
// path from a cluster user's identity into a commit. Renaming a default
// without updating .gitignore fails here.
func TestDefaultOutputDirsAreGitignored(t *testing.T) {
	root := repoRoot(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH; the ignore rules cannot be checked")
	}
	inside := exec.CommandContext(t.Context(), git, "rev-parse", "--is-inside-work-tree")
	inside.Dir = root
	if out, err := inside.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "true" {
		t.Skipf("not a git work tree: %v %s", err, out)
	}
	for _, name := range []string{"backup.sh", "must-gather.sh"} {
		rel := filepath.Join("operator", scriptDefaultOutputDir(t, name))
		// A file inside the directory, not the bare name: a rule that ignored
		// only the directory entry would still let its contents be added.
		probe := filepath.Join(rel, "clusterbaseline.yaml")
		check := exec.CommandContext(t.Context(), git, "check-ignore", "-q", "--no-index", probe)
		check.Dir = root
		if err := check.Run(); err != nil {
			t.Errorf("%s default output %s is not gitignored; a backup there can be committed with the waiver attribution it carries", name, rel)
		}
	}
}

// backupMaxAgeDays reads a hack/ script's staleness threshold from its
// `NAME=<n>` assignment.
var backupMaxAgeDays = regexp.MustCompile(`(?m)^(?:MAX_AGE_DAYS|STALE_BACKUP_MAX_AGE_DAYS)=(\d+)$`)

// TestBackupStalenessThresholdsAgree pins the one staleness policy the backup
// scripts share. verify-backup.sh fails a backup older than its threshold and
// restore.sh warns at its own, and the two answers are the same question asked
// of the same directory, so an admin who tunes one expects the other to move
// with it. They are separate scripts with separate constants and only a comment
// ties them, which is exactly the shape a value drifts out of: verify-backup
// then passes a backup restore.sh immediately warns about, and the alert built
// on verify-backup's exit status reports a healthy schedule that restore
// refuses to trust.
func TestBackupStalenessThresholdsAgree(t *testing.T) {
	thresholds := map[string]int{}
	for _, name := range []string{"verify-backup.sh", "restore.sh"} {
		raw, err := os.ReadFile(scriptPath(t, name))
		if err != nil {
			t.Fatal(err)
		}
		m := backupMaxAgeDays.FindSubmatch(raw)
		if m == nil {
			t.Fatalf("%s declares no staleness threshold for the age check to use", name)
		}
		n, err := strconv.Atoi(string(m[1]))
		if err != nil {
			t.Fatalf("%s staleness threshold %q is not a number: %v", name, m[1], err)
		}
		thresholds[name] = n
	}
	if thresholds["verify-backup.sh"] != thresholds["restore.sh"] {
		t.Errorf("staleness thresholds disagree: verify-backup.sh fails at %dd, restore.sh warns at %dd; a backup can pass the check and still be one restore.sh distrusts",
			thresholds["verify-backup.sh"], thresholds["restore.sh"])
	}
}
