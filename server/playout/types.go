package playout

import (
	"errors"
	"strings"
	"time"
)

// ChannelState represents the state of a playout channel.
type ChannelState int

const (
	StateStopped  ChannelState = 0
	StateCued     ChannelState = 1
	StateOnAir    ChannelState = 2
	StateCompleted ChannelState = 3
)

func (s ChannelState) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateCued:
		return "cued"
	case StateOnAir:
		return "on_air"
	case StateCompleted:
		return "completed"
	default:
		return "unknown"
	}
}

// OnAirMode represents the automation mode of an on-air channel.
type OnAirMode int

const (
	ModeAutomated OnAirMode = 0
	ModeManual    OnAirMode = 1
	ModeBreak     OnAirMode = 2
)

func (m OnAirMode) String() string {
	switch m {
	case ModeAutomated:
		return "automated"
	case ModeManual:
		return "manual"
	case ModeBreak:
		return "break"
	default:
		return "unknown"
	}
}

// ItemType identifies the kind of playlist item.
type ItemType string

const (
	ItemClip  ItemType = "clip"
	ItemLive  ItemType = "live"
	ItemSlate ItemType = "slate"
)

// TransitionConfig defines the transition between playlist items.
type TransitionConfig struct {
	Type       string `json:"type"`
	DurationMs int    `json:"duration_ms,omitempty"`
}

// SCTE35EventData holds full SCTE-35 parameters for a cue secondary event.
// The same structure is used in Go, TypeScript, and the playlist manifest JSON.
// Omitted fields get defaults: timing="immediate", tier=0, preset="custom".
type SCTE35EventData struct {
	Preset          string           `json:"preset,omitempty"`           // UI hint: "program_start", "cue_out", "custom", etc.
	CommandType     uint8            `json:"command_type,omitempty"`     // 0x00=splice_null, 0x05=splice_insert, 0x06=time_signal
	EventID         *uint32          `json:"event_id,omitempty"`         // nil = auto-assign by injector
	IsOut           bool             `json:"is_out,omitempty"`           // cue direction
	BreakDurationMs *int64           `json:"break_duration_ms,omitempty"`
	AutoReturn      bool             `json:"auto_return,omitempty"`
	Timing          string           `json:"timing,omitempty"`           // "immediate" | "scheduled"
	SpliceTimePTS   *int64           `json:"splice_time_pts,omitempty"`  // 90kHz ticks for scheduled mode
	PTSAdjustment   int64            `json:"pts_adjustment,omitempty"`
	UniqueProgramID uint16           `json:"unique_program_id,omitempty"`
	AvailNum        uint8            `json:"avail_num,omitempty"`
	AvailsExpected  uint8            `json:"avails_expected,omitempty"`
	Tier            uint16           `json:"tier,omitempty"`             // 0-4095, 0=unrestricted
	Descriptors     []SegDescriptor  `json:"descriptors,omitempty"`
}

// SegDescriptor holds a segmentation descriptor for time_signal commands.
type SegDescriptor struct {
	SegmentationType       uint8  `json:"segmentation_type"`
	SegEventID             uint32 `json:"seg_event_id,omitempty"`
	DurationMs             *int64 `json:"duration_ms,omitempty"`
	UPIDType               uint8  `json:"upid_type,omitempty"`
	UPID                   string `json:"upid,omitempty"`                   // hex-encoded or ASCII depending on type
	AdditionalUPIDs        []UPID `json:"additional_upids,omitempty"`
	DeliveryNotRestricted  bool   `json:"delivery_not_restricted,omitempty"`
	WebDeliveryAllowed     bool   `json:"web_delivery_allowed,omitempty"`
	NoRegionalBlackout     bool   `json:"no_regional_blackout,omitempty"`
	ArchiveAllowed         bool   `json:"archive_allowed,omitempty"`
	DeviceRestrictions     uint8  `json:"device_restrictions,omitempty"`    // 0x00-0x03
	Cancel                 bool   `json:"cancel,omitempty"`
}

// UPID holds a Unique Program Identifier (SCTE-35 UPID).
type UPID struct {
	Type  uint8  `json:"type"`
	Value string `json:"value"`
}

