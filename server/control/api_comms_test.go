package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/switcher"
)

// mockCommsManager implements CommsManagerAPI for testing.
type mockCommsManager struct {
	joined map[string]string // operatorID → name
	muted  map[string]bool   // operatorID → muted
}

func newMockCommsManager() *mockCommsManager {
	return &mockCommsManager{
		joined: make(map[string]string),
		muted:  make(map[string]bool),
	}
}

func (m *mockCommsManager) Join(operatorID, name string) error {
	m.joined[operatorID] = name
	return nil
}

func (m *mockCommsManager) Leave(operatorID string) {
	delete(m.joined, operatorID)
	delete(m.muted, operatorID)
}

func (m *mockCommsManager) SetMuted(operatorID string, muted bool) error {
	if _, ok := m.joined[operatorID]; !ok {
		return errMockNotInComms
	}
	m.muted[operatorID] = muted
	return nil
}

func (m *mockCommsManager) State() *internal.CommsState {
	if len(m.joined) == 0 {
		return nil
	}
	participants := make([]internal.CommsParticipant, 0, len(m.joined))
	for id, name := range m.joined {
		participants = append(participants, internal.CommsParticipant{
			OperatorID: id,
			Name:       name,
			Muted:      m.muted[id],
		})
	}
	return &internal.CommsState{
		Active:       true,
		Participants: participants,
	}
}

// errMockNotInComms is a test sentinel for the mock.
var errMockNotInComms = &mockNotInCommsError{}

type mockNotInCommsError struct{}

func (e *mockNotInCommsError) Error() string { return "operator not in comms" }

// newTestCommsAPI creates an API with a real switcher and mock comms manager.
func newTestCommsAPI(t *testing.T, mcm *mockCommsManager) *API {
	t.Helper()
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	return NewAPI(sw, WithCommsManager(mcm))
}

func TestHandleCommsJoin(t *testing.T) {
	mcm := newMockCommsManager()
	api := newTestCommsAPI(t, mcm)

	body, _ := json.Marshal(commsJoinRequest{OperatorID: "op1", Name: "Alice"})
	req := httptest.NewRequest("POST", "/api/comms/join", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "Alice", mcm.joined["op1"])

	// Response should be JSON (enriched state).
	require.Contains(t, rr.Header().Get("Content-Type"), "application/json")
}

