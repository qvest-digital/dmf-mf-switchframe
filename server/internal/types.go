// Package internal provides shared types for the Switchframe server.
package internal

import (
	"encoding/json"

	"github.com/zsiec/switchframe/server/colorgrade"
)

// SourceKeyConfig describes the upstream key configuration for a source,
// included in SourceInfo so the browser knows the current key state.
type SourceKeyConfig struct {
	Type           string  `json:"type"` // "chroma", "luma", or ""
	Enabled        bool    `json:"enabled"`
	KeyColorY      uint8   `json:"keyColorY,omitempty"`
	KeyColorCb     uint8   `json:"keyColorCb,omitempty"`
	KeyColorCr     uint8   `json:"keyColorCr,omitempty"`
	Similarity     float32 `json:"similarity,omitempty"`
	Smoothness     float32 `json:"smoothness,omitempty"`
	SpillSuppress  float32 `json:"spillSuppress,omitempty"`
	SpillReplaceCb uint8   `json:"spillReplaceCb,omitempty"`
	SpillReplaceCr uint8   `json:"spillReplaceCr,omitempty"`
	LowClip        float32 `json:"lowClip,omitempty"`
	HighClip       float32 `json:"highClip,omitempty"`
	Softness       float32 `json:"softness,omitempty"`
	FillSource     string  `json:"fillSource,omitempty"`
}

// SourceInfo describes a connected video source and its current state.
type SourceInfo struct {
	Key         string           `json:"key"`
	Label       string           `json:"label,omitempty"`
	Type        string           `json:"type"` // "demo", "mxl", "srt", "replay", "clip"
	Status      string           `json:"status"`
	Position    int              `json:"position"`
	DelayMs     int              `json:"delayMs,omitempty"`
	KeyConfig   *SourceKeyConfig `json:"keyConfig,omitempty"`
	SRTInfo     *SRTSourceInfo   `json:"srt,omitempty"` // non-nil for SRT sources
	Corrector   *CorrectorState  `json:"corrector,omitempty"`
	IsVirtual   bool             `json:"isVirtual,omitempty"`
	HasCaptions bool             `json:"hasCaptions,omitempty"`
}

// SRTSourceInfo holds live SRT connection metadata for ControlRoomState broadcast.
// This is separate from srt.SRTSourceInfo to avoid circular imports.
type SRTSourceInfo struct {
	Mode                 string  `json:"mode"`
	StreamID             string  `json:"streamID"`
	RemoteAddr           string  `json:"remoteAddr,omitempty"`
	LatencyMs            int     `json:"latencyMs"`
	NegotiatedLatencyMs  int     `json:"negotiatedLatencyMs"`
	RTTMs                float64 `json:"rttMs"`
	RTTVarMs             float64 `json:"rttVarMs"`
	LossRate             float64 `json:"lossRate"`
	BitrateKbps          float64 `json:"bitrateKbps"`
	RecvBufMs            float64 `json:"recvBufMs"`
	RecvBufPackets       int     `json:"recvBufPackets"`
	FlightSize           int     `json:"flightSize"`
	Connected            bool    `json:"connected"`
	UptimeMs             int64   `json:"uptimeMs"`
	PacketsReceived      int64   `json:"packetsReceived"`
	PacketsLost          int64   `json:"packetsLost"`
	PacketsDropped       int64   `json:"packetsDropped"`
	PacketsRetransmitted int64   `json:"packetsRetransmitted"`
	PacketsBelated       int64   `json:"packetsBelated"`
	ReconnectCount       int     `json:"reconnectCount,omitempty"`
}

// EQBand describes the settings for a single EQ band.
type EQBand struct {
	Frequency float64 `json:"frequency"`
	Gain      float64 `json:"gain"`
	Q         float64 `json:"q"`
	Enabled   bool    `json:"enabled"`
}

// CompressorSettings describes the settings for a channel compressor.
type CompressorSettings struct {
	Threshold  float64 `json:"threshold"`
	Ratio      float64 `json:"ratio"`
	Attack     float64 `json:"attack"`
	Release    float64 `json:"release"`
	MakeupGain float64 `json:"makeupGain"`
}

