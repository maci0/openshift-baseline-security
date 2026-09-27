package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	certutil "k8s.io/client-go/util/cert"
)

// writeTestPair stages a rotated cert/key pair and renames each into place.
// Rename, not os.WriteFile: TestMetricsCertProviderConcurrentReload rotates
// while other goroutines read the same two paths, and an in-place write truncates
// before it writes, so a concurrent reader could see a zero-length tls.crt and
// take the corrupt-Secret path for reasons the rotation never intended. Each path
// now flips atomically, so a reader sees the whole old file or the whole new one.
func writeTestPair(t *testing.T, dir string) {
	t.Helper()
	certPEM, keyPEM, err := certutil.GenerateSelfSignedCertKeyWithFixtures(
		"localhost", []net.IP{{127, 0, 0, 1}}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	writeFileAtomic(t, filepath.Join(dir, "tls.crt"), certPEM)
	writeFileAtomic(t, filepath.Join(dir, "tls.key"), keyPEM)
}

// writeFileAtomic stages the bytes in a fresh temp file in the target's directory
// and renames them over it, so a concurrent reader never observes a partially
// written file. CreateTemp picks the name atomically: a shared fixed temp name
// would be the same torn-write race one level down.
func writeFileAtomic(t *testing.T, path string, data []byte) {
	t.Helper()
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	name := tmp.Name()
	// Cleanup covers every failure below and runs after Fatal, so no branch has
	// to remember to unlink the temp file it just created.
	t.Cleanup(func() { _ = os.Remove(name) })
	if _, err := tmp.Write(data); err != nil {
		if cerr := tmp.Close(); cerr != nil {
			t.Logf("closing %s: %v", name, cerr)
		}
		discardTemp(t, name)
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		discardTemp(t, name)
		t.Fatal(err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		discardTemp(t, name)
		t.Fatal(err)
	}
	if err := os.Rename(name, path); err != nil {
		discardTemp(t, name)
		t.Fatal(err)
	}
}

// discardTemp unlinks the staged file on an already-failing path. The test is
// ending in t.Fatal either way, so an unlink error only decides whether a
// stray file is left in the test's temp dir; report it and keep the original
// failure as the cause.
func discardTemp(t *testing.T, name string) {
	t.Helper()
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Logf("removing %s: %v", name, err)
	}
}

func TestMetricsCertProviderSelfSignedWhenMissing(t *testing.T) {
	p := &metricsCertProvider{certDir: t.TempDir()}
	c1, err := p.GetCertificate(nil)
	if err != nil || c1 == nil {
		t.Fatalf("self-signed: %v %v", c1, err)
	}
	c2, err := p.GetCertificate(nil)
	if err != nil || c2 != c1 {
		t.Fatal("expected cached self-signed")
	}
}

func TestMetricsCertProviderLoadsAndReloads(t *testing.T) {
	dir := t.TempDir()
	writeTestPair(t, dir)

	p := &metricsCertProvider{certDir: dir}
	c1, err := p.GetCertificate(nil)
	if err != nil || c1 == nil {
		t.Fatalf("load: %v %v", c1, err)
	}
	c2, err := p.GetCertificate(nil)
	if err != nil || c2 != c1 {
		t.Fatal("expected fingerprint cache hit")
	}

	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	certInfo, err := os.Stat(certPath)
	if err != nil {
		t.Fatal(err)
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	writeTestPair(t, dir)
	// Preserve both mtimes: content, not timestamp ordering, must drive reload.
	if err := os.Chtimes(certPath, certInfo.ModTime(), certInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(keyPath, keyInfo.ModTime(), keyInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	c3, err := p.GetCertificate(nil)
	if err != nil || c3 == nil {
		t.Fatalf("reload: %v", err)
	}
	if c3 == c1 {
		t.Fatal("expected new certificate pointer after same-mtime content change")
	}
}

func TestMetricsCertProviderKeepsLastGoodPairWhenFilesDisappear(t *testing.T) {
	dir := t.TempDir()
	writeTestPair(t, dir)
	p := &metricsCertProvider{certDir: dir}

	loaded, err := p.GetCertificate(nil)
	if err != nil || loaded == nil {
		t.Fatalf("initial load: cert=%v err=%v", loaded, err)
	}
	if err := os.Remove(filepath.Join(dir, "tls.crt")); err != nil {
		t.Fatal(err)
	}
	got, err := p.GetCertificate(nil)
	if err != nil || got != loaded {
		t.Fatalf("missing projected file replaced last good cert: got=%p want=%p err=%v", got, loaded, err)
	}
}

// A set-but-unreadable cert dir must mark the missing-files episode (logged once)
// and clear it again once a valid pair is installed, so a later outage re-logs.
func TestMetricsCertProviderMissingFilesEpisodeLifecycle(t *testing.T) {
	dir := t.TempDir()
	p := &metricsCertProvider{certDir: dir}

	if _, err := p.GetCertificate(nil); err != nil {
		t.Fatalf("empty dir fallback: %v", err)
	}
	p.mu.Lock()
	logged := p.loggedMissing
	p.mu.Unlock()
	if !logged {
		t.Fatal("unreadable files must mark the missing episode as logged")
	}

	writeTestPair(t, dir)
	if _, err := p.GetCertificate(nil); err != nil {
		t.Fatalf("load after projection: %v", err)
	}
	p.mu.Lock()
	logged = p.loggedMissing
	p.mu.Unlock()
	if logged {
		t.Fatal("successful install must close the missing episode so a later outage re-logs")
	}

	if err := os.Remove(filepath.Join(dir, "tls.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetCertificate(nil); err != nil {
		t.Fatalf("call with half-missing pair: %v", err)
	}
	p.mu.Lock()
	logged = p.loggedMissing
	p.mu.Unlock()
	if !logged {
		t.Fatal("re-appearing unreadable files must open a new missing episode")
	}
}

// An empty --metrics-cert-dir is a supported configuration that means
// "self-signed, no service-ca". It must not take the parse path (there is
// nothing on disk to parse) and must not consume the once-per-corrupt-content
// log slot with a false "failed to parse" error.
func TestMetricsCertProviderEmptyCertDirSkipsParse(t *testing.T) {
	p := &metricsCertProvider{certDir: ""}
	c, err := p.GetCertificate(nil)
	if err != nil {
		t.Fatalf("empty certDir: %v", err)
	}
	if c == nil {
		t.Fatal("empty certDir must still serve a certificate")
	}
	if c2, err := p.GetCertificate(nil); err != nil || c2 != c {
		t.Fatal("expected the cached self-signed pair on the second handshake")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loggedBad {
		t.Fatal("empty certDir must not be reported as a corrupt cert/key pair")
	}
	if p.cert != nil {
		t.Fatal("empty certDir must not install a cached service certificate")
	}
}

func TestIsLoopbackMetricsAddr(t *testing.T) {
	for _, a := range []string{"0", "127.0.0.1:8080", "localhost:8443", "[::1]:8443"} {
		if !isLoopbackMetricsAddr(a) {
			t.Fatalf("%q should be loopback", a)
		}
	}
	// Empty is not safe: controller-runtime defaults it to ":8080" (all interfaces).
	for _, a := range []string{"", ":8443", "0.0.0.0:8443", "[::]:8443"} {
		if isLoopbackMetricsAddr(a) {
			t.Fatalf("%q should not be loopback", a)
		}
	}
}

func TestValidateListenAddr(t *testing.T) {
	for _, a := range []string{":8443", "127.0.0.1:8081", "[::1]:8443", "0.0.0.0:8443"} {
		if err := validateListenAddr(a, false); err != nil {
			t.Fatalf("%q should be valid: %v", a, err)
		}
	}
	if err := validateListenAddr("0", true); err != nil {
		t.Fatalf("0 with disableOK should be valid: %v", err)
	}
	for _, a := range []string{"", "0", "bogus", "127.0.0.1", ":0", ":65536", "host:notaport", "[::1]"} {
		if err := validateListenAddr(a, false); err == nil {
			t.Fatalf("%q should be invalid", a)
		}
	}
	if err := validateListenAddr("0", false); err == nil {
		t.Fatal("0 without disableOK should be invalid (probe cannot use metrics disable)")
	}
}

func TestValidateMetricsCertDir(t *testing.T) {
	if err := validateMetricsCertDir(""); err != nil {
		t.Fatalf("empty should be valid (self-signed only): %v", err)
	}
	if err := validateMetricsCertDir("/var/run/metrics-certs"); err != nil {
		t.Fatalf("absolute path should be valid: %v", err)
	}
	for _, d := range []string{"metrics-certs", "./certs", "var/run/metrics-certs", "certs/"} {
		if err := validateMetricsCertDir(d); err == nil {
			t.Fatalf("%q should be rejected (relative)", d)
		}
	}
}

func TestParseEnvBool(t *testing.T) {
	const key = "BASELINE_SECURITY_SKIP_DEFAULT_CR"
	for _, v := range []string{"true", "TRUE", " True ", "1", "yes", "YES", "on", "ON", "y", "t", "enable", "enabled"} {
		t.Setenv(key, v)
		got, err := parseEnvBool(key)
		if err != nil || !got {
			t.Fatalf("%q should be true: got=%v err=%v", v, got, err)
		}
	}
	for _, v := range []string{"", "false", "FALSE", " 0 ", "no", "NO", "off", "n", "f", "disable", "disabled"} {
		t.Setenv(key, v)
		got, err := parseEnvBool(key)
		if err != nil || got {
			t.Fatalf("%q should be false: got=%v err=%v", v, got, err)
		}
	}
	for _, v := range []string{"maybe", " truex", "onx", "skip", "true extra"} {
		t.Setenv(key, v)
		got, err := parseEnvBool(key)
		if err == nil {
			t.Fatalf("%q should be rejected, got %v", v, got)
		}
		if !errors.Is(err, errInvalidEnvBool) {
			t.Fatalf("%q err = %v, want %v", v, err, errInvalidEnvBool)
		}
		if got {
			t.Fatalf("%q must not return true on error", v)
		}
	}
	t.Setenv(key, "")
	got, err := parseEnvBool("BASELINE_SECURITY_UNSET_KEY")
	if err != nil || got {
		t.Fatalf("unset key should be false: got=%v err=%v", got, err)
	}
}

func TestLookupFlagMissing(t *testing.T) {
	if got := lookupFlag("this-flag-does-not-exist"); got != "" {
		t.Fatalf("missing flag = %q, want empty", got)
	}
}

// A multi-kilobyte junk value must still fail closed and not paste the whole
// blob into the error (setup logs would balloon).
func TestParseEnvBoolTruncatesRejectedValue(t *testing.T) {
	const key = "BASELINE_SECURITY_SKIP_DEFAULT_CR"
	t.Setenv(key, strings.Repeat("x", envBoolValueMaxLog+32))
	_, err := parseEnvBool(key)
	if err == nil {
		t.Fatal("oversize junk must be rejected")
	}
	if !strings.Contains(err.Error(), "...") {
		t.Fatalf("rejected value should be truncated: %v", err)
	}
	if strings.Count(err.Error(), "x") > envBoolValueMaxLog {
		t.Fatalf("error still contains the full value: len=%d", len(err.Error()))
	}
}

// A mostly-multibyte rejected value must truncate on a rune boundary. A byte
// slice cuts mid-rune here, and the invalid byte reaches the error string
// escaped as \x.., so the setup log shows mojibake for a value the admin set.
func TestParseEnvBoolTruncationKeepsValidUTF8(t *testing.T) {
	const key = "BASELINE_SECURITY_SKIP_DEFAULT_CR"
	// 'é' is 2 bytes: 64 of them is 128 bytes, so the old byte-slice cap cut
	// inside the 33rd rune. '€' is 3 bytes and exercises a different stride.
	for _, filler := range []string{"é", "€", "💩"} {
		t.Setenv(key, strings.Repeat(filler, envBoolValueMaxLog+8)+"junk")
		_, err := parseEnvBool(key)
		if err == nil {
			t.Fatalf("%q: oversize junk must be rejected", filler)
		}
		if !strings.Contains(err.Error(), "...") {
			t.Fatalf("%q: rejected value should be truncated: %v", filler, err)
		}
		if strings.Contains(err.Error(), `\x`) {
			t.Fatalf("%q: truncation split a rune: %v", filler, err)
		}
		if !utf8.ValidString(err.Error()) {
			t.Fatalf("%q: error is not valid UTF-8: %q", filler, err)
		}
		// The cap is in runes, so exactly envBoolValueMaxLog survive.
		if got := strings.Count(err.Error(), filler); got != envBoolValueMaxLog {
			t.Fatalf("%q: kept %d runes, want %d", filler, got, envBoolValueMaxLog)
		}
	}
}

// FuzzParseEnvBool: BASELINE_SECURITY_SKIP_DEFAULT_CR is untrusted env text.
// Known spellings must parse; everything else must error rather than silently
// create (or skip) the default ClusterBaseline.
func FuzzParseEnvBool(f *testing.F) {
	for _, seed := range []string{
		"", "true", "false", "1", "0", "yes", "no", "on", "off",
		"maybe", "skip", "TRUE", " True ", "onx", "true extra", "enable",
		"disable", strings.Repeat("x", 80),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, v string) {
		if len(v) > 256 {
			v = v[:256]
		}
		// execve rejects a NUL inside an environment value, so no such value
		// can reach parseEnvBool; t.Setenv would fail before the call itself.
		if strings.ContainsRune(v, 0) {
			t.Skip("NUL cannot appear in an environment value")
		}
		t.Setenv("BASELINE_SECURITY_SKIP_DEFAULT_CR", v)
		got, err := parseEnvBool("BASELINE_SECURITY_SKIP_DEFAULT_CR")
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on", "y", "t", "enable", "enabled":
			if err != nil || !got {
				t.Fatalf("truthy %q: got=%v err=%v", v, got, err)
			}
		case "", "0", "false", "no", "off", "n", "f", "disable", "disabled":
			if err != nil || got {
				t.Fatalf("falsy %q: got=%v err=%v", v, got, err)
			}
		default:
			if err == nil {
				t.Fatalf("unknown %q must error", v)
			}
			if !errors.Is(err, errInvalidEnvBool) {
				t.Fatalf("unknown %q err = %v, want %v", v, err, errInvalidEnvBool)
			}
			if got {
				t.Fatalf("unknown %q must not return true", v)
			}
		}
	})
}

func TestMetricsTLSOptsMinVersion(t *testing.T) {
	opts := metricsTLSOpts(t.TempDir())
	if len(opts) != 1 {
		t.Fatalf("opts len = %d", len(opts))
	}
	cfg := &tls.Config{}
	opts[0](cfg)
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %d, want TLS 1.2", cfg.MinVersion)
	}
	if cfg.GetCertificate == nil {
		t.Fatal("GetCertificate not set")
	}
	// HTTP/2 is disabled on the metrics endpoint as a Rapid Reset (CVE-2023-44487
	// / CVE-2023-39325) mitigation. Pin it: dropping NextProtos re-enables h2 and
	// reopens the DoS with no other test noticing.
	if len(cfg.NextProtos) != 1 || cfg.NextProtos[0] != "http/1.1" {
		t.Fatalf("NextProtos = %v, want [http/1.1] (HTTP/2 must stay disabled)", cfg.NextProtos)
	}
}

func concurrentCertificates(t *testing.T, p *metricsCertProvider, n int) []*tls.Certificate {
	t.Helper()
	var wg sync.WaitGroup
	certs := make([]*tls.Certificate, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			certs[i], errs[i] = p.GetCertificate(nil)
		}()
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil || certs[i] == nil {
			t.Fatalf("goroutine %d: cert=%v err=%v", i, certs[i], errs[i])
		}
	}
	return certs
}

func requireSameCertificate(t *testing.T, certs []*tls.Certificate, msg string) {
	t.Helper()
	for i := 1; i < len(certs); i++ {
		if certs[i] != certs[0] {
			t.Fatal(msg)
		}
	}
}

// Concurrent handshakes share one self-signed identity and never return nil/err
// under the empty-dir fallback path (GetCertificate is on the TLS hot path).
func TestMetricsCertProviderConcurrentSelfSigned(t *testing.T) {
	p := &metricsCertProvider{certDir: t.TempDir()}
	requireSameCertificate(t, concurrentCertificates(t, p, 32),
		"concurrent first load produced multiple self-signed identities")
}

// Concurrent loads of the same on-disk pair share one cached certificate pointer.
func TestMetricsCertProviderConcurrentLoad(t *testing.T) {
	dir := t.TempDir()
	writeTestPair(t, dir)
	p := &metricsCertProvider{certDir: dir}
	requireSameCertificate(t, concurrentCertificates(t, p, 32),
		"concurrent load produced multiple certificate pointers for one fingerprint")
}

// A failed under-lock re-read must not install a stale outer parse over a cert
// that a concurrent handshake already published (re-read !ok used to fall through).
func TestMetricsCertProviderRereadFailureKeepsCachedCert(t *testing.T) {
	dir := t.TempDir()
	writeTestPair(t, dir)
	p := &metricsCertProvider{certDir: dir}
	seeded, err := p.GetCertificate(nil)
	if err != nil || seeded == nil {
		t.Fatalf("seed: cert=%v err=%v", seeded, err)
	}
	p.mu.Lock()
	seededFP := p.fingerprint
	p.mu.Unlock()

	// New on-disk pair so the outer read is a cache miss and parse runs.
	writeTestPair(t, dir)
	var reads atomic.Int32
	p.readPair = func(certPath, keyPath string) ([]byte, []byte, [32]byte, error) {
		n := reads.Add(1)
		certPEM, keyPEM, fp, err := readCertPair(certPath, keyPath)
		if n == 1 {
			// Outer read succeeds (new content).
			return certPEM, keyPEM, fp, err
		}
		// Under-lock re-read fails (transient projection blip).
		return nil, nil, [32]byte{}, errors.New("injected re-read failure")
	}
	got, err := p.GetCertificate(nil)
	if err != nil || got != seeded {
		t.Fatalf("reread failure replaced cache: got=%p want=%p err=%v", got, seeded, err)
	}
	p.mu.Lock()
	cachedFP := p.fingerprint
	p.mu.Unlock()
	if cachedFP != seededFP {
		t.Fatal("cache fingerprint changed after failed re-read")
	}
}

// Concurrent handshakes during an on-disk rotation must not leave the cache on
// a pair whose fingerprint no longer matches disk (stale parse overwriting a
// newer install). After the dust settles, GetCertificate matches the final files.
func TestMetricsCertProviderConcurrentReload(t *testing.T) {
	dir := t.TempDir()
	writeTestPair(t, dir)
	p := &metricsCertProvider{certDir: dir}
	// Seed cache with the first pair.
	if _, err := p.GetCertificate(nil); err != nil {
		t.Fatal(err)
	}

	const n = 32
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if i == n/2 {
				// Mid-flight rotation while others load/reload.
				writeTestPair(t, dir)
			}
			_, errs[i] = p.GetCertificate(nil)
		}()
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
	}
	// Final load must match current on-disk pair (not a stale overwritten cert).
	got, err := p.GetCertificate(nil)
	if err != nil || got == nil {
		t.Fatalf("final load: cert=%v err=%v", got, err)
	}
	_, _, wantFP, readErr := readCertPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	if readErr != nil {
		t.Fatalf("final on-disk pair unreadable: %v", readErr)
	}
	p.mu.Lock()
	cachedFP := p.fingerprint
	cached := p.cert
	p.mu.Unlock()
	if cached != got {
		t.Fatal("final GetCertificate did not return cached cert")
	}
	if cachedFP != wantFP {
		t.Fatal("cache fingerprint does not match final on-disk pair after concurrent reload")
	}
}

