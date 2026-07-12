package main

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
