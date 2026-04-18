package peer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func TestIsRFC1918Host(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"10.0.0.5", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.254", true},
		{"192.168.1.1", true},
		{"172.15.0.1", false},   // just below RFC 1918 172.16/12
		{"172.32.0.1", false},   // just above RFC 1918 172.16/12
		{"8.8.8.8", false},      // public
		{"127.0.0.1", false},    // loopback, not RFC 1918
		{"169.254.1.1", false},  // link-local, not RFC 1918
		{"example.com", false},  // hostname, not IP
		{"engine-b.prod", false},
		{"::1", false},         // IPv6 loopback
		{"fc00::1", false},     // IPv6 ULA is not RFC 1918; spec is v4-only
		{"", false},
	}
	for _, c := range cases {
		if got := IsRFC1918Host(c.in); got != c.want {
			t.Errorf("IsRFC1918Host(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestValidatePeerTLSConfig_FQDNIgnoresHash(t *testing.T) {
	// DNS names pass without a hash.
	if err := ValidatePeerTLSConfig("https://engine-b.prod.internal:8080", ""); err != nil {
		t.Fatalf("FQDN without hash should pass: %v", err)
	}
	// DNS + hash still passes (hash is an optional extra pin).
	if err := ValidatePeerTLSConfig("https://engine-b.prod.internal:8080",
		"aa"+makeHex(62)); err != nil {
		t.Fatalf("FQDN with hash should pass: %v", err)
	}
}

func TestValidatePeerTLSConfig_InternalIPRequiresHash(t *testing.T) {
	err := ValidatePeerTLSConfig("https://10.0.0.5:8080", "")
	if !errors.Is(err, ErrCertHashRequired) {
		t.Fatalf("internal IP without hash: want ErrCertHashRequired, got %v", err)
	}
}

func TestValidatePeerTLSConfig_EmptyURLPasses(t *testing.T) {
	if err := ValidatePeerTLSConfig("", ""); err != nil {
		t.Fatalf("empty URL + empty hash should pass (no peer configured): %v", err)
	}
}

// Even when --peer-engine-url is unset, a syntactically invalid
// --peer-cert-hash / SWITCHFRAME_PEER_CERT_HASH must fail at startup so a
// typo doesn't lurk until the operator later wires up the URL.
func TestValidatePeerTLSConfig_EmptyURLRejectsInvalidHash(t *testing.T) {
	err := ValidatePeerTLSConfig("", "not-hex")
	if !errors.Is(err, ErrInvalidCertHash) {
		t.Fatalf("empty URL + bad hash: want ErrInvalidCertHash, got %v", err)
	}
}

func TestValidatePeerTLSConfig_InvalidHash(t *testing.T) {
	cases := []string{
		"not-hex",
		"tooshort",
		"a",
		makeHex(63),      // 63 chars — odd length
		makeHex(66),      // too long
		"zz" + makeHex(62),
	}
	for _, s := range cases {
		if err := ValidatePeerTLSConfig("https://engine-b.prod.internal:8080", s); !errors.Is(err, ErrInvalidCertHash) {
			t.Errorf("ValidatePeerTLSConfig(hash=%q): want ErrInvalidCertHash, got %v", s, err)
		}
	}
}

func TestValidatePeerTLSConfig_RotationPairAccepted(t *testing.T) {
	pair := makeHex(64) + "," + makeHex(64)
	if err := ValidatePeerTLSConfig("https://10.0.0.5:8080", pair); err != nil {
		t.Fatalf("comma-separated pair on internal IP: want nil, got %v", err)
	}
}

func TestNewPinnedTLSConfig_RejectsBadInput(t *testing.T) {
	if _, err := NewPinnedTLSConfig(""); !errors.Is(err, ErrInvalidCertHash) {
		t.Errorf("empty hash: want ErrInvalidCertHash, got %v", err)
	}
	if _, err := NewPinnedTLSConfig("zz" + makeHex(62)); !errors.Is(err, ErrInvalidCertHash) {
		t.Error("non-hex rune should fail")
	}
	if _, err := NewPinnedTLSConfig("a"); !errors.Is(err, ErrInvalidCertHash) {
		t.Error("too-short hash should fail")
	}
	if _, err := NewPinnedTLSConfig(",," + makeHex(64)); !errors.Is(err, ErrInvalidCertHash) {
		t.Error("empty-pin in list should fail")
	}
}

func TestNewPinnedTLSConfig_VerifyPeerCertificatePath(t *testing.T) {
	// Fabricate a leaf DER, compute its hash, ensure the callback accepts it.
	leafDER := []byte("fake-leaf-der-bytes-for-test")
	digest := sha256.Sum256(leafDER)
	hexPin := hex.EncodeToString(digest[:])

	cfg, err := NewPinnedTLSConfig(hexPin)
	if err != nil {
		t.Fatalf("NewPinnedTLSConfig: %v", err)
	}
	if cfg.VerifyPeerCertificate == nil {
		t.Fatal("VerifyPeerCertificate must be installed")
	}
	if !cfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify must be true so pin verifier is the gate " +
			"(normal chain verification would reject FQDN-cert-over-IP before callback runs)")
	}

	if err := cfg.VerifyPeerCertificate([][]byte{leafDER}, nil); err != nil {
		t.Fatalf("matching hash: want nil, got %v", err)
	}

	wrong := sha256.Sum256([]byte("different-bytes"))
	if err := cfg.VerifyPeerCertificate([][]byte{wrong[:]}, nil); !errors.Is(err, ErrCertHashMismatch) {
		t.Fatalf("wrong hash: want ErrCertHashMismatch, got %v", err)
	}

	if err := cfg.VerifyPeerCertificate(nil, nil); err == nil {
		t.Error("empty peer cert chain should be rejected")
	}
}

func TestNewPinnedTLSConfig_RotationAcceptsEither(t *testing.T) {
	oldDER := []byte("old-leaf")
	newDER := []byte("new-leaf")
	oldSum := sha256.Sum256(oldDER)
	newSum := sha256.Sum256(newDER)
	pins := hex.EncodeToString(oldSum[:]) + "," + hex.EncodeToString(newSum[:])

	cfg, err := NewPinnedTLSConfig(pins)
	if err != nil {
		t.Fatalf("NewPinnedTLSConfig rotation: %v", err)
	}
	if err := cfg.VerifyPeerCertificate([][]byte{oldDER}, nil); err != nil {
		t.Errorf("old pin should match during rotation: %v", err)
	}
	if err := cfg.VerifyPeerCertificate([][]byte{newDER}, nil); err != nil {
		t.Errorf("new pin should match during rotation: %v", err)
	}
	other := sha256.Sum256([]byte("imposter"))
	if err := cfg.VerifyPeerCertificate([][]byte{other[:]}, nil); !errors.Is(err, ErrCertHashMismatch) {
		t.Errorf("unknown leaf: want ErrCertHashMismatch, got %v", err)
	}
}

func makeHex(n int) string {
	const alphabet = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[i%len(alphabet)]
	}
	return string(b)
}
