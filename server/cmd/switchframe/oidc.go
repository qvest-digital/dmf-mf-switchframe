package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/zsiec/switchframe/server/oidcauth"
	"github.com/zsiec/switchframe/server/operator"
)

// oidcSettings are the --oidc-* flags after environment fallback.
type oidcSettings struct {
	Issuer           string
	ClientID         string
	ClientSecretFile string
	RedirectURL      string
	Scopes           string
	RoleClaim        string
	RoleMap          string
	DefaultRole      string
}

// config builds the sign-in configuration, or returns nil when no issuer is
// set. The client secret is read from a file so that it stays out of the
// process arguments and environment.
func (s oidcSettings) config() (*oidcauth.Config, error) {
	if s.Issuer == "" {
		if s != (oidcSettings{}) {
			return nil, errors.New("--oidc-* flags require --oidc-issuer")
		}
		return nil, nil
	}

	cfg := &oidcauth.Config{
		Issuer:      s.Issuer,
		ClientID:    s.ClientID,
		RedirectURL: s.RedirectURL,
		Scopes:      splitAndTrim(s.Scopes),
		RoleClaim:   s.RoleClaim,
		DefaultRole: operator.Role(s.DefaultRole),
	}
	if s.ClientSecretFile != "" {
		b, err := os.ReadFile(s.ClientSecretFile)
		if err != nil {
			return nil, fmt.Errorf("--oidc-client-secret-file: %w", err)
		}
		cfg.ClientSecret = strings.TrimSpace(string(b))
		if cfg.ClientSecret == "" {
			return nil, fmt.Errorf("--oidc-client-secret-file %s is empty", s.ClientSecretFile)
		}
	}
	if s.RedirectURL != "" {
		u, err := url.Parse(s.RedirectURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("--oidc-redirect-url %q must be an absolute http(s) URL", s.RedirectURL)
		}
	}
	if s.RoleMap != "" {
		m, err := oidcauth.ParseRoleMap(s.RoleMap)
		if err != nil {
			return nil, fmt.Errorf("--oidc-role-map: %w", err)
		}
		cfg.RoleMap = m
	}
	if _, err := oidcauth.New(*cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// initOIDC creates the authenticator and tries discovery once, so a
// misconfigured issuer shows in the startup log rather than at the first
// sign-in. A failure is not fatal: the session API token keeps working and
// discovery is retried on use.
func (a *App) initOIDC() error {
	if a.cfg.OIDC == nil {
		return nil
	}
	auth, err := oidcauth.New(*a.cfg.OIDC)
	if err != nil {
		return err
	}
	a.oidcAuth = auth

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := auth.Discover(ctx); err != nil {
		slog.Warn("OpenID Connect sign-in enabled, provider not reachable yet", "issuer", a.cfg.OIDC.Issuer, "error", err)
	} else {
		slog.Info("OpenID Connect sign-in enabled", "issuer", a.cfg.OIDC.Issuer, "client_id", a.cfg.OIDC.ClientID)
	}
	return nil
}

// resolveOIDCOperator lets the operator store accept a verified ID token as
// an operator's bearer token. The user becomes, or stays, a registered
// operator, so role checks, locks and the team list treat them like any
// other operator.
func (a *App) resolveOIDCOperator(token string) (operator.Operator, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := a.oidcAuth.Authenticate(ctx, token)
	if err != nil {
		slog.Debug("bearer token is not an accepted ID token", "error", err)
		return operator.Operator{}, operator.ErrNotFound
	}
	op, err := a.operatorStore.Upsert(id.OperatorID, id.Name, id.Role)
	if err != nil {
		// A verified user the store refuses, e.g. one whose name an
		// operator registered with a token already holds, gets a 401
		// like any unknown token; the reason is only here.
		slog.Warn("signed-in user not accepted as operator", "name", id.Name, "error", err)
	}
	return op, err
}
