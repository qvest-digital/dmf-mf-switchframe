package oidcauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/oidcauth/oidctest"

	"github.com/zsiec/switchframe/server/operator"
)

func newTestAuthenticator(t *testing.T, p *oidctest.Provider, mutate func(*Config)) *Authenticator {
	t.Helper()
	cfg := Config{
		Issuer:       p.URL,
		ClientID:     oidctest.ClientID,
		ClientSecret: oidctest.ClientSecret,
		RoleMap: map[string]operator.Role{
			"sf-director":  operator.RoleDirector,
			"sf-audio":     operator.RoleAudio,
			"sf-graphics":  operator.RoleGraphics,
			"sf-captioner": operator.RoleCaptioner,
			"sf-viewer":    operator.RoleViewer,
		},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	a, err := New(cfg)
	require.NoError(t, err)
	return a
}

func TestAuthenticate_ValidToken(t *testing.T) {
	p := oidctest.New(t)
	a := newTestAuthenticator(t, p, nil)

	id, err := a.Authenticate(context.Background(), p.Sign(p.Claims(nil)))
	require.NoError(t, err)
	require.Equal(t, "alice", id.Name)
	require.Equal(t, operator.RoleDirector, id.Role)
	require.WithinDuration(t, time.Now().Add(5*time.Minute), id.Expiry, 5*time.Second)

	// The operator ID is what lets two engines agree on who holds a lock:
	// it depends on issuer and subject only, and fits the store's ID rules.
	again, err := newTestAuthenticator(t, p, nil).Authenticate(context.Background(),
		p.Sign(p.Claims(map[string]any{"preferred_username": "alice2"})))
	require.NoError(t, err)
	require.Equal(t, id.OperatorID, again.OperatorID)
	require.Regexp(t, `^[a-z0-9-]{8,64}$`, id.OperatorID)
	require.True(t, strings.HasPrefix(id.OperatorID, operator.ExternalIDPrefix), id.OperatorID)

	other, err := a.Authenticate(context.Background(), p.Sign(p.Claims(map[string]any{"sub": "user-2"})))
	require.NoError(t, err)
	require.NotEqual(t, id.OperatorID, other.OperatorID)
}

func TestAuthenticate_RejectsInvalidTokens(t *testing.T) {
	p := oidctest.New(t)
	a := newTestAuthenticator(t, p, nil)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	cases := map[string]string{
		"wrong audience": p.Sign(p.Claims(map[string]any{"aud": "another-client"})),
		"wrong issuer":   p.Sign(p.Claims(map[string]any{"iss": "https://elsewhere.example"})),
		"expired": p.Sign(p.Claims(map[string]any{
			"iat": time.Now().Add(-time.Hour).Unix(),
			"exp": time.Now().Add(-time.Minute).Unix(),
		})),
		"foreign signature": oidctest.SignWith(t, otherKey, p.Claims(nil)),
		"not a JWT":         "0123456789abcdef",
		// Unsigned, and signed with a shared secret such as the client
		// secret: the classic ways to forge a token for a verifier that
		// trusts the token's own alg header.
		"alg none": unsignedToken(t, p.Claims(nil)),
		"HS256":    hmacToken(t, []byte(strings.Repeat(oidctest.ClientSecret, 3)), p.Claims(nil)),
		// Every subject-less user would share one operator ID.
		"no subject": p.Sign(p.Claims(map[string]any{"sub": nil})),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := a.Authenticate(context.Background(), raw)
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrNoRole)
		})
	}
}

func unsignedToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT", "kid": "k1"})
	payload, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding.EncodeToString
	return enc(header) + "." + enc(payload) + "."
}

func hmacToken(t *testing.T, key []byte, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	require.NoError(t, err)
	payload, _ := json.Marshal(claims)
	jws, err := signer.Sign(payload)
	require.NoError(t, err)
	raw, err := jws.CompactSerialize()
	require.NoError(t, err)
	return raw
}

