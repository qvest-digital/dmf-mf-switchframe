//go:build cgo && !noffmpeg

package output

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testCert issues a certificate for host, signed by parent, or self-signed
// when parent is nil.
func testCert(t *testing.T, host string, ca bool, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  ca,
	}
	if ca {
		tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	} else {
		tmpl.DNSNames = []string{host}
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, key, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// rtmpsProbe listens for one TLS connection and reports whether the client
// went on to send RTMP after the TLS handshake, which it does only once it
// accepted the certificate.
func rtmpsProbe(t *testing.T, cert tls.Certificate) (string, <-chan bool) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	sent := make(chan bool, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		b := make([]byte, 1)
		_, err = c.Read(b)
		sent <- err == nil && b[0] == 0x03 // RTMP C0: version 3
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port, sent
}

func rtmpsConnect(t *testing.T, port string, allowUntrusted bool, caFile string) {
	t.Helper()
	r, err := NewRTMPDirect(RTMPCallerConfig{
		URL:                "rtmps://localhost:" + port + "/live/key",
		AllowUntrustedCert: allowUntrusted,
	})
	require.NoError(t, err)
	if caFile != "" {
		r.caFile = caFile
	}
	r.bo.Initial = time.Hour
	require.NoError(t, r.Start(context.Background()))
	t.Cleanup(func() { _ = r.Close() })
	require.NoError(t, r.WriteVideo(rtmpTestKeyframe(90000)))
}

func writeCAFile(t *testing.T, ca *x509.Certificate) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0o600))
	return path
}

func requireProbe(t *testing.T, sent <-chan bool, want bool, msg string) {
	t.Helper()
	select {
	case got := <-sent:
		require.Equal(t, want, got, msg)
	case <-time.After(6 * time.Second):
		t.Fatal("no TLS connection reached the probe")
	}
}

// A certificate no CA in the system bundle signed is refused: an rtmps://
// stream key must not go to whoever answers in the ingest's place.
func TestRTMPDirect_RTMPSRefusesUntrustedCert(t *testing.T) {
	_, _, cert := testCert(t, "localhost", false, nil, nil)
	port, sent := rtmpsProbe(t, cert)
	rtmpsConnect(t, port, false, "")
	requireProbe(t, sent, false, "sent RTMP to a server with a self-signed certificate")
}

// The status names the refused certificate and the way around it rather
// than a step code of the open.
func TestRTMPDirect_RTMPSRefusedCertReportsCause(t *testing.T) {
	_, _, cert := testCert(t, "localhost", false, nil, nil)
	port, _ := rtmpsProbe(t, cert)
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmps://localhost:" + port + "/live/key"})
	require.NoError(t, err)
	r.bo.Initial = time.Hour
	require.NoError(t, r.Start(context.Background()))
	t.Cleanup(func() { _ = r.Close() })
	require.NoError(t, r.WriteVideo(rtmpTestKeyframe(90000)))
	require.Eventually(t, func() bool { return r.Status().Error != "" }, 6*time.Second, 10*time.Millisecond)
	msg := r.Status().Error
	require.Contains(t, msg, "Input/output error")
	require.Contains(t, msg, "TLS handshake or certificate verification failed; allowUntrustedCert skips verification")
}

// A refused TCP connection reports FFmpeg's error, with no TLS hint.
func TestRTMPDirect_OpenErrorCarriesFFmpegError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, ln.Close())
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://127.0.0.1:" + port + "/live/key"})
	require.NoError(t, err)
	r.bo.Initial = time.Hour
	require.NoError(t, r.Start(context.Background()))
	t.Cleanup(func() { _ = r.Close() })
	require.NoError(t, r.WriteVideo(rtmpTestKeyframe(90000)))
	require.Eventually(t, func() bool { return r.Status().Error != "" }, 6*time.Second, 10*time.Millisecond)
	msg := r.Status().Error
	require.Contains(t, msg, "Connection refused")
	require.NotContains(t, msg, "TLS")
}

func TestRTMPDirect_RTMPSAllowUntrustedCert(t *testing.T) {
	_, _, cert := testCert(t, "localhost", false, nil, nil)
	port, sent := rtmpsProbe(t, cert)
	rtmpsConnect(t, port, true, "")
	requireProbe(t, sent, true, "refused a certificate the destination allows")
}

func TestRTMPDirect_RTMPSAcceptsTrustedCert(t *testing.T) {
	ca, caKey, _ := testCert(t, "test CA", true, nil, nil)
	_, _, cert := testCert(t, "localhost", false, ca, caKey)
	port, sent := rtmpsProbe(t, cert)
	rtmpsConnect(t, port, false, writeCAFile(t, ca))
	requireProbe(t, sent, true, "refused a certificate a trusted CA signed")
}

func TestRTMPDirect_RTMPSRefusesCertForOtherHost(t *testing.T) {
	ca, caKey, _ := testCert(t, "test CA", true, nil, nil)
	_, _, cert := testCert(t, "ingest.example.com", false, ca, caKey)
	port, sent := rtmpsProbe(t, cert)
	rtmpsConnect(t, port, false, writeCAFile(t, ca))
	requireProbe(t, sent, false, "sent RTMP to a server whose certificate names another host")
}

// The destination's opt-out reaches the adapter; without it the adapter
// verifies.
func TestManager_RTMPSAllowUntrustedCertReachesAdapter(t *testing.T) {
	for _, allow := range []bool{false, true} {
		m := NewManager()
		id, err := m.AddDestination(DestinationConfig{Type: "rtmp", URL: "rtmps://localhost:1/live/key", AllowUntrustedCert: allow})
		require.NoError(t, err)
		require.NoError(t, m.StartDestination(id))
		m.mu.Lock()
		r := m.destinations[id].frameAdapter.(*RTMPDirect)
		m.mu.Unlock()
		require.Equal(t, allow, r.config.AllowUntrustedCert)
		require.NoError(t, m.Close())
	}
}
