package main

import (
	"crypto/sha256"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"sync"

	certutil "k8s.io/client-go/util/cert"
)

// metricsCertProvider loads service-ca certs from certDir when present and
// reloads when either tls.crt or tls.key content changes. Falls back to a
// one-shot self-signed cert so the metrics server can start before the Secret
// exists (optional volume).
//
// GetCertificate is called from concurrent TLS handshakes. File I/O and
// X509KeyPair parsing run outside the mutex so one slow reload cannot stall
// every metrics connection; only the cache pointer swap is serialized.
type metricsCertProvider struct {
	certDir string

	mu          sync.Mutex
	cert        *tls.Certificate
	fingerprint [sha256.Size]byte
	selfSigned  *tls.Certificate
}

func readCertPair(certPath, keyPath string) ([]byte, []byte, [sha256.Size]byte, bool) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, [sha256.Size]byte{}, false
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, [sha256.Size]byte{}, false
	}
	h := sha256.New()
	_, _ = h.Write(certPEM)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(keyPEM)
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], h.Sum(nil))
	return certPEM, keyPEM, fingerprint, true
}

func (p *metricsCertProvider) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	var (
		certPEM, keyPEM []byte
		fingerprint     [sha256.Size]byte
		haveFiles       bool
	)
	if p.certDir != "" {
		certPath := filepath.Join(p.certDir, "tls.crt")
		keyPath := filepath.Join(p.certDir, "tls.key")
		certPEM, keyPEM, fingerprint, haveFiles = readCertPair(certPath, keyPath)
	}

	// Cache hit: same on-disk content as last successful load.
	p.mu.Lock()
	if haveFiles && p.cert != nil && fingerprint == p.fingerprint {
		c := p.cert
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()

	// Parse outside the lock: rare (startup / cert rotation) but can be slow.
	if haveFiles {
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err == nil {
			p.mu.Lock()
			// Another handshake may have published the same fingerprint already.
			if p.cert != nil && fingerprint == p.fingerprint {
				c := p.cert
				p.mu.Unlock()
				return c, nil
			}
			p.cert = &pair
			p.fingerprint = fingerprint
			c := p.cert
			p.mu.Unlock()
			return c, nil
		}
		// Partial/corrupt Secret: fall through to last good / self-signed.
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Secret projection updates can briefly make one path disappear. Keep the
	// last known-good service certificate instead of unexpectedly falling back
	// to a self-signed identity during that window.
	if p.cert != nil {
		return p.cert, nil
	}

	if p.selfSigned != nil {
		return p.selfSigned, nil
	}
	// Generate under the lock so concurrent first-handshakes share one identity
	// instead of racing multiple self-signed keys into the cache.
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
			// Match the console plugin nginx floor (TLS 1.2+). Go's default is
			// also 1.2 since Go 1.18, but pin it so a library default change
			// cannot reopen TLS 1.0/1.1 on the metrics endpoint.
			c.MinVersion = tls.VersionTLS12
			c.GetCertificate = p.GetCertificate
		},
	}
}