// A bearer token is whatever a client sends. If each one with an unknown
// signature made the engine fetch the provider's keys again, unauthenticated
// requests would turn into provider traffic one for one.
func TestAuthenticate_ForgedTokensDoNotRefetchKeys(t *testing.T) {
	p := oidctest.New(t)
	a := newTestAuthenticator(t, p, nil)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	_, err = a.Authenticate(context.Background(), p.Sign(p.Claims(nil)))
	require.NoError(t, err)
	require.Equal(t, 1, p.KeyFetches())

	for i := range 20 {
		forged := oidctest.SignWith(t, otherKey, p.Claims(map[string]any{"jti": i}))
		_, err := a.Authenticate(context.Background(), forged)
		require.Error(t, err)
	}
	require.Equal(t, 1, p.KeyFetches())
}

func TestAuthenticate_PicksUpRotatedKey(t *testing.T) {
	prev := keyRefetchInterval
	keyRefetchInterval = 0
	t.Cleanup(func() { keyRefetchInterval = prev })

	p := oidctest.New(t)
	a := newTestAuthenticator(t, p, nil)
	_, err := a.Authenticate(context.Background(), p.Sign(p.Claims(nil)))
	require.NoError(t, err)

	p.RotateKey()
	_, err = a.Authenticate(context.Background(), p.Sign(p.Claims(nil)))
	require.NoError(t, err)
	require.Equal(t, 2, p.KeyFetches())
}

// One request asks for its operator several times; only the first may pay
// for verification.
func TestAuthenticate_CachesVerifiedTokens(t *testing.T) {
	p := oidctest.New(t)
	a := newTestAuthenticator(t, p, nil)
	token := p.Sign(p.Claims(nil))
	_, err := a.Authenticate(context.Background(), token)
	require.NoError(t, err)

	// From here on, verification fails for every token.
	a.disc.verifier = oidc.NewVerifier(p.URL, &keySet{url: p.URL + "/no-keys"},
		&oidc.Config{ClientID: oidctest.ClientID})

	id, err := a.Authenticate(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, "alice", id.Name)
	_, err = a.Authenticate(context.Background(), p.Sign(p.Claims(map[string]any{"jti": "other"})))
	require.Error(t, err)
}

// The cache must not outlive the token.
func TestAuthenticate_CacheEndsAtTokenExpiry(t *testing.T) {
	p := oidctest.New(t)
	a := newTestAuthenticator(t, p, nil)
	token := p.Sign(p.Claims(map[string]any{"exp": time.Now().Add(time.Second).Unix()}))
	_, err := a.Authenticate(context.Background(), token)
	require.NoError(t, err)

	time.Sleep(2 * time.Second)
	_, err = a.Authenticate(context.Background(), token)
	require.Error(t, err)
}

