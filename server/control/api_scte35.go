package control

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/scte35"
)

// scte35CueRequest is the JSON body for POST /api/scte35/cue.
type scte35CueRequest struct {
	CommandType string                    `json:"commandType"` // "splice_insert" or "time_signal"
	IsOut       *bool                     `json:"isOut,omitempty"`
	DurationMs  *int64                    `json:"durationMs,omitempty"`
	AutoReturn  *bool                     `json:"autoReturn,omitempty"`
	PreRollMs   *int64                    `json:"preRollMs,omitempty"`
	EventID     *uint32                   `json:"eventId,omitempty"`
	Descriptors []scte35DescriptorRequest `json:"descriptors,omitempty"`
}

// scte35DescriptorRequest is the JSON representation of a segmentation descriptor.
type scte35DescriptorRequest struct {
	SegmentationType uint8  `json:"segmentationType"`
	DurationMs       *int64 `json:"durationMs,omitempty"`
	UPIDType         uint8  `json:"upidType"`
	UPID             string `json:"upid"`
}

// scte35ExtendRequest is the JSON body for POST /api/scte35/extend/{eventId}.
type scte35ExtendRequest struct {
	DurationMs *int64 `json:"durationMs"`
}

// scte35DefaultRequest is the JSON body for PUT /api/scte35/rules/default.
type scte35DefaultRequest struct {
	Action scte35.RuleAction `json:"action"`
}

// scte35ReorderRequest is the JSON body for POST /api/scte35/rules/reorder.
type scte35ReorderRequest struct {
	IDs []string `json:"ids"`
}

// scte35TemplateRequest is the JSON body for POST /api/scte35/rules/from-template.
type scte35TemplateRequest struct {
	Name string `json:"name"`
}

// --- Cue injection handlers ---

// handleSCTE35Cue handles POST /api/scte35/cue.
// Parses the CueRequest JSON, builds a CueMessage, and calls InjectCue or ScheduleCue.
func (a *API) handleSCTE35Cue(w http.ResponseWriter, r *http.Request) {
	if a.scte35 == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	var req scte35CueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	msg, err := buildCueMessage(req)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	var eventID uint32
	if req.PreRollMs != nil && *req.PreRollMs > 0 {
		eventID, err = a.scte35.ScheduleCue(msg, *req.PreRollMs)
	} else {
		eventID, err = a.scte35.InjectCue(msg)
	}
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"eventId": eventID,
		"state":   a.enrichedState(),
	})
}

// --- Return handlers ---

