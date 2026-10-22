// Package oidctest runs a minimal OpenID Connect provider for tests:
// discovery, JWKS, an authorize endpoint that signs the configured user in
// without a prompt, and a token endpoint for the authorization code (with
// PKCE) and refresh grants. It accepts only the refresh token "refresh-1".
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// Client credentials the provider accepts.
const (
	ClientID     = "switchframe"
	ClientSecret = "test-secret"
)

// Provider is a running test provider. Its URL is the issuer.
type Provider struct {
	*httptest.Server
	t   *testing.T
	key *rsa.PrivateKey

	mu sync.Mutex
	// user is the claims set put into every ID token, besides iss, aud,
	// iat, exp and nonce.
	user  map[string]any
	codes map[string]authRequest
}

type authRequest struct {
	nonce, challenge, redirectURI string
}

// New starts a provider whose user is alice (subject user-1) in group
// sf-director. It is closed when the test ends.
func New(t *testing.T) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{
		t:     t,
		key:   key,
		user:  map[string]any{"sub": "user-1", "preferred_username": "alice", "groups": []string{"sf-director"}},
		codes: map[string]authRequest{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("GET /keys", p.handleKeys)
	mux.HandleFunc("GET /authorize", p.handleAuthorize)
	mux.HandleFunc("POST /token", p.handleToken)
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

// SetUser replaces the claims of the signed-in user.
func (p *Provider) SetUser(claims map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.user = claims
}

func (p *Provider) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                p.URL,
		"authorization_endpoint":                p.URL + "/authorize",
		"token_endpoint":                        p.URL + "/token",
		"jwks_uri":                              p.URL + "/keys",
		"end_session_endpoint":                  p.URL + "/logout",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *Provider) handleKeys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: &p.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
	}})
}

func (p *Provider) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != ClientID || q.Get("code_challenge_method") != "S256" {
		http.Error(w, "bad authorize request", http.StatusBadRequest)
		return
	}
	code := rand.Text()
	p.mu.Lock()
	p.codes[code] = authRequest{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri")}
	p.mu.Unlock()
	back, _ := url.Parse(q.Get("redirect_uri"))
	bq := back.Query()
	bq.Set("code", code)
	bq.Set("state", q.Get("state"))
	back.RawQuery = bq.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (p *Provider) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != ClientID || secret != ClientSecret {
		tokenError(w, "invalid_client")
		return
	}

	nonce := ""
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		p.mu.Lock()
		req, found := p.codes[r.PostForm.Get("code")]
		delete(p.codes, r.PostForm.Get("code"))
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !found || base64.RawURLEncoding.EncodeToString(sum[:]) != req.challenge ||
			r.PostForm.Get("redirect_uri") != req.redirectURI {
			tokenError(w, "invalid_grant")
			return
		}
		nonce = req.nonce
	case "refresh_token":
		if r.PostForm.Get("refresh_token") != "refresh-1" {
			tokenError(w, "invalid_grant")
			return
		}
	default:
		tokenError(w, "unsupported_grant_type")
		return
	}

	claims := p.Claims(nil)
	if nonce != "" {
		claims["nonce"] = nonce
	}
	writeJSON(w, map[string]any{
		"access_token":  "access",
		"token_type":    "Bearer",
		"expires_in":    300,
		"refresh_token": "refresh-1",
		"id_token":      p.Sign(claims),
	})
}

func tokenError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// Claims returns the user's claims with valid iss, aud, iat and exp, then
// applies override; a nil override value deletes the claim.
func (p *Provider) Claims(override map[string]any) map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	c := map[string]any{
		"iss": p.URL,
		"aud": ClientID,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	}
	for k, v := range p.user {
		c[k] = v
	}
	for k, v := range override {
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

// Sign signs claims with the provider's key.
func (p *Provider) Sign(claims map[string]any) string {
	return SignWith(p.t, p.key, claims)
}

// SignWith signs claims with key, under the provider's key ID.
func SignWith(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(claims)
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
