package nmos

import (
	"encoding/json"
	"testing"
)

func TestNodeJSON(t *testing.T) {
	node := Node{
		ID:          "d1713110-7343-4d05-b4f7-a0aeedaa221c",
		Version:     NewVersion(),
		Label:       "Test Node",
		Description: "A test NMOS node",
		Hostname:    "node1.local",
		Tags:        map[string][]string{"location": {"studio-a"}},
		Caps:        map[string]interface{}{},
		Href:        "http://node1.local:80/",
		API: NodeAPI{
			Versions:  []string{"v1.3"},
			Endpoints: []APIEndpoint{{Host: "node1.local", Port: 80, Protocol: "http"}},
		},
		Clocks: []Clock{
			{Name: "clk0", RefType: "internal"},
			{Name: "clk1", RefType: "ptp", Traceable: boolPtr(true), Version: strPtr("IEEE1588-2008"), GMID: strPtr("00-11-22-ff-fe-33-44-55"), Locked: boolPtr(true)},
		},
		Interfaces: []Interface{
			{Name: "eth0", ChassisID: "5e-21-51-e3-62-d0", PortID: "5e-21-51-e3-62-d0"},
		},
		Services: []interface{}{},
	}

	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Node
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != node.ID {
		t.Errorf("ID: got %q, want %q", got.ID, node.ID)
	}
	if got.Label != node.Label {
		t.Errorf("Label: got %q, want %q", got.Label, node.Label)
	}
	if got.Hostname != node.Hostname {
		t.Errorf("Hostname: got %q, want %q", got.Hostname, node.Hostname)
	}
	if len(got.API.Versions) != 1 || got.API.Versions[0] != "v1.3" {
		t.Errorf("API.Versions: got %v", got.API.Versions)
	}
	if len(got.Clocks) != 2 {
		t.Errorf("Clocks: got %d, want 2", len(got.Clocks))
	}
	if got.Clocks[1].RefType != "ptp" {
		t.Errorf("Clocks[1].RefType: got %q, want %q", got.Clocks[1].RefType, "ptp")
	}
	if got.Clocks[1].Traceable == nil || !*got.Clocks[1].Traceable {
		t.Errorf("Clocks[1].Traceable: expected true")
	}
	if len(got.Interfaces) != 1 {
		t.Errorf("Interfaces: got %d, want 1", len(got.Interfaces))
	}
	if got.Href != node.Href {
		t.Errorf("Href: got %q, want %q", got.Href, node.Href)
	}

	// Verify snake_case in raw JSON.
	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, key := range []string{"id", "version", "label", "description", "hostname", "tags", "caps", "href", "api", "clocks", "interfaces", "services"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing snake_case key %q in JSON", key)
		}
	}
}

func TestDeviceJSON(t *testing.T) {
	dev := Device{
		ID:          "b1a13222-3334-4d05-b4f7-a0aeedaa221c",
		Version:     NewVersion(),
		Label:       "Switcher Device",
		Description: "A switching device",
		Tags:        map[string][]string{},
		Type:        DeviceTypePipeline,
		NodeID:      "d1713110-7343-4d05-b4f7-a0aeedaa221c",
		Senders:     []string{"sender-1", "sender-2"},
		Receivers:   []string{"receiver-1"},
		Controls: []Control{
			{Type: ControlTypeSRCtrl, Href: "http://node1.local:80/x-nmos/connection/v1.1"},
		},
	}

	data, err := json.Marshal(dev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Device
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != dev.ID {
		t.Errorf("ID mismatch")
	}
	if got.Type != DeviceTypePipeline {
		t.Errorf("Type: got %q, want %q", got.Type, DeviceTypePipeline)
	}
	if got.NodeID != dev.NodeID {
		t.Errorf("NodeID mismatch")
	}
	if len(got.Senders) != 2 {
		t.Errorf("Senders: got %d, want 2", len(got.Senders))
	}
	if len(got.Controls) != 1 || got.Controls[0].Type != ControlTypeSRCtrl {
		t.Errorf("Controls mismatch")
	}

	// Verify snake_case keys.
	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, key := range []string{"id", "version", "label", "description", "tags", "type", "node_id", "senders", "receivers", "controls"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing snake_case key %q in JSON", key)
		}
	}
}

