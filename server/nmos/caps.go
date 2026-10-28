package nmos

// Further parameter constraints a Sender's caps state, from the NMOS
// capabilities register.
const (
	CapInterlaceMode = "urn:x-nmos:cap:format:interlace_mode"
	CapColorspace    = "urn:x-nmos:cap:format:colorspace"
	CapChannelCount  = "urn:x-nmos:cap:format:channel_count"
	CapSampleDepth   = "urn:x-nmos:cap:format:sample_depth"

	CapColorSampling  = "urn:x-nmos:cap:format:color_sampling"
	CapComponentDepth = "urn:x-nmos:cap:format:component_depth"
	CapPacketTime     = "urn:x-nmos:cap:transport:packet_time"
)

// SenderCaps is a Sender's caps per BCP-004-02: one constraint set stating
// the flow f the Sender writes, which is the only stream it produces, and the
// version the caps were stated at. channels is the audio channel count, which
// a Flow does not carry; its Source lists the channels.
//
// A Flow attribute left unset is left out, and an absent interlace_mode is
// stated as progressive, its IS-04 default.
func SenderCaps(f *Flow, channels int, version Version) map[string]any {
	one := func(v any) map[string]any { return map[string]any{"enum": []any{v}} }
	set := map[string]any{CapMediaType: one(f.MediaType)}
	if f.GrainRate != nil {
		set[CapGrainRate] = one(*f.GrainRate)
	}
	switch f.Format {
	case FormatVideo:
		if f.FrameWidth > 0 {
			set[CapFrameWidth] = one(f.FrameWidth)
		}
		if f.FrameHeight > 0 {
			set[CapFrameHeight] = one(f.FrameHeight)
		}
		interlace := f.InterlaceMode
		if interlace == "" {
			interlace = "progressive"
		}
		set[CapInterlaceMode] = one(interlace)
		if f.Colorspace != "" {
			set[CapColorspace] = one(f.Colorspace)
		}
	case FormatAudio:
		if f.SampleRate != nil {
			set[CapSampleRate] = one(*f.SampleRate)
		}
		if channels > 0 {
			set[CapChannelCount] = one(channels)
		}
		if f.BitDepth > 0 {
			set[CapSampleDepth] = one(f.BitDepth)
		}
	}
	return map[string]any{
		"constraint_sets": []map[string]any{set},
		"version":         version,
	}
}

// ST2110SenderCaps is SenderCaps for an ST 2110 Sender, which also states
// what its transmitted stream fixes whatever the flow: a picture goes out as
// 10-bit YCbCr 4:2:2 and audio as packets of packetTimeMs milliseconds.
func ST2110SenderCaps(f *Flow, channels int, packetTimeMs float64, version Version) map[string]any {
	caps := SenderCaps(f, channels, version)
	set := caps["constraint_sets"].([]map[string]any)[0]
	one := func(v any) map[string]any { return map[string]any{"enum": []any{v}} }
	switch f.Format {
	case FormatVideo:
		set[CapColorSampling] = one("YCbCr-4:2:2")
		set[CapComponentDepth] = one(10)
	case FormatAudio:
		set[CapPacketTime] = one(packetTimeMs)
	}
	return caps
}
