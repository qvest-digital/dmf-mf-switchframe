package nmos

import (
	"strings"
	"testing"
)

func TestGenerateVideoSDP(t *testing.T) {
	params := SDPParams{
		SessionName:    "Cam 1 Video",
		SourceIP:       "192.168.1.10",
		MulticastIP:    "239.1.0.1",
		Port:           5004,
		PayloadType:    96,
		PTPGrandmaster: "08-00-11-ff-fe-21-e1-b0",
		PTPDomain:      127,
		Video: &SDPVideoParams{
			Sampling:    "YCbCr-4:2:2",
			Width:       1920,
			Height:      1080,
			Depth:       10,
			Colorimetry: "BT709",
			ExactFPS:    "30000/1001",
			Interlace:   false,
			PackingMode: "2110GPM",
			SenderType:  "2110TPN",
		},
	}

	sdp, err := GenerateSDP(params)
	if err != nil {
		t.Fatalf("GenerateSDP failed: %v", err)
	}

	// Check version line
	if !strings.HasPrefix(sdp, "v=0\r\n") {
		t.Error("SDP must start with v=0")
	}

	// Check session name
	if !strings.Contains(sdp, "s=Cam 1 Video\r\n") {
		t.Error("missing session name")
	}

	// Check origin line has source IP
	if !strings.Contains(sdp, "IN IP4 192.168.1.10\r\n") {
		t.Error("origin line missing source IP")
	}

	// Check media line
	if !strings.Contains(sdp, "m=video 5004 RTP/AVP 96\r\n") {
		t.Error("wrong media line")
	}

	// Check connection line
	if !strings.Contains(sdp, "c=IN IP4 239.1.0.1/64\r\n") {
		t.Error("wrong connection line")
	}

	// Check source-filter
	if !strings.Contains(sdp, "a=source-filter: incl IN IP4 239.1.0.1 192.168.1.10\r\n") {
		t.Error("missing source-filter")
	}

	// Check rtpmap
	if !strings.Contains(sdp, "a=rtpmap:96 raw/90000\r\n") {
		t.Error("wrong rtpmap for video")
	}

	// Check fmtp contains required params
	fmtpLine := ""
	for _, line := range strings.Split(sdp, "\r\n") {
		if strings.HasPrefix(line, "a=fmtp:96") {
			fmtpLine = line
			break
		}
	}
	if fmtpLine == "" {
		t.Fatal("missing fmtp line")
	}

	fmtpChecks := []string{
		"sampling=YCbCr-4:2:2",
		"width=1920",
		"height=1080",
		"depth=10",
		"exactframerate=30000/1001",
		"TCS=SDR",
		"colorimetry=BT709",
		"PM=2110GPM",
		"SSN=ST2110-20:2017",
		"TP=2110TPN",
		"PAR=1:1",
	}
	for _, check := range fmtpChecks {
		if !strings.Contains(fmtpLine, check) {
			t.Errorf("fmtp missing %s, got: %s", check, fmtpLine)
		}
	}

	// Progressive — should not contain interlace
	if strings.Contains(fmtpLine, "interlace") {
		t.Error("progressive SDP should not contain interlace")
	}

	// Check ts-refclk
	if !strings.Contains(sdp, "a=ts-refclk:ptp=IEEE1588-2008:08-00-11-ff-fe-21-e1-b0:127\r\n") {
		t.Error("missing or wrong ts-refclk")
	}

	// Check mediaclk
	if !strings.Contains(sdp, "a=mediaclk:direct=0\r\n") {
		t.Error("missing mediaclk for video")
	}
}

func TestGenerateVideoSDP_Interlaced(t *testing.T) {
	params := SDPParams{
		SessionName:    "Cam 2 Video",
		SourceIP:       "192.168.1.11",
		MulticastIP:    "239.1.0.2",
		Port:           5004,
		PayloadType:    96,
		PTPGrandmaster: "08-00-11-ff-fe-21-e1-b0",
		PTPDomain:      0,
		Video: &SDPVideoParams{
			Sampling:    "YCbCr-4:2:2",
			Width:       1920,
			Height:      1080,
			Depth:       10,
			Colorimetry: "BT709",
			ExactFPS:    "30000/1001",
			Interlace:   true,
			PackingMode: "2110GPM",
			SenderType:  "2110TPN",
		},
	}

	sdp, err := GenerateSDP(params)
	if err != nil {
		t.Fatalf("GenerateSDP failed: %v", err)
	}

	fmtpLine := ""
	for _, line := range strings.Split(sdp, "\r\n") {
		if strings.HasPrefix(line, "a=fmtp:96") {
			fmtpLine = line
			break
		}
	}
	if fmtpLine == "" {
		t.Fatal("missing fmtp line")
	}
	if !strings.Contains(fmtpLine, "interlace") {
		t.Error("interlaced SDP must contain interlace in fmtp")
	}
}

