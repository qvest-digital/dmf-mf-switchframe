package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/switcher"
)

// --- mock SCTE35API ---

type mockSCTE35 struct {
	injectCalls   []mockInjectCall
	scheduleCalls []mockScheduleCall
	returnCalls   []uint32
	cancelCalls   []uint32
	holdCalls     []uint32
	extendCalls   []mockExtendCall
	activeIDs     []uint32
	state         scte35.InjectorState
	eventLog      []scte35.EventLogEntry

	injectErr   error
	scheduleErr error
	returnErr   error
	cancelErr   error
	holdErr     error
	extendErr   error
}

type mockInjectCall struct {
	msg *scte35.CueMessage
}

type mockScheduleCall struct {
	msg       *scte35.CueMessage
	preRollMs int64
}

type mockExtendCall struct {
	eventID       uint32
	newDurationMs int64
}

func (m *mockSCTE35) InjectCue(msg *scte35.CueMessage) (uint32, error) {
	m.injectCalls = append(m.injectCalls, mockInjectCall{msg: msg})
	if m.injectErr != nil {
		return 0, m.injectErr
	}
	eid := msg.EventID
	if eid == 0 {
		eid = 42
	}
	return eid, nil
}

func (m *mockSCTE35) ScheduleCue(msg *scte35.CueMessage, preRollMs int64) (uint32, error) {
	m.scheduleCalls = append(m.scheduleCalls, mockScheduleCall{msg: msg, preRollMs: preRollMs})
	if m.scheduleErr != nil {
		return 0, m.scheduleErr
	}
	eid := msg.EventID
	if eid == 0 {
		eid = 42
	}
	return eid, nil
}

func (m *mockSCTE35) ReturnToProgram(eventID uint32) error {
	m.returnCalls = append(m.returnCalls, eventID)
	return m.returnErr
}

func (m *mockSCTE35) CancelEvent(eventID uint32) error {
	m.cancelCalls = append(m.cancelCalls, eventID)
	return m.cancelErr
}

func (m *mockSCTE35) CancelSegmentationEvent(segEventID uint32, source string) error {
	m.cancelCalls = append(m.cancelCalls, segEventID)
	return m.cancelErr
}

func (m *mockSCTE35) HoldBreak(eventID uint32) error {
	m.holdCalls = append(m.holdCalls, eventID)
	return m.holdErr
}

func (m *mockSCTE35) ExtendBreak(eventID uint32, newDurationMs int64) error {
	m.extendCalls = append(m.extendCalls, mockExtendCall{eventID, newDurationMs})
	return m.extendErr
}

func (m *mockSCTE35) ActiveEventIDs() []uint32 {
	return m.activeIDs
}

func (m *mockSCTE35) State() scte35.InjectorState {
	return m.state
}

func (m *mockSCTE35) EventLog() []scte35.EventLogEntry {
	return m.eventLog
}

// --- mock SCTE35RulesAPI ---

type mockSCTE35Rules struct {
	rules         []scte35.Rule
	defaultAction scte35.RuleAction
	templates     []scte35.Rule

	createCalls   []scte35.Rule
	updateCalls   []mockUpdateCall
	deleteCalls   []string
	reorderCalls  [][]string
	setDefCalls   []scte35.RuleAction
	fromTmplCalls []string

	createErr   error
	updateErr   error
	deleteErr   error
	reorderErr  error
	setDefErr   error
	fromTmplErr error
}

type mockUpdateCall struct {
	id   string
	rule scte35.Rule
}

func (m *mockSCTE35Rules) List() []scte35.Rule {
	return m.rules
}

func (m *mockSCTE35Rules) Create(rule scte35.Rule) (scte35.Rule, error) {
	m.createCalls = append(m.createCalls, rule)
	if m.createErr != nil {
		return scte35.Rule{}, m.createErr
	}
	rule.ID = "abc12345"
	return rule, nil
}

func (m *mockSCTE35Rules) Update(id string, rule scte35.Rule) error {
	m.updateCalls = append(m.updateCalls, mockUpdateCall{id, rule})
	return m.updateErr
}