// A rotation must not be observable as a truncated file. writeTestPair renames
// each path into place, so a reader racing the writer sees the whole old file or
// the whole new one; the in-place write it replaced left a truncate-to-zero window
// that concurrent handshakes could read as an empty Secret.
func TestWriteTestPairIsAtomicUnderConcurrentReads(t *testing.T) {
	dir := t.TempDir()
	writeTestPair(t, dir)
	// Large enough that the truncate-then-write window of an in-place write spans
	// many read attempts rather than a few instructions.
	big := bytes.Repeat([]byte("-----BEGIN CERTIFICATE-----\n"), 4096)
	// Establish the post-rotation size before the reader starts, so every read
	// is compared against the same length.
	writeFileAtomic(t, filepath.Join(dir, "tls.crt"), big)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var short atomic.Int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, p := range []string{"tls.crt", "tls.key"} {
				b, err := os.ReadFile(filepath.Join(dir, p))
				if err != nil {
					continue
				}
				if len(b) == 0 || (p == "tls.crt" && len(b) < len(big)) {
					short.Add(1)
				}
			}
		}
	}()
	for i := 0; i < 50; i++ {
		writeFileAtomic(t, filepath.Join(dir, "tls.crt"), big)
	}
	close(stop)
	wg.Wait()
	if n := short.Load(); n > 0 {
		t.Fatalf("observed %d truncated reads during rotation", n)
	}
}