func TestGenerateAudioSDP(t *testing.T) {
	params := SDPParams{
		SessionName:    "Cam 1 Audio",
		SourceIP:       "192.168.1.10",
		MulticastIP:    "239.1.0.10",
		Port:           5006,
		PayloadType:    97,
		PTPGrandmaster: "08-00-11-ff-fe-21-e1-b0",
		PTPDomain:      127,
		Audio: &SDPAudioParams{
			BitDepth:     24,
			SampleRate:   48000,
			Channels:     2,
			PacketTimeMs: 1.0,
			ChannelOrder: "SMPTE2110.(ST)",
		},
	}

	sdp, err := GenerateSDP(params)
	if err != nil {
		t.Fatalf("GenerateSDP failed: %v", err)
	}

	// Check media line for audio
	if !strings.Contains(sdp, "m=audio 5006 RTP/AVP 97\r\n") {
		t.Error("wrong media line for audio")
	}

	// Check rtpmap: L24/48000/2
	if !strings.Contains(sdp, "a=rtpmap:97 L24/48000/2\r\n") {
		t.Error("wrong rtpmap for audio")
	}

	// Check ptime
	if !strings.Contains(sdp, "a=ptime:1\r\n") {
		t.Error("missing ptime")
	}

	// Check framecount: 48000 * 0.001 = 48
	if !strings.Contains(sdp, "a=framecount:48\r\n") {
		t.Error("missing or wrong framecount")
	}

	// Check channel-order in fmtp
	if !strings.Contains(sdp, "a=fmtp:97 channel-order=SMPTE2110.(ST)\r\n") {
		t.Error("missing channel-order fmtp")
	}

	// Check mediaclk has rate
	if !strings.Contains(sdp, "a=mediaclk:direct=0 rate=48000\r\n") {
		t.Error("missing mediaclk with rate for audio")
	}
}

func TestGenerateAudioSDP_NoChannelOrder(t *testing.T) {
	params := SDPParams{
		SessionName:    "Audio No Channel Order",
		SourceIP:       "192.168.1.10",
		MulticastIP:    "239.1.0.11",
		Port:           5008,
		PayloadType:    97,
		PTPGrandmaster: "08-00-11-ff-fe-21-e1-b0",
		PTPDomain:      0,
		Audio: &SDPAudioParams{
			BitDepth:     24,
			SampleRate:   48000,
			Channels:     8,
			PacketTimeMs: 0.125,
		},
	}

	sdp, err := GenerateSDP(params)
	if err != nil {
		t.Fatalf("GenerateSDP failed: %v", err)
	}

	// Should not have fmtp line when no channel order
	for _, line := range strings.Split(sdp, "\r\n") {
		if strings.HasPrefix(line, "a=fmtp:") {
			t.Error("should not have fmtp line when ChannelOrder is empty")
		}
	}

	// Check rtpmap has 8 channels
	if !strings.Contains(sdp, "a=rtpmap:97 L24/48000/8\r\n") {
		t.Error("wrong rtpmap for 8-channel audio")
	}
}

