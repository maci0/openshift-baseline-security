package main

import (
	"crypto/sha256"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"sync"

	certutil "k8s.io/client-go/util/cert"
	ctrl "sigs.k8s.io/controller-runtime"
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
	// badFingerprint is the last corrupt on-disk pair we logged so a sticky
	// parse failure does not spam every TLS handshake.
	badFingerprint [sha256.Size]byte
	loggedBad      bool

	// readPair overrides on-disk reads (tests only). Production leaves nil.
	readPair func(certPath, keyPath string) ([]byte, []byte, [sha256.Size]byte, bool)
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

func (p *metricsCertProvider) loadCertPair(certPath, keyPath string) ([]byte, []byte, [sha256.Size]byte, bool) {
	if p.readPair != nil {
		return p.readPair(certPath, keyPath)
	}
	return readCertPair(certPath, keyPath)
}

func (p *metricsCertProvider) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	// One retry when disk rotates between read and install so a slow parse of an
	// older pair cannot overwrite a newer cert installed by a concurrent handshake.
	for attempt := 0; attempt < 2; attempt++ {
		var (
			certPEM, keyPEM []byte
			fingerprint     [sha256.Size]byte
			haveFiles       bool
		)
		if p.certDir != "" {
			certPath := filepath.Join(p.certDir, "tls.crt")
			keyPath := filepath.Join(p.certDir, "tls.key")
			certPEM, keyPEM, fingerprint, haveFiles = p.loadCertPair(certPath, keyPath)
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
				// Stale-parse guard: re-read under the lock so we never install an
				// older parse over a different fingerprint that is still (or now)
				// on disk after a concurrent rotation. A re-read failure must not
				// fall through to install either: a concurrent handshake may have
				// published a newer pair while our parse of an older read was in
				// flight, and overwriting it would leave the cache on rotated-out
				// material until the next successful load.
				if p.certDir != "" {
					_, _, currentFP, ok := p.loadCertPair(
						filepath.Join(p.certDir, "tls.crt"),
						filepath.Join(p.certDir, "tls.key"),
					)
					if !ok || currentFP != fingerprint {
						if p.cert != nil {
							// Cache holds something else (often the concurrent winner),
							// or disk is briefly unreadable: keep last known-good.
							c := p.cert
							p.mu.Unlock()
							return c, nil
						}
						if ok {
							// Empty cache and disk moved: re-read/parse once.
							p.mu.Unlock()
							continue
						}
						// Empty cache and re-read failed: install this parse so
						// metrics TLS still has a cert (best effort).
					}
				}
				p.cert = &pair
				p.fingerprint = fingerprint
				// Clear sticky bad log so a later rotation of the same path re-logs.
				p.loggedBad = false
				c := p.cert
				p.mu.Unlock()
				return c, nil
			}
			// Partial/corrupt Secret: fall through to last good / self-signed.
			// Log once per corrupt content so scrapers failing TLS are debuggable
			// without spamming every handshake while the Secret stays broken.
			p.mu.Lock()
			if !p.loggedBad || fingerprint != p.badFingerprint {
				p.loggedBad = true
				p.badFingerprint = fingerprint
				p.mu.Unlock()
				// Structured (not stdlib log): matches operator zap fields so log
				// aggregation can filter metrics-cert failures with the rest of
				// the process. Still once-per-corrupt-content (sticky fingerprint).
				ctrl.Log.WithName("metrics-cert").Error(err,
					"failed to parse metrics TLS cert/key; using previous or self-signed",
					"certDir", p.certDir)
			} else {
				p.mu.Unlock()
			}
		}
		break
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
			// Disable HTTP/2 on the metrics endpoint: it mitigates the Rapid
			// Reset stream-multiplexing DoS (CVE-2023-44487, CVE-2023-39325)
			// at the protocol layer, before the authn/authz filter runs.
			// Prometheus scrapes over HTTP/1.1, so this is functionally inert.
			c.NextProtos = []string{"http/1.1"}
		},
	}
}
