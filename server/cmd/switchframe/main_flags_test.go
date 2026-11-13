package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestParseAudioChannelPairs(t *testing.T) {
	got, err := parseAudioChannelPairs("mxl:mxl-3=3, srt:cam2=5,mxl:5fbec3b1-2c4c-4d4c-9d6a-1f2e3d4c5b6a=15")
	require.NoError(t, err)
	require.Equal(t, map[string]int{
		"mxl:mxl-3": 3,
		"srt:cam2":  5,
		"mxl:5fbec3b1-2c4c-4d4c-9d6a-1f2e3d4c5b6a": 15,
	}, got)

	got, err = parseAudioChannelPairs("")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestParseAudioChannelPairs_RejectsInvalid(t *testing.T) {
	for _, s := range []string{
		"mxl:cam1",              // no first channel
		"=3",                    // no source key
		"mxl:cam1=",             // empty first channel
		"mxl:cam1=two",          // not a number
		"mxl:cam1=0",            // the default is no entry
		"mxl:cam1=4",            // even
		"mxl:cam1=17",           // beyond 16 channels
		"mxl:cam1=3,mxl:cam1=5", // named twice
	} {
		_, err := parseAudioChannelPairs(s)
		require.Error(t, err, s)
	}
}
