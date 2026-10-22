package oidcauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/oidcauth/oidctest"
)

// noRedirect returns a client that stops at the first redirect, so each
// hop of the sign-in can be inspected.
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// signIn drives /auth/login through the test provider to the engine's
// callback and returns the callback response.
func signIn(t *testing.T, engine *httptest.Server, tamper func(callback *url.URL, cookie *http.Cookie)) *http.Response {
	t.Helper()
	c := noRedirect()

	login, err := c.Get(engine.URL + "/auth/login")
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, login.StatusCode)
	var flow *http.Cookie
	for _, ck := range login.Cookies() {
		if ck.Name == flowCookie {
			flow = ck
		}
	}
	require.NotNil(t, flow)
	require.True(t, flow.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, flow.SameSite)

	authorize, err := c.Get(login.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, authorize.StatusCode, "provider rejected the authorize request")
	callback, err := url.Parse(authorize.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, engine.URL+callbackPath, callback.Scheme+"://"+callback.Host+callback.Path)

	if tamper != nil {
		tamper(callback, flow)
	}
	req, _ := http.NewRequest(http.MethodGet, callback.String(), nil)
	if flow != nil {
		req.AddCookie(&http.Cookie{Name: flow.Name, Value: flow.Value})
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func newEngine(t *testing.T, a *Authenticator) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(Handler(a))
	t.Cleanup(s.Close)
	return s
}

func TestSignIn_HandsIDTokenToTheUI(t *testing.T) {
	p := oidctest.New(t)
	a := newTestAuthenticator(t, p, nil)
	engine := newEngine(t, a)

	resp := signIn(t, engine, nil)
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	loc := resp.Header.Get("Location")
	require.True(t, strings.HasPrefix(loc, "/#"), loc)
	frag, err := url.ParseQuery(strings.TrimPrefix(loc, "/#"))
	require.NoError(t, err)
	require.Equal(t, "refresh-1", frag.Get("refresh_token"))

	// The handed-over token is the bearer token every engine accepts.
	id, err := a.Authenticate(context.Background(), frag.Get("id_token"))
	require.NoError(t, err)
	require.Equal(t, "alice", id.Name)
	peer := newTestAuthenticator(t, p, nil)
	_, err = peer.Authenticate(context.Background(), frag.Get("id_token"))
	require.NoError(t, err)
}

func TestSignIn_RejectsForgedOrStaleCallbacks(t *testing.T) {
	p := oidctest.New(t)
	engine := newEngine(t, newTestAuthenticator(t, p, nil))

	cases := map[string]func(*url.URL, *http.Cookie){
		"state mismatch": func(u *url.URL, _ *http.Cookie) {
			q := u.Query()
			q.Set("state", "forged")
			u.RawQuery = q.Encode()
		},
		"no flow cookie": func(_ *url.URL, c *http.Cookie) { c.Name = "other" },
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			resp := signIn(t, engine, tamper)
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Empty(t, resp.Header.Get("Location"))
		})
	}

	t.Run("wrong PKCE verifier", func(t *testing.T) {
		resp := signIn(t, engine, func(_ *url.URL, c *http.Cookie) {
			parts := strings.SplitN(c.Value, ".", 3)
			c.Value = parts[0] + "." + parts[1] + ".wrong-verifier"
		})
		require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	})

	t.Run("nonce mismatch", func(t *testing.T) {
		resp := signIn(t, engine, func(_ *url.URL, c *http.Cookie) {
			parts := strings.SplitN(c.Value, ".", 3)
			c.Value = parts[0] + ".another-nonce." + parts[2]
		})
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

func TestSignIn_UnmappedUserGetsForbidden(t *testing.T) {
	p := oidctest.New(t)
	p.SetUser(map[string]any{"sub": "user-3", "preferred_username": "mallory", "groups": []string{"staff"}})
	engine := newEngine(t, newTestAuthenticator(t, p, nil))

	resp := signIn(t, engine, nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Empty(t, resp.Header.Get("Location"))
	body, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(body), "mallory")
}

func postRefresh(t *testing.T, engine *httptest.Server, refreshToken string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(engine.URL+"/auth/refresh", "application/json",
		strings.NewReader(`{"refresh_token":"`+refreshToken+`"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func TestRefresh(t *testing.T) {
	p := oidctest.New(t)
	a := newTestAuthenticator(t, p, nil)
	engine := newEngine(t, a)

	status, body := postRefresh(t, engine, "refresh-1")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "refresh-1", body["refresh_token"])
	require.NotZero(t, body["expires_at"])
	_, err := a.Authenticate(context.Background(), body["id_token"].(string))
	require.NoError(t, err)

	status, _ = postRefresh(t, engine, "revoked")
	require.Equal(t, http.StatusUnauthorized, status)

	// A role taken away at the provider ends the session at the next refresh.
	p.SetUser(map[string]any{"sub": "user-1", "preferred_username": "alice", "groups": []string{}})
	status, body = postRefresh(t, engine, "refresh-1")
	require.Equal(t, http.StatusForbidden, status)
	require.Contains(t, body["error"], "alice")
}

func TestConfigEndpoint(t *testing.T) {
	var cfg map[string]any
	get := func(s *httptest.Server) {
		resp, err := http.Get(s.URL + "/auth/config")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		cfg = nil
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&cfg))
	}

	get(newEngine(t, nil))
	require.Equal(t, map[string]any{"enabled": false}, cfg)

	p := oidctest.New(t)
	get(newEngine(t, newTestAuthenticator(t, p, func(c *Config) {
		c.RedirectURL = "https://engine-a.example.com/auth/callback"
	})))
	require.Equal(t, true, cfg["enabled"])
	logout, err := url.Parse(cfg["logoutUrl"].(string))
	require.NoError(t, err)
	require.Equal(t, p.URL+"/logout", logout.Scheme+"://"+logout.Host+logout.Path)
	require.Equal(t, oidctest.ClientID, logout.Query().Get("client_id"))
	require.Equal(t, "https://engine-a.example.com/", logout.Query().Get("post_logout_redirect_uri"))
}

func TestHandler_DisabledServesOnlyConfig(t *testing.T) {
	engine := newEngine(t, nil)
	resp, err := noRedirect().Get(engine.URL + "/auth/login")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
