package nmos

// NMOS format URNs (IS-04 v1.3).
const (
	FormatVideo = "urn:x-nmos:format:video"
	FormatAudio = "urn:x-nmos:format:audio"
	FormatData  = "urn:x-nmos:format:data"
	FormatMux   = "urn:x-nmos:format:mux"
)

// NMOS transport URNs (IS-04 v1.3).
const (
	TransportRTP      = "urn:x-nmos:transport:rtp"
	TransportRTPMcast = "urn:x-nmos:transport:rtp.mcast"
	TransportRTPUcast = "urn:x-nmos:transport:rtp.ucast"
)

// NMOS device type URNs (IS-04 v1.3).
const (
	DeviceTypePipeline = "urn:x-nmos:device:pipeline"
	DeviceTypeGeneric  = "urn:x-nmos:device:generic"
)

// NMOS control type URNs (IS-05).
const (
	ControlTypeSRCtrl = "urn:x-nmos:control:sr-ctrl/v1.1"
)

// Rational represents a rational number with integer numerator and denominator.
type Rational struct {
	Numerator   int64 `json:"numerator"`
	Denominator int64 `json:"denominator"`
}

// Node represents an NMOS IS-04 Node resource.
type Node struct {
	ID          string                 `json:"id"`
	Version     Version                `json:"version"`
	Label       string                 `json:"label"`
	Description string                 `json:"description"`
	Hostname    string                 `json:"hostname"`
	Tags        map[string][]string    `json:"tags"`
	Caps        map[string]interface{} `json:"caps"`
	Href        string                 `json:"href"`
	API         NodeAPI                `json:"api"`
	Clocks      []Clock                `json:"clocks"`
	Interfaces  []Interface            `json:"interfaces"`
	Services    []interface{}          `json:"services"`
}

// NodeAPI describes the API endpoints exposed by a Node.
type NodeAPI struct {
	Versions  []string      `json:"versions"`
	Endpoints []APIEndpoint `json:"endpoints"`
}

// APIEndpoint describes a single API endpoint.
type APIEndpoint struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Protocol      string `json:"protocol"`
	Authorization bool   `json:"authorization,omitempty"`
}

// Clock describes a clock source on a Node.
type Clock struct {
	Name      string  `json:"name"`
	RefType   string  `json:"ref_type"`
	Traceable *bool   `json:"traceable,omitempty"`
	Version   *string `json:"version,omitempty"`
	GMID      *string `json:"gmid,omitempty"`
	Locked    *bool   `json:"locked,omitempty"`
}

// Interface describes a network interface on a Node.
type Interface struct {
	Name      string `json:"name"`
	ChassisID string `json:"chassis_id"`
	PortID    string `json:"port_id"`
}

// Device represents an NMOS IS-04 Device resource.
type Device struct {
	ID          string              `json:"id"`
	Version     Version             `json:"version"`
	Label       string              `json:"label"`
	Description string              `json:"description"`
	Tags        map[string][]string `json:"tags"`
	Type        string              `json:"type"`
	NodeID      string              `json:"node_id"`
	Senders     []string            `json:"senders"`
	Receivers   []string            `json:"receivers"`
	Controls    []Control           `json:"controls"`
}

// Control describes a control endpoint on a Device.
type Control struct {
	Type          string `json:"type"`
	Href          string `json:"href"`
	Authorization bool   `json:"authorization,omitempty"`
}

// Source represents an NMOS IS-04 Source resource.
type Source struct {
	ID          string              `json:"id"`
	Version     Version             `json:"version"`
	Label       string              `json:"label"`
	Description string              `json:"description"`
	Tags        map[string][]string `json:"tags"`
	Caps        map[string]interface{} `json:"caps"`
	Format      string              `json:"format"`
	Parents     []string            `json:"parents"`
	DeviceID    string              `json:"device_id"`
	ClockName   string              `json:"clock_name"`
	Channels    []SourceChannel     `json:"channels,omitempty"`
}

// SourceChannel describes a channel within an audio Source.
type SourceChannel struct {
	Label  string `json:"label"`
	Symbol string `json:"symbol"`
}

// Flow represents an NMOS IS-04 Flow resource.
// Video-specific and audio-specific fields use omitempty so they only
// appear in JSON when set.
type Flow struct {
	ID          string              `json:"id"`
	Version     Version             `json:"version"`
	Label       string              `json:"label"`
	Description string              `json:"description"`
	Tags        map[string][]string `json:"tags"`
	Format      string              `json:"format"`
	SourceID    string              `json:"source_id"`
	DeviceID    string              `json:"device_id"`
	Parents     []string            `json:"parents"`
	MediaType   string              `json:"media_type"`

	// Video-specific fields.
	FrameWidth    int         `json:"frame_width,omitempty"`
	FrameHeight   int         `json:"frame_height,omitempty"`
	InterlaceMode string      `json:"interlace_mode,omitempty"`
	Colorspace    string      `json:"colorspace,omitempty"`
	Components    []Component `json:"components,omitempty"`
	GrainRate     *Rational   `json:"grain_rate,omitempty"`

	// Audio-specific fields.
	SampleRate *Rational `json:"sample_rate,omitempty"`
	BitDepth   int       `json:"bit_depth,omitempty"`
}

// Component describes a video component (e.g. Y, Cb, Cr).
type Component struct {
	Name     string `json:"name"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	BitDepth int    `json:"bit_depth"`
}

// Sender represents an NMOS IS-04 Sender resource.
type Sender struct {
	ID                string              `json:"id"`
	Version           Version             `json:"version"`
	Label             string              `json:"label"`
	Description       string              `json:"description"`
	Tags              map[string][]string `json:"tags"`
	FlowID            string              `json:"flow_id"`
	Transport         string              `json:"transport"`
	DeviceID          string              `json:"device_id"`
	ManifestHref      string              `json:"manifest_href"`
	InterfaceBindings []string            `json:"interface_bindings"`
	Caps              map[string]interface{} `json:"caps"`
	Subscription      SenderSubscription  `json:"subscription"`
}

// SenderSubscription tracks which receiver a sender is connected to.
type SenderSubscription struct {
	ReceiverID *string `json:"receiver_id"`
	Active     bool    `json:"active"`
}

// Receiver represents an NMOS IS-04 Receiver resource.
type Receiver struct {
	ID                string              `json:"id"`
	Version           Version             `json:"version"`
	Label             string              `json:"label"`
	Description       string              `json:"description"`
	Tags              map[string][]string `json:"tags"`
	Format            string              `json:"format"`
	Caps              ReceiverCaps        `json:"caps"`
	Transport         string              `json:"transport"`
	DeviceID          string              `json:"device_id"`
	InterfaceBindings []string            `json:"interface_bindings"`
	Subscription      ReceiverSubscription `json:"subscription"`
}

// ReceiverCaps describes the media types a receiver can accept.
type ReceiverCaps struct {
	MediaTypes []string `json:"media_types,omitempty"`
}

// ReceiverSubscription tracks which sender a receiver is connected to.
type ReceiverSubscription struct {
	SenderID *string `json:"sender_id"`
	Active   bool    `json:"active"`
}
