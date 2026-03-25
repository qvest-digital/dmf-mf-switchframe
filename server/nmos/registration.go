package nmos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrNodeNotFound is returned when a heartbeat receives a 404,
// indicating the node must be re-registered.
var ErrNodeNotFound = errors.New("nmos: node not found in registry")

// RegistrationClient registers NMOS IS-04 resources with an external
// NMOS registry and maintains periodic heartbeats.
type RegistrationClient struct {
	baseURL           string
	httpClient        *http.Client
	nodeID            string
	heartbeatInterval time.Duration
}

// NewRegistrationClient creates a registration client targeting the given
// NMOS registry base URL. The nodeID is used for heartbeat requests.
func NewRegistrationClient(baseURL, nodeID string) *RegistrationClient {
	return &RegistrationClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		nodeID:            nodeID,
		heartbeatInterval: 5 * time.Second,
	}
}

// registrationBody is the JSON envelope sent to the registration API.
type registrationBody struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// RegisterResource POSTs a single resource to the NMOS registry.
func (c *RegistrationClient) RegisterResource(ctx context.Context, resourceType string, resource interface{}) error {
	body := registrationBody{
		Type: resourceType,
		Data: resource,
	}

	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("nmos: marshal resource: %w", err)
	}

	url := c.baseURL + "/x-nmos/registration/v1.3/resource"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBytes))
	if err != nil {
		return fmt.Errorf("nmos: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("nmos: register %s: %w", resourceType, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("nmos: register %s: status %d: %s", resourceType, resp.StatusCode, string(respBody))
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// RegisterAll registers all resources in strict dependency order:
// node -> device -> sources -> flows -> senders, receivers.
// Returns on first error.
func (c *RegistrationClient) RegisterAll(ctx context.Context, node *Node, device *Device, sources []*Source, flows []*Flow, senders []*Sender, receivers []*Receiver) error {
	if err := c.RegisterResource(ctx, "node", node); err != nil {
		return err
	}
	if err := c.RegisterResource(ctx, "device", device); err != nil {
		return err
	}
	for _, s := range sources {
		if err := c.RegisterResource(ctx, "source", s); err != nil {
			return err
		}
	}
	for _, f := range flows {
		if err := c.RegisterResource(ctx, "flow", f); err != nil {
			return err
		}
	}
	for _, s := range senders {
		if err := c.RegisterResource(ctx, "sender", s); err != nil {
			return err
		}
	}
	for _, r := range receivers {
		if err := c.RegisterResource(ctx, "receiver", r); err != nil {
			return err
		}
	}
	return nil
}

// Heartbeat sends a health heartbeat for the node.
// Returns nil on 200, ErrNodeNotFound on 404, or an error on other failures.
func (c *RegistrationClient) Heartbeat(ctx context.Context) error {
	url := fmt.Sprintf("%s/x-nmos/registration/v1.3/health/nodes/%s", c.baseURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("nmos: create heartbeat request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("nmos: heartbeat: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, resp.Body)
		return ErrNodeNotFound
	default:
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("nmos: heartbeat: status %d: %s", resp.StatusCode, string(respBody))
	}
}

// StartHeartbeat starts a background goroutine that sends heartbeats at the
// configured interval (default 5s). Errors are sent to the returned channel.
// On ErrNodeNotFound the error is sent and the caller should re-register.
// The goroutine stops when the context is cancelled.
func (c *RegistrationClient) StartHeartbeat(ctx context.Context) <-chan error {
	errCh := make(chan error, 1)

	go func() {
		defer close(errCh)

		ticker := time.NewTicker(c.heartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.Heartbeat(ctx); err != nil {
					// Don't report context cancellation — it's an expected shutdown.
					if ctx.Err() != nil {
						return
					}
					// Non-blocking send.
					select {
					case errCh <- err:
					default:
					}
					if errors.Is(err, ErrNodeNotFound) {
						return
					}
				}
			}
		}
	}()

	return errCh
}

// UnregisterAll removes all resources in reverse dependency order:
// senders, receivers -> flows -> sources -> device -> node.
// Continues on errors (best-effort cleanup) and returns the first error.
func (c *RegistrationClient) UnregisterAll(ctx context.Context, node *Node, device *Device, sources []*Source, flows []*Flow, senders []*Sender, receivers []*Receiver) error {
	var firstErr error

	deleteResource := func(resourceType, id string) {
		url := fmt.Sprintf("%s/x-nmos/registration/v1.3/resource/%ss/%s", c.baseURL, resourceType, id)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("nmos: create delete request for %s/%s: %w", resourceType, id, err)
			}
			return
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("nmos: delete %s/%s: %w", resourceType, id, err)
			}
			return
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if firstErr == nil {
				firstErr = fmt.Errorf("nmos: delete %s/%s: status %d: %s", resourceType, id, resp.StatusCode, string(respBody))
			}
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
		}
	}

	// Senders first.
	for _, s := range senders {
		deleteResource("sender", s.ID)
	}
	// Receivers.
	for _, r := range receivers {
		deleteResource("receiver", r.ID)
	}
	// Flows.
	for _, f := range flows {
		deleteResource("flow", f.ID)
	}
	// Sources (reverse order for symmetry).
	for i := len(sources) - 1; i >= 0; i-- {
		deleteResource("source", sources[i].ID)
	}
	// Device.
	deleteResource("device", device.ID)
	// Node.
	deleteResource("node", node.ID)

	return firstErr
}
