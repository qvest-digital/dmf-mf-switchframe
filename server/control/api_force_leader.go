package control

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/zsiec/switchframe/server/control/httperr"
)

const (
	// maxOperatorHeaderLen caps the X-Cloud-Proxied-Operator header length.
	// Prometheus label cardinality + log-injection defense.
	maxOperatorHeaderLen = 128
	// defaultOperatorLabel is the audit attribution when no proxied header
	// is present — i.e. a direct SRE call with only the control-plane secret.
	defaultOperatorLabel = "control-plane"
	// otherReasonClass is the metric-label bucket for a reason body whose
	// first token does not match a known class.  Keeps the reason_class
	// label cardinality bounded against free-text typos from well-meaning
	// clients.
	otherReasonClass       = "other"
	unspecifiedReasonClass = "unspecified"
)

// knownReasonClasses is the closed enum of reason_class label values
// that may appear on the force_leader_total counter.  Anything else
// falls through to otherReasonClass.  Keep this set small — every
// addition expands the Prometheus timeseries cardinality.
var knownReasonClasses = map[string]struct{}{
	"partition":   {}, // peer unreachable, operator taking over
	"recovery":    {}, // post-incident restoration (test cutover, planned drill)
	"maintenance": {}, // planned maintenance window
	"test":        {}, // synthetic test / runbook validation
	"drill":       {}, // ops drill / tabletop
}

// handleForceLeader bumps the local peer epoch, promoting this engine to
// leader in the tiebreak regardless of label.  Authenticated via the
// X-Control-Plane-Secret header (NOT the regular API key) so an operator
// pressing a normal UI button cannot accidentally fire it — the secret
// lives on the cloud control plane, which is the only path for browser
// operators to reach this endpoint.
//
// Optional audit header:
//
//	X-Cloud-Proxied-Operator: <operator-name>
//
// set by the switchframe-cloud proxy when a browser operator triggers
// the call, so the engine's audit log + Prometheus metric identify
// *who* initiated the takeover rather than just reporting
// "control-plane."
//
// Response: {"newEpoch": <uint64>, "role": <string>, "label": <string>, "operator": <string>}.
// 501 if no secret is configured OR no peer poller exists (standalone engine).
// 403 if the secret header is missing or mismatched (constant-time compare).
func (a *API) handleForceLeader(w http.ResponseWriter, r *http.Request) {
	if a.controlPlaneSecret == "" || a.peerPoller == nil {
		httperr.Write(w, http.StatusNotImplemented,
			"force-leader not configured (missing --control-plane-secret or --peer-engine-url)")
		return
	}

	provided := r.Header.Get("X-Control-Plane-Secret")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(a.controlPlaneSecret)) != 1 {
		httperr.Write(w, http.StatusForbidden, "invalid control-plane secret")
		return
	}

	// Body is optional; decode reason for audit logging, ignore parse errors.
	// Capped at 4 KiB — the reason is a short human string, not a payload.
	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		data, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		_ = json.Unmarshal(data, &body)
	}

	operator := sanitizeOperatorHeader(r.Header.Get("X-Cloud-Proxied-Operator"))
	reasonClass := reasonClassOf(body.Reason)

	newEpoch := a.peerPoller.BumpEpoch()
	slog.Warn("engine forced to leader role via /api/peer/force-leader",
		"newEpoch", newEpoch,
		"label", a.peerPoller.LocalLabel(),
		"operator", operator,
		"reason", body.Reason,
		"reason_class", reasonClass)

	if a.appMetrics != nil {
		a.appMetrics.ForceLeaderTotal.WithLabelValues(operator, reasonClass).Inc()
	}

	resp := struct {
		NewEpoch uint64 `json:"newEpoch"`
		Role     string `json:"role"`
		Label    string `json:"label"`
		Operator string `json:"operator"`
	}{
		NewEpoch: newEpoch,
		Role:     a.peerPoller.Role().String(),
		Label:    a.peerPoller.LocalLabel(),
		Operator: operator,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// sanitizeOperatorHeader normalizes the X-Cloud-Proxied-Operator value
// into a safe audit-log and Prometheus-label string.  unicode.IsPrint
// returns false for Unicode categories Cc, Cf, Cs, Co, Cn, Zl, Zp — so
// the !IsPrint check rejects ASCII < 0x20, DEL, NEL (U+0085) and the
// rest of the C1 block, bidi overrides (U+202A..U+202E), zero-width
// formatters (ZWSP U+200B, ZWJ U+200D) that let visually-identical
// operators parse as distinct metric series, and the Zl/Zp line +
// paragraph separators (U+2028/U+2029) that downstream line-based log
// consumers treat as newlines.  Surrounding whitespace is trimmed, the
// result is capped at maxOperatorHeaderLen to bound Prometheus label
// cardinality, and an empty result (unset header or entirely stripped)
// returns defaultOperatorLabel.
func sanitizeOperatorHeader(raw string) string {
	if raw == "" {
		return defaultOperatorLabel
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if unicode.IsControl(r) {
			continue
		}
		if !unicode.IsPrint(r) {
			continue
		}
		b.WriteRune(r)
	}
	v := strings.TrimSpace(b.String())
	if v == "" {
		return defaultOperatorLabel
	}
	if len(v) > maxOperatorHeaderLen {
		v = v[:maxOperatorHeaderLen]
	}
	return v
}

// reasonClassOf maps the free-text reason body to a closed-set
// Prometheus label value.  We take the first whitespace-delimited
// token of the reason, lowercase it, and match against the
// knownReasonClasses enum; anything else buckets to otherReasonClass.
// Free-text detail still lands in slog via the unbounded `reason`
// field — the metric label is deliberately coarse-grained to bound
// Prometheus timeseries cardinality under typo'd / mis-categorized
// inputs.
func reasonClassOf(reason string) string {
	v := strings.TrimSpace(reason)
	if v == "" {
		return unspecifiedReasonClass
	}
	if i := strings.IndexFunc(v, func(r rune) bool { return r == ' ' || r == '\t' }); i > 0 {
		v = v[:i]
	}
	v = strings.ToLower(v)
	if _, ok := knownReasonClasses[v]; ok {
		return v
	}
	return otherReasonClass
}
