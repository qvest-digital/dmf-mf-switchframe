package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zsiec/prism/certs"
)

func TestPeerBrowserURL(t *testing.T) {
	for _, tc := range []struct{ engine, public, want string }{
		{"", "", ""},
		{"https://b.example.com", "", "https://b.example.com"},
		// Engines that poll each other in-cluster still hand the browser an
		// origin it can reach.
		{"http://switcher-b:8081", "https://switcher-b.example.com", "https://switcher-b.example.com"},
	} {
		got := peerBrowserURL(AppConfig{PeerEngineURL: tc.engine, PeerPublicURL: tc.public})
		if got != tc.want {
			t.Errorf("peerBrowserURL(%q, %q) = %q, want %q", tc.engine, tc.public, got, tc.want)
		}
	}
}

// The UI loaded from one engine reads the other engine's cert-hash cross
// origin; without CORS headers the browser withholds the response.
func TestCertHashHandlerAllowsCrossOrigin(t *testing.T) {
	cert, err := certs.Generate(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{cert: cert, cfg: AppConfig{Addr: ":8080"}}
	req := httptest.NewRequest(http.MethodGet, "/api/cert-hash", nil)
	req.Header.Set("Origin", "https://engine-a.example.com")
	rec := httptest.NewRecorder()
	a.certHashHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
}
