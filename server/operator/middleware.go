package operator

import (
	"net/http"
	"strings"

	"github.com/zsiec/switchframe/server/control/httperr"
)

// endpointSubsystemMap maps exact API paths to subsystems.
var endpointSubsystemMap = map[string]Subsystem{
	"/api/switch/cut":                        SubsystemSwitching,
	"/api/switch/preview":                    SubsystemSwitching,
	"/api/switch/transition":                 SubsystemSwitching,
	"/api/switch/transition/position":        SubsystemSwitching,
	"/api/switch/ftb":                        SubsystemSwitching,
	"/api/switch/ftb-duration":               SubsystemSwitching,
	"/api/switch/transition-abort-threshold": SubsystemSwitching,
	"/api/audio/trim":                        SubsystemAudio,
	"/api/audio/level":                       SubsystemAudio,
	"/api/audio/mute":                        SubsystemAudio,
	"/api/audio/afv":                         SubsystemAudio,
	"/api/audio/master":                      SubsystemAudio,
	"/api/recording/start":                   SubsystemOutput,
	"/api/recording/stop":                    SubsystemOutput,
	"/api/output/srt/start":                  SubsystemOutput,
	"/api/output/srt/stop":                   SubsystemOutput,
	"/api/replay/mark-in":                    SubsystemReplay,
	"/api/replay/mark-out":                   SubsystemReplay,
	"/api/replay/play":                       SubsystemReplay,
	"/api/replay/stop":                       SubsystemReplay,
	"/api/replay/quick":                      SubsystemReplay,
	"/api/replay/pause":                      SubsystemReplay,
	"/api/replay/resume":                     SubsystemReplay,
	"/api/replay/seek":                       SubsystemReplay,
	"/api/replay/speed":                      SubsystemReplay,
	"/api/replay/marks":                      SubsystemReplay,
	"/api/format":                            SubsystemSwitching,
	"/api/encoder":                           SubsystemSwitching,
	"/api/dve":                               SubsystemSwitching,
	"/api/output/destinations":               SubsystemOutput,
	// Program-affecting endpoints. These mutate on-air output and were
	// previously unmapped, meaning they fell through to the (formerly)
	// allow-by-default branch and could be driven by any registered
	// operator — including a read-only viewer. Mapping them to a lockable
	// subsystem routes them through the role/lock checks.
	"/api/colorgrade":           SubsystemSwitching, // PUT alters program color grade
	"/api/stmap/program":        SubsystemSwitching, // PUT/DELETE program geometry
	"/api/replay/shutter-angle": SubsystemReplay,    // PUT replay shutter angle
}

// endpointPrefixSubsystemMap maps path prefixes to subsystems,
// used for endpoints with path parameters (e.g., /api/audio/{source}/eq).
// Order matters: more specific prefixes must come before less specific ones
// (e.g., /api/output/destinations/ before /api/output/ if both existed).
var endpointPrefixSubsystemMap = []struct {
	prefix    string
	subsystem Subsystem
}{
	{"/api/audio/", SubsystemAudio},
	{"/api/sources/", SubsystemSwitching},          // label, delay, key
	{"/api/macros/", SubsystemSwitching},           // macro run
	{"/api/presets/", SubsystemSwitching},          // preset create, update, delete, recall
	{"/api/captions/", SubsystemCaptions},          // caption mode, text, clear
	{"/api/graphics/", SubsystemGraphics},          // layer on/off/frame/animate/fly/image
	{"/api/dve/", SubsystemSwitching},              // DVE slots, presets
	{"/api/output/destinations/", SubsystemOutput}, // multi-destination SRT
	{"/api/clips/", SubsystemSwitching},            // clip CRUD, player controls
	{"/api/scte35/", SubsystemOutput},              // SCTE-35 ad insertion
	{"/api/stinger/", SubsystemGraphics},           // stinger upload/delete/cut-point
	// Program-affecting subsystems previously unmapped (privilege-escalation
	// risk: a viewer could drive on-air playout / color / geometry / pipeline).
	{"/api/playout/", SubsystemSwitching},        // channel + pod on-air automation
	{"/api/colorgrade/", SubsystemSwitching},     // corrector set/reset, LUT, looks
	{"/api/stmap/", SubsystemSwitching},          // map upload/delete + source/program assign
	{"/api/pipeline/nodes/", SubsystemSwitching}, // node bypass (DSK/color/AI on program)
	{"/api/asr/", SubsystemCaptions},             // ASR config drives captions
	{"/api/wipes/custom", SubsystemGraphics},     // custom wipe upload/delete
}

