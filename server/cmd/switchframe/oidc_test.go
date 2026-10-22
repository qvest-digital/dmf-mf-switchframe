package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/oidcauth"
	"github.com/zsiec/switchframe/server/oidcauth/oidctest"
	"github.com/zsiec/switchframe/server/operator"
)

func TestOIDCSettings_DisabledWithoutIssuer(t *testing.T) {
	cfg, err := oidcSettings{}.config()
	require.NoError(t, err)
	require.Nil(t, cfg)

	_, err = oidcSettings{ClientID: "switchframe"}.config()
	require.Error(t, err, "a client ID without an issuer is a misconfiguration, not a disabled feature")
}

func TestOIDCSettings_Config(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(secretFile, []byte("s3cret\n"), 0o600))

	cfg, err := oidcSettings{
		Issuer:           "https://idp.example.com/realms/media",
		ClientID:         "switchframe",
		ClientSecretFile: secretFile,
		RedirectURL:      "https://switchframe.example.com/auth/callback",
		Scopes:           "openid, profile",
		RoleClaim:        "realm_access.roles",
		RoleMap:          "sf-director=director,sf-audio=audio",
		DefaultRole:      "viewer",
	}.config()
	require.NoError(t, err)
	require.Equal(t, "s3cret", cfg.ClientSecret)
	require.Equal(t, []string{"openid", "profile"}, cfg.Scopes)
	require.Equal(t, operator.RoleAudio, cfg.RoleMap["sf-audio"])
	require.Equal(t, operator.RoleViewer, cfg.DefaultRole)
}

func TestOIDCSettings_RejectsBadValues(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(empty, []byte("\n"), 0o600))
	base := oidcSettings{Issuer: "https://idp.example.com", ClientID: "switchframe"}

	for name, mutate := range map[string]func(*oidcSettings){
		"missing client ID":    func(s *oidcSettings) { s.ClientID = "" },
		"missing secret file":  func(s *oidcSettings) { s.ClientSecretFile = "/nonexistent/secret" },
		"empty secret file":    func(s *oidcSettings) { s.ClientSecretFile = empty },
		"relative redirect":    func(s *oidcSettings) { s.RedirectURL = "/auth/callback" },
		"malformed role map":   func(s *oidcSettings) { s.RoleMap = "sf-director" },
		"unknown mapped role":  func(s *oidcSettings) { s.RoleMap = "sf-x=admin" },
		"unknown default role": func(s *oidcSettings) { s.DefaultRole = "admin" },
	} {
		t.Run(name, func(t *testing.T) {
			s := base
			mutate(&s)
			_, err := s.config()
			require.Error(t, err)
		})
	}
}

// An ID token is accepted wherever an operator token is: it passes the
// API's authentication, the user becomes a registered operator, and the
// operator middleware applies their mapped role. The session API token keeps
// authenticating alongside.
func TestOIDCOperator_ThroughAPIMiddleware(t *testing.T) {
	p := oidctest.New(t)
	store, err := operator.NewStore(filepath.Join(t.TempDir(), "operators.json"))
	require.NoError(t, err)
	sm := operator.NewSessionManager()
	t.Cleanup(sm.Close)

	app := &App{
		cfg: AppConfig{OIDC: &oidcauth.Config{
			Issuer:   p.URL,
			ClientID: oidctest.ClientID,
			RoleMap: map[string]operator.Role{
				"sf-director": operator.RoleDirector,
				"sf-viewer":   operator.RoleViewer,
			},
		}},
		operatorStore: store,
	}
	require.NoError(t, app.initOIDC())
	store.SetTokenResolver(app.resolveOIDCOperator)

	// The same composition initInfra and initPrismServer build.
	checker := control.OperatorTokenChecker(func(token string) bool {
		_, err := store.GetByToken(token)
		return err == nil
	})
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := control.AuthMiddleware("session-token", checker)(operator.NewMiddleware(store, sm)(inner))

	do := func(method, path, token string) int {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	director := p.Sign(p.Claims(nil))
	viewer := p.Sign(p.Claims(map[string]any{"sub": "user-2", "preferred_username": "bob", "groups": []string{"sf-viewer"}}))
	expired := p.Sign(p.Claims(map[string]any{"iat": time.Now().Add(-time.Hour).Unix(), "exp": time.Now().Add(-time.Minute).Unix()}))
	unmapped := p.Sign(p.Claims(map[string]any{"sub": "user-3", "preferred_username": "eve", "groups": []string{"staff"}}))

	require.Equal(t, http.StatusOK, do(http.MethodPost, "/api/switch/cut", director))
	require.Equal(t, http.StatusOK, do(http.MethodGet, "/api/switch/state", viewer))
	require.Equal(t, http.StatusForbidden, do(http.MethodPost, "/api/switch/cut", viewer))
	require.Equal(t, http.StatusUnauthorized, do(http.MethodGet, "/api/switch/state", expired))
	require.Equal(t, http.StatusUnauthorized, do(http.MethodGet, "/api/switch/state", unmapped))
	require.Equal(t, http.StatusUnauthorized, do(http.MethodGet, "/api/switch/state", ""))
	require.Equal(t, http.StatusOK, do(http.MethodGet, "/api/switch/state", "session-token"))

	names := map[string]operator.Role{}
	for _, op := range store.List() {
		names[op.Name] = op.Role
		require.Empty(t, op.Token, "an OIDC operator must not get a second, never-expiring credential")
	}
	require.Equal(t, map[string]operator.Role{"alice": operator.RoleDirector, "bob": operator.RoleViewer}, names)
}
