package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
)

// audioLevelRequest is the JSON body for the audio level endpoint.
type audioLevelRequest struct {
	Source string  `json:"source"`
	Level  float64 `json:"level"`
}

// audioMuteRequest is the JSON body for the audio mute endpoint.
type audioMuteRequest struct {
	Source string `json:"source"`
	Muted  bool   `json:"muted"`
}

// audioAFVRequest is the JSON body for the audio AFV endpoint.
type audioAFVRequest struct {
	Source string `json:"source"`
	AFV    bool   `json:"afv"`
}

// audioMasterRequest is the JSON body for the audio master level endpoint.
type audioMasterRequest struct {
	Level float64 `json:"level"`
}

// audioTrimRequest is the JSON body for the audio trim endpoint.
type audioTrimRequest struct {
	Source string  `json:"source"`
	Trim   float64 `json:"trim"`
}

// eqRequest is the JSON body for the EQ endpoint.
type eqRequest struct {
	Band      int     `json:"band"`
	Frequency float64 `json:"frequency"`
	Gain      float64 `json:"gain"`
	Q         float64 `json:"q"`
	Enabled   bool    `json:"enabled"`
}

// compressorRequest is the JSON body for the compressor endpoint.
type compressorRequest struct {
	Threshold  float64 `json:"threshold"`
	Ratio      float64 `json:"ratio"`
	Attack     float64 `json:"attack"`
	Release    float64 `json:"release"`
	MakeupGain float64 `json:"makeupGain"`
}

// compressorResponse is the JSON response for the compressor GET endpoint.
type compressorResponse struct {
	Threshold     float64 `json:"threshold"`
	Ratio         float64 `json:"ratio"`
	Attack        float64 `json:"attack"`
	Release       float64 `json:"release"`
	MakeupGain    float64 `json:"makeupGain"`
	GainReduction float64 `json:"gainReduction"`
}

// audioBalanceRequest is the JSON body for the audio balance endpoint.
type audioBalanceRequest struct {
	Source  string  `json:"source"`
	Balance float64 `json:"balance"`
}

// audioPhaseRequest is the JSON body for the audio phase invert endpoint.
type audioPhaseRequest struct {
	Source string `json:"source"`
	Invert bool   `json:"invert"`
}

// hpfRequest is the JSON body for the HPF endpoint.
type hpfRequest struct {
	Frequency float64 `json:"frequency"`
	Enabled   bool    `json:"enabled"`
}

// hpfResponse is the JSON response for the HPF GET endpoint.
type hpfResponse struct {
	Frequency float64 `json:"frequency"`
	Enabled   bool    `json:"enabled"`
}

// gateRequest is the JSON body for the gate endpoint.
type gateRequest struct {
	Threshold float64 `json:"threshold"`
	Range     float64 `json:"range"`
	Attack    float64 `json:"attack"`
	Release   float64 `json:"release"`
	Hold      float64 `json:"hold"`
	Enabled   bool    `json:"enabled"`
}

// gateResponse is the JSON response for the gate GET endpoint.
type gateResponse struct {
	Threshold     float64 `json:"threshold"`
	Range         float64 `json:"range"`
	Attack        float64 `json:"attack"`
	Release       float64 `json:"release"`
	Hold          float64 `json:"hold"`
	Enabled       bool    `json:"enabled"`
	GainReduction float64 `json:"gainReduction"`
}

// audioDelayRequest is the JSON body for the audio delay endpoint.
type audioDelayRequest struct {
	DelayMs int `json:"delayMs"`
}

// limiterRequest is the JSON body for the limiter PUT endpoint.
type limiterRequest struct {
	Threshold float64 `json:"threshold"`
	Ceiling   float64 `json:"ceiling"`
}

// limiterResponse is the JSON response for the limiter endpoints.
type limiterResponse struct {
	Threshold     float64 `json:"threshold"`
	Ceiling       float64 `json:"ceiling"`
	GainReduction float64 `json:"gainReduction"`
}

