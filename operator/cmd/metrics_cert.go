package main

import (
	"crypto/sha256"
	"crypto/tls"
	"fmt"
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
// GetCertificate is called from concurrent TLS handshakes. The steady-state
// path is a cache hit under the mutex: one file read, no parse, no lock held
// across I/O.
//
// A cache miss (startup, or service-ca rotation) does the expensive work
// outside the mutex: the read and the X509KeyPair parse never hold it, so a
// slow reload cannot stall concurrent handshakes that are still serving the
// last known-good certificate.
//
// One read IS taken under the mutex, and it is load-bearing, not an oversight:
// the freshness re-read that guards against installing a stale parse. A
// handshake that read the old pair, parsed it, and blocked on the mutex can
// otherwise overwrite a newer pair a concurrent handshake already published,
// leaving the cache on rotated-out material. Re-reading inside the critical
// section is what makes "still on disk" and "about to install" atomic. Do not
// hoist it out to shorten the critical section: a pre-lock read cannot
// distinguish "disk still holds my pair" from "another handshake won the
// rotation while I was parsing", and TestMetricsCertProviderConcurrentReload
// covers exactly that interleaving.
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

	// loggedMissing marks the unreadable-files episode already logged so a
	// never-projected Secret does not spam every TLS handshake either.
	loggedMissing bool

	// readPair overrides on-disk reads (tests only). Production leaves nil.
	// It is invoked with p.mu held on the freshness re-read path, so an
	// implementation that takes its own lock to serialize simulated disk
	// rotation inverts lock order and deadlocks every concurrent handshake.
	// Serialize test-side state outside readPair, not inside it.
	readPair func(certPath, keyPath string) ([]byte, []byte, [sha256.Size]byte, error)
}

func readCertPair(certPath, keyPath string) ([]byte, []byte, [sha256.Size]byte, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, [sha256.Size]byte{}, fmt.Errorf("read %s: %w", certPath, err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, [sha256.Size]byte{}, fmt.Errorf("read %s: %w", keyPath, err)
	}
	h := sha256.New()
	_, _ = h.Write(certPEM)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(keyPEM)
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], h.Sum(nil))
	return certPEM, keyPEM, fingerprint, nil
}

func (p *metricsCertProvider) loadCertPair(certPath, keyPath string) ([]byte, []byte, [sha256.Size]byte, error) {
	if p.readPair != nil {
		return p.readPair(certPath, keyPath)
	}
	return readCertPair(certPath, keyPath)
}

func (p *metricsCertProvider) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	// One retry when disk rotates between read and install so a slow parse of an
	// older pair cannot overwrite a newer cert installed by a concurrent handshake.
	for attempt := 0; attempt < 2; attempt++ {
		// No cert dir configured: there is nothing on disk to read or parse, so
		// skip straight to the self-signed fallback below. Parsing the zero-length
		// pair would only produce a "failed to parse metrics TLS cert/key" error
		// for a configuration that is self-signed by design, and that false
		// alarm would consume the once-per-episode corrupt-Secret log slot.
		if p.certDir == "" {
			break
		}
		certPath := filepath.Join(p.certDir, "tls.crt")
		keyPath := filepath.Join(p.certDir, "tls.key")
		certPEM, keyPEM, fingerprint, readErr := p.loadCertPair(certPath, keyPath)
		if readErr != nil {
			// A set-but-unreadable dir (wrong mount, Secret never projected)
			// otherwise falls back to self-signed with no breadcrumb while
			// service-ca scrapers fail TLS trust. Log once per episode.
			p.logMissingOnce(readErr)
		}

		// Cache hit: same on-disk content as last successful load.
		p.mu.Lock()
		if readErr == nil && p.cert != nil && fingerprint == p.fingerprint {
			c := p.cert
			p.mu.Unlock()
			return c, nil
		}
		p.mu.Unlock()

		// Parse outside the lock: rare (startup / cert rotation) but can be slow.
		if readErr == nil {
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
					_, _, currentFP, rerr := p.loadCertPair(
						filepath.Join(p.certDir, "tls.crt"),
						filepath.Join(p.certDir, "tls.key"),
					)
					if rerr != nil || currentFP != fingerprint {
						if p.cert != nil {
							// Cache holds something else (often the concurrent winner),
							// or disk is briefly unreadable: keep last known-good.
							c := p.cert
							p.mu.Unlock()
							return c, nil
						}
						if rerr == nil {
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
				// Clear sticky bad/missing logs so a later rotation of the same
				// path re-logs.
				p.loggedBad = false
				p.loggedMissing = false
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

// logMissingOnce logs unreadable cert files once per episode: until a pair is
// successfully installed again, repeated handshakes stay quiet while the first
// failure keeps its cause for whoever correlates scraper TLS errors.
func (p *metricsCertProvider) logMissingOnce(readErr error) {
	p.mu.Lock()
	if p.loggedMissing {
		p.mu.Unlock()
		return
	}
	p.loggedMissing = true
	p.mu.Unlock()
	ctrl.Log.WithName("metrics-cert").Error(readErr,
		"metrics TLS cert/key files unreadable; using previous or self-signed",
		"certDir", p.certDir)
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
