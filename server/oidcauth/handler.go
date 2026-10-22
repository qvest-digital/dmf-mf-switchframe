package oidcauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/zsiec/switchframe/server/control/httperr"
)

const (
	callbackPath = "/auth/callback"
	// flowCookie carries state, nonce and PKCE verifier from /auth/login
	// to /auth/callback, binding the callback to the browser that started
	// the sign-in.
	flowCookie = "switchframe_oidc"
	flowTTL    = 10 * time.Minute
)

// Handler serves the sign-in endpoints under /auth/:
//
//	GET  /auth/config    {"enabled":bool,"logoutUrl":string}, unauthenticated
//	GET  /auth/login     redirects to the provider
//	GET  /auth/callback  redirects to /#id_token=...&refresh_token=...
//	POST /auth/refresh   {"refresh_token"} -> {"id_token","refresh_token","expires_at"}
//
// With a nil Authenticator only /auth/config is served, reporting sign-in
// as disabled.
func Handler(a *Authenticator) http.Handler {
	mux := http.NewServeMux()
	if a == nil {
		mux.HandleFunc("GET /auth/config", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"enabled": false})
		})
		return mux
	}
	mux.HandleFunc("GET /auth/config", a.handleConfig)
	mux.HandleFunc("GET /auth/login", a.handleLogin)
	mux.HandleFunc("GET "+callbackPath, a.handleCallback)
	mux.HandleFunc("POST /auth/refresh", a.handleRefresh)
	return mux
}

func (a *Authenticator) handleConfig(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"enabled": true}
	if d, err := a.discover(r.Context()); err == nil && d.endSession != "" {
		if u, err := url.Parse(d.endSession); err == nil {
			q := u.Query()
			q.Set("client_id", a.cfg.ClientID)
			q.Set("post_logout_redirect_uri", a.origin(r)+"/")
			u.RawQuery = q.Encode()
			resp["logoutUrl"] = u.String()
		}
	}
	writeJSON(w, resp)
}

func (a *Authenticator) handleLogin(w http.ResponseWriter, r *http.Request) {
	d, err := a.discover(r.Context())
	if err != nil {
		slog.Warn("oidc: sign-in unavailable", "error", err)
		http.Error(w, "Sign-in is unavailable: the identity provider cannot be reached.", http.StatusServiceUnavailable)
		return
	}
	state, nonce := randomString(), randomString()
	verifier := oauth2.GenerateVerifier()

	http.SetCookie(w, &http.Cookie{
		Name:     flowCookie,
		Value:    state + "." + nonce + "." + verifier,
		Path:     callbackPath,
		MaxAge:   int(flowTTL.Seconds()),
		HttpOnly: true,
		Secure:   strings.HasPrefix(a.origin(r), "https://"),
		// Lax, not Strict: the callback is a top-level navigation coming
		// from the provider's site.
		SameSite: http.SameSiteLaxMode,
	})
	authURL := a.oauthConfig(r, d).AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (a *Authenticator) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		// Both values arrive in the URL, so anyone can craft them; they are
		// logged, never shown on this origin.
		slog.Info("oidc: provider returned an error", "error", e, "description", q.Get("error_description"))
		http.Error(w, "Sign-in failed at the identity provider.", http.StatusUnauthorized)
		return
	}
	c, err := r.Cookie(flowCookie)
	parts := []string{}
	if err == nil {
		parts = strings.SplitN(c.Value, ".", 3)
	}
	if len(parts) != 3 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(parts[0])) != 1 {
		http.Error(w, "Sign-in expired or was started in another browser; start it again.", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: flowCookie, Path: callbackPath, MaxAge: -1})

	d, err := a.discover(r.Context())
	if err != nil {
		slog.Warn("oidc: sign-in unavailable", "error", err)
		http.Error(w, "Sign-in is unavailable: the identity provider cannot be reached.", http.StatusServiceUnavailable)
		return
	}
	tok, err := a.oauthConfig(r, d).Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(parts[2]))
	if err != nil {
		slog.Warn("oidc: code exchange failed", "error", err)
		http.Error(w, "The identity provider refused the sign-in code.", http.StatusBadGateway)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		http.Error(w, "The identity provider returned no ID token.", http.StatusBadGateway)
		return
	}
	_, nonce, err := a.verify(r.Context(), raw)
	if err != nil && !errors.Is(err, ErrNoRole) {
		slog.Warn("oidc: ID token failed verification", "error", err)
		http.Error(w, "The ID token failed verification.", http.StatusUnauthorized)
		return
	}
	if subtle.ConstantTimeCompare([]byte(nonce), []byte(parts[1])) != 1 {
		http.Error(w, "The ID token does not belong to this sign-in.", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "Signed in, but not allowed to operate: "+err.Error(), http.StatusForbidden)
		return
	}

	// The fragment never reaches a server; the UI removes it from the
	// address bar and history on load.
	frag := url.Values{"id_token": {raw}}
	if tok.RefreshToken != "" {
		frag.Set("refresh_token", tok.RefreshToken)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", "/#"+frag.Encode())
	w.WriteHeader(http.StatusSeeOther)
}

func (a *Authenticator) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.RefreshToken == "" {
		httperr.Write(w, http.StatusBadRequest, "refresh_token required")
		return
	}
	d, err := a.discover(r.Context())
	if err != nil {
		slog.Warn("oidc: refresh unavailable", "error", err)
		httperr.Write(w, http.StatusServiceUnavailable, "the identity provider cannot be reached")
		return
	}
	tok, err := a.oauthConfig(r, d).TokenSource(r.Context(), &oauth2.Token{RefreshToken: req.RefreshToken}).Token()
	if err != nil {
		httperr.Write(w, http.StatusUnauthorized, "the identity provider refused the refresh token")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		httperr.Write(w, http.StatusBadGateway, "the identity provider returned no ID token")
		return
	}
	id, err := a.Authenticate(r.Context(), raw)
	if errors.Is(err, ErrNoRole) {
		httperr.WriteErr(w, http.StatusForbidden, err)
		return
	}
	if err != nil {
		slog.Warn("oidc: refreshed ID token failed verification", "error", err)
		httperr.Write(w, http.StatusUnauthorized, "the refreshed ID token failed verification")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{
		"id_token":      raw,
		"refresh_token": tok.RefreshToken,
		"expires_at":    id.Expiry.Unix(),
	})
}

func (a *Authenticator) oauthConfig(r *http.Request, d *discovered) *oauth2.Config {
	redirect := a.cfg.RedirectURL
	if redirect == "" {
		redirect = requestOrigin(r) + callbackPath
	}
	return &oauth2.Config{
		ClientID:     a.cfg.ClientID,
		ClientSecret: a.cfg.ClientSecret,
		Endpoint:     d.endpoint,
		RedirectURL:  redirect,
		Scopes:       a.cfg.Scopes,
	}
}

// origin is the origin the browser reaches this engine on: that of the
// configured redirect URL, else the request's.
func (a *Authenticator) origin(r *http.Request) string {
	if a.cfg.RedirectURL != "" {
		if u, err := url.Parse(a.cfg.RedirectURL); err == nil && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return requestOrigin(r)
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if p := r.Header.Get("X-Forwarded-Proto"); p == "https" || p == "http" {
		scheme = p
	}
	return scheme + "://" + r.Host
}

func randomString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