func TestGenerateAncillarySDP(t *testing.T) {
	params := SDPParams{
		SessionName:    "Cam 1 Ancillary",
		SourceIP:       "192.168.1.10",
		MulticastIP:    "239.1.0.20",
		Port:           5010,
		PayloadType:    100,
		PTPGrandmaster: "08-00-11-ff-fe-21-e1-b0",
		PTPDomain:      127,
		Ancillary: &SDPAncillaryParams{
			DIDSDIDs: [][2]byte{{0x61, 0x02}, {0x41, 0x07}},
		},
	}

	sdp, err := GenerateSDP(params)
	if err != nil {
		t.Fatalf("GenerateSDP failed: %v", err)
	}

	// Ancillary uses "video" as media type per RFC 8331
	if !strings.Contains(sdp, "m=video 5010 RTP/AVP 100\r\n") {
		t.Error("ancillary must use video as media type")
	}

	// Check rtpmap
	if !strings.Contains(sdp, "a=rtpmap:100 smpte291/90000\r\n") {
		t.Error("wrong rtpmap for ancillary")
	}

	// Check fmtp with DID_SDID pairs
	fmtpLine := ""
	for _, line := range strings.Split(sdp, "\r\n") {
		if strings.HasPrefix(line, "a=fmtp:100") {
			fmtpLine = line
			break
		}
	}
	if fmtpLine == "" {
		t.Fatal("missing fmtp line for ancillary")
	}
	if !strings.Contains(fmtpLine, "DID_SDID={0x61,0x02}") {
		t.Errorf("missing first DID_SDID pair, got: %s", fmtpLine)
	}
	if !strings.Contains(fmtpLine, "DID_SDID={0x41,0x07}") {
		t.Errorf("missing second DID_SDID pair, got: %s", fmtpLine)
	}
}

func TestParseSDP(t *testing.T) {
	raw := "v=0\r\n" +
		"o=- 1700000000 1700000000 IN IP4 192.168.1.10\r\n" +
		"s=Test Video\r\n" +
		"t=0 0\r\n" +
		"m=video 5004 RTP/AVP 96\r\n" +
		"c=IN IP4 239.1.0.1/64\r\n" +
		"a=source-filter: incl IN IP4 239.1.0.1 192.168.1.10\r\n" +
		"a=rtpmap:96 raw/90000\r\n" +
		"a=fmtp:96 sampling=YCbCr-4:2:2; width=1920; height=1080; depth=10; exactframerate=30000/1001; TCS=SDR; colorimetry=BT709; PM=2110GPM; SSN=ST2110-20:2017; TP=2110TPN; PAR=1:1\r\n" +
		"a=mediaclk:direct=0\r\n" +
		"a=ts-refclk:ptp=IEEE1588-2008:08-00-11-ff-fe-21-e1-b0:127\r\n"

	p, err := ParseSDP(raw)
	if err != nil {
		t.Fatalf("ParseSDP failed: %v", err)
	}

	if p.SessionName != "Test Video" {
		t.Errorf("SessionName = %q, want %q", p.SessionName, "Test Video")
	}
	if p.SourceIP != "192.168.1.10" {
		t.Errorf("SourceIP = %q, want %q", p.SourceIP, "192.168.1.10")
	}
	if p.MulticastIP != "239.1.0.1" {
		t.Errorf("MulticastIP = %q, want %q", p.MulticastIP, "239.1.0.1")
	}
	if p.Port != 5004 {
		t.Errorf("Port = %d, want %d", p.Port, 5004)
	}
	if p.PayloadType != 96 {
		t.Errorf("PayloadType = %d, want %d", p.PayloadType, 96)
	}
	if p.PTPGrandmaster != "08-00-11-ff-fe-21-e1-b0" {
		t.Errorf("PTPGrandmaster = %q, want %q", p.PTPGrandmaster, "08-00-11-ff-fe-21-e1-b0")
	}
	if p.PTPDomain != 127 {
		t.Errorf("PTPDomain = %d, want %d", p.PTPDomain, 127)
	}

	if p.Video == nil {
		t.Fatal("Video params should not be nil")
	}
	if p.Video.Sampling != "YCbCr-4:2:2" {
		t.Errorf("Sampling = %q, want %q", p.Video.Sampling, "YCbCr-4:2:2")
	}
	if p.Video.Width != 1920 {
		t.Errorf("Width = %d, want %d", p.Video.Width, 1920)
	}
	if p.Video.Height != 1080 {
		t.Errorf("Height = %d, want %d", p.Video.Height, 1080)
	}
	if p.Video.Depth != 10 {
		t.Errorf("Depth = %d, want %d", p.Video.Depth, 10)
	}
	if p.Video.ExactFPS != "30000/1001" {
		t.Errorf("ExactFPS = %q, want %q", p.Video.ExactFPS, "30000/1001")
	}
	if p.Video.Colorimetry != "BT709" {
		t.Errorf("Colorimetry = %q, want %q", p.Video.Colorimetry, "BT709")
	}
	if p.Video.PackingMode != "2110GPM" {
		t.Errorf("PackingMode = %q, want %q", p.Video.PackingMode, "2110GPM")
	}
	if p.Video.SenderType != "2110TPN" {
		t.Errorf("SenderType = %q, want %q", p.Video.SenderType, "2110TPN")
	}
	if p.Video.Interlace {
		t.Error("Interlace should be false for progressive")
	}
	if p.Audio != nil {
		t.Error("Audio should be nil for video SDP")
	}
	if p.Ancillary != nil {
		t.Error("Ancillary should be nil for video SDP")
	}
}

