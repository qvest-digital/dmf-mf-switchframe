package operator

import (
	"net/http"
	"strings"

	"github.com/zsiec/switchframe/server/control/httperr"
)

// endpointSubsystemMap maps exact API paths to subsystems.
var endpointSubsystemMap = map[string]Subsystem{
	"/api/switch/cut":                 SubsystemSwitching,
	"/api/switch/preview":             SubsystemSwitching,
	"/api/switch/transition":          SubsystemSwitching,
	"/api/switch/transition/position": SubsystemSwitching,
	"/api/switch/ftb":                 SubsystemSwitching,
	"/api/audio/trim":                 SubsystemAudio,
	"/api/audio/level":                SubsystemAudio,
	"/api/audio/mute":                 SubsystemAudio,
	"/api/audio/afv":                  SubsystemAudio,
	"/api/audio/master":               SubsystemAudio,
	"/api/recording/start":            SubsystemOutput,
	"/api/recording/stop":             SubsystemOutput,
	"/api/output/srt/start":           SubsystemOutput,
	"/api/output/srt/stop":            SubsystemOutput,
	"/api/replay/mark-in":             SubsystemReplay,
	"/api/replay/mark-out":            SubsystemReplay,
	"/api/replay/play":                SubsystemReplay,
	"/api/replay/stop":                SubsystemReplay,
	"/api/format":                     SubsystemSwitching,
	"/api/encoder":                    SubsystemSwitching,
	"/api/layout":                     SubsystemSwitching,
	"/api/output/destinations":        SubsystemOutput,
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
	{"/api/sources/", SubsystemSwitching},           // label, delay, key
	{"/api/macros/", SubsystemSwitching},             // macro run
	{"/api/presets/", SubsystemSwitching},            // preset create, update, delete, recall
	{"/api/captions/", SubsystemCaptions},            // caption mode, text, clear
	{"/api/graphics/", SubsystemGraphics},            // layer on/off/frame/animate/fly/image
	{"/api/layout/", SubsystemSwitching},             // PIP slots, layout presets
	{"/api/output/destinations/", SubsystemOutput},   // multi-destination SRT
	{"/api/clips/", SubsystemSwitching},              // clip CRUD, player controls
	{"/api/scte35/", SubsystemOutput},                // SCTE-35 ad insertion
	{"/api/stinger/", SubsystemGraphics},             // stinger upload/delete/cut-point
}

// EndpointSubsystem maps an API path to its subsystem. Returns the subsystem
// and true if the path requires subsystem permission checking; false if the
// path is exempt (GETs, operator management, CRUD-only endpoints).
func EndpointSubsystem(path string) (Subsystem, bool) {
	// Exact match first.
	if sub, ok := endpointSubsystemMap[path]; ok {
		return sub, true
	}

	// Prefix match for parameterized endpoints.
	for _, pm := range endpointPrefixSubsystemMap {
		if strings.HasPrefix(path, pm.prefix) {
			return pm.subsystem, true
		}
	}

	return "", false
}

// NewMiddleware creates HTTP middleware that enforces operator role permissions
// and subsystem locking. The middleware is backward-compatible: when no operators
// are registered, all requests pass through without checks.
//
// Check order:
//  1. No operators registered → pass through
//  2. GET requests → pass through
//  3. /api/operator/* routes → exempt
//  4. Endpoint not mapped to subsystem → pass through
//  5. Identify operator from bearer token
//  6. Role allows commanding subsystem? → 403 if not
//  7. Subsystem locked by someone else? → 409 if so
//  8. Proceed
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
			if strings.HasPrefix(r.URL.Path, "/api/operator/") {
				next.ServeHTTP(w, r)
				return
			}

			// 4. Map endpoint to subsystem.
			sub, mapped := EndpointSubsystem(r.URL.Path)
			if !mapped {
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
