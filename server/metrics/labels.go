package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// srtSourcePrefix is the key prefix of an SRT input. Its suffix comes from
// the stream id a publisher sends, which can carry a stream key.
const srtSourcePrefix = "srt:"

// SourceLabel is the value a source key takes in a metric label. An SRT
// input's key is derived from the publisher's stream id, which can be a
// credential, so it is replaced by a short digest of itself: stable for
// the same source, and never the stream id. Every other key is a name the
// booking or the operator gave.
func SourceLabel(key string) string {
	if !strings.HasPrefix(key, srtSourcePrefix) {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return srtSourcePrefix + hex.EncodeToString(sum[:6])
}

// MethodLabel is the value an HTTP method takes in a metric label: the
// method itself for the standard ones, "other" for anything a client made
// up, so an unauthenticated request cannot mint a series of its own.
func MethodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return method
	}
	return "other"
}