func TestParseSDP_Interlaced(t *testing.T) {
	raw := "v=0\r\n" +
		"o=- 1700000000 1700000000 IN IP4 192.168.1.10\r\n" +
		"s=Interlaced\r\n" +
		"t=0 0\r\n" +
		"m=video 5004 RTP/AVP 96\r\n" +
		"c=IN IP4 239.1.0.1/64\r\n" +
		"a=source-filter: incl IN IP4 239.1.0.1 192.168.1.10\r\n" +
		"a=rtpmap:96 raw/90000\r\n" +
		"a=fmtp:96 sampling=YCbCr-4:2:2; width=1920; height=1080; depth=10; exactframerate=30000/1001; interlace; TCS=SDR; colorimetry=BT709; PM=2110GPM; SSN=ST2110-20:2017; TP=2110TPN\r\n" +
		"a=ts-refclk:ptp=IEEE1588-2008:08-00-11-ff-fe-21-e1-b0:0\r\n"

	p, err := ParseSDP(raw)
	if err != nil {
		t.Fatalf("ParseSDP failed: %v", err)
	}
	if p.Video == nil {
		t.Fatal("Video params should not be nil")
	}
	if !p.Video.Interlace {
		t.Error("Interlace should be true")
	}
}

func TestParseAudioSDP(t *testing.T) {
	raw := "v=0\r\n" +
		"o=- 1700000000 1700000000 IN IP4 192.168.1.10\r\n" +
		"s=Audio\r\n" +
		"t=0 0\r\n" +
		"m=audio 5006 RTP/AVP 97\r\n" +
		"c=IN IP4 239.1.0.10/64\r\n" +
		"a=source-filter: incl IN IP4 239.1.0.10 192.168.1.10\r\n" +
		"a=rtpmap:97 L24/48000/2\r\n" +
		"a=fmtp:97 channel-order=SMPTE2110.(ST)\r\n" +
		"a=ptime:1\r\n" +
		"a=framecount:48\r\n" +
		"a=mediaclk:direct=0 rate=48000\r\n" +
		"a=ts-refclk:ptp=IEEE1588-2008:08-00-11-ff-fe-21-e1-b0:127\r\n"

	p, err := ParseSDP(raw)
	if err != nil {
		t.Fatalf("ParseSDP failed: %v", err)
	}

	if p.Audio == nil {
		t.Fatal("Audio params should not be nil")
	}
	if p.Audio.BitDepth != 24 {
		t.Errorf("BitDepth = %d, want %d", p.Audio.BitDepth, 24)
	}
	if p.Audio.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want %d", p.Audio.SampleRate, 48000)
	}
	if p.Audio.Channels != 2 {
		t.Errorf("Channels = %d, want %d", p.Audio.Channels, 2)
	}
	if p.Audio.PacketTimeMs != 1.0 {
		t.Errorf("PacketTimeMs = %f, want %f", p.Audio.PacketTimeMs, 1.0)
	}
	if p.Audio.ChannelOrder != "SMPTE2110.(ST)" {
		t.Errorf("ChannelOrder = %q, want %q", p.Audio.ChannelOrder, "SMPTE2110.(ST)")
	}
	if p.Video != nil {
		t.Error("Video should be nil for audio SDP")
	}
}

func TestParseAudioSDP_L16(t *testing.T) {
	raw := "v=0\r\n" +
		"o=- 1700000000 1700000000 IN IP4 192.168.1.10\r\n" +
		"s=Audio 16bit\r\n" +
		"t=0 0\r\n" +
		"m=audio 5006 RTP/AVP 97\r\n" +
		"c=IN IP4 239.1.0.10/64\r\n" +
		"a=rtpmap:97 L16/48000/8\r\n" +
		"a=ptime:1\r\n" +
		"a=ts-refclk:ptp=IEEE1588-2008:08-00-11-ff-fe-21-e1-b0:0\r\n"

	p, err := ParseSDP(raw)
	if err != nil {
		t.Fatalf("ParseSDP failed: %v", err)
	}

	if p.Audio == nil {
		t.Fatal("Audio params should not be nil")
	}
	if p.Audio.BitDepth != 16 {
		t.Errorf("BitDepth = %d, want %d", p.Audio.BitDepth, 16)
	}
	if p.Audio.Channels != 8 {
		t.Errorf("Channels = %d, want %d", p.Audio.Channels, 8)
	}
}