// Discovery runs under a lock every sign-in and verification waits on, so a
// provider that accepts the connection and never answers must not hold it
// for longer than providerTimeout, whatever the caller's context.
func TestDiscover_StalledProviderTimesOut(t *testing.T) {
	prev := providerTimeout
	providerTimeout = 200 * time.Millisecond
	t.Cleanup(func() { providerTimeout = prev })

	stall := make(chan struct{})
	idp := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-stall }))
	t.Cleanup(idp.Close)
	t.Cleanup(func() { close(stall) })

	a, err := New(Config{Issuer: idp.URL, ClientID: oidctest.ClientID})
	require.NoError(t, err)
	start := time.Now()
	err = a.Discover(context.Background())
	require.ErrorIs(t, err, ErrUnavailable)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestAuthenticate_RoleMapping(t *testing.T) {
	p := oidctest.New(t)

	cases := []struct {
		name   string
		mutate func(*Config)
		claims map[string]any
		want   operator.Role
	}{
		{
			name:   "several roles take the most privileged",
			claims: map[string]any{"groups": []string{"sf-viewer", "sf-audio", "unrelated"}},
			want:   operator.RoleAudio,
		},
		{
			name:   "director wins over every other role",
			claims: map[string]any{"groups": []string{"sf-captioner", "sf-director", "sf-graphics"}},
			want:   operator.RoleDirector,
		},
		{
			name:   "a single string claim",
			claims: map[string]any{"groups": "sf-graphics"},
			want:   operator.RoleGraphics,
		},
		{
			name:   "nested claim path",
			mutate: func(c *Config) { c.RoleClaim = "realm_access.roles" },
			claims: map[string]any{"groups": nil, "realm_access": map[string]any{"roles": []string{"sf-captioner"}}},
			want:   operator.RoleCaptioner,
		},
		{
			name:   "claim name containing dots",
			mutate: func(c *Config) { c.RoleClaim = "https://example.com/roles" },
			claims: map[string]any{"groups": nil, "https://example.com/roles": []string{"sf-viewer"}},
			want:   operator.RoleViewer,
		},
		{
			name:   "without a role map, role names map to themselves",
			mutate: func(c *Config) { c.RoleMap = nil },
			claims: map[string]any{"groups": []string{"sf-director", "graphics"}},
			want:   operator.RoleGraphics,
		},
		{
			name:   "default role for an unmapped user",
			mutate: func(c *Config) { c.DefaultRole = operator.RoleViewer },
			claims: map[string]any{"groups": []string{"unrelated"}},
			want:   operator.RoleViewer,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAuthenticator(t, p, tc.mutate)
			id, err := a.Authenticate(context.Background(), p.Sign(p.Claims(tc.claims)))
			require.NoError(t, err)
			require.Equal(t, tc.want, id.Role)
		})
	}
}

func TestAuthenticate_UnmappedUserIsRejected(t *testing.T) {
	p := oidctest.New(t)
	a := newTestAuthenticator(t, p, nil)

	for name, groups := range map[string]any{
		"no mapped value": []string{"unrelated"},
		"claim missing":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			id, err := a.Authenticate(context.Background(), p.Sign(p.Claims(map[string]any{"groups": groups})))
			require.ErrorIs(t, err, ErrNoRole)
			require.Contains(t, err.Error(), "alice")
			require.Contains(t, err.Error(), `"groups"`)
			require.Empty(t, id.Role)
		})
	}
}

func TestAuthenticate_ProviderUnavailable(t *testing.T) {
	a, err := New(Config{Issuer: "http://127.0.0.1:1", ClientID: oidctest.ClientID})
	require.NoError(t, err)
	_, err = a.Authenticate(context.Background(), "x.y.z")
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestNew_ValidatesConfig(t *testing.T) {
	_, err := New(Config{ClientID: "c"})
	require.Error(t, err)
	_, err = New(Config{Issuer: "https://idp.example"})
	require.Error(t, err)
	_, err = New(Config{Issuer: "https://idp.example", ClientID: "c", RoleMap: map[string]operator.Role{"x": "admin"}})
	require.Error(t, err)
	_, err = New(Config{Issuer: "https://idp.example", ClientID: "c", DefaultRole: "admin"})
	require.Error(t, err)

	a, err := New(Config{Issuer: "https://idp.example", ClientID: "c", Scopes: []string{"email"}})
	require.NoError(t, err)
	require.Equal(t, []string{"openid", "email"}, a.cfg.Scopes)
	require.Equal(t, DefaultRoleClaim, a.cfg.RoleClaim)
}

func TestParseRoleMap(t *testing.T) {
	m, err := ParseRoleMap(" sf-director=director, sf-ops=director,sf-audio=audio,")
	require.NoError(t, err)
	require.Equal(t, map[string]operator.Role{
		"sf-director": operator.RoleDirector,
		"sf-ops":      operator.RoleDirector,
		"sf-audio":    operator.RoleAudio,
	}, m)

	for _, bad := range []string{"sf-director", "=director", "sf-x=admin"} {
		_, err := ParseRoleMap(bad)
		require.Error(t, err, bad)
	}
}