// EndpointSubsystem maps an API path to its subsystem. Returns the subsystem
// and true if the path requires subsystem permission checking; false if the
// path is exempt (GETs, operator management, CRUD-only endpoints).
//
// Paths with /api/v1/ prefix are normalized to /api/ before lookup so that
// versioned requests go through the same permission checks.
func EndpointSubsystem(path string) (Subsystem, bool) {
	// Normalize /api/v1/... to /api/... so versioned paths are checked.
	normalized := normalizeAPIPath(path)

	// Exact match first.
	if sub, ok := endpointSubsystemMap[normalized]; ok {
		return sub, true
	}

	// Prefix match for parameterized endpoints. Also match the bare collection
	// root (the prefix without its trailing slash) so that mutating
	// collection-root routes — e.g. POST /api/graphics, POST /api/sources,
	// POST /api/presets — are guarded by the same subsystem as their subtree.
	// Without this, strings.HasPrefix("/api/graphics", "/api/graphics/") is
	// false and the bare route falls through to the deny-by-default branch,
	// bypassing the per-subsystem role and lock checks the prefix entry implies.
	for _, pm := range endpointPrefixSubsystemMap {
		if strings.HasPrefix(normalized, pm.prefix) ||
			normalized == strings.TrimSuffix(pm.prefix, "/") {
			return pm.subsystem, true
		}
	}

	return "", false
}

// machineAuthPaths lists exact mutating paths that are authenticated by their
// own handler with a machine credential rather than an operator bearer token.
// POST /api/peer/force-leader is called by the cloud control plane during a
// failover and authenticated via the X-Control-Plane-Secret header (see
// control/api_force_leader.go and its entry in control/auth.go
// authExemptPaths); the caller never holds an operator token, so routing it
// through the operator role check would 403 every promotion attempt while
// operators are registered. The operator middleware must therefore pass these
// through untouched — the handler itself rejects requests without the correct
// secret. Keep this an explicit per-path allowlist (no prefix pass-through) so
// new /api/peer/* mutating routes do not silently bypass operator authz.
var machineAuthPaths = map[string]bool{
	"/api/peer/force-leader": true, // X-Control-Plane-Secret enforced in handler
}

// isMachineAuthPath reports whether a path is handler-authenticated by a
// machine credential. The /api/v1/ prefix is normalized first so versioned
// requests are classified identically.
func isMachineAuthPath(path string) bool {
	return machineAuthPaths[normalizeAPIPath(path)]
}

// authenticatedOperatorMutatingPaths lists mutating paths open to ANY
// registered operator, provided the request carries a valid operator bearer
// token. These are the dual-engine reconciliation endpoints: the auto-reconcile
// loop runs unconditionally in EVERY operator's browser (ui
// dual-api-client.ts / +page.svelte wire it with no role gating), posting with
// the operator's own token. Restricting them to directors would break engine
// reconciliation whenever no director has a browser open.
//
// Privilege note (re: the deny-by-default rationale in NewMiddleware): yes, a
// viewer token can push a state snapshot here. That is the pre-hardening
// behavior — these endpoints are how every browser participates in keeping the
// two engines converged — but unlike main, which required nothing at all, an
// identified, registered operator token is now mandatory: anonymous or
// unknown-token callers are rejected. The reconcile-lock handlers additionally
// serialize snapshot pushes server-side.
var authenticatedOperatorMutatingPaths = map[string]bool{
	"/api/sync/state-snapshot": true, // POST applies a snapshot (GET passes via read-only rule)
}

// authenticatedOperatorMutatingPrefixes complements the exact-path map above
// for parameterized reconcile routes (POST /api/sync/reconcile-lock,
// PUT/DELETE /api/sync/reconcile-lock/{lockId}).
var authenticatedOperatorMutatingPrefixes = []string{
	"/api/sync/reconcile-lock",
}

// isAuthenticatedOperatorMutating reports whether a non-GET path is open to
// any registered operator that presents a valid token. The /api/v1/ prefix is
// normalized first so versioned requests are classified identically.
func isAuthenticatedOperatorMutating(path string) bool {
	normalized := normalizeAPIPath(path)
	if authenticatedOperatorMutatingPaths[normalized] {
		return true
	}
	for _, p := range authenticatedOperatorMutatingPrefixes {
		if strings.HasPrefix(normalized, p) {
			return true
		}
	}
	return false
}

// normalizeAPIPath rewrites /api/v1/... to /api/... so versioned and
// unversioned requests are classified identically.
func normalizeAPIPath(path string) string {
	if strings.HasPrefix(path, "/api/v1/") {
		return "/api/" + path[len("/api/v1/"):]
	}
	return path
}

// exemptMutatingPrefixes lists mutating (non-GET) path prefixes that are NOT
// tied to a lockable subsystem but are intentionally usable by any registered
// operator, including a viewer. These are coordination / talkback primitives
// that do not alter the on-air program. Everything else that is not mapped to
// a subsystem is treated as privileged (director-only) under the deny-by-default
// policy in NewMiddleware — this prevents new or overlooked mutating routes
// from being silently reachable by low-privilege operators.
var exemptMutatingPrefixes = []string{
	"/api/comms/", // talkback join/leave/mute — does not touch program
}