func TestParseAncillarySDP(t *testing.T) {
	raw := "v=0\r\n" +
		"o=- 1700000000 1700000000 IN IP4 192.168.1.10\r\n" +
		"s=Ancillary\r\n" +
		"t=0 0\r\n" +
		"m=video 5010 RTP/AVP 100\r\n" +
		"c=IN IP4 239.1.0.20/64\r\n" +
		"a=rtpmap:100 smpte291/90000\r\n" +
		"a=fmtp:100 DID_SDID={0x61,0x02};DID_SDID={0x41,0x07}\r\n" +
		"a=ts-refclk:ptp=IEEE1588-2008:08-00-11-ff-fe-21-e1-b0:127\r\n"

	p, err := ParseSDP(raw)
	if err != nil {
		t.Fatalf("ParseSDP failed: %v", err)
	}

	if p.Ancillary == nil {
		t.Fatal("Ancillary params should not be nil")
	}
	if len(p.Ancillary.DIDSDIDs) != 2 {
		t.Fatalf("DIDSDIDs len = %d, want 2", len(p.Ancillary.DIDSDIDs))
	}
	if p.Ancillary.DIDSDIDs[0] != [2]byte{0x61, 0x02} {
		t.Errorf("DIDSDIDs[0] = %v, want [0x61, 0x02]", p.Ancillary.DIDSDIDs[0])
	}
	if p.Ancillary.DIDSDIDs[1] != [2]byte{0x41, 0x07} {
		t.Errorf("DIDSDIDs[1] = %v, want [0x41, 0x07]", p.Ancillary.DIDSDIDs[1])
	}
	if p.Video != nil {
		t.Error("Video should be nil for ancillary SDP")
	}
	if p.Audio != nil {
		t.Error("Audio should be nil for ancillary SDP")
	}
}

func TestSDPRoundTrip(t *testing.T) {
	original := SDPParams{
		SessionName:    "RoundTrip Video",
		SourceIP:       "10.0.0.5",
		MulticastIP:    "239.10.20.1",
		Port:           5004,
		PayloadType:    96,
		PTPGrandmaster: "00-11-22-ff-fe-33-44-55",
		PTPDomain:      42,
		Video: &SDPVideoParams{
			Sampling:    "YCbCr-4:2:2",
			Width:       3840,
			Height:      2160,
			Depth:       10,
			Colorimetry: "BT2020",
			ExactFPS:    "60",
			Interlace:   false,
			PackingMode: "2110GPM",
			SenderType:  "2110TPNL",
		},
	}

	sdp, err := GenerateSDP(original)
	if err != nil {
		t.Fatalf("GenerateSDP failed: %v", err)
	}
	parsed, err := ParseSDP(sdp)
	if err != nil {
		t.Fatalf("ParseSDP round-trip failed: %v", err)
	}

	// Verify all fields survived the round-trip
	if parsed.SessionName != original.SessionName {
		t.Errorf("SessionName: got %q, want %q", parsed.SessionName, original.SessionName)
	}
	if parsed.SourceIP != original.SourceIP {
		t.Errorf("SourceIP: got %q, want %q", parsed.SourceIP, original.SourceIP)
	}
	if parsed.MulticastIP != original.MulticastIP {
		t.Errorf("MulticastIP: got %q, want %q", parsed.MulticastIP, original.MulticastIP)
	}
	if parsed.Port != original.Port {
		t.Errorf("Port: got %d, want %d", parsed.Port, original.Port)
	}
	if parsed.PayloadType != original.PayloadType {
		t.Errorf("PayloadType: got %d, want %d", parsed.PayloadType, original.PayloadType)
	}
	if parsed.PTPGrandmaster != original.PTPGrandmaster {
		t.Errorf("PTPGrandmaster: got %q, want %q", parsed.PTPGrandmaster, original.PTPGrandmaster)
	}
	if parsed.PTPDomain != original.PTPDomain {
		t.Errorf("PTPDomain: got %d, want %d", parsed.PTPDomain, original.PTPDomain)
	}

	if parsed.Video == nil {
		t.Fatal("Video should not be nil after round-trip")
	}
	if parsed.Video.Sampling != original.Video.Sampling {
		t.Errorf("Sampling: got %q, want %q", parsed.Video.Sampling, original.Video.Sampling)
	}
	if parsed.Video.Width != original.Video.Width {
		t.Errorf("Width: got %d, want %d", parsed.Video.Width, original.Video.Width)
	}
	if parsed.Video.Height != original.Video.Height {
		t.Errorf("Height: got %d, want %d", parsed.Video.Height, original.Video.Height)
	}
	if parsed.Video.Depth != original.Video.Depth {
		t.Errorf("Depth: got %d, want %d", parsed.Video.Depth, original.Video.Depth)
	}
	if parsed.Video.ExactFPS != original.Video.ExactFPS {
		t.Errorf("ExactFPS: got %q, want %q", parsed.Video.ExactFPS, original.Video.ExactFPS)
	}
	if parsed.Video.Colorimetry != original.Video.Colorimetry {
		t.Errorf("Colorimetry: got %q, want %q", parsed.Video.Colorimetry, original.Video.Colorimetry)
	}
	if parsed.Video.PackingMode != original.Video.PackingMode {
		t.Errorf("PackingMode: got %q, want %q", parsed.Video.PackingMode, original.Video.PackingMode)
	}
	if parsed.Video.SenderType != original.Video.SenderType {
		t.Errorf("SenderType: got %q, want %q", parsed.Video.SenderType, original.Video.SenderType)
	}
	if parsed.Video.Interlace != original.Video.Interlace {
		t.Errorf("Interlace: got %v, want %v", parsed.Video.Interlace, original.Video.Interlace)
	}
}