// FuzzMetricsCertCorruptPair: service-ca Secret projections can be partial,
// truncated, or binary garbage during rotation. GetCertificate is on the TLS
// handshake hot path and must never panic; corrupt pairs fall back to last-good
// or self-signed so metrics stay available.
func FuzzMetricsCertCorruptPair(f *testing.F) {
	f.Add([]byte(""), []byte(""))
	f.Add([]byte("not-a-pem"), []byte("also-not"))
	f.Add([]byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"),
		[]byte("-----BEGIN PRIVATE KEY-----\nBBBB\n-----END PRIVATE KEY-----\n"))
	f.Add([]byte{0x00, 0xff, 0x30, 0x82}, []byte{0x30, 0x82, 0x00})
	f.Fuzz(func(t *testing.T, certPEM, keyPEM []byte) {
		// Bound I/O: real Secrets are small; huge blobs only stress the fuzzer.
		const max = 8192
		if len(certPEM) > max {
			certPEM = certPEM[:max]
		}
		if len(keyPEM) > max {
			keyPEM = keyPEM[:max]
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "tls.crt"), certPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "tls.key"), keyPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		p := &metricsCertProvider{certDir: dir}
		c, err := p.GetCertificate(nil)
		if err != nil || c == nil {
			t.Fatalf("GetCertificate must return a cert (self-signed fallback): cert=%v err=%v", c, err)
		}
		// Second call: either cache hit (valid pair) or sticky bad + self-signed.
		c2, err2 := p.GetCertificate(nil)
		if err2 != nil || c2 == nil {
			t.Fatalf("second GetCertificate: cert=%v err=%v", c2, err2)
		}
	})
}