func (m *mockSCTE35Rules) Delete(id string) error {
	m.deleteCalls = append(m.deleteCalls, id)
	return m.deleteErr
}

func (m *mockSCTE35Rules) Reorder(ids []string) error {
	m.reorderCalls = append(m.reorderCalls, ids)
	return m.reorderErr
}

func (m *mockSCTE35Rules) DefaultAction() scte35.RuleAction {
	return m.defaultAction
}

func (m *mockSCTE35Rules) SetDefaultAction(action scte35.RuleAction) error {
	m.setDefCalls = append(m.setDefCalls, action)
	return m.setDefErr
}

func (m *mockSCTE35Rules) Templates() []scte35.Rule {
	return m.templates
}

func (m *mockSCTE35Rules) CreateFromTemplate(name string) (scte35.Rule, error) {
	m.fromTmplCalls = append(m.fromTmplCalls, name)
	if m.fromTmplErr != nil {
		return scte35.Rule{}, m.fromTmplErr
	}
	return scte35.Rule{ID: "tmpl0001", Name: name, Action: scte35.ActionPass}, nil
}

// --- helpers ---

func setupSCTE35TestAPI(t *testing.T) (*API, *mockSCTE35, *mockSCTE35Rules) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	ms := &mockSCTE35{
		state: scte35.InjectorState{
			Enabled:      true,
			HeartbeatOK:  true,
			ActiveEvents: make(map[uint32]scte35.ActiveEventState),
		},
		activeIDs: []uint32{},
	}
	mr := &mockSCTE35Rules{
		rules:         []scte35.Rule{},
		defaultAction: scte35.ActionPass,
		templates:     []scte35.Rule{},
	}
	api := NewAPI(sw, WithSCTE35(ms, mr))
	return api, ms, mr
}

// --- Cue injection tests ---

func TestHandleSCTE35Cue_SpliceInsert(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)

	dur := int64(30000)
	isOut := true
	autoReturn := true
	body := fmt.Sprintf(`{
		"commandType": "splice_insert",
		"isOut": %t,
		"durationMs": %d,
		"autoReturn": %t
	}`, isOut, dur, autoReturn)

	req := httptest.NewRequest("POST", "/api/scte35/cue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, ms.injectCalls, 1)
	msg := ms.injectCalls[0].msg
	require.Equal(t, uint8(scte35.CommandSpliceInsert), msg.CommandType)
	require.True(t, msg.IsOut)
	require.True(t, msg.AutoReturn)
	require.NotNil(t, msg.BreakDuration)
	require.Equal(t, 30*time.Second, *msg.BreakDuration)

	// Response should include the eventId.
	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Contains(t, resp, "eventId")
}

func TestHandleSCTE35Cue_TimeSignal(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)

	body := `{
		"commandType": "time_signal",
		"descriptors": [
			{
				"segmentationType": 52,
				"upidType": 9,
				"upid": "SIGNAL:test123"
			}
		]
	}`

	req := httptest.NewRequest("POST", "/api/scte35/cue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, ms.injectCalls, 1)
	msg := ms.injectCalls[0].msg
	require.Equal(t, uint8(scte35.CommandTimeSignal), msg.CommandType)
	require.Len(t, msg.Descriptors, 1)
	require.Equal(t, uint8(52), msg.Descriptors[0].SegmentationType)
	require.Equal(t, uint8(9), msg.Descriptors[0].UPIDType)
	require.Equal(t, []byte("SIGNAL:test123"), msg.Descriptors[0].UPID)
}