// LimiterSettings describes the master bus limiter state.
type LimiterSettings struct {
	Threshold     float64 `json:"threshold"`
	Ceiling       float64 `json:"ceiling"`
	GainReduction float64 `json:"gainReduction"`
}

// GateSettings describes the settings for a channel noise gate.
type GateSettings struct {
	Threshold float64 `json:"threshold"`
	Range     float64 `json:"range"`
	Attack    float64 `json:"attack"`
	Release   float64 `json:"release"`
	Hold      float64 `json:"hold"`
}

// AudioChannel describes the audio mixer state for a single source.
type AudioChannel struct {
	Level         float64            `json:"level"` // dB (-inf to +12)
	Trim          float64            `json:"trim"`  // dB (-20 to +20), input gain
	Muted         bool               `json:"muted"`
	AFV           bool               `json:"afv"`                   // audio-follows-video
	PhaseInvert   bool               `json:"phaseInvert,omitempty"` // 180-degree phase flip
	Balance       float64            `json:"balance"`               // -1.0 (L) to +1.0 (R), 0 = center
	HPFFrequency  float64            `json:"hpfFrequency,omitempty"`
	HPFEnabled    bool               `json:"hpfEnabled,omitempty"`
	PeakL         float64            `json:"peakL"` // dBFS, updated per frame
	PeakR         float64            `json:"peakR"` // dBFS
	EQ            [3]EQBand          `json:"eq"`
	Compressor    CompressorSettings `json:"compressor"`
	GainReduction float64            `json:"gainReduction"` // compressor GR in dB
	Gate          GateSettings       `json:"gate"`
	GateEnabled   bool               `json:"gateEnabled,omitempty"`
	GateReduction float64            `json:"gateReduction,omitempty"` // gate GR in dB
	AudioDelayMs  int                `json:"audioDelayMs,omitempty"`  // lip-sync delay (0-500ms)
}

// RecordingStatus is the JSON-serializable status for recording output,
// included in ControlRoomState for the browser.
type RecordingStatus struct {
	Active         bool    `json:"active"`
	Filename       string  `json:"filename,omitempty"`
	BytesWritten   int64   `json:"bytesWritten,omitempty"`
	DurationSecs   float64 `json:"durationSecs,omitempty"`
	DroppedPackets int64   `json:"droppedPackets,omitempty"`
	Error          string  `json:"error,omitempty"`
}

// SRTOutputStatus is the JSON-serializable status for SRT output,
// included in ControlRoomState for the browser.
type SRTOutputStatus struct {
	Active         bool   `json:"active"`
	Mode           string `json:"mode,omitempty"`
	Address        string `json:"address,omitempty"`
	Port           int    `json:"port,omitempty"`
	State          string `json:"state,omitempty"`
	Connections    int    `json:"connections,omitempty"`
	BytesWritten   int64  `json:"bytesWritten,omitempty"`
	DroppedPackets int64  `json:"droppedPackets,omitempty"`
	OverflowCount  int64  `json:"overflowCount,omitempty"`
	Error          string `json:"error,omitempty"`
}

// PresetInfo is a summary of a saved preset, included in ControlRoomState
// so the browser knows which presets are available for recall.
type PresetInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GraphicsState is the JSON-serializable state for the downstream
// keyer (DSK) graphics overlay layers, included in ControlRoomState.
type GraphicsState struct {
	Layers        []GraphicsLayerState `json:"layers,omitempty"`
	ProgramWidth  int                  `json:"programWidth,omitempty"`
	ProgramHeight int                  `json:"programHeight,omitempty"`
}