func TestFlowVideoJSON(t *testing.T) {
	flow := Flow{
		ID:          "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Version:     NewVersion(),
		Label:       "Video Flow",
		Description: "1080i59.94 video",
		Tags:        map[string][]string{},
		Format:      FormatVideo,
		SourceID:    "src-001",
		DeviceID:    "dev-001",
		Parents:     []string{},
		MediaType:   "video/raw",

		FrameWidth:    1920,
		FrameHeight:   1080,
		InterlaceMode: "interlaced_tff",
		Colorspace:    "BT709",
		Components: []Component{
			{Name: "Y", Width: 1920, Height: 1080, BitDepth: 10},
			{Name: "Cb", Width: 960, Height: 1080, BitDepth: 10},
			{Name: "Cr", Width: 960, Height: 1080, BitDepth: 10},
		},
		GrainRate: &Rational{Numerator: 60000, Denominator: 1001},
	}

	data, err := json.Marshal(flow)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Flow
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.FrameWidth != 1920 {
		t.Errorf("FrameWidth: got %d, want 1920", got.FrameWidth)
	}
	if got.FrameHeight != 1080 {
		t.Errorf("FrameHeight: got %d, want 1080", got.FrameHeight)
	}
	if got.InterlaceMode != "interlaced_tff" {
		t.Errorf("InterlaceMode: got %q", got.InterlaceMode)
	}
	if got.Colorspace != "BT709" {
		t.Errorf("Colorspace: got %q", got.Colorspace)
	}
	if len(got.Components) != 3 {
		t.Errorf("Components: got %d, want 3", len(got.Components))
	}
	if got.GrainRate == nil || got.GrainRate.Numerator != 60000 || got.GrainRate.Denominator != 1001 {
		t.Errorf("GrainRate mismatch: %+v", got.GrainRate)
	}
	if got.Format != FormatVideo {
		t.Errorf("Format: got %q, want %q", got.Format, FormatVideo)
	}

	// Verify snake_case for video-specific fields.
	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, key := range []string{"frame_width", "frame_height", "interlace_mode", "colorspace", "components", "grain_rate", "source_id", "device_id", "media_type"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing snake_case key %q in JSON", key)
		}
	}
}

func TestSenderJSON(t *testing.T) {
	recvID := "recv-001"
	sender := Sender{
		ID:                "s-001",
		Version:           NewVersion(),
		Label:             "Video Sender",
		Description:       "Sends video",
		Tags:              map[string][]string{},
		FlowID:            "f-001",
		Transport:         TransportRTP,
		DeviceID:          "dev-001",
		ManifestHref:      "http://node1.local/sdp/s-001",
		InterfaceBindings: []string{"eth0"},
		Subscription: SenderSubscription{
			ReceiverID: &recvID,
			Active:     true,
		},
	}

	data, err := json.Marshal(sender)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Sender
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != sender.ID {
		t.Errorf("ID mismatch")
	}
	if got.Transport != TransportRTP {
		t.Errorf("Transport: got %q, want %q", got.Transport, TransportRTP)
	}
	if got.ManifestHref != sender.ManifestHref {
		t.Errorf("ManifestHref mismatch")
	}
	if got.Subscription.ReceiverID == nil || *got.Subscription.ReceiverID != recvID {
		t.Errorf("Subscription.ReceiverID mismatch")
	}
	if !got.Subscription.Active {
		t.Errorf("Subscription.Active should be true")
	}

	// Verify snake_case.
	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, key := range []string{"flow_id", "device_id", "manifest_href", "interface_bindings", "subscription"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing snake_case key %q in JSON", key)
		}
	}
}

func TestReceiverJSON(t *testing.T) {
	recv := Receiver{
		ID:          "r-001",
		Version:     NewVersion(),
		Label:       "Video Receiver",
		Description: "Receives video",
		Tags:        map[string][]string{},
		Format:      FormatVideo,
		Caps: ReceiverCaps{
			MediaTypes: []string{"video/raw"},
		},
		Transport:         TransportRTPMcast,
		DeviceID:          "dev-001",
		InterfaceBindings: []string{"eth0"},
		Subscription: ReceiverSubscription{
			SenderID: nil,
			Active:   false,
		},
	}

	data, err := json.Marshal(recv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Receiver
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != recv.ID {
		t.Errorf("ID mismatch")
	}
	if got.Format != FormatVideo {
		t.Errorf("Format: got %q, want %q", got.Format, FormatVideo)
	}
	if got.Transport != TransportRTPMcast {
		t.Errorf("Transport: got %q, want %q", got.Transport, TransportRTPMcast)
	}
	if got.Subscription.SenderID != nil {
		t.Errorf("Subscription.SenderID should be nil")
	}
	if got.Subscription.Active {
		t.Errorf("Subscription.Active should be false")
	}
	if len(got.Caps.MediaTypes) != 1 || got.Caps.MediaTypes[0] != "video/raw" {
		t.Errorf("Caps.MediaTypes mismatch: %v", got.Caps.MediaTypes)
	}

	// Verify snake_case.
	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, key := range []string{"interface_bindings", "device_id"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing snake_case key %q in JSON", key)
		}
	}

	// Verify subscription has sender_id key (even when nil).
	var subRaw map[string]json.RawMessage
	if err := json.Unmarshal(raw["subscription"], &subRaw); err != nil {
		t.Fatalf("unmarshal subscription: %v", err)
	}
	if _, ok := subRaw["sender_id"]; !ok {
		t.Errorf("missing sender_id in subscription JSON")
	}
	if _, ok := subRaw["active"]; !ok {
		t.Errorf("missing active in subscription JSON")
	}
}