func TestHandleSCTE35Cue_InvalidJSON(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/cue", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Cue_InvalidCommandType(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	body := `{"commandType": "invalid_type"}`
	req := httptest.NewRequest("POST", "/api/scte35/cue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Cue_Scheduled(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)

	body := `{
		"commandType": "splice_insert",
		"isOut": true,
		"durationMs": 30000,
		"autoReturn": true,
		"preRollMs": 5000
	}`

	req := httptest.NewRequest("POST", "/api/scte35/cue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	// When preRollMs is present, ScheduleCue should be used.
	require.Len(t, ms.scheduleCalls, 1)
	require.Len(t, ms.injectCalls, 0)
	require.Equal(t, int64(5000), ms.scheduleCalls[0].preRollMs)
}

func TestHandleSCTE35Cue_WithEventID(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)

	eventID := uint32(99)
	body := fmt.Sprintf(`{
		"commandType": "splice_insert",
		"isOut": true,
		"durationMs": 30000,
		"eventId": %d
	}`, eventID)

	req := httptest.NewRequest("POST", "/api/scte35/cue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, ms.injectCalls, 1)
	require.Equal(t, uint32(99), ms.injectCalls[0].msg.EventID)
}

// --- Return tests ---

func TestHandleSCTE35Return(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/return", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, ms.returnCalls, 1)
	require.Equal(t, uint32(0), ms.returnCalls[0]) // 0 = most recent
}

func TestHandleSCTE35Return_ByEventID(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/return/42", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, ms.returnCalls, 1)
	require.Equal(t, uint32(42), ms.returnCalls[0])
}

func TestHandleSCTE35Return_InvalidEventID(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/return/notanumber", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Return_Error(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)
	ms.returnErr = fmt.Errorf("no active events")

	req := httptest.NewRequest("POST", "/api/scte35/return", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

// --- Cancel tests ---

func TestHandleSCTE35Cancel(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/cancel/42", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, ms.cancelCalls, 1)
	require.Equal(t, uint32(42), ms.cancelCalls[0])
}

func TestHandleSCTE35Cancel_InvalidEventID(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/cancel/bad", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Cancel_Error(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)
	ms.cancelErr = fmt.Errorf("event 42 not active")

	req := httptest.NewRequest("POST", "/api/scte35/cancel/42", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

// --- Hold tests ---

func TestHandleSCTE35Hold(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/hold/42", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, ms.holdCalls, 1)
	require.Equal(t, uint32(42), ms.holdCalls[0])
}

func TestHandleSCTE35Hold_InvalidEventID(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/hold/bad", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Hold_Error(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)
	ms.holdErr = fmt.Errorf("event 42 not active")

	req := httptest.NewRequest("POST", "/api/scte35/hold/42", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

// --- Extend tests ---

func TestHandleSCTE35Extend(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)

	body := `{"durationMs": 60000}`
	req := httptest.NewRequest("POST", "/api/scte35/extend/42", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, ms.extendCalls, 1)
	require.Equal(t, uint32(42), ms.extendCalls[0].eventID)
	require.Equal(t, int64(60000), ms.extendCalls[0].newDurationMs)
}

func TestHandleSCTE35Extend_InvalidEventID(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	body := `{"durationMs": 60000}`
	req := httptest.NewRequest("POST", "/api/scte35/extend/bad", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Extend_InvalidJSON(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/extend/42", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Extend_MissingDuration(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/scte35/extend/42", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Extend_Error(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)
	ms.extendErr = fmt.Errorf("event 42 not active")

	body := `{"durationMs": 60000}`
	req := httptest.NewRequest("POST", "/api/scte35/extend/42", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

// --- Status tests ---

func TestHandleSCTE35Status(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)
	ms.state = scte35.InjectorState{
		Enabled:     true,
		HeartbeatOK: true,
		ActiveEvents: map[uint32]scte35.ActiveEventState{
			42: {
				EventID:     42,
				CommandType: "splice_insert",
				IsOut:       true,
				ElapsedMs:   5000,
			},
		},
	}

	req := httptest.NewRequest("GET", "/api/scte35/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var state scte35.InjectorState
	err := json.NewDecoder(rec.Body).Decode(&state)
	require.NoError(t, err)
	require.True(t, state.Enabled)
	require.True(t, state.HeartbeatOK)
	require.Len(t, state.ActiveEvents, 1)
	require.Equal(t, uint32(42), state.ActiveEvents[42].EventID)
}

// --- Log tests ---

func TestHandleSCTE35Log(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)
	ms.eventLog = []scte35.EventLogEntry{
		{EventID: 1, CommandType: "splice_insert", Status: "injected"},
		{EventID: 1, CommandType: "splice_insert", Status: "returned"},
	}

	req := httptest.NewRequest("GET", "/api/scte35/log", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var entries []scte35.EventLogEntry
	err := json.NewDecoder(rec.Body).Decode(&entries)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "injected", entries[0].Status)
	require.Equal(t, "returned", entries[1].Status)
}

// --- Active events tests ---

func TestHandleSCTE35Active(t *testing.T) {
	api, ms, _ := setupSCTE35TestAPI(t)
	ms.activeIDs = []uint32{42, 99}

	req := httptest.NewRequest("GET", "/api/scte35/active", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var ids []uint32
	err := json.NewDecoder(rec.Body).Decode(&ids)
	require.NoError(t, err)
	require.Equal(t, []uint32{42, 99}, ids)
}

// --- Not enabled tests ---

func TestHandleSCTE35_NotEnabled(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw) // no SCTE-35 configured

	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/api/scte35/cue"},
		{"POST", "/api/scte35/return"},
		{"POST", "/api/scte35/return/42"},
		{"POST", "/api/scte35/cancel/42"},
		{"POST", "/api/scte35/hold/42"},
		{"POST", "/api/scte35/extend/42"},
		{"GET", "/api/scte35/status"},
		{"GET", "/api/scte35/log"},
		{"GET", "/api/scte35/active"},
		{"GET", "/api/scte35/rules"},
		{"POST", "/api/scte35/rules"},
		{"PUT", "/api/scte35/rules/abc"},
		{"DELETE", "/api/scte35/rules/abc"},
		{"PUT", "/api/scte35/rules/default"},
		{"POST", "/api/scte35/rules/reorder"},
		{"GET", "/api/scte35/rules/templates"},
		{"POST", "/api/scte35/rules/from-template"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Mux().ServeHTTP(rec, req)
			// When SCTE-35 is not configured, routes are not registered,
			// so we expect 405 Method Not Allowed or 404 Not Found.
			require.True(t, rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed,
				"expected 404 or 405 for %s %s, got %d: %s", ep.method, ep.path, rec.Code, rec.Body.String())
		})
	}
}

// --- Rules tests ---

func TestHandleSCTE35Rules_List(t *testing.T) {
	api, _, mr := setupSCTE35TestAPI(t)
	mr.rules = []scte35.Rule{
		{ID: "r1", Name: "Block short", Action: scte35.ActionDelete, Enabled: true},
		{ID: "r2", Name: "Pass long", Action: scte35.ActionPass, Enabled: false},
	}

	req := httptest.NewRequest("GET", "/api/scte35/rules", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var rules []scte35.Rule
	err := json.NewDecoder(rec.Body).Decode(&rules)
	require.NoError(t, err)
	require.Len(t, rules, 2)
	require.Equal(t, "Block short", rules[0].Name)
}

func TestHandleSCTE35Rules_Create(t *testing.T) {
	api, _, mr := setupSCTE35TestAPI(t)

	body := `{"name":"Test rule","action":"delete","enabled":true,"conditions":[{"field":"command_type","operator":"=","value":"5"}]}`
	req := httptest.NewRequest("POST", "/api/scte35/rules", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mr.createCalls, 1)
	require.Equal(t, "Test rule", mr.createCalls[0].Name)

	var created scte35.Rule
	err := json.NewDecoder(rec.Body).Decode(&created)
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
}

func TestHandleSCTE35Rules_Create_InvalidJSON(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/rules", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Rules_Update(t *testing.T) {
	api, _, mr := setupSCTE35TestAPI(t)

	body := `{"name":"Updated rule","action":"pass","enabled":true}`
	req := httptest.NewRequest("PUT", "/api/scte35/rules/r1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mr.updateCalls, 1)
	require.Equal(t, "r1", mr.updateCalls[0].id)
	require.Equal(t, "Updated rule", mr.updateCalls[0].rule.Name)
}

func TestHandleSCTE35Rules_Update_NotFound(t *testing.T) {
	api, _, mr := setupSCTE35TestAPI(t)
	mr.updateErr = scte35.ErrRuleNotFound

	body := `{"name":"Updated rule","action":"pass"}`
	req := httptest.NewRequest("PUT", "/api/scte35/rules/nonexistent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleSCTE35Rules_Delete(t *testing.T) {
	api, _, mr := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("DELETE", "/api/scte35/rules/r1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mr.deleteCalls, 1)
	require.Equal(t, "r1", mr.deleteCalls[0])
}

func TestHandleSCTE35Rules_Delete_NotFound(t *testing.T) {
	api, _, mr := setupSCTE35TestAPI(t)
	mr.deleteErr = scte35.ErrRuleNotFound

	req := httptest.NewRequest("DELETE", "/api/scte35/rules/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleSCTE35Rules_SetDefault(t *testing.T) {
	api, _, mr := setupSCTE35TestAPI(t)

	body := `{"action":"delete"}`
	req := httptest.NewRequest("PUT", "/api/scte35/rules/default", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mr.setDefCalls, 1)
	require.Equal(t, scte35.RuleAction("delete"), mr.setDefCalls[0])
}

func TestHandleSCTE35Rules_SetDefault_InvalidJSON(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("PUT", "/api/scte35/rules/default", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Rules_SetDefault_EmptyAction(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	body := `{"action":""}`
	req := httptest.NewRequest("PUT", "/api/scte35/rules/default", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Rules_Reorder(t *testing.T) {
	api, _, mr := setupSCTE35TestAPI(t)

	body := `{"ids":["r2","r1"]}`
	req := httptest.NewRequest("POST", "/api/scte35/rules/reorder", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mr.reorderCalls, 1)
	require.Equal(t, []string{"r2", "r1"}, mr.reorderCalls[0])
}

func TestHandleSCTE35Rules_Reorder_InvalidJSON(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/rules/reorder", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSCTE35Rules_Templates(t *testing.T) {
	api, _, mr := setupSCTE35TestAPI(t)
	mr.templates = []scte35.Rule{
		{Name: "Strip short avails", Action: scte35.ActionDelete},
		{Name: "Pass placement", Action: scte35.ActionPass},
	}

	req := httptest.NewRequest("GET", "/api/scte35/rules/templates", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var templates []scte35.Rule
	err := json.NewDecoder(rec.Body).Decode(&templates)
	require.NoError(t, err)
	require.Len(t, templates, 2)
}

func TestHandleSCTE35Rules_FromTemplate(t *testing.T) {
	api, _, mr := setupSCTE35TestAPI(t)

	body := `{"name":"Strip short avails"}`
	req := httptest.NewRequest("POST", "/api/scte35/rules/from-template", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mr.fromTmplCalls, 1)
	require.Equal(t, "Strip short avails", mr.fromTmplCalls[0])
}

func TestHandleSCTE35Rules_FromTemplate_NotFound(t *testing.T) {
	api, _, mr := setupSCTE35TestAPI(t)
	mr.fromTmplErr = scte35.ErrTemplateNotFound

	body := `{"name":"nonexistent"}`
	req := httptest.NewRequest("POST", "/api/scte35/rules/from-template", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleSCTE35Rules_FromTemplate_InvalidJSON(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	req := httptest.NewRequest("POST", "/api/scte35/rules/from-template", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- buildCueMessage unit tests ---

func TestBuildCueMessage_SpliceNull(t *testing.T) {
	req := scte35CueRequest{
		CommandType: "splice_null",
	}
	msg, err := buildCueMessage(req)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandSpliceNull), msg.CommandType)
	require.Equal(t, "api", msg.Source)
}

func TestBuildCueMessage_SpliceInsert_Source(t *testing.T) {
	isOut := true
	req := scte35CueRequest{
		CommandType: "splice_insert",
		IsOut:       &isOut,
	}
	msg, err := buildCueMessage(req)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandSpliceInsert), msg.CommandType)
	require.Equal(t, "api", msg.Source)
}

func TestBuildCueMessage_TimeSignal_Source(t *testing.T) {
	req := scte35CueRequest{
		CommandType: "time_signal",
		Descriptors: []scte35DescriptorRequest{
			{SegmentationType: 0x34, UPIDType: 0x09, UPID: "test"},
		},
	}
	msg, err := buildCueMessage(req)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandTimeSignal), msg.CommandType)
	require.Equal(t, "api", msg.Source)
}

func TestHandleSCTE35Rules_FromTemplate_EmptyName(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	body := `{"name":""}`
	req := httptest.NewRequest("POST", "/api/scte35/rules/from-template", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBuildCueMessage_SpliceInsert_RejectsDescriptors(t *testing.T) {
	req := scte35CueRequest{
		CommandType: "splice_insert",
		Descriptors: []scte35DescriptorRequest{
			{SegmentationType: 0x34},
		},
	}
	_, err := buildCueMessage(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "descriptors are only supported with time_signal")
}

func TestBuildCueMessage_TimeSignal_RequiresDescriptors(t *testing.T) {
	req := scte35CueRequest{
		CommandType: "time_signal",
	}
	_, err := buildCueMessage(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "time_signal requires at least one descriptor")
}

func TestBuildCueMessage_NegativeDescriptorDuration(t *testing.T) {
	negDur := int64(-1)
	req := scte35CueRequest{
		CommandType: "time_signal",
		Descriptors: []scte35DescriptorRequest{
			{
				SegmentationType: 0x34,
				DurationMs:       &negDur,
				UPIDType:         0x09,
				UPID:             "test",
			},
		},
	}
	_, err := buildCueMessage(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-negative")
}

func TestBuildCueMessage_SegEventID_Wired(t *testing.T) {
	segID := uint32(12345)
	req := scte35CueRequest{
		CommandType: "time_signal",
		Descriptors: []scte35DescriptorRequest{
			{
				SegmentationType: 0x34,
				SegEventID:       &segID,
				UPIDType:         0x09,
				UPID:             "test",
			},
		},
	}
	msg, err := buildCueMessage(req)
	require.NoError(t, err)
	require.Len(t, msg.Descriptors, 1)
	require.Equal(t, uint32(12345), msg.Descriptors[0].SegEventID)
}

func TestBuildCueMessage_TimingIsImmediate(t *testing.T) {
	// Verify that buildCueMessage always sets Timing to "immediate" so
	// API-originated time_signals don't silently get PTS assigned.
	isOut := true
	req := scte35CueRequest{
		CommandType: "splice_insert",
		IsOut:       &isOut,
	}
	msg, err := buildCueMessage(req)
	require.NoError(t, err)
	require.Equal(t, "immediate", msg.Timing)

	// Also verify for time_signal.
	req2 := scte35CueRequest{
		CommandType: "time_signal",
		Descriptors: []scte35DescriptorRequest{
			{SegmentationType: 0x34, UPIDType: 0x09, UPID: "test"},
		},
	}
	msg2, err := buildCueMessage(req2)
	require.NoError(t, err)
	require.Equal(t, "immediate", msg2.Timing)
}

func TestBuildCueMessage_OptionalFields(t *testing.T) {
	// Verify uniqueProgramId, availNum, availsExpected are wired through.
	isOut := true
	dur := int64(30000)
	upid := uint16(500)
	anum := uint8(1)
	aexp := uint8(3)
	req := scte35CueRequest{
		CommandType:     "splice_insert",
		IsOut:           &isOut,
		DurationMs:      &dur,
		UniqueProgramID: &upid,
		AvailNum:        &anum,
		AvailsExpected:  &aexp,
	}
	msg, err := buildCueMessage(req)
	require.NoError(t, err)
	require.Equal(t, uint16(500), msg.UniqueProgramID)
	require.Equal(t, uint8(1), msg.AvailNum)
	require.Equal(t, uint8(3), msg.AvailsExpected)
}