// GraphicsLayerState is the JSON-serializable state for a single graphics layer.
type GraphicsLayerState struct {
	ID                 int            `json:"id"`
	Template           string         `json:"template,omitempty"`
	Active             bool           `json:"active"`
	FadePosition       float64        `json:"fadePosition,omitempty"`
	AnimationMode      string         `json:"animationMode,omitempty"`
	AnimationHz        float64        `json:"animationHz,omitempty"`
	ZOrder             int            `json:"zOrder"`
	X                  int            `json:"x"`
	Y                  int            `json:"y"`
	Width              int            `json:"width"`
	Height             int            `json:"height"`
	ImageName          string         `json:"imageName,omitempty"`
	ImageWidth         int            `json:"imageWidth,omitempty"`
	ImageHeight        int            `json:"imageHeight,omitempty"`
	HTML5URL           string         `json:"html5Url,omitempty"`
	HTML5Format        string         `json:"html5Format,omitempty"`
	HTML5Status        string         `json:"html5Status,omitempty"`
	HTML5DataSchema    map[string]any `json:"html5DataSchema,omitempty"`
	SequenceLoaded     bool           `json:"sequenceLoaded,omitempty"`
	SequencePhase      string         `json:"sequencePhase,omitempty"`
	SequenceProcessing bool           `json:"sequenceProcessing,omitempty"`
	SequenceProgress   float64        `json:"sequenceProgress,omitempty"`
	SequenceSpeed      float64        `json:"sequenceSpeed,omitempty"`
}

// ReplayState is the JSON-serializable state for the instant replay system,
// included in ControlRoomState for the browser.
type ReplayState struct {
	State          string             `json:"state"`
	Source         string             `json:"source,omitempty"`
	Speed          float64            `json:"speed,omitempty"`
	Loop           bool               `json:"loop,omitempty"`
	Position       float64            `json:"position,omitempty"`
	MarkIn         *int64             `json:"markIn,omitempty"`  // Unix ms
	MarkOut        *int64             `json:"markOut,omitempty"` // Unix ms
	MarkSource     string             `json:"markSource,omitempty"`
	Buffers        []ReplayBufferInfo `json:"buffers,omitempty"`
	ShutterAngle   float64            `json:"shutterAngle,omitempty"` // 0-360 degrees
	GPUAccelerated bool               `json:"gpuAccelerated,omitempty"`
}

// ReplayBufferInfo describes the buffer state for a single replay source.
type ReplayBufferInfo struct {
	Source       string  `json:"source"`
	FrameCount   int     `json:"frameCount"`
	GOPCount     int     `json:"gopCount"`
	DurationSecs float64 `json:"durationSecs"`
	BytesUsed    int64   `json:"bytesUsed"`
}

// DestinationInfo describes an output destination for ControlRoomState broadcast.
type DestinationInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	Type           string `json:"type"`
	Address        string `json:"address,omitempty"`
	Port           int    `json:"port,omitempty"`
	URL            string `json:"url,omitempty"` // RTMP destinations
	State          string `json:"state"`
	BytesWritten   int64  `json:"bytesWritten,omitempty"`
	DroppedPackets int64  `json:"droppedPackets,omitempty"`
	Connections    int    `json:"connections,omitempty"`
	Error          string `json:"error,omitempty"`
}

// OperatorInfo describes a registered operator for ControlRoomState broadcast.
type OperatorInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Connected bool   `json:"connected"`
}

// LockInfo describes an active subsystem lock for ControlRoomState broadcast.
type LockInfo struct {
	HolderID   string `json:"holderId"`
	HolderName string `json:"holderName"`
	AcquiredAt int64  `json:"acquiredAt"` // Unix ms
}

// PipelineFormatInfo describes the current video pipeline format.
type PipelineFormatInfo struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPSNum      int    `json:"fpsNum"`
	FPSDen      int    `json:"fpsDen"`
	Name        string `json:"name"`
	ColorFormat string `json:"colorFormat,omitempty"`
	Codec       string `json:"codec,omitempty"`
}

// EncoderInfo describes an available video encoder backend.
type EncoderInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	IsDefault   bool   `json:"isDefault"`
}

// EncoderState describes the current encoder and available alternatives.
type EncoderState struct {
	Current   string        `json:"current"`
	Available []EncoderInfo `json:"available"`
}