// FuzzIsLoopbackMetricsAddr: --metrics-bind-address is operator config but
// shapes a security decision (whether insecure metrics are allowed). Hostile
// or partial addresses must never panic; only disabled/"0" and explicit
// loopback hosts classify as loopback.
func FuzzIsLoopbackMetricsAddr(f *testing.F) {
	for _, seed := range []string{
		"", "0", ":8443", "127.0.0.1:8080", "localhost:8443", "[::1]:8443",
		"0.0.0.0:8443", "[::]:8443", "example.com:8443", "127.0.0.1",
		"localhost", "::1", "[::1]", "127.0.0.1:", ":::8443",
		"127.0.0.1:8443:extra", " 127.0.0.1:8443",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, addr string) {
		if len(addr) > 512 {
			addr = addr[:512]
		}
		got := isLoopbackMetricsAddr(addr)
		if addr == "0" {
			if !got {
				t.Fatal(`"0" must be loopback (disabled metrics)`)
			}
			return
		}
		host := addr
		if i := strings.LastIndex(addr, ":"); i >= 0 {
			host = addr[:i]
		}
		host = strings.Trim(host, "[]")
		want := host == "127.0.0.1" || host == "localhost" || host == "::1"
		if got != want {
			t.Fatalf("isLoopbackMetricsAddr(%q) = %v, want %v (host=%q)", addr, got, want, host)
		}
	})
}