func TestSDPRoundTrip_Audio(t *testing.T) {
	original := SDPParams{
		SessionName:    "RoundTrip Audio",
		SourceIP:       "10.0.0.5",
		MulticastIP:    "239.10.20.10",
		Port:           5006,
		PayloadType:    97,
		PTPGrandmaster: "00-11-22-ff-fe-33-44-55",
		PTPDomain:      0,
		Audio: &SDPAudioParams{
			BitDepth:     24,
			SampleRate:   48000,
			Channels:     2,
			PacketTimeMs: 1.0,
			ChannelOrder: "SMPTE2110.(ST)",
		},
	}

	sdp, err := GenerateSDP(original)
	if err != nil {
		t.Fatalf("GenerateSDP failed: %v", err)
	}
	parsed, err := ParseSDP(sdp)
	if err != nil {
		t.Fatalf("ParseSDP round-trip failed: %v", err)
	}

	if parsed.Audio == nil {
		t.Fatal("Audio should not be nil after round-trip")
	}
	if parsed.Audio.BitDepth != original.Audio.BitDepth {
		t.Errorf("BitDepth: got %d, want %d", parsed.Audio.BitDepth, original.Audio.BitDepth)
	}
	if parsed.Audio.SampleRate != original.Audio.SampleRate {
		t.Errorf("SampleRate: got %d, want %d", parsed.Audio.SampleRate, original.Audio.SampleRate)
	}
	if parsed.Audio.Channels != original.Audio.Channels {
		t.Errorf("Channels: got %d, want %d", parsed.Audio.Channels, original.Audio.Channels)
	}
	if parsed.Audio.PacketTimeMs != original.Audio.PacketTimeMs {
		t.Errorf("PacketTimeMs: got %f, want %f", parsed.Audio.PacketTimeMs, original.Audio.PacketTimeMs)
	}
	if parsed.Audio.ChannelOrder != original.Audio.ChannelOrder {
		t.Errorf("ChannelOrder: got %q, want %q", parsed.Audio.ChannelOrder, original.Audio.ChannelOrder)
	}
}