// CBRStatus describes the CBR pacer state for ControlRoomState broadcast.
type CBRStatus struct {
	Enabled          bool  `json:"enabled"`
	MuxrateBps       int64 `json:"muxrateBps"`
	NullPacketsTotal int64 `json:"nullPacketsTotal"`
	RealBytesTotal   int64 `json:"realBytesTotal"`
	PadBytesTotal    int64 `json:"padBytesTotal"`
	BurstTicksTotal  int64 `json:"burstTicksTotal"`
}

// ConnectionInfo holds SRT connection details for the UI.
type ConnectionInfo struct {
	Domain         string `json:"domain,omitempty"`
	SRTIngestPort  int    `json:"srtIngestPort,omitempty"`
	SRTOutputPorts []int  `json:"srtOutputPorts,omitempty"`
}

// PodStatusInfo is the broadcast state for an active pod.
type PodStatusInfo struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	State       string `json:"state"`
	ElapsedMs   int64  `json:"elapsedMs,omitempty"`
	RemainingMs int64  `json:"remainingMs,omitempty"`
	DurationMs  int64  `json:"durationMs"`
	Armed       bool   `json:"armed"`
}

// PlayoutChannelInfo is the broadcast state for a playout channel.
type PlayoutChannelInfo struct {
	ID            int             `json:"id"`
	State         string          `json:"state"`
	Mode          string          `json:"mode,omitempty"`
	PlaylistName  string          `json:"playlistName,omitempty"`
	CurrentItemID string          `json:"currentItemId,omitempty"`
	CurrentTitle  string          `json:"currentTitle,omitempty"`
	NextItemID    string          `json:"nextItemId,omitempty"`
	NextTitle     string          `json:"nextTitle,omitempty"`
	ItemIndex     int             `json:"itemIndex"`
	ItemCount     int             `json:"itemCount"`
	ItemProgress  float64         `json:"itemProgress,omitempty"`
	ItemRemainMs  int64           `json:"itemRemainMs,omitempty"`
	ActivePod     *PodStatusInfo  `json:"activePod,omitempty"`
	Pods          []PodStatusInfo `json:"pods,omitempty"`
	PodsArmed     int             `json:"podsArmed,omitempty"`
	PodsCompleted int             `json:"podsCompleted,omitempty"`
}

// PlayoutState holds the broadcast state for all playout channels.
type PlayoutState struct {
	Channels []PlayoutChannelInfo `json:"channels"`
}

