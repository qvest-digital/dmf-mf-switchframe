package peer

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrCertHashRequired is returned from ValidatePeerTLSConfig when the peer
	// URL resolves to an RFC 1918 IP but no --peer-cert-hash is set. Standard
	// Let's Encrypt certs cover the FQDN only, so connecting to the private
	// IP without a pin would either break (SAN mismatch) or silently
	// downgrade; we refuse at startup.
	ErrCertHashRequired = errors.New("peer: --peer-cert-hash required when --peer-engine-url uses an RFC 1918 IP")

	// ErrInvalidCertHash is returned when a configured pin is not 64 hex
	// characters (SHA-256 of the leaf DER).
	ErrInvalidCertHash = errors.New("peer: --peer-cert-hash must be 64-character hex SHA-256 of the leaf certificate DER")

	// ErrCertHashMismatch is returned from the TLS VerifyPeerCertificate
	// callback when the peer's leaf cert does not match any configured pin.
	ErrCertHashMismatch = errors.New("peer: leaf certificate hash does not match any configured --peer-cert-hash pin")
)

// IsRFC1918Host returns true if host is an IPv4 literal in 10/8, 172.16/12, or
// 192.168/16. Hostnames, loopback, link-local, and IPv6 all return false.
// Used to decide whether cert-hash pinning must be required.
func IsRFC1918Host(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	switch {
	case ip4[0] == 10:
		return true
	case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
		return true
	case ip4[0] == 192 && ip4[1] == 168:
		return true
	}
	return false
}

// ValidatePeerTLSConfig enforces the startup contract:
//   - FQDN host: pins optional; any supplied pins must be syntactically valid
//   - RFC 1918 IP host: pins are required and must be syntactically valid
//   - empty peerURL: pins are unused but still parse-checked so a typo in
//     SWITCHFRAME_PEER_CERT_HASH fails loudly at startup instead of lurking
//     until the operator later sets --peer-engine-url
func ValidatePeerTLSConfig(peerURL, certHash string) error {
	if certHash != "" {
		if _, err := parsePinList(certHash); err != nil {
			return err
		}
	}
	if peerURL == "" {
		return nil
	}
	u, err := url.Parse(peerURL)
	if err != nil {
		return fmt.Errorf("parse peer URL: %w", err)
	}
	if IsRFC1918Host(u.Hostname()) && certHash == "" {
		return ErrCertHashRequired
	}
	return nil
}

// NewPinnedTLSConfig returns a *tls.Config whose VerifyPeerCertificate accepts
// only leaf certificates whose SHA-256 hash matches one of the pins. The
// pinCSV argument is one or two 64-char hex SHA-256 values separated by a
// comma (for cert rotation: either value matches during the rotation window).
//
// InsecureSkipVerify is set to true intentionally. Let's Encrypt certs only
// cover the engine's FQDN; when --peer-engine-url uses a private IP, Go's
// default chain/hostname verification aborts the handshake before the callback
// would run. Hash pinning replaces chain verification for this narrow client:
// the control plane vends the hash out-of-band, and any mismatch fails closed.
func NewPinnedTLSConfig(pinCSV string) (*tls.Config, error) {
	pins, err := parsePinList(pinCSV)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 || len(rawCerts[0]) == 0 {
				return errors.New("peer: no peer certificate presented")
			}
			digest := sha256.Sum256(rawCerts[0])
			for _, pin := range pins {
				if subtle.ConstantTimeCompare(digest[:], pin) == 1 {
					return nil
				}
			}
			return ErrCertHashMismatch
		},
	}
	return cfg, nil
}

// NewPinnedHTTPClient returns an *http.Client configured to enforce cert-hash
// pinning against the peer engine. Timeout defaults to 3 seconds if zero.
func NewPinnedHTTPClient(pinCSV string, timeout time.Duration) (*http.Client, error) {
	tlsCfg, err := NewPinnedTLSConfig(pinCSV)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	// Defensive: some instrumented builds wrap http.DefaultTransport in a
	// round-tripper, in which case the type assertion would panic. Fall back
	// to a fresh *http.Transport if the assertion fails.
	var transport *http.Transport
	if def, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = def.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.TLSClientConfig = tlsCfg
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}, nil
}

// parsePinList validates and decodes the comma-separated hex pins into raw
// SHA-256 digests. Returns ErrInvalidCertHash on any parse error.
func parsePinList(pinCSV string) ([][]byte, error) {
	if pinCSV == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidCertHash)
	}
	parts := strings.Split(pinCSV, ",")
	out := make([][]byte, 0, len(parts))
	for _, raw := range parts {
		pin := strings.TrimSpace(raw)
		if len(pin) != sha256.Size*2 {
			return nil, fmt.Errorf("%w: %q is not 64 hex chars", ErrInvalidCertHash, pin)
		}
		b, err := hex.DecodeString(pin)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCertHash, err)
		}
		out = append(out, b)
	}
	return out, nil
}