func TestSDPRoundTrip_Ancillary(t *testing.T) {
	original := SDPParams{
		SessionName:    "RoundTrip Anc",
		SourceIP:       "10.0.0.5",
		MulticastIP:    "239.10.20.20",
		Port:           5010,
		PayloadType:    100,
		PTPGrandmaster: "00-11-22-ff-fe-33-44-55",
		PTPDomain:      0,
		Ancillary: &SDPAncillaryParams{
			DIDSDIDs: [][2]byte{{0x61, 0x02}},
		},
	}

	sdp, err := GenerateSDP(original)
	if err != nil {
		t.Fatalf("GenerateSDP failed: %v", err)
	}
	parsed, err := ParseSDP(sdp)
	if err != nil {
		t.Fatalf("ParseSDP round-trip failed: %v", err)
	}

	if parsed.Ancillary == nil {
		t.Fatal("Ancillary should not be nil after round-trip")
	}
	if len(parsed.Ancillary.DIDSDIDs) != 1 {
		t.Fatalf("DIDSDIDs len: got %d, want 1", len(parsed.Ancillary.DIDSDIDs))
	}
	if parsed.Ancillary.DIDSDIDs[0] != original.Ancillary.DIDSDIDs[0] {
		t.Errorf("DIDSDIDs[0]: got %v, want %v", parsed.Ancillary.DIDSDIDs[0], original.Ancillary.DIDSDIDs[0])
	}
}

func TestParseSDP_InvalidInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"no media line", "v=0\r\ns=Test\r\nt=0 0\r\n"},
		{"no rtpmap", "v=0\r\ns=Test\r\nt=0 0\r\nm=video 5004 RTP/AVP 96\r\nc=IN IP4 239.1.0.1/64\r\n"},
		{"invalid port", "v=0\r\n" +
			"o=- 1700000000 1700000000 IN IP4 192.168.1.10\r\n" +
			"s=Test\r\n" +
			"t=0 0\r\n" +
			"m=video abc RTP/AVP 96\r\n" +
			"c=IN IP4 239.1.0.1/64\r\n" +
			"a=rtpmap:96 raw/90000\r\n"},
		{"invalid payload type", "v=0\r\n" +
			"o=- 1700000000 1700000000 IN IP4 192.168.1.10\r\n" +
			"s=Test\r\n" +
			"t=0 0\r\n" +
			"m=video 5004 RTP/AVP xyz\r\n" +
			"c=IN IP4 239.1.0.1/64\r\n" +
			"a=rtpmap:96 raw/90000\r\n"},
		{"invalid PTP domain", "v=0\r\n" +
			"o=- 1700000000 1700000000 IN IP4 192.168.1.10\r\n" +
			"s=Test\r\n" +
			"t=0 0\r\n" +
			"m=video 5004 RTP/AVP 96\r\n" +
			"c=IN IP4 239.1.0.1/64\r\n" +
			"a=rtpmap:96 raw/90000\r\n" +
			"a=fmtp:96 sampling=YCbCr-4:2:2; width=1920; height=1080; depth=10; exactframerate=25\r\n" +
			"a=ts-refclk:ptp=IEEE1588-2008:08-00-11-ff-fe-21-e1-b0:notanumber\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseSDP(tt.raw)
			if err == nil {
				t.Error("expected error for invalid SDP")
			}
		})
	}
}

func TestGenerateSDP_NoMediaType(t *testing.T) {
	_, err := GenerateSDP(SDPParams{
		SessionName: "Empty",
		SourceIP:    "192.168.1.10",
		MulticastIP: "239.1.0.1",
		Port:        5004,
		PayloadType: 96,
	})
	if err == nil {
		t.Error("expected error when no media type (Video/Audio/Ancillary) is set")
	}
	if err != nil && !strings.Contains(err.Error(), "at least one of") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParseSDP_SourceIPFromOrigin(t *testing.T) {
	// When source-filter is absent, source IP should come from o= line.
	raw := "v=0\r\n" +
		"o=- 1700000000 1700000000 IN IP4 192.168.1.10\r\n" +
		"s=No Filter\r\n" +
		"t=0 0\r\n" +
		"m=video 5004 RTP/AVP 96\r\n" +
		"c=IN IP4 239.1.0.1/64\r\n" +
		"a=rtpmap:96 raw/90000\r\n" +
		"a=fmtp:96 sampling=YCbCr-4:2:2; width=1920; height=1080; depth=10; exactframerate=25; colorimetry=BT709; PM=2110GPM; TP=2110TPN\r\n" +
		"a=ts-refclk:ptp=IEEE1588-2008:08-00-11-ff-fe-21-e1-b0:0\r\n"

	p, err := ParseSDP(raw)
	if err != nil {
		t.Fatalf("ParseSDP failed: %v", err)
	}
	if p.SourceIP != "192.168.1.10" {
		t.Errorf("SourceIP = %q, want %q", p.SourceIP, "192.168.1.10")
	}
}