func TestHandleCommsJoin_MissingOperatorID(t *testing.T) {
	mcm := newMockCommsManager()
	api := newTestCommsAPI(t, mcm)

	body, _ := json.Marshal(commsJoinRequest{Name: "Alice"})
	req := httptest.NewRequest("POST", "/api/comms/join", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleCommsJoin_MissingName(t *testing.T) {
	mcm := newMockCommsManager()
	api := newTestCommsAPI(t, mcm)

	body, _ := json.Marshal(commsJoinRequest{OperatorID: "op1"})
	req := httptest.NewRequest("POST", "/api/comms/join", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleCommsJoin_InvalidJSON(t *testing.T) {
	mcm := newMockCommsManager()
	api := newTestCommsAPI(t, mcm)

	req := httptest.NewRequest("POST", "/api/comms/join", bytes.NewReader([]byte("not json")))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleCommsLeave(t *testing.T) {
	mcm := newMockCommsManager()
	mcm.joined["op1"] = "Alice"
	api := newTestCommsAPI(t, mcm)

	body, _ := json.Marshal(commsLeaveRequest{OperatorID: "op1"})
	req := httptest.NewRequest("POST", "/api/comms/leave", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	_, exists := mcm.joined["op1"]
	require.False(t, exists, "operator should be removed after leave")
}

func TestHandleCommsLeave_MissingOperatorID(t *testing.T) {
	mcm := newMockCommsManager()
	api := newTestCommsAPI(t, mcm)

	body, _ := json.Marshal(commsLeaveRequest{})
	req := httptest.NewRequest("POST", "/api/comms/leave", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleCommsMute(t *testing.T) {
	mcm := newMockCommsManager()
	mcm.joined["op1"] = "Alice"
	api := newTestCommsAPI(t, mcm)

	body, _ := json.Marshal(commsMuteRequest{OperatorID: "op1", Muted: true})
	req := httptest.NewRequest("PUT", "/api/comms/mute", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, mcm.muted["op1"])
}

func TestHandleCommsMute_MissingOperatorID(t *testing.T) {
	mcm := newMockCommsManager()
	api := newTestCommsAPI(t, mcm)

	body, _ := json.Marshal(commsMuteRequest{Muted: true})
	req := httptest.NewRequest("PUT", "/api/comms/mute", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleCommsMute_NotInComms(t *testing.T) {
	mcm := newMockCommsManager()
	api := newTestCommsAPI(t, mcm)

	body, _ := json.Marshal(commsMuteRequest{OperatorID: "op1", Muted: true})
	req := httptest.NewRequest("PUT", "/api/comms/mute", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	// Mock returns error for unknown operator → 500 (default for unknown errors).
	require.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestHandleCommsStatus(t *testing.T) {
	mcm := newMockCommsManager()
	mcm.joined["op1"] = "Alice"
	api := newTestCommsAPI(t, mcm)

	req := httptest.NewRequest("GET", "/api/comms/status", nil)
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var state internal.CommsState
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&state))
	require.True(t, state.Active)
	require.Len(t, state.Participants, 1)
	require.Equal(t, "op1", state.Participants[0].OperatorID)
	require.Equal(t, "Alice", state.Participants[0].Name)
}

func TestHandleCommsStatus_Empty(t *testing.T) {
	mcm := newMockCommsManager()
	api := newTestCommsAPI(t, mcm)

	req := httptest.NewRequest("GET", "/api/comms/status", nil)
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	// State() returns nil when no participants — handler should return empty CommsState.
	var state internal.CommsState
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&state))
	require.False(t, state.Active)
}

// newAuthedCommsAPI creates an API with a real switcher, a mock comms manager,
// and an operator store so the comms handlers enforce bearer-token identity.
func newAuthedCommsAPI(t *testing.T, mcm *mockCommsManager) (*API, *operator.Store) {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "operators.json")
	store, err := operator.NewStore(storePath)
	require.NoError(t, err)
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	api := NewAPI(sw, WithCommsManager(mcm), WithOperatorStore(store))
	return api, store
}

// TestHandleComms_IDOR verifies that, when operators are registered, the comms
// handlers derive identity from the bearer token: a non-director may only act on
// their own operator ID, while a director may act on any operator.
func TestHandleComms_IDOR(t *testing.T) {
	t.Run("non-director cannot join as another operator", func(t *testing.T) {
		mcm := newMockCommsManager()
		api, store := newAuthedCommsAPI(t, mcm)
		victim, err := store.Register("Victim", operator.RoleAudio)
		require.NoError(t, err)
		attacker, err := store.Register("Attacker", operator.RoleAudio)
		require.NoError(t, err)

		body, _ := json.Marshal(commsJoinRequest{OperatorID: victim.ID, Name: "Victim"})
		req := httptest.NewRequest("POST", "/api/comms/join", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+attacker.Token)
		rr := httptest.NewRecorder()
		api.mux.ServeHTTP(rr, req)

		require.Equal(t, http.StatusForbidden, rr.Code)
		_, joined := mcm.joined[victim.ID]
		require.False(t, joined, "attacker must not be able to join as the victim")
	})

	t.Run("non-director cannot mute another operator", func(t *testing.T) {
		mcm := newMockCommsManager()
		api, store := newAuthedCommsAPI(t, mcm)
		victim, err := store.Register("Victim", operator.RoleAudio)
		require.NoError(t, err)
		attacker, err := store.Register("Attacker", operator.RoleAudio)
		require.NoError(t, err)
		mcm.joined[victim.ID] = "Victim"

		body, _ := json.Marshal(commsMuteRequest{OperatorID: victim.ID, Muted: true})
		req := httptest.NewRequest("PUT", "/api/comms/mute", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+attacker.Token)
		rr := httptest.NewRecorder()
		api.mux.ServeHTTP(rr, req)

		require.Equal(t, http.StatusForbidden, rr.Code)
		require.False(t, mcm.muted[victim.ID], "attacker must not be able to mute the victim")
	})

	t.Run("non-director cannot remove another operator", func(t *testing.T) {
		mcm := newMockCommsManager()
		api, store := newAuthedCommsAPI(t, mcm)
		victim, err := store.Register("Victim", operator.RoleAudio)
		require.NoError(t, err)
		attacker, err := store.Register("Attacker", operator.RoleAudio)
		require.NoError(t, err)
		mcm.joined[victim.ID] = "Victim"

		body, _ := json.Marshal(commsLeaveRequest{OperatorID: victim.ID})
		req := httptest.NewRequest("POST", "/api/comms/leave", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+attacker.Token)
		rr := httptest.NewRecorder()
		api.mux.ServeHTTP(rr, req)

		require.Equal(t, http.StatusForbidden, rr.Code)
		_, joined := mcm.joined[victim.ID]
		require.True(t, joined, "attacker must not be able to remove the victim")
	})

	t.Run("operator can act on self", func(t *testing.T) {
		mcm := newMockCommsManager()
		api, store := newAuthedCommsAPI(t, mcm)
		self, err := store.Register("Self", operator.RoleAudio)
		require.NoError(t, err)

		body, _ := json.Marshal(commsJoinRequest{OperatorID: self.ID, Name: "Self"})
		req := httptest.NewRequest("POST", "/api/comms/join", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+self.Token)
		rr := httptest.NewRecorder()
		api.mux.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())
		require.Equal(t, "Self", mcm.joined[self.ID])
	})

	t.Run("director can act on another operator", func(t *testing.T) {
		mcm := newMockCommsManager()
		api, store := newAuthedCommsAPI(t, mcm)
		director, err := store.Register("Director", operator.RoleDirector)
		require.NoError(t, err)
		other, err := store.Register("Other", operator.RoleAudio)
		require.NoError(t, err)
		mcm.joined[other.ID] = "Other"

		body, _ := json.Marshal(commsMuteRequest{OperatorID: other.ID, Muted: true})
		req := httptest.NewRequest("PUT", "/api/comms/mute", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+director.Token)
		rr := httptest.NewRecorder()
		api.mux.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())
		require.True(t, mcm.muted[other.ID])
	})

	t.Run("unidentified caller is rejected when operators registered", func(t *testing.T) {
		mcm := newMockCommsManager()
		api, store := newAuthedCommsAPI(t, mcm)
		_, err := store.Register("Someone", operator.RoleAudio)
		require.NoError(t, err)

		body, _ := json.Marshal(commsJoinRequest{OperatorID: "whoever", Name: "Ghost"})
		req := httptest.NewRequest("POST", "/api/comms/join", bytes.NewReader(body))
		// No Authorization header.
		rr := httptest.NewRecorder()
		api.mux.ServeHTTP(rr, req)

		require.Equal(t, http.StatusForbidden, rr.Code)
	})
}

func TestHandleCommsNotEnabled(t *testing.T) {
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	api := NewAPI(sw) // no comms manager

	// All comms routes should not be registered → 404/405.
	for _, tc := range []struct {
		method, path string
	}{
		{"POST", "/api/comms/join"},
		{"POST", "/api/comms/leave"},
		{"PUT", "/api/comms/mute"},
		{"GET", "/api/comms/status"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rr := httptest.NewRecorder()
		api.mux.ServeHTTP(rr, req)
		require.NotEqual(t, http.StatusOK, rr.Code,
			"expected non-200 for %s %s when comms not enabled", tc.method, tc.path)
	}
}