// registerAudioRoutes registers audio-related API routes on the given mux.
func (a *API) registerAudioRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/audio/trim", timedHandler(a.cmdQueue, "audio_trim", a.handleAudioTrimInner))
	mux.HandleFunc("POST /api/audio/level", timedHandler(a.cmdQueue, "audio_level", a.handleAudioLevelInner))
	mux.HandleFunc("POST /api/audio/mute", timedHandler(a.cmdQueue, "audio_mute", a.handleAudioMuteInner))
	mux.HandleFunc("POST /api/audio/afv", timedHandler(a.cmdQueue, "audio_afv", a.handleAudioAFVInner))
	mux.HandleFunc("POST /api/audio/phase", timedHandler(a.cmdQueue, "audio_phase", a.handleAudioPhaseInner))
	mux.HandleFunc("POST /api/audio/balance", timedHandler(a.cmdQueue, "audio_balance", a.handleAudioBalanceInner))
	mux.HandleFunc("POST /api/audio/master", timedHandler(a.cmdQueue, "audio_master", a.handleAudioMasterInner))
	mux.HandleFunc("PUT /api/audio/{source}/eq", timedHandlerWithPath(a.cmdQueue, "audio_eq", []string{"source"}, a.handleSetEQInner))
	mux.HandleFunc("GET /api/audio/{source}/eq", a.handleGetEQ)
	mux.HandleFunc("PUT /api/audio/{source}/compressor", timedHandlerWithPath(a.cmdQueue, "audio_compressor", []string{"source"}, a.handleSetCompressorInner))
	mux.HandleFunc("GET /api/audio/{source}/compressor", a.handleGetCompressor)
	mux.HandleFunc("PUT /api/audio/{source}/hpf", timedHandlerWithPath(a.cmdQueue, "audio_hpf", []string{"source"}, a.handleSetHPFInner))
	mux.HandleFunc("GET /api/audio/{source}/hpf", a.handleGetHPF)
	mux.HandleFunc("PUT /api/audio/{source}/gate", timedHandlerWithPath(a.cmdQueue, "audio_gate", []string{"source"}, a.handleSetGateInner))
	mux.HandleFunc("GET /api/audio/{source}/gate", a.handleGetGate)
	mux.HandleFunc("PUT /api/audio/{source}/audio-delay", timedHandlerWithPath(a.cmdQueue, "audio_delay", []string{"source"}, a.handleSetAudioDelayInner))
	mux.HandleFunc("PUT /api/audio/limiter", timedHandler(a.cmdQueue, "audio_limiter", a.handleSetLimiterInner))
	mux.HandleFunc("GET /api/audio/limiter", a.handleGetLimiter)
}

// handleAudioTrimInner sets the input trim for a source channel.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleAudioTrimInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioTrimRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := validateRange("trim", req.Trim, MinTrimDB, MaxTrimDB); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.mixer.SetTrim(req.Source, req.Trim); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleAudioLevelInner sets the audio level for a source channel.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleAudioLevelInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioLevelRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := validateRange("level", req.Level, MinLevelDB, MaxLevelDB); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.mixer.SetLevel(req.Source, req.Level); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleAudioMuteInner sets the mute state for a source channel.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleAudioMuteInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioMuteRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := a.mixer.SetMuted(req.Source, req.Muted); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleAudioAFVInner sets the audio-follow-video state for a source channel.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleAudioAFVInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioAFVRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := a.mixer.SetAFV(req.Source, req.AFV); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleAudioPhaseInner sets the phase invert state for a source channel.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleAudioPhaseInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioPhaseRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := a.mixer.SetPhaseInvert(req.Source, req.Invert); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleAudioBalanceInner sets the stereo balance for a source channel.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleAudioBalanceInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioBalanceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := validateRange("balance", req.Balance, -1.0, 1.0); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.mixer.SetBalance(req.Source, req.Balance); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleAudioMasterInner sets the master output level.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleAudioMasterInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioMasterRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validateRange("level", req.Level, MinLevelDB, MaxLevelDB); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.mixer.SetMasterLevel(req.Level); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleSetEQInner sets a single EQ band for a source channel.
// body is the pre-read request body provided by timedHandlerWithPath.
func (a *API) handleSetEQInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	var req eqRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validateRange("frequency", req.Frequency, MinEQFreq, MaxEQFreq); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRange("gain", req.Gain, MinEQGainDB, MaxEQGainDB); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRange("q", req.Q, MinEQQ, MaxEQQ); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.mixer.SetEQ(source, req.Band, req.Frequency, req.Gain, req.Q, req.Enabled); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleGetEQ returns the current EQ settings for a source channel.
func (a *API) handleGetEQ(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	bands, err := a.mixer.GetEQ(source)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(bands)
}

