package main

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
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
