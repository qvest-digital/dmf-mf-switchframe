package nmos

import (
	"reflect"
	"testing"
)

// An ST 2110 Sender's caps state the flow it sends and what its transmitted
// stream fixes whatever the flow: 10-bit YCbCr 4:2:2 for a picture, the
// packet time for audio. The Flow it references satisfies every constraint.
func TestST2110SenderCapsStateTheTransmittedStream(t *testing.T) {
	enum := func(v ...any) map[string]any { return map[string]any{"enum": v} }
	video := &Flow{Format: FormatVideo, MediaType: "video/raw", FrameWidth: 1920, FrameHeight: 1080,
		Colorspace: "BT709", GrainRate: &Rational{Numerator: 50, Denominator: 1}}
	audio := &Flow{Format: FormatAudio, MediaType: "audio/L24",
		SampleRate: &Rational{Numerator: 48000, Denominator: 1}, BitDepth: 24}

	for _, tc := range []struct {
		name     string
		flow     *Flow
		channels int
		want     map[string]any
	}{
		{"video", video, 0, map[string]any{
			CapMediaType:      enum("video/raw"),
			CapGrainRate:      enum(Rational{Numerator: 50, Denominator: 1}),
			CapFrameWidth:     enum(1920),
			CapFrameHeight:    enum(1080),
			CapInterlaceMode:  enum("progressive"),
			CapColorspace:     enum("BT709"),
			CapColorSampling:  enum("YCbCr-4:2:2"),
			CapComponentDepth: enum(10),
		}},
		{"audio", audio, 2, map[string]any{
			CapMediaType:    enum("audio/L24"),
			CapSampleRate:   enum(Rational{Numerator: 48000, Denominator: 1}),
			CapChannelCount: enum(2),
			CapSampleDepth:  enum(24),
			CapPacketTime:   enum(float64(1)),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := NewVersion()
			caps := ST2110SenderCaps(tc.flow, tc.channels, 1, v)
			if caps["version"] != v {
				t.Errorf("caps version %v, want %v", caps["version"], v)
			}
			sets := caps["constraint_sets"].([]map[string]any)
			if len(sets) != 1 {
				t.Fatalf("%d constraint sets, want one", len(sets))
			}
			if !reflect.DeepEqual(sets[0], tc.want) {
				t.Errorf("constraint set\n got %v\nwant %v", sets[0], tc.want)
			}
		})
	}
}

// The MXL Sender states the flow alone: nothing about the wire is fixed by
// writing a flow into a domain.
func TestSenderCapsStateNoTransmission(t *testing.T) {
	f := &Flow{Format: FormatVideo, MediaType: "video/v210", FrameWidth: 1920, FrameHeight: 1080}
	set := SenderCaps(f, 0, NewVersion())["constraint_sets"].([]map[string]any)[0]
	for _, k := range []string{CapColorSampling, CapComponentDepth, CapPacketTime} {
		if _, ok := set[k]; ok {
			t.Errorf("an MXL sender states %s", k)
		}
	}
}