// ControlRoomState is the full state of the switcher control room,
// broadcast to all connected browsers via the MoQ "control" track.
type ControlRoomState struct {
	ProgramSource        string                  `json:"programSource"`
	PreviewSource        string                  `json:"previewSource"`
	TransitionType       string                  `json:"transitionType"`
	TransitionDurationMs int                     `json:"transitionDurationMs,omitempty"`
	TransitionPosition   float64                 `json:"transitionPosition,omitempty"`
	TransitionEasing     string                  `json:"transitionEasing,omitempty"`
	InTransition         bool                    `json:"inTransition,omitempty"`
	FTBActive            bool                    `json:"ftbActive,omitempty"`
	AudioChannels        map[string]AudioChannel `json:"audioChannels"`
	MasterLevel          float64                 `json:"masterLevel"`
	ProgramPeak          [2]float64              `json:"programPeak"`
	GainReduction        float64                 `json:"gainReduction,omitempty"`
	Limiter              *LimiterSettings        `json:"limiter,omitempty"`
	MomentaryLUFS        float64                 `json:"momentaryLufs,omitempty"`
	ShortTermLUFS        float64                 `json:"shortTermLufs,omitempty"`
	IntegratedLUFS       float64                 `json:"integratedLufs,omitempty"`
	TallyState           map[string]string       `json:"tallyState"`
	Recording            *RecordingStatus        `json:"recording,omitempty"`
	SRTOutput            *SRTOutputStatus        `json:"srtOutput,omitempty"`
	Destinations         []DestinationInfo       `json:"destinations,omitempty"`
	Sources              map[string]SourceInfo   `json:"sources"`
	Presets              []PresetInfo            `json:"presets,omitempty"`
	Graphics             *GraphicsState          `json:"graphics,omitempty"`
	Layout               *LayoutState            `json:"layout,omitempty"`
	Replay               *ReplayState            `json:"replay,omitempty"`
	ClipPlayers          []ClipPlayerInfo        `json:"clipPlayers,omitempty"`
	ClipCount            int                     `json:"clipCount,omitempty"`
	ClipUpload           *ClipUploadProgress     `json:"clipUpload,omitempty"`
	Operators            []OperatorInfo          `json:"operators,omitempty"`
	Locks                map[string]LockInfo     `json:"locks,omitempty"`
	PipelineFormat       *PipelineFormatInfo     `json:"pipelineFormat,omitempty"`
	Encoder              *EncoderState           `json:"encoder,omitempty"`
	SCTE35               *SCTE35State            `json:"scte35,omitempty"`
	CBR                  *CBRStatus              `json:"cbr,omitempty"`
	ConnectionInfo       *ConnectionInfo         `json:"connectionInfo,omitempty"`
	Captions             *CaptionState           `json:"captions,omitempty"`
	Comms                *CommsState             `json:"comms,omitempty"`
	STMap                *STMapState             `json:"stmap,omitempty"`
	AISegmentation       *AISegmentationState    `json:"aiSegmentation,omitempty"`
	NeuralKey            *NeuralKeyState         `json:"neuralKey,omitempty"`
	ASR                  *ASRState               `json:"asr,omitempty"`
	Macro                *MacroExecutionState    `json:"macro,omitempty"`
	ColorGrade           *ColorGradeState        `json:"colorGrade,omitempty"`
	Playout              *PlayoutState           `json:"playout,omitempty"`
	PeerReachable        *bool                   `json:"peerReachable,omitempty"` // nil when no peer configured
	PeerLastSeenMs       int64                   `json:"peerLastSeenMs,omitempty"`
	EngineLabel          string                  `json:"engineLabel,omitempty"`          // "" (standalone), "a", or "b"
	Role                 string                  `json:"role,omitempty"`                 // "standalone" | "leader" | "follower-healthy" | "follower-partitioned"
	EngineBURL           string                  `json:"engineBUrl,omitempty"`
	SessionID            string                  `json:"sessionId,omitempty"`
	LastChangedBy        string                  `json:"lastChangedBy,omitempty"`
	Seq                  uint64                  `json:"seq"`
	LastCommandSeq       uint64                  `json:"lastCommandSeq"`
	LastExecutedSeq      uint64                  `json:"lastExecutedSeq"`
	LastExecutedErr      string                  `json:"lastExecutedErr,omitempty"`
	LateCommands         int64                   `json:"lateCommands,omitempty"`
	RecentCommandErrors  []CommandError          `json:"recentCommandErrors,omitempty"`
	Timestamp            int64                   `json:"timestamp"`
}

// CommandError records a single failed command execution (sequence + error string).
type CommandError struct {
	Seq   uint64 `json:"seq"`
	Error string `json:"error"`
}

// MacroExecutionState represents the progress of a running macro.
type MacroExecutionState struct {
	Running     bool             `json:"running"`
	MacroName   string           `json:"macroName"`
	Steps       []MacroStepState `json:"steps"`
	CurrentStep int              `json:"currentStep"`
	Error       string           `json:"error,omitempty"`
}