// SecondaryEvent is a timed event attached to a playlist item (e.g., graphic on/off).
type SecondaryEvent struct {
	Type     string `json:"type"`
	Layer    int    `json:"layer,omitempty"`
	Template string `json:"template,omitempty"`
	OffsetMs int    `json:"offset_ms"`
	Anchor   string `json:"anchor"`

	// Audio event fields.
	SourceKey string  `json:"source_key,omitempty"`
	LevelDB   *float64 `json:"level_db,omitempty"`
	Muted     *bool   `json:"muted,omitempty"`
	AFV       *bool   `json:"afv,omitempty"`

	// Caption event fields.
	CaptionMode string `json:"caption_mode,omitempty"`
	CaptionText string `json:"caption_text,omitempty"`

	// SCTE-35 event data.
	SCTE35 *SCTE35EventData `json:"scte35,omitempty"`
}

// PlaylistItem is a single entry in a playout playlist.
type PlaylistItem struct {
	ID              string            `json:"id"`
	Type            ItemType          `json:"type"`
	AssetURI        string            `json:"asset_uri,omitempty"`
	Source          string            `json:"source,omitempty"`
	Title           string            `json:"title"`
	InPointMs       int64             `json:"in_point_ms,omitempty"`
	OutPointMs      int64             `json:"out_point_ms,omitempty"`
	DurationMs      int64             `json:"duration_ms,omitempty"`
	Hold            bool              `json:"hold,omitempty"`
	Loop            bool              `json:"loop,omitempty"`
	Transition      *TransitionConfig `json:"transition,omitempty"`
	ScheduledTime   *time.Time        `json:"scheduled_time,omitempty"`
	SecondaryEvents []SecondaryEvent  `json:"secondary_events,omitempty"`
}

// PodTemplateDef defines a pod template in the playlist manifest.
// These drive the pod board UI buttons with user-defined labels.
type PodTemplateDef struct {
	Label      string      `json:"label"`
	DurationMs int64       `json:"duration_ms"`
	AutoReturn bool        `json:"auto_return"`
	Priority   PodPriority `json:"priority,omitempty"`
}

// Playlist is an ordered list of items for playout.
type Playlist struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Channel           string           `json:"channel"`
	Mode              string           `json:"mode,omitempty"`
	DefaultTransition TransitionConfig `json:"default_transition"`
	Items             []PlaylistItem   `json:"items"`
	IdleAssetURI      string           `json:"idle_asset,omitempty"`
	IdleLoop          bool             `json:"idle_loop,omitempty"`
	PodTemplates      []PodTemplateDef `json:"pods,omitempty"`
}

// ChannelStatus is the broadcast state of a playout channel.
type ChannelStatus struct {
	ID            int     `json:"id"`
	State         string  `json:"state"`
	Mode          string  `json:"mode,omitempty"`
	PlaylistName  string  `json:"playlistName,omitempty"`
	CurrentItemID string  `json:"currentItemId,omitempty"`
	CurrentTitle  string  `json:"currentTitle,omitempty"`
	NextItemID    string  `json:"nextItemId,omitempty"`
	NextTitle     string  `json:"nextTitle,omitempty"`
	ItemIndex     int     `json:"itemIndex"`
	ItemCount     int     `json:"itemCount"`
	ItemProgress  float64          `json:"itemProgress,omitempty"`
	ItemRemainMs  int64            `json:"itemRemainMs,omitempty"`
	Pods          *PodBoardStatus  `json:"pods,omitempty"`
}

// MaxChannels is the maximum number of playout channels.
const MaxChannels = 2

// Sentinel errors.
var (
	ErrNoPlaylist     = errors.New("playout: no playlist loaded")
	ErrAlreadyOnAir   = errors.New("playout: channel already on air")
	ErrNotOnAir       = errors.New("playout: channel not on air")
	ErrInvalidChannel = errors.New("playout: invalid channel ID")
	ErrInvalidItem    = errors.New("playout: invalid playlist item")
	ErrEmptyPlaylist  = errors.New("playout: playlist is empty")
	ErrItemNotFound   = errors.New("playout: item not found")
	ErrAlreadyCued    = errors.New("playout: channel already cued")
	ErrInvalidState   = errors.New("playout: invalid state transition")

	// Asset pipeline errors.
	ErrAssetNotFound   = errors.New("playout: asset not found in cache")
	ErrAssetNotReady   = errors.New("playout: asset not yet cached")
	ErrCacheFull       = errors.New("playout: cache full, eviction failed")
	ErrUnsupportedURI  = errors.New("playout: unsupported asset URI scheme")
	ErrPathTraversal   = errors.New("playout: path traversal detected")
	ErrFetchFailed     = errors.New("playout: asset fetch failed")
	ErrTranscodeFailed = errors.New("playout: asset transcode failed")
)

