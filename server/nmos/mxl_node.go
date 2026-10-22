package nmos

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GroupHintTag is the IS-04 tag a controller groups a node's senders and
// receivers by, with values of the form "<group name>:<role>".
const GroupHintTag = "urn:x-nmos:tag:grouphint/v1.0"

// BCP-004-01 parameter constraint names used by MXL receivers.
const (
	CapMediaType   = "urn:x-nmos:cap:format:media_type"
	CapFrameWidth  = "urn:x-nmos:cap:format:frame_width"
	CapFrameHeight = "urn:x-nmos:cap:format:frame_height"
	CapGrainRate   = "urn:x-nmos:cap:format:grain_rate"
	CapSampleRate  = "urn:x-nmos:cap:format:sample_rate"
)

// idNamespace is the UUID namespace DeriveID generates under.
var idNamespace = [16]byte{
	0x2b, 0x4f, 0x0c, 0x6e, 0x5a, 0x31, 0x4d, 0x8e,
	0x9b, 0x2c, 0x7f, 0x10, 0x63, 0xa4, 0xd5, 0x08,
}

// DeriveID is a stable resource id: a version 5 UUID over seed and parts.
// The same seed gives the same ids on every restart and on every engine
// configured with it, which IS-04 relies on for a controller to re-establish
// the connections it made.
func DeriveID(seed string, parts ...string) string {
	h := sha1.New()
	h.Write(idNamespace[:])
	h.Write([]byte(seed))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	var b [16]byte
	copy(b[:], h.Sum(nil))
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// QueryClient reads resources from an IS-04 Query API.
type QueryClient struct {
	baseURL string
	http    *http.Client
}

// NewQueryClient creates a client for the Query API at baseURL, the
// registry's origin without the /x-nmos path.
func NewQueryClient(baseURL string) *QueryClient {
	return &QueryClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *QueryClient) get(ctx context.Context, kind, id string, out any) error {
	url := fmt.Sprintf("%s/x-nmos/query/v1.3/%s/%s", c.baseURL, kind, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("nmos: query %s %s: %w", kind, id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("nmos: query %s %s: status %d", kind, id, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// SenderLabel is what a sender carries, for showing the operator what an
// input is connected to: the label of the sender's source, or of its flow
// or of the sender itself where the one before is empty or cannot be read.
func (c *QueryClient) SenderLabel(ctx context.Context, senderID string) (string, error) {
	var snd struct {
		Label  string  `json:"label"`
		FlowID *string `json:"flow_id"`
	}
	if err := c.get(ctx, "senders", senderID, &snd); err != nil {
		return "", err
	}
	label := snd.Label
	if snd.FlowID == nil {
		return label, nil
	}
	var flow struct {
		Label    string `json:"label"`
		SourceID string `json:"source_id"`
	}
	if err := c.get(ctx, "flows", *snd.FlowID, &flow); err != nil {
		return label, nil
	}
	if flow.Label != "" {
		label = flow.Label
	}
	var src struct {
		Label string `json:"label"`
	}
	if err := c.get(ctx, "sources", flow.SourceID, &src); err == nil && src.Label != "" {
		label = src.Label
	}
	return label, nil
}
