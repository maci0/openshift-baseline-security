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
// reloads on mtime change. Falls back to a one-shot self-signed cert so the
// metrics server can start before the Secret exists (optional volume).
type metricsCertProvider struct {
	certDir string

	mu         sync.Mutex
	cert       *tls.Certificate
	modTime    time.Time
	selfSigned *tls.Certificate
}

func (p *metricsCertProvider) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.certDir != "" {
		certPath := filepath.Join(p.certDir, "tls.crt")
		keyPath := filepath.Join(p.certDir, "tls.key")
		if fi, err := os.Stat(certPath); err == nil {
			if p.cert != nil && !fi.ModTime().After(p.modTime) {
				return p.cert, nil
			}
			// Load outside the hot path would be nicer, but keeping the lock is
			// fine: LoadX509KeyPair is rare (startup / cert rotation).
			pair, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err == nil {
				p.cert = &pair
				p.modTime = fi.ModTime()
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