// handleSetCompressorInner sets the compressor parameters for a source channel.
// body is the pre-read request body provided by timedHandlerWithPath.
func (a *API) handleSetCompressorInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	var req compressorRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validateRange("threshold", req.Threshold, -40.0, 0.0); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRange("ratio", req.Ratio, 1.0, 20.0); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRange("attack", req.Attack, 0.1, 100.0); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRange("release", req.Release, 10.0, 1000.0); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRange("makeupGain", req.MakeupGain, 0.0, 24.0); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.mixer.SetCompressor(source, req.Threshold, req.Ratio, req.Attack, req.Release, req.MakeupGain); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleGetCompressor returns the current compressor settings and gain reduction for a source channel.
func (a *API) handleGetCompressor(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	state, err := a.mixer.GetCompressor(source)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(compressorResponse{
		Threshold:     state.Threshold,
		Ratio:         state.Ratio,
		Attack:        state.Attack,
		Release:       state.Release,
		MakeupGain:    state.MakeupGain,
		GainReduction: state.GainReduction,
	})
}

// handleSetHPFInner sets the high-pass filter parameters for a source channel.
// body is the pre-read request body provided by timedHandlerWithPath.
func (a *API) handleSetHPFInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	var req hpfRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validateRange("frequency", req.Frequency, MinHPFFreq, MaxHPFFreq); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.mixer.SetHPF(source, req.Frequency, req.Enabled); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleGetHPF returns the current HPF settings for a source channel.
func (a *API) handleGetHPF(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	freq, enabled := a.mixer.GetHPF(source)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(hpfResponse{
		Frequency: freq,
		Enabled:   enabled,
	})
}

// handleSetGateInner sets the noise gate parameters for a source channel.
// body is the pre-read request body provided by timedHandlerWithPath.
func (a *API) handleSetGateInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	var req gateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validateRange("threshold", req.Threshold, MinGateThreshold, MaxGateThreshold); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRange("range", req.Range, MinGateRange, MaxGateRange); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRange("attack", req.Attack, MinGateAttack, MaxGateAttack); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRange("release", req.Release, MinGateRelease, MaxGateRelease); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRange("hold", req.Hold, MinGateHold, MaxGateHold); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.mixer.SetGate(source, req.Threshold, req.Range, req.Attack, req.Release, req.Hold, req.Enabled); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleGetGate returns the current noise gate settings and gain reduction for a source channel.
func (a *API) handleGetGate(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	state, err := a.mixer.GetGate(source)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gateResponse{
		Threshold:     state.Threshold,
		Range:         state.Range,
		Attack:        state.Attack,
		Release:       state.Release,
		Hold:          state.Hold,
		Enabled:       state.Enabled,
		GainReduction: state.GainReduction,
	})
}

// handleSetAudioDelayInner sets the audio delay in milliseconds for a source channel.
// body is the pre-read request body provided by timedHandlerWithPath.
func (a *API) handleSetAudioDelayInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	var req audioDelayRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validateIntRange("delayMs", req.DelayMs, MinDelayMs, MaxDelayMs); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.mixer.SetAudioDelay(source, req.DelayMs); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleSetLimiterInner sets the master limiter threshold and ceiling.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleSetLimiterInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req limiterRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.mixer.SetLimiterThreshold(req.Threshold); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.mixer.SetLimiterCeiling(req.Ceiling); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleGetLimiter returns the current master limiter settings and gain reduction.
func (a *API) handleGetLimiter(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	threshold, ceiling, gr := a.mixer.LimiterSettings()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(limiterResponse{
		Threshold:     threshold,
		Ceiling:       ceiling,
		GainReduction: gr,
	})
}
