package main

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	certutil "k8s.io/client-go/util/cert"
)

func writeTestPair(t *testing.T, dir string) {
	t.Helper()
	certPEM, keyPEM, err := certutil.GenerateSelfSignedCertKeyWithFixtures(
		"localhost", []net.IP{{127, 0, 0, 1}}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.crt"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.key"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
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

func TestEnvTruthy(t *testing.T) {
	for _, v := range []string{"true", "TRUE", " True ", "1", "yes", "YES"} {
		t.Setenv("BASELINE_SECURITY_SKIP_DEFAULT_CR", v)
		if !envTruthy("BASELINE_SECURITY_SKIP_DEFAULT_CR") {
			t.Fatalf("%q should be truthy", v)
		}
	}
	for _, v := range []string{"", "false", "0", "no", "maybe", " truex"} {
		t.Setenv("BASELINE_SECURITY_SKIP_DEFAULT_CR", v)
		if envTruthy("BASELINE_SECURITY_SKIP_DEFAULT_CR") {
			t.Fatalf("%q should be falsy", v)
		}
	}
	t.Setenv("BASELINE_SECURITY_SKIP_DEFAULT_CR", "")
	if envTruthy("BASELINE_SECURITY_UNSET_KEY") {
		t.Fatal("unset key should be falsy")
	}
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

// Concurrent handshakes share one self-signed identity and never return nil/err
// under the empty-dir fallback path (GetCertificate is on the TLS hot path).
func TestMetricsCertProviderConcurrentSelfSigned(t *testing.T) {
	p := &metricsCertProvider{certDir: t.TempDir()}
	const n = 32
	var wg sync.WaitGroup
	certs := make([]*tls.Certificate, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			certs[i], errs[i] = p.GetCertificate(nil)
		}()
	}
	wg.Wait()
	var first *tls.Certificate
	for i := 0; i < n; i++ {
		if errs[i] != nil || certs[i] == nil {
			t.Fatalf("goroutine %d: cert=%v err=%v", i, certs[i], errs[i])
		}
		if first == nil {
			first = certs[i]
		} else if certs[i] != first {
			t.Fatal("concurrent first load produced multiple self-signed identities")
		}
	}
}

// Concurrent loads of the same on-disk pair share one cached certificate pointer.
func TestMetricsCertProviderConcurrentLoad(t *testing.T) {
	dir := t.TempDir()
	writeTestPair(t, dir)
	p := &metricsCertProvider{certDir: dir}
	const n = 32
	var wg sync.WaitGroup
	certs := make([]*tls.Certificate, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			certs[i], errs[i] = p.GetCertificate(nil)
		}()
	}
	wg.Wait()
	var first *tls.Certificate
	for i := 0; i < n; i++ {
		if errs[i] != nil || certs[i] == nil {
			t.Fatalf("goroutine %d: cert=%v err=%v", i, certs[i], errs[i])
		}
		if first == nil {
			first = certs[i]
		} else if certs[i] != first {
			t.Fatal("concurrent load produced multiple certificate pointers for one fingerprint")
		}
	}
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
	p.readPair = func(certPath, keyPath string) ([]byte, []byte, [32]byte, bool) {
		n := reads.Add(1)
		certPEM, keyPEM, fp, ok := readCertPair(certPath, keyPath)
		if n == 1 {
			// Outer read succeeds (new content).
			return certPEM, keyPEM, fp, ok
		}
		// Under-lock re-read fails (transient projection blip).
		return nil, nil, [32]byte{}, false
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
		i := i
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
	_, _, wantFP, ok := readCertPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	if !ok {
		t.Fatal("final on-disk pair missing")
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
