package main

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	certutil "k8s.io/client-go/util/cert"
)

// metricsCertProvider loads service-ca certs from certDir when present and
// reloads when either tls.crt or tls.key mtime advances. Falls back to a
// one-shot self-signed cert so the metrics server can start before the Secret
// exists (optional volume).
type metricsCertProvider struct {
	certDir string

	mu         sync.Mutex
	cert       *tls.Certificate
	modTime    time.Time
	selfSigned *tls.Certificate
}

func certPairModTime(certPath, keyPath string) (time.Time, bool) {
	cfi, err := os.Stat(certPath)
	if err != nil {
		return time.Time{}, false
	}
	kfi, err := os.Stat(keyPath)
	if err != nil {
		return time.Time{}, false
	}
	mt := cfi.ModTime()
	if kfi.ModTime().After(mt) {
		mt = kfi.ModTime()
	}
	return mt, true
}

func (p *metricsCertProvider) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.certDir != "" {
		certPath := filepath.Join(p.certDir, "tls.crt")
		keyPath := filepath.Join(p.certDir, "tls.key")
		if mt, ok := certPairModTime(certPath, keyPath); ok {
			if p.cert != nil && !mt.After(p.modTime) {
				return p.cert, nil
			}
			// Load under the lock: rare (startup / cert rotation).
			pair, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err == nil {
				p.cert = &pair
				p.modTime = mt
				return p.cert, nil
			}
			// Partial/corrupt Secret: keep last good cert if any.
			if p.cert != nil {
				return p.cert, nil
			}
		}
	}

	if p.selfSigned != nil {
		return p.selfSigned, nil
	}
	certPEM, keyPEM, err := certutil.GenerateSelfSignedCertKeyWithFixtures(
		"localhost", []net.IP{{127, 0, 0, 1}}, nil, "")
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	p.selfSigned = &pair
	return p.selfSigned, nil
}

func metricsTLSOpts(certDir string) []func(*tls.Config) {
	p := &metricsCertProvider{certDir: certDir}
	return []func(*tls.Config){
		func(c *tls.Config) {
			c.GetCertificate = p.GetCertificate
		},
	}
}