// isExemptMutating reports whether a non-GET path is on the explicit exemption
// allowlist (open to any registered operator). The /api/v1/ prefix is
// normalized first so versioned requests are classified identically.
func isExemptMutating(path string) bool {
	normalized := normalizeAPIPath(path)
	for _, p := range exemptMutatingPrefixes {
		if strings.HasPrefix(normalized, p) {
			return true
		}
	}
	return false
}

// NewMiddleware creates HTTP middleware that enforces operator role permissions
// and subsystem locking. The middleware is backward-compatible: when no operators
// are registered, all requests pass through without checks.
//
// Check order:
//  1. No operators registered → pass through
//  2. GET requests → pass through
//  3. /api/operator/* routes → exempt
//  4. Mutating path not mapped to a subsystem:
//     - machine-authenticated (e.g. peer/force-leader, secret enforced by the
//     handler itself) → pass through
//     - dual-engine sync/reconcile endpoints → require any identified
//     registered operator (any role), else 403
//     - on the explicit exemption allowlist (e.g. comms) → pass through
//     - otherwise deny-by-default: require an identified director, else 403
//  5. Identify operator from bearer token
//  6. Role allows commanding subsystem? → 403 if not
//  7. Subsystem locked by someone else? → 409 if so
//  8. Proceed
//
// Step 4 is deny-by-default for mutating requests: any unmapped POST/PUT/DELETE/
// PATCH that is not explicitly exempt is treated as privileged and requires the
// director role. This closes the prior privilege-escalation hole where any
// unmapped mutating route (e.g. /api/playout/*, /api/colorgrade/*) was
// reachable by every registered operator, including a read-only viewer. The
// sync/reconcile endpoints are an intentional, documented carve-out (see
// authenticatedOperatorMutatingPaths): they must remain callable by every
// operator's browser, but unlike on main they now require a valid registered
// operator token rather than nothing.
func NewMiddleware(store *Store, sm *SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. No operators registered → backward compatible pass-through.
			if store.Count() == 0 {
				next.ServeHTTP(w, r)
				return
			}

			// 2. GET requests always pass through (read-only).
			if r.Method == http.MethodGet {
				next.ServeHTTP(w, r)
				return
			}

			// 3. /api/operator/* routes are exempt from lock checking.
			if strings.HasPrefix(r.URL.Path, "/api/operator/") ||
				strings.HasPrefix(r.URL.Path, "/api/v1/operator/") {
				next.ServeHTTP(w, r)
				return
			}

			// 4. Map endpoint to subsystem.
			sub, mapped := EndpointSubsystem(r.URL.Path)
			if !mapped {
				// Machine-authenticated endpoints (X-Control-Plane-Secret)
				// carry no operator token by design; their handlers enforce
				// the secret themselves.
				if isMachineAuthPath(r.URL.Path) {
					next.ServeHTTP(w, r)
					return
				}
				// Dual-engine sync/reconcile endpoints are driven by the
				// auto-reconcile loop in every operator's browser: any
				// identified registered operator may call them, but anonymous
				// or unknown-token requests are rejected.
				if isAuthenticatedOperatorMutating(r.URL.Path) {
					if _, err := store.GetByToken(ExtractBearerToken(r)); err != nil {
						httperr.Write(w, http.StatusForbidden, "operator not identified")
						return
					}
					next.ServeHTTP(w, r)
					return
				}
				// Explicitly exempt mutating endpoints (e.g. comms) are open to
				// any registered operator.
				if isExemptMutating(r.URL.Path) {
					next.ServeHTTP(w, r)
					return
				}
				// Deny-by-default: an unmapped, non-exempt mutating path is
				// privileged. Require an identified director — anything else
				// (unknown token or a non-director role) is rejected. This
				// prevents overlooked mutating routes from being silently
				// reachable by low-privilege operators.
				op, err := store.GetByToken(ExtractBearerToken(r))
				if err != nil {
					httperr.Write(w, http.StatusForbidden, "operator not identified")
					return
				}
				if op.Role != RoleDirector {
					httperr.Write(w, http.StatusForbidden, "role '"+string(op.Role)+"' cannot access privileged endpoint")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// 5. Identify operator from bearer token.
			token := ExtractBearerToken(r)
			op, err := store.GetByToken(token)
			if err != nil {
				// Unknown token — reject as forbidden.
				httperr.Write(w, http.StatusForbidden, "operator not identified")
				return
			}

			// 6. Role permission check.
			if !CanCommand(op.Role, sub) {
				httperr.Write(w, http.StatusForbidden, "role '"+string(op.Role)+"' cannot command '"+string(sub)+"'")
				return
			}

			// 7. Lock ownership check.
			if err := sm.CheckLock(op.ID, sub); err != nil {
				httperr.Write(w, http.StatusConflict, "subsystem '"+string(sub)+"' is locked by another operator")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ExtractBearerToken extracts the token from the Authorization header.
func ExtractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}
