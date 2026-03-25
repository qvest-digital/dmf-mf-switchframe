package playout

import (
	"errors"
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

// SecondaryEvent is a timed event attached to a playlist item (e.g., graphic on/off).
type SecondaryEvent struct {
	Type     string `json:"type"`
	Layer    int    `json:"layer,omitempty"`
	Template string `json:"template,omitempty"`
	OffsetMs int    `json:"offset_ms"`
	Anchor   string `json:"anchor"`
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
	Transition      *TransitionConfig `json:"transition,omitempty"`
	ScheduledTime   *time.Time        `json:"scheduled_time,omitempty"`
	SecondaryEvents []SecondaryEvent  `json:"secondary_events,omitempty"`
}

// Playlist is an ordered list of items for playout.
type Playlist struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Channel           string           `json:"channel"`
	Mode              string           `json:"mode,omitempty"`
	DefaultTransition TransitionConfig `json:"default_transition"`
	Items             []PlaylistItem   `json:"items"`
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
	ItemProgress  float64 `json:"itemProgress,omitempty"`
	ItemRemainMs  int64   `json:"itemRemainMs,omitempty"`
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
)
