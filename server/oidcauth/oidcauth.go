// Package oidcauth signs operators in with an OpenID Connect provider.
//
// The engine runs the authorization code flow with PKCE and hands the
// browser the provider's ID token. The browser sends that token as its
// bearer token to every engine; each engine verifies it against the
// provider's published keys on its own, so no engine holds session state
// and a token from one engine's sign-in is accepted by its peer.
package oidcauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/zsiec/switchframe/server/operator"
)

// DefaultScopes are requested when Config.Scopes is empty.
var DefaultScopes = []string{oidc.ScopeOpenID, "profile", "email"}

// DefaultRoleClaim is read when Config.RoleClaim is empty.
const DefaultRoleClaim = "groups"

// discoveryRetry is how long a failed provider discovery is remembered
// before the next request tries again.
const discoveryRetry = 10 * time.Second

// rolePrecedence orders roles from most to least privileged; a user whose
// claim maps to several roles gets the first.
var rolePrecedence = []operator.Role{
	operator.RoleDirector,
	operator.RoleAudio,
	operator.RoleGraphics,
	operator.RoleCaptioner,
	operator.RoleViewer,
}

var (
	// ErrNoRole is returned for a valid token none of whose role claim
	// values maps to a switchframe role.
	ErrNoRole = errors.New("oidc: no switchframe role for this user")
	// ErrUnavailable is returned while the provider's discovery document
	// cannot be fetched.
	ErrUnavailable = errors.New("oidc: provider unavailable")
)

// Config configures sign-in with one OpenID Connect provider.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string // empty for a public client
	// RedirectURL is the callback registered with the provider. Empty
	// derives it from each request: <scheme>://<host>/auth/callback.
	RedirectURL string
	Scopes      []string
	// RoleClaim names the ID token claim holding the user's roles or
	// groups, as a string or a list of strings. A name that is not a
	// top-level claim is read as a dot-separated path, e.g.
	// realm_access.roles.
	RoleClaim string
	// RoleMap maps claim values to switchframe roles. Empty maps a value
	// that is itself a role name (director, audio, ...) to that role.
	RoleMap map[string]operator.Role
	// DefaultRole is given to a user none of whose claim values maps.
	// Empty rejects such a user.
	DefaultRole operator.Role
}

// Identity is a verified user as switchframe sees it.
type Identity struct {
	// OperatorID is derived from issuer and subject, so every engine
	// configured with the same issuer derives the same ID.
	OperatorID string
	Name       string
	Role       operator.Role
	Expiry     time.Time
}

// Authenticator verifies ID tokens and serves the sign-in endpoints.
type Authenticator struct {
	cfg Config

	mu      sync.Mutex
	disc    *discovered
	lastErr error
	retryAt time.Time
}

type discovered struct {
	verifier   *oidc.IDTokenVerifier
	endpoint   oauth2.Endpoint
	endSession string
}

// New validates cfg and returns an Authenticator. It does not contact the
// provider; discovery happens on first use and is retried while it fails,
// so an engine starts while its provider is unreachable.
func New(cfg Config) (*Authenticator, error) {
	if cfg.Issuer == "" {
		return nil, errors.New("oidc: issuer is required")
	}
	if cfg.ClientID == "" {
		return nil, errors.New("oidc: client ID is required")
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = DefaultScopes
	}
	if !containsString(cfg.Scopes, oidc.ScopeOpenID) {
		cfg.Scopes = append([]string{oidc.ScopeOpenID}, cfg.Scopes...)
	}
	if cfg.RoleClaim == "" {
		cfg.RoleClaim = DefaultRoleClaim
	}
	for value, role := range cfg.RoleMap {
		if !operator.IsValidRole(role) {
			return nil, fmt.Errorf("oidc: role map entry %q: unknown role %q", value, role)
		}
	}
	if cfg.DefaultRole != "" && !operator.IsValidRole(cfg.DefaultRole) {
		return nil, fmt.Errorf("oidc: unknown default role %q", cfg.DefaultRole)
	}
	return &Authenticator{cfg: cfg}, nil
}

// Discover fetches the provider's discovery document, unless an earlier
// call succeeded.
func (a *Authenticator) Discover(ctx context.Context) error {
	_, err := a.discover(ctx)
	return err
}