// AssetState represents the state of an asset in the cache pipeline.
type AssetState int

const (
	AssetPending     AssetState = 0
	AssetFetching    AssetState = 1
	AssetTranscoding AssetState = 2
	AssetCached      AssetState = 3
	AssetError       AssetState = 4
)

func (s AssetState) String() string {
	switch s {
	case AssetPending:
		return "pending"
	case AssetFetching:
		return "fetching"
	case AssetTranscoding:
		return "transcoding"
	case AssetCached:
		return "cached"
	case AssetError:
		return "error"
	default:
		return "unknown"
	}
}

// ParseAssetURI splits an asset URI into scheme and path.
// Bare absolute paths are treated as local.
func ParseAssetURI(uri string) (scheme, path string) {
	// Bare absolute path.
	if strings.HasPrefix(uri, "/") {
		return "local", uri
	}
	// file:// is an alias for local.
	if strings.HasPrefix(uri, "file:///") {
		return "local", uri[len("file://"):]
	}
	// local:// scheme.
	if strings.HasPrefix(uri, "local:///") {
		return "local", uri[len("local://"):]
	}
	// Generic scheme://rest.
	if idx := strings.Index(uri, "://"); idx > 0 {
		return uri[:idx], uri[idx+3:]
	}
	return "local", uri
}

// CacheEntry tracks one cached asset.
type CacheEntry struct {
	AssetURI     string     `json:"asset_uri"`
	LocalPath    string     `json:"local_path"`
	State        AssetState `json:"state"`
	ByteSize     int64      `json:"byte_size"`
	Checksum     string     `json:"checksum,omitempty"`
	Transcoded   bool       `json:"transcoded"`
	LastAccessed time.Time  `json:"last_accessed"`
	FetchedAt    time.Time  `json:"fetched_at,omitempty"`
	ErrorMsg     string     `json:"error_msg,omitempty"`
}

// CacheStatus is the summary status of the asset cache.
type CacheStatus struct {
	CacheDir      string `json:"cacheDir"`
	MaxBytes      int64  `json:"maxBytes"`
	UsedBytes     int64  `json:"usedBytes"`
	ItemCount     int    `json:"itemCount"`
	FetchingCount int    `json:"fetchingCount"`
	ErrorCount    int    `json:"errorCount"`
}

// CacheConfig holds configuration for the asset cache.
type CacheConfig struct {
	Dir              string `json:"cache_dir"`
	MaxBytes         int64  `json:"cache_size_bytes"`
	LookaheadMinutes int    `json:"lookahead_minutes"`
	PreDecodeItems   int    `json:"pre_decode_items"`
}

// PodState represents the lifecycle state of a pod.
type PodState int

const (
	PodScheduled     PodState = 0
	PodPreRoll       PodState = 1
	PodSplicePending PodState = 2
	PodBreakActive   PodState = 3
	PodReturnPending PodState = 4
	PodRejoined      PodState = 5
	PodDumpedOut     PodState = 6
)

func (s PodState) String() string {
	switch s {
	case PodScheduled:
		return "scheduled"
	case PodPreRoll:
		return "pre_roll"
	case PodSplicePending:
		return "splice_pending"
	case PodBreakActive:
		return "break_active"
	case PodReturnPending:
		return "return_pending"
	case PodRejoined:
		return "rejoined"
	case PodDumpedOut:
		return "dumped_out"
	default:
		return "unknown"
	}
}

// IsTerminal returns true if the pod has reached a final state.
func (s PodState) IsTerminal() bool {
	return s == PodRejoined || s == PodDumpedOut
}

// PodPriority determines which pod wins in contention.
type PodPriority int

const (
	PriorityNormal    PodPriority = 0
	PriorityHigh      PodPriority = 1
	PriorityMandatory PodPriority = 2
)

func (p PodPriority) String() string {
	switch p {
	case PriorityNormal:
		return "normal"
	case PriorityHigh:
		return "high"
	case PriorityMandatory:
		return "mandatory"
	default:
		return "unknown"
	}
}

// Less returns true if p has lower priority than other.
func (p PodPriority) Less(other PodPriority) bool {
	return p < other
}

// PodConfig defines the parameters for creating a pod instance.
type PodConfig struct {
	Type                  string      `json:"type"`
	DurationMs            int64       `json:"duration_ms"`
	AutoReturn            bool        `json:"auto_return"`
	AllowDumpOut          bool        `json:"allow_dump_out"`
	Priority              PodPriority `json:"priority"`
	Contractual           bool        `json:"contractual,omitempty"`
	Ephemeral             bool        `json:"ephemeral,omitempty"`
	HoldUntilManualReturn bool        `json:"hold_until_manual_return,omitempty"`
	Position              string      `json:"position,omitempty"`
}

// Pod is a runtime pod instance tracking a break lifecycle.
type Pod struct {
	ID            string    `json:"id"`
	Config        PodConfig `json:"config"`
	State         PodState  `json:"state"`
	Armed         bool      `json:"armed"`
	ElapsedMs     int64     `json:"elapsed_ms"`
	RemainingMs   int64     `json:"remaining_ms,omitempty"`
	FiredAt       int64     `json:"fired_at,omitempty"`
	CompletedAt   int64     `json:"completed_at,omitempty"`
	SCTE35EventID uint32    `json:"scte35_event_id,omitempty"`
}

// PodBoardStatus is the broadcast state for the pod board.
type PodBoardStatus struct {
	ActivePod     *PodStatus  `json:"activePod,omitempty"`
	PodsArmed     int         `json:"podsArmed"`
	PodsCompleted int         `json:"podsCompleted"`
	Pods          []PodStatus `json:"pods"`
}

// PodStatus is the broadcast-safe snapshot of a single pod.
type PodStatus struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	State       string `json:"state"`
	Armed       bool   `json:"armed"`
	ElapsedMs   int64  `json:"elapsedMs,omitempty"`
	RemainingMs int64  `json:"remainingMs,omitempty"`
	DurationMs  int64  `json:"durationMs"`
	Priority    string `json:"priority"`
	Position    string `json:"position,omitempty"`
}

// Pod sentinel errors.
var (
	ErrPodNotFound      = errors.New("playout: pod not found")
	ErrPodAlreadyActive = errors.New("playout: pod already active")
	ErrPodNotArmed      = errors.New("playout: pod not armed")
	ErrPodTerminal      = errors.New("playout: pod in terminal state")
	ErrPodNoDumpOut     = errors.New("playout: dump out not allowed for this pod")
	ErrPodNotActive     = errors.New("playout: no active pod")
	ErrNoPodBoard       = errors.New("playout: pod board not initialized")
)

// AsRunEventType classifies as-run log entries.
type AsRunEventType string

const (
	AsRunItemStart      AsRunEventType = "item_start"
	AsRunItemEnd        AsRunEventType = "item_end"
	AsRunSecondaryFire  AsRunEventType = "secondary_event_fire"
	AsRunSpliceActive   AsRunEventType = "splice_active"
	AsRunPodOperation   AsRunEventType = "pod_operation"
	AsRunManualOverride AsRunEventType = "manual_override"
	AsRunCacheEvent     AsRunEventType = "cache_event"
	AsRunError          AsRunEventType = "error"
)

// BlackDetectInfo holds black detection results for splice events.
type BlackDetectInfo struct {
	Found    bool    `json:"found"`
	LumaMean float64 `json:"luma_mean"`
	OffsetMs float64 `json:"offset_ms"`
}

// AsRunEvent is a single entry in the as-run event log.
type AsRunEvent struct {
	Timestamp    time.Time      `json:"ts"`
	Type         AsRunEventType `json:"type"`
	ChannelID    int            `json:"channel_id"`
	ItemID       string         `json:"item_id,omitempty"`
	Title        string         `json:"title,omitempty"`
	ScheduledTS  *time.Time     `json:"scheduled_ts,omitempty"`
	DeltaMs      float64        `json:"delta_ms,omitempty"`
	Source       string         `json:"source,omitempty"`

	// Secondary event fields.
	EventName string `json:"event,omitempty"`
	TargetPTS int64  `json:"target_pts,omitempty"`
	ActualPTS int64  `json:"actual_pts,omitempty"`

	// Splice/pod fields.
	PodID               string           `json:"pod_id,omitempty"`
	PodType             string           `json:"pod_type,omitempty"`
	Operation           string           `json:"operation,omitempty"`
	Operator            string           `json:"operator,omitempty"`
	ScheduledDurationMs int64            `json:"scheduled_duration_ms,omitempty"`
	ActualDurationMs    int64            `json:"actual_duration_ms,omitempty"`
	Reason              string           `json:"reason,omitempty"`
	BlackDetect         *BlackDetectInfo `json:"black_detect,omitempty"`

	// Error/cache fields.
	Message string `json:"message,omitempty"`
}