// MacroStepState tracks the execution state of one macro step.
type MacroStepState struct {
	Action      string `json:"action"`
	Summary     string `json:"summary"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	WaitMs      int    `json:"waitMs,omitempty"`
	WaitStartMs int64  `json:"waitStartMs,omitempty"`
}

// CaptionState represents the current closed captioning state.
type CaptionState struct {
	Mode           string          `json:"mode"`
	AuthorBuffer   string          `json:"authorBuffer,omitempty"`
	SourceCaptions map[string]bool `json:"sourceCaptions,omitempty"`
}

// CommsState represents the current operator voice comms state.
type CommsState struct {
	Active       bool               `json:"active"`
	Participants []CommsParticipant `json:"participants"`
}

// CommsParticipant represents a single operator in the voice comms channel.
type CommsParticipant struct {
	OperatorID string `json:"operatorId"`
	Name       string `json:"name"`
	Muted      bool   `json:"muted"`
	Speaking   bool   `json:"speaking"`
}

// STMapState represents ST map assignments in the control room state broadcast.
type STMapState struct {
	Sources   map[string]string  `json:"sources"`           // source key → map name
	Program   *STMapProgramState `json:"program,omitempty"` // nil if no program map
	Available []string           `json:"available"`         // all stored map names
}

// STMapProgramState describes the current program ST map assignment.
type STMapProgramState struct {
	Map   string `json:"map"`
	Type  string `json:"type"`  // "static" or "animated"
	Frame int    `json:"frame"` // current frame index (animated only)
}

// SCTE35State represents the current SCTE-35 signaling state.
type SCTE35State struct {
	Enabled        bool                    `json:"enabled"`
	SCTE104Enabled bool                    `json:"scte104Enabled,omitempty"`
	ActiveEvents   map[uint32]SCTE35Active `json:"activeEvents"`
	EventLog       []SCTE35Event           `json:"eventLog"`
	HeartbeatOK    bool                    `json:"heartbeatOk"`
	Config         SCTE35Config            `json:"config"`
}

// SCTE35Active describes an in-progress SCTE-35 event.
type SCTE35Active struct {
	EventID       uint32                 `json:"eventId"`
	CommandType   string                 `json:"commandType"`
	IsOut         bool                   `json:"isOut"`
	DurationMs    *int64                 `json:"durationMs,omitempty"`
	ElapsedMs     int64                  `json:"elapsedMs"`
	RemainingMs   *int64                 `json:"remainingMs,omitempty"`
	AutoReturn    bool                   `json:"autoReturn"`
	Held          bool                   `json:"held"`
	SpliceTimePTS int64                  `json:"spliceTimePts"`
	StartedAt     int64                  `json:"startedAt"`
	PreRollMs     int64                  `json:"preRollMs,omitempty"`
	Descriptors   []SCTE35DescriptorInfo `json:"descriptors,omitempty"`
}

// SCTE35DescriptorInfo describes a segmentation descriptor in an active event.
type SCTE35DescriptorInfo struct {
	SegEventID           uint32 `json:"segEventId"`
	SegmentationType     uint8  `json:"segmentationType"`
	SegmentationTypeName string `json:"segmentationTypeName"`
	UPIDType             uint8  `json:"upidType"`
	UPIDTypeName         string `json:"upidTypeName"`
	UPID                 string `json:"upid"`
	DurationMs           *int64 `json:"durationMs,omitempty"`
	SubSegmentNum        uint8  `json:"subSegmentNum,omitempty"`
	SubSegmentsExpected  uint8  `json:"subSegmentsExpected,omitempty"`
	Cancelled            bool   `json:"cancelled,omitempty"`
}

// SCTE35Event describes a logged SCTE-35 event.
type SCTE35Event struct {
	EventID        uint32                 `json:"eventId"`
	CommandType    string                 `json:"commandType"`
	IsOut          bool                   `json:"isOut"`
	DurationMs     *int64                 `json:"durationMs,omitempty"`
	AutoReturn     bool                   `json:"autoReturn"`
	Descriptors    []SCTE35DescriptorInfo `json:"descriptors,omitempty"`
	AvailNum       *uint8                 `json:"availNum,omitempty"`
	AvailsExpected *uint8                 `json:"availsExpected,omitempty"`
	SpliceTimePTS  *int64                 `json:"spliceTimePts,omitempty"`
	Timestamp      int64                  `json:"timestamp"`
	Status         string                 `json:"status"`
	Source         string                 `json:"source,omitempty"`
	DestinationID  string                 `json:"destinationId,omitempty"`
}

// SCTE35Config describes the SCTE-35 injector configuration.
type SCTE35Config struct {
	HeartbeatIntervalMs int64  `json:"heartbeatIntervalMs"`
	DefaultPreRollMs    int64  `json:"defaultPreRollMs"`
	PID                 uint16 `json:"pid"`
	VerifyEncoding      bool   `json:"verifyEncoding"`
	WebhookURL          string `json:"webhookUrl,omitempty"`
}

// ClipPlayerInfo is the broadcast state of a single clip player.
type ClipPlayerInfo struct {
	ID       int     `json:"id"`
	ClipID   string  `json:"clipId,omitempty"`
	ClipName string  `json:"clipName,omitempty"`
	State    string  `json:"state"`
	Speed    float64 `json:"speed,omitempty"`
	Position float64 `json:"position,omitempty"`
	Loop     bool    `json:"loop,omitempty"`
}

// ClipUploadProgress tracks server-side clip upload stages for state broadcast.
type ClipUploadProgress struct {
	Stage    string `json:"stage"`   // "uploading","analyzing","transcoding","validating"
	Percent  int    `json:"percent"` // 0-100 within current stage
	Filename string `json:"filename,omitempty"`
}

// ClipInfo is the broadcast-friendly clip metadata.
type ClipInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	DurationMs int64  `json:"durationMs"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Ephemeral  bool   `json:"ephemeral,omitempty"`
	Loop       bool   `json:"loop"`
}