func TestFlowAudioJSON(t *testing.T) {
	flow := Flow{
		ID:          "a1b2c3d4-5678-4abc-9def-0123456789ab",
		Version:     NewVersion(),
		Label:       "Audio Flow",
		Description: "48kHz 24-bit audio",
		Tags:        map[string][]string{},
		Format:      FormatAudio,
		SourceID:    "src-audio-001",
		DeviceID:    "dev-001",
		Parents:     []string{},
		MediaType:   "audio/L24",
		SampleRate:  &Rational{Numerator: 48000, Denominator: 1},
		BitDepth:    24,
	}

	data, err := json.Marshal(flow)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Flow
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Format != FormatAudio {
		t.Errorf("Format: got %q, want %q", got.Format, FormatAudio)
	}
	if got.SampleRate == nil || got.SampleRate.Numerator != 48000 || got.SampleRate.Denominator != 1 {
		t.Errorf("SampleRate mismatch: %+v", got.SampleRate)
	}
	if got.BitDepth != 24 {
		t.Errorf("BitDepth: got %d, want 24", got.BitDepth)
	}
	if got.MediaType != "audio/L24" {
		t.Errorf("MediaType: got %q, want %q", got.MediaType, "audio/L24")
	}

	// Verify snake_case and omitempty: audio fields present, video fields absent.
	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, key := range []string{"sample_rate", "bit_depth", "source_id", "device_id", "media_type"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing expected key %q in JSON", key)
		}
	}
	// Video-specific fields should be omitted (omitempty with zero values).
	for _, key := range []string{"frame_width", "frame_height", "interlace_mode", "colorspace", "components", "grain_rate"} {
		if _, ok := raw[key]; ok {
			t.Errorf("video-specific key %q should be omitted for audio flow", key)
		}
	}
}

func TestSourceJSON(t *testing.T) {
	src := Source{
		ID:          "s1a2b3c4-d5e6-4f78-9012-abcdef012345",
		Version:     NewVersion(),
		Label:       "Audio Source",
		Description: "A stereo audio source",
		Tags:        map[string][]string{"studio": {"main"}},
		Caps:        map[string]interface{}{},
		Format:      FormatAudio,
		Parents:     []string{},
		DeviceID:    "dev-001",
		ClockName:   "clk0",
		Channels: []SourceChannel{
			{Label: "Left", Symbol: "L"},
			{Label: "Right", Symbol: "R"},
		},
	}

	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Source
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != src.ID {
		t.Errorf("ID mismatch")
	}
	if got.ClockName != "clk0" {
		t.Errorf("ClockName: got %q, want %q", got.ClockName, "clk0")
	}
	if got.Format != FormatAudio {
		t.Errorf("Format: got %q, want %q", got.Format, FormatAudio)
	}
	if got.DeviceID != "dev-001" {
		t.Errorf("DeviceID: got %q, want %q", got.DeviceID, "dev-001")
	}
	if len(got.Channels) != 2 {
		t.Fatalf("Channels: got %d, want 2", len(got.Channels))
	}
	if got.Channels[0].Label != "Left" || got.Channels[0].Symbol != "L" {
		t.Errorf("Channels[0]: got %+v", got.Channels[0])
	}
	if got.Channels[1].Label != "Right" || got.Channels[1].Symbol != "R" {
		t.Errorf("Channels[1]: got %+v", got.Channels[1])
	}

	// Verify snake_case in raw JSON.
	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, key := range []string{"id", "version", "label", "description", "tags", "caps", "format", "parents", "device_id", "clock_name", "channels"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing snake_case key %q in JSON", key)
		}
	}
}

// helper
func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }
