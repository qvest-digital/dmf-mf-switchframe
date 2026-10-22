package oidcauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// signingAlgs are the asymmetric algorithms an ID token may be signed
// with. Symmetric algorithms and "none" are never accepted.
var signingAlgs = []jose.SignatureAlgorithm{
	jose.RS256, jose.RS384, jose.RS512,
	jose.PS256, jose.PS384, jose.PS512,
	jose.ES256, jose.ES384, jose.ES512,
	jose.EdDSA,
}

// keyRefetchInterval is the shortest time between two fetches of the
// provider's keys. A variable so tests can shorten it.
var keyRefetchInterval = 30 * time.Second

// keySet verifies token signatures against the provider's published keys.
// It refetches them when a signature matches none, which is how a key
// rotation is picked up, but at most once per keyRefetchInterval: a bearer
// token is attacker-controlled, and without the limit every forged token
// would cost a request to the provider.
type keySet struct {
	url string

	mu        sync.Mutex
	keys      []jose.JSONWebKey
	fetchedAt time.Time
}

func (k *keySet) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	jws, err := jose.ParseSigned(raw, signingAlgs)
	if err != nil {
		return nil, fmt.Errorf("oidc: malformed token: %w", err)
	}
	if len(jws.Signatures) != 1 {
		return nil, errors.New("oidc: token must carry exactly one signature")
	}
	kid := jws.Signatures[0].Header.KeyID

	k.mu.Lock()
	keys := k.keys
	k.mu.Unlock()
	if payload, ok := verifyWith(jws, keys, kid); ok {
		return payload, nil
	}

	keys, err = k.refetch(ctx)
	if err != nil {
		return nil, err
	}
	if payload, ok := verifyWith(jws, keys, kid); ok {
		return payload, nil
	}
	return nil, errors.New("oidc: token signature matches no provider key")
}

func verifyWith(jws *jose.JSONWebSignature, keys []jose.JSONWebKey, kid string) ([]byte, bool) {
	for i := range keys {
		if kid != "" && keys[i].KeyID != kid {
			continue
		}
		if payload, err := jws.Verify(&keys[i]); err == nil {
			return payload, true
		}
	}
	return nil, false
}

// refetch fetches the key set unless that happened within the last
// keyRefetchInterval, and returns the current keys either way.
func (k *keySet) refetch(ctx context.Context) ([]jose.JSONWebKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.fetchedAt.IsZero() && time.Since(k.fetchedAt) < keyRefetchInterval {
		return k.keys, nil
	}
	k.fetchedAt = time.Now()

	// Detached from the request so a cancelled request cannot leave a
	// half-done fetch, and bounded because the lock is held.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), providerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return k.keys, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return k.keys, fmt.Errorf("%w: fetch keys: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return k.keys, fmt.Errorf("%w: fetch keys: %s", ErrUnavailable, resp.Status)
	}
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return k.keys, fmt.Errorf("%w: decode keys: %v", ErrUnavailable, err)
	}
	// A key this package cannot use, such as an encryption key or one of
	// an unsupported type, is skipped rather than failing the whole set.
	keys := make([]jose.JSONWebKey, 0, len(set.Keys))
	for _, raw := range set.Keys {
		var key jose.JSONWebKey
		if err := key.UnmarshalJSON(raw); err != nil || key.Use == "enc" || !key.IsPublic() {
			continue
		}
		keys = append(keys, key)
	}
	k.keys = keys
	return k.keys, nil
}