// LayoutState represents the current layout configuration for state broadcast.
type LayoutState struct {
	ActivePreset string            `json:"activePreset"`
	Slots        []LayoutSlotState `json:"slots"`
	DVEPresets   []DVEPresetInfo   `json:"dvePresets,omitempty"`
	DVEAnimating bool              `json:"dveAnimating,omitempty"`
}

// AISegmentConfig describes the AI segmentation configuration for a source.
// Background values: "" or "transparent" (pass-through), "blur:N" (N=1-50),
// "color:RRGGBB" (hex color).
type AISegmentConfig struct {
	Enabled     bool    `json:"enabled"`
	Sensitivity float32 `json:"sensitivity"`
	EdgeSmooth  float32 `json:"edgeSmooth"`
	Background  string  `json:"background"`
}

// NeuralKeyState is broadcast in ControlRoomState when the neural key
// engine is available (GPU + TensorRT + model loaded). Omitted on non-GPU builds.
type NeuralKeyState struct {
	Available bool `json:"available"`
}

// AISegmentationState is broadcast in ControlRoomState when AI segmentation
// is available (GPU + TensorRT). Omitted entirely on non-GPU builds.
type AISegmentationState struct {
	Available bool                       `json:"available"`
	ModelName string                     `json:"modelName,omitempty"`
	Sources   map[string]AISegmentConfig `json:"sources,omitempty"`
}

// ASRState represents the ASR system state for ControlRoomState broadcast.
type ASRState struct {
	Available         bool            `json:"available"`
	Active            bool            `json:"active"`
	Backend           string          `json:"backend"`
	AvailableBackends []string        `json:"availableBackends,omitempty"`
	ModelName         string          `json:"modelName,omitempty"`
	BackendConfig     json.RawMessage `json:"backendConfig,omitempty"`
	ConfidenceFilter  string          `json:"confidenceFilter,omitempty"`
	SpeakerDetection  string          `json:"speakerDetection,omitempty"`
}

// LookMetadata describes a single available color look with group/description.
type LookMetadata struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	Description string `json:"description"`
	Builtin     bool   `json:"builtin"`
}

// CurvePoint is an alias for colorgrade.CurvePoint (single source of truth).
type CurvePoint = colorgrade.CurvePoint

// CorrectorState is an alias for colorgrade.CorrectorState (single source of truth).
type CorrectorState = colorgrade.CorrectorState

