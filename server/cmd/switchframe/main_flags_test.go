package main

import (
	"errors"
	"testing"

	"github.com/zsiec/switchframe/server/peer"
)

func TestValidateEngineLabel_AcceptsEmptyAndAB(t *testing.T) {
	for _, v := range []string{"", "a", "b"} {
		if err := validateEngineLabel(v); err != nil {
			t.Errorf("validateEngineLabel(%q) = %v, want nil", v, err)
		}
	}
}

func TestValidateEngineLabel_RejectsInvalid(t *testing.T) {
	for _, v := range []string{"c", "A", "B", "1", "primary", " ", "ab"} {
		if err := validateEngineLabel(v); err == nil {
			t.Errorf("validateEngineLabel(%q) = nil, want error", v)
		}
	}
}

// Startup must refuse an RFC 1918 peer URL with no cert-hash pin — the
// handshake would otherwise fail silently on every poll (LE certs cover the
// FQDN, not the private IP). This guards the same contract as
// peer.ValidatePeerTLSConfig, but at the cmd/switchframe startup boundary
// where run() calls it.
func TestRun_RejectsInternalIPPeerURLWithoutCertHash(t *testing.T) {
	cases := []string{
		"https://10.0.0.5:8080",
		"https://172.16.0.1:8080",
		"https://192.168.1.1:8080",
	}
	for _, u := range cases {
		err := peer.ValidatePeerTLSConfig(u, "")
		if !errors.Is(err, peer.ErrCertHashRequired) {
			t.Errorf("url=%s: want ErrCertHashRequired, got %v", u, err)
		}
	}
}

func TestRun_AcceptsFQDNPeerURLWithoutCertHash(t *testing.T) {
	if err := peer.ValidatePeerTLSConfig("https://engine-b.prod.example.com:8080", ""); err != nil {
		t.Errorf("FQDN URL without pin should be accepted: %v", err)
	}
}