func (a *Authenticator) discover(ctx context.Context) (*discovered, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.disc != nil {
		return a.disc, nil
	}
	if time.Now().Before(a.retryAt) {
		return nil, a.lastErr
	}

	p, err := oidc.NewProvider(ctx, a.cfg.Issuer)
	if err != nil {
		a.lastErr = fmt.Errorf("%w: %v", ErrUnavailable, err)
		a.retryAt = time.Now().Add(discoveryRetry)
		return nil, a.lastErr
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&meta)

	// The key set outlives this request, so it must not inherit its
	// cancellation.
	keyCtx := context.WithoutCancel(ctx)
	a.disc = &discovered{
		verifier:   p.VerifierContext(keyCtx, &oidc.Config{ClientID: a.cfg.ClientID}),
		endpoint:   p.Endpoint(),
		endSession: meta.EndSession,
	}
	return a.disc, nil
}

// Authenticate verifies a raw ID token - signature, issuer, audience and
// expiry - and maps it to an Identity.
func (a *Authenticator) Authenticate(ctx context.Context, rawIDToken string) (Identity, error) {
	id, _, err := a.verify(ctx, rawIDToken)
	return id, err
}

// verify is Authenticate that also returns the token's nonce, which only
// the callback checks.
func (a *Authenticator) verify(ctx context.Context, rawIDToken string) (Identity, string, error) {
	d, err := a.discover(ctx)
	if err != nil {
		return Identity{}, "", err
	}
	tok, err := d.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Identity{}, "", err
	}
	var claims map[string]any
	if err := tok.Claims(&claims); err != nil {
		return Identity{}, "", fmt.Errorf("oidc: decode claims: %w", err)
	}

	id := Identity{
		OperatorID: operatorID(tok.Issuer, tok.Subject),
		Name:       displayName(claims, tok.Subject),
		Expiry:     tok.Expiry,
	}
	role, ok := a.mapRole(claimValues(claims, a.cfg.RoleClaim))
	if !ok {
		return id, tok.Nonce, fmt.Errorf("%w: %s holds no value mapped to a role in claim %q",
			ErrNoRole, id.Name, a.cfg.RoleClaim)
	}
	id.Role = role
	return id, tok.Nonce, nil
}

// mapRole returns the most privileged role any of values maps to, or the
// default role.
func (a *Authenticator) mapRole(values []string) (operator.Role, bool) {
	granted := make(map[operator.Role]bool, len(values))
	for _, v := range values {
		if len(a.cfg.RoleMap) > 0 {
			if r, ok := a.cfg.RoleMap[v]; ok {
				granted[r] = true
			}
		} else if r := operator.Role(v); operator.IsValidRole(r) {
			granted[r] = true
		}
	}
	for _, r := range rolePrecedence {
		if granted[r] {
			return r, true
		}
	}
	if a.cfg.DefaultRole != "" {
		return a.cfg.DefaultRole, true
	}
	return "", false
}

// ParseRoleMap parses "value=role,value=role" into a role map. Claim
// values may repeat a role.
func ParseRoleMap(s string) (map[string]operator.Role, error) {
	m := make(map[string]operator.Role)
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		value, role, ok := strings.Cut(pair, "=")
		value, role = strings.TrimSpace(value), strings.TrimSpace(role)
		if !ok || value == "" {
			return nil, fmt.Errorf("oidc: role map entry %q: want value=role", pair)
		}
		if !operator.IsValidRole(operator.Role(role)) {
			return nil, fmt.Errorf("oidc: role map entry %q: unknown role %q", pair, role)
		}
		m[value] = operator.Role(role)
	}
	return m, nil
}

// operatorID derives a stable operator ID from any subject format. It
// carries the store's external ID prefix, which no registered operator's ID
// can, and fits the store's ID rules (8-64 characters, alphanumeric and
// hyphens).
func operatorID(issuer, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return operator.ExternalIDPrefix + hex.EncodeToString(sum[:16])
}

func displayName(claims map[string]any, subject string) string {
	for _, k := range []string{"preferred_username", "name", "email"} {
		if v, ok := claims[k].(string); ok && v != "" {
			return v
		}
	}
	return subject
}

// claimValues reads the role claim as a top-level claim first, so names
// that contain dots (URL-style namespaced claims) work, then as a path.
func claimValues(claims map[string]any, name string) []string {
	v, ok := claims[name]
	if !ok {
		var cur any = claims
		for _, part := range strings.Split(name, ".") {
			m, isMap := cur.(map[string]any)
			if !isMap {
				return nil
			}
			cur = m[part]
		}
		v = cur
	}
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}