// ColorGradeState represents the color grading state in ControlRoomState.
type ColorGradeState struct {
	Enabled   bool            `json:"enabled"`
	LookName  string          `json:"lookName"`
	LookGroup string          `json:"lookGroup"`
	Intensity float32         `json:"intensity"`
	Available []LookMetadata  `json:"available"`
	Corrector *CorrectorState `json:"corrector,omitempty"`
}

// LayoutSlotState represents a single layout slot in the state broadcast.
type LayoutSlotState struct {
	ID         int           `json:"id"`
	SourceKey  string        `json:"sourceKey"`
	Enabled    bool          `json:"enabled"`
	X          int           `json:"x"`
	Y          int           `json:"y"`
	Width      int           `json:"width"`
	Height     int           `json:"height"`
	ZOrder     int           `json:"zOrder"`
	Animating  bool          `json:"animating,omitempty"`
	ScaleMode  string        `json:"scaleMode,omitempty"`
	CropAnchor [2]float64    `json:"cropAnchor,omitempty"`
	DVE        *DVESlotState `json:"dve,omitempty"`
}

// DVESlotState holds the full DVE transform parameters for a layout slot.
type DVESlotState struct {
	PositionX     float64         `json:"positionX"`
	PositionY     float64         `json:"positionY"`
	Width         float64         `json:"width"`
	Height        float64         `json:"height"`
	RotationDeg   float64         `json:"rotationDeg,omitempty"`
	AnchorX       float64         `json:"anchorX,omitempty"`
	AnchorY       float64         `json:"anchorY,omitempty"`
	PerspectiveX  float64         `json:"perspectiveX,omitempty"`
	PerspectiveY  float64         `json:"perspectiveY,omitempty"`
	CropTop       float64         `json:"cropTop,omitempty"`
	CropBottom    float64         `json:"cropBottom,omitempty"`
	CropLeft      float64         `json:"cropLeft,omitempty"`
	CropRight     float64         `json:"cropRight,omitempty"`
	CropSoftness  float64         `json:"cropSoftness,omitempty"`
	Opacity       float64         `json:"opacity,omitempty"`
	Border        *DVEBorderState `json:"border,omitempty"`
	Shadow        *DVEShadowState `json:"shadow,omitempty"`
	DefocusRadius float64         `json:"defocusRadius,omitempty"`
	MirrorH       bool            `json:"mirrorH,omitempty"`
	MirrorV       bool            `json:"mirrorV,omitempty"`
	MosaicSize    int             `json:"mosaicSize,omitempty"`
	Saturation    float64         `json:"saturation,omitempty"`
	Brightness    float64         `json:"brightness,omitempty"`
	Contrast      float64         `json:"contrast,omitempty"`
	Mode3D        string          `json:"mode3d,omitempty"`
	RotateX       float64         `json:"rotateX,omitempty"`
	RotateY       float64         `json:"rotateY,omitempty"`
	RotateZ       float64         `json:"rotateZ,omitempty"`
}

// DVEBorderState holds DVE border configuration for state broadcast.
type DVEBorderState struct {
	Width        float64 `json:"width"`
	ColorY       byte    `json:"colorY"`
	ColorCb      byte    `json:"colorCb"`
	ColorCr      byte    `json:"colorCr"`
	CornerRadius float64 `json:"cornerRadius,omitempty"`
	Softness     float64 `json:"softness,omitempty"`
	InnerWidth   float64 `json:"innerWidth,omitempty"`
}

// DVEShadowState holds DVE shadow configuration for state broadcast.
type DVEShadowState struct {
	Enabled bool    `json:"enabled"`
	OffsetX float64 `json:"offsetX,omitempty"`
	OffsetY float64 `json:"offsetY,omitempty"`
	Blur    float64 `json:"blur,omitempty"`
	Opacity float64 `json:"opacity,omitempty"`
	ColorY  byte    `json:"colorY,omitempty"`
}

// DVEPresetInfo is a summary of a DVE preset for state broadcast.
type DVEPresetInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Builtin bool   `json:"builtin,omitempty"`
}
