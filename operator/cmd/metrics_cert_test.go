package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	certutil "k8s.io/client-go/util/cert"
	"net"
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
		t.Fatal("expected mtime cache hit")
	}

	writeTestPair(t, dir)
	certPath := filepath.Join(dir, "tls.crt")
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(certPath, now, now); err != nil {
		t.Fatal(err)
	}
	c3, err := p.GetCertificate(nil)
	if err != nil || c3 == nil {
		t.Fatalf("reload: %v", err)
	}
	if c3 == c1 {
		t.Fatal("expected new certificate pointer after mtime change")
	}
}