// FuzzValidateMetricsCertDir: --metrics-cert-dir is operator config (Deployment
// args). Empty is valid (self-signed only); absolute paths are valid; relative
// paths are always rejected so CWD-dependent dirs cannot slip through.
func FuzzValidateMetricsCertDir(f *testing.F) {
	for _, seed := range []string{
		"", "/", "/var/run/metrics-certs", "/tmp/x",
		"metrics-certs", "./certs", "var/run/metrics-certs", "certs/",
		"//absolute-looking", "C:\\windows", "\\relative",
		"/", strings.Repeat("a", 400),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, dir string) {
		if len(dir) > 1024 {
			dir = dir[:1024]
		}
		err := validateMetricsCertDir(dir)
		if dir == "" {
			if err != nil {
				t.Fatalf("empty must be valid: %v", err)
			}
			return
		}
		if filepath.IsAbs(dir) {
			if err != nil {
				t.Fatalf("absolute %q must be valid: %v", dir, err)
			}
			return
		}
		if err == nil {
			t.Fatalf("relative %q must be rejected", dir)
		}
	})
}

// FuzzValidateListenAddr: --metrics-bind-address / --health-probe-bind-address
// come from Deployment args and must never panic. Valid host:port (1-65535) is
// accepted; "0" only when disableOK (metrics). Empty and garbage always fail.
func FuzzValidateListenAddr(f *testing.F) {
	for _, seed := range []string{
		"", "0", ":8443", "127.0.0.1:8081", "[::1]:8443", "0.0.0.0:8443",
		"bogus", "127.0.0.1", ":0", ":65536", "host:notaport", "[::1]",
		":::8443", "127.0.0.1:8443:extra", " :8443", "127.0.0.1:",
		":1", ":65535", "localhost:0", "localhost:65536",
	} {
		f.Add(seed, true)
		f.Add(seed, false)
	}
	f.Fuzz(func(t *testing.T, addr string, disableOK bool) {
		if len(addr) > 512 {
			addr = addr[:512]
		}
		err := validateListenAddr(addr, disableOK)
		if disableOK && addr == "0" {
			if err != nil {
				t.Fatalf(`"0" with disableOK must be valid: %v`, err)
			}
			return
		}
		if err == nil {
			// Accepted: host:port with numeric port in 1..65535.
			_, port, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				t.Fatalf("accepted but SplitHostPort failed: addr=%q err=%v", addr, splitErr)
			}
			p, atoiErr := strconv.Atoi(port)
			if atoiErr != nil || p < 1 || p > 65535 {
				t.Fatalf("accepted invalid port: addr=%q port=%q", addr, port)
			}
		}
		// Rejected path: only assert no panic (err non-nil). No further shape.
		_ = err
	})
}