// handleSCTE35Return handles POST /api/scte35/return.
// Returns the most recent active event to program.
func (a *API) handleSCTE35Return(w http.ResponseWriter, r *http.Request) {
	if a.scte35 == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	if err := a.scte35.ReturnToProgram(0); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleSCTE35ReturnEvent handles POST /api/scte35/return/{eventId}.
// Returns a specific active event to program by event ID.
func (a *API) handleSCTE35ReturnEvent(w http.ResponseWriter, r *http.Request) {
	if a.scte35 == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	eventID, err := parseEventID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := a.scte35.ReturnToProgram(eventID); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// --- Cancel handler ---

// handleSCTE35Cancel handles POST /api/scte35/cancel/{eventId}.
func (a *API) handleSCTE35Cancel(w http.ResponseWriter, r *http.Request) {
	if a.scte35 == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	eventID, err := parseEventID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := a.scte35.CancelEvent(eventID); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// --- Hold handler ---

// handleSCTE35Hold handles POST /api/scte35/hold/{eventId}.
func (a *API) handleSCTE35Hold(w http.ResponseWriter, r *http.Request) {
	if a.scte35 == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	eventID, err := parseEventID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := a.scte35.HoldBreak(eventID); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// --- Extend handler ---

// handleSCTE35Extend handles POST /api/scte35/extend/{eventId}.
func (a *API) handleSCTE35Extend(w http.ResponseWriter, r *http.Request) {
	if a.scte35 == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	eventID, err := parseEventID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	var req scte35ExtendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.DurationMs == nil || *req.DurationMs <= 0 {
		httperr.Write(w, http.StatusBadRequest, "durationMs required and must be positive")
		return
	}

	if err := a.scte35.ExtendBreak(eventID, *req.DurationMs); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// --- Read-only handlers ---

// handleSCTE35Status handles GET /api/scte35/status.
func (a *API) handleSCTE35Status(w http.ResponseWriter, _ *http.Request) {
	if a.scte35 == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.scte35.State())
}

// handleSCTE35Log handles GET /api/scte35/log.
func (a *API) handleSCTE35Log(w http.ResponseWriter, _ *http.Request) {
	if a.scte35 == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}

	entries := a.scte35.EventLog()
	if entries == nil {
		entries = []scte35.EventLogEntry{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entries)
}

// handleSCTE35Active handles GET /api/scte35/active.
func (a *API) handleSCTE35Active(w http.ResponseWriter, _ *http.Request) {
	if a.scte35 == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}

	ids := a.scte35.ActiveEventIDs()
	if ids == nil {
		ids = []uint32{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ids)
}

// --- Rules handlers ---

// handleSCTE35ListRules handles GET /api/scte35/rules.
func (a *API) handleSCTE35ListRules(w http.ResponseWriter, _ *http.Request) {
	if a.scte35Rules == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}

	rules := a.scte35Rules.List()
	if rules == nil {
		rules = []scte35.Rule{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rules)
}

// handleSCTE35CreateRule handles POST /api/scte35/rules.
func (a *API) handleSCTE35CreateRule(w http.ResponseWriter, r *http.Request) {
	if a.scte35Rules == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	var rule scte35.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	created, err := a.scte35Rules.Create(rule)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(created)
}

// handleSCTE35UpdateRule handles PUT /api/scte35/rules/{id}.
func (a *API) handleSCTE35UpdateRule(w http.ResponseWriter, r *http.Request) {
	if a.scte35Rules == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	id := r.PathValue("id")

	var rule scte35.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := a.scte35Rules.Update(id, rule); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleSCTE35DeleteRule handles DELETE /api/scte35/rules/{id}.
func (a *API) handleSCTE35DeleteRule(w http.ResponseWriter, r *http.Request) {
	if a.scte35Rules == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	id := r.PathValue("id")

	if err := a.scte35Rules.Delete(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleSCTE35SetDefault handles PUT /api/scte35/rules/default.
func (a *API) handleSCTE35SetDefault(w http.ResponseWriter, r *http.Request) {
	if a.scte35Rules == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	var req scte35DefaultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Action == "" {
		httperr.Write(w, http.StatusBadRequest, "action required")
		return
	}

	if err := a.scte35Rules.SetDefaultAction(req.Action); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleSCTE35ReorderRules handles POST /api/scte35/rules/reorder.
func (a *API) handleSCTE35ReorderRules(w http.ResponseWriter, r *http.Request) {
	if a.scte35Rules == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	var req scte35ReorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := a.scte35Rules.Reorder(req.IDs); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleSCTE35Templates handles GET /api/scte35/rules/templates.
func (a *API) handleSCTE35Templates(w http.ResponseWriter, _ *http.Request) {
	if a.scte35Rules == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}

	templates := a.scte35Rules.Templates()
	if templates == nil {
		templates = []scte35.Rule{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(templates)
}

// handleSCTE35FromTemplate handles POST /api/scte35/rules/from-template.
func (a *API) handleSCTE35FromTemplate(w http.ResponseWriter, r *http.Request) {
	if a.scte35Rules == nil {
		httperr.Write(w, http.StatusNotImplemented, "scte35 not enabled")
		return
	}
	a.setLastOperator(r)

	var req scte35TemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Name == "" {
		httperr.Write(w, http.StatusBadRequest, "name required")
		return
	}

	rule, err := a.scte35Rules.CreateFromTemplate(req.Name)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rule)
}

// --- Helpers ---

// parseEventID extracts and validates the {eventId} path parameter.
func parseEventID(r *http.Request) (uint32, error) {
	s := r.PathValue("eventId")
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

// buildCueMessage converts a scte35CueRequest into a CueMessage.
func buildCueMessage(req scte35CueRequest) (*scte35.CueMessage, error) {
	msg := &scte35.CueMessage{}

	switch req.CommandType {
	case "splice_insert":
		msg.CommandType = scte35.CommandSpliceInsert
	case "time_signal":
		msg.CommandType = scte35.CommandTimeSignal
	default:
		return nil, &invalidCommandTypeError{req.CommandType}
	}

	if req.IsOut != nil {
		msg.IsOut = *req.IsOut
	}
	if req.AutoReturn != nil {
		msg.AutoReturn = *req.AutoReturn
	}
	if req.EventID != nil {
		msg.EventID = *req.EventID
	}
	if req.DurationMs != nil {
		dur := time.Duration(*req.DurationMs) * time.Millisecond
		msg.BreakDuration = &dur
	}

	// Convert descriptor requests.
	for _, d := range req.Descriptors {
		desc := scte35.SegmentationDescriptor{
			SegmentationType: d.SegmentationType,
			UPIDType:         d.UPIDType,
			UPID:             []byte(d.UPID),
		}
		if d.DurationMs != nil {
			// Convert milliseconds to 90 kHz ticks.
			ticks := uint64(*d.DurationMs) * 90
			desc.DurationTicks = &ticks
		}
		msg.Descriptors = append(msg.Descriptors, desc)
	}

	return msg, nil
}

// invalidCommandTypeError is returned when the command type is not recognized.
type invalidCommandTypeError struct {
	commandType string
}

func (e *invalidCommandTypeError) Error() string {
	return "invalid commandType: " + e.commandType
}
