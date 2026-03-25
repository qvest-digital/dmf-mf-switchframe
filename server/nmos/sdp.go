package nmos

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SDPParams holds all parameters needed to generate or returned from parsing
// an SDP description for ST 2110 streams.
type SDPParams struct {
	SessionName string
	SourceIP    string
	MulticastIP string
	Port        int
	PayloadType int

	// Exactly one of Video, Audio, or Ancillary should be non-nil.
	Video     *SDPVideoParams
	Audio     *SDPAudioParams
	Ancillary *SDPAncillaryParams

	PTPGrandmaster string // e.g., "08-00-11-ff-fe-21-e1-b0"
	PTPDomain      int
}

// SDPVideoParams contains ST 2110-20 video-specific SDP parameters.
type SDPVideoParams struct {
	Sampling    string // "YCbCr-4:2:2", "YCbCr-4:2:0", etc.
	Width       int
	Height      int
	Depth       int
	Colorimetry string // "BT709", "BT2020"
	ExactFPS    string // "30000/1001", "60", "25"
	Interlace   bool
	PackingMode string // "2110GPM", "2110BPM"
	SenderType  string // "2110TPN", "2110TPNL", "2110TPW"
}

// SDPAudioParams contains ST 2110-30 audio-specific SDP parameters.
type SDPAudioParams struct {
	BitDepth     int
	SampleRate   int
	Channels     int
	PacketTimeMs float64 // e.g., 1.0
	ChannelOrder string  // "SMPTE2110.(ST)", etc. — optional
}

// SDPAncillaryParams contains ST 2110-40 ancillary data SDP parameters.
type SDPAncillaryParams struct {
	DIDSDIDs [][2]byte // DID/SDID pairs, e.g., {{0x61, 0x02}}
}

// GenerateSDP produces an RFC 4566 SDP string for ST 2110 streams.
// Returns an error if none of Video, Audio, or Ancillary params are set.
func GenerateSDP(params SDPParams) (string, error) {
	if params.Video == nil && params.Audio == nil && params.Ancillary == nil {
		return "", fmt.Errorf("at least one of Video, Audio, or Ancillary must be set")
	}

	ts := time.Now().Unix()
	var b strings.Builder

	// Session-level lines.
	b.WriteString("v=0\r\n")
	fmt.Fprintf(&b, "o=- %d %d IN IP4 %s\r\n", ts, ts, params.SourceIP)
	fmt.Fprintf(&b, "s=%s\r\n", params.SessionName)
	b.WriteString("t=0 0\r\n")

	// Determine media type for the m= line.
	mediaType := "video"
	if params.Audio != nil {
		mediaType = "audio"
	}
	// Ancillary uses "video" per RFC 8331, so no change needed.

	fmt.Fprintf(&b, "m=%s %d RTP/AVP %d\r\n", mediaType, params.Port, params.PayloadType)
	fmt.Fprintf(&b, "c=IN IP4 %s/64\r\n", params.MulticastIP)
	fmt.Fprintf(&b, "a=source-filter: incl IN IP4 %s %s\r\n", params.MulticastIP, params.SourceIP)

	pt := params.PayloadType

	switch {
	case params.Video != nil:
		generateVideoSDP(&b, pt, params)
	case params.Audio != nil:
		generateAudioSDP(&b, pt, params)
	case params.Ancillary != nil:
		generateAncillarySDP(&b, pt, params)
	}

	// PTP reference clock.
	if params.PTPGrandmaster != "" {
		fmt.Fprintf(&b, "a=ts-refclk:ptp=IEEE1588-2008:%s:%d\r\n", params.PTPGrandmaster, params.PTPDomain)
	}

	return b.String(), nil
}

func generateVideoSDP(b *strings.Builder, pt int, params SDPParams) {
	v := params.Video

	fmt.Fprintf(b, "a=rtpmap:%d raw/90000\r\n", pt)

	// Build fmtp parameter string.
	fmtpParts := []string{
		fmt.Sprintf("sampling=%s", v.Sampling),
		fmt.Sprintf("width=%d", v.Width),
		fmt.Sprintf("height=%d", v.Height),
		fmt.Sprintf("depth=%d", v.Depth),
		fmt.Sprintf("exactframerate=%s", v.ExactFPS),
	}
	if v.Interlace {
		fmtpParts = append(fmtpParts, "interlace")
	}
	fmtpParts = append(fmtpParts,
		"TCS=SDR",
		fmt.Sprintf("colorimetry=%s", v.Colorimetry),
		fmt.Sprintf("PM=%s", v.PackingMode),
		"SSN=ST2110-20:2017",
		fmt.Sprintf("TP=%s", v.SenderType),
		"PAR=1:1",
	)

	fmt.Fprintf(b, "a=fmtp:%d %s\r\n", pt, strings.Join(fmtpParts, "; "))
	b.WriteString("a=mediaclk:direct=0\r\n")
}

func generateAudioSDP(b *strings.Builder, pt int, params SDPParams) {
	a := params.Audio

	encoding := fmt.Sprintf("L%d", a.BitDepth)
	fmt.Fprintf(b, "a=rtpmap:%d %s/%d/%d\r\n", pt, encoding, a.SampleRate, a.Channels)

	// ptime: format without trailing zeros.
	ptime := strconv.FormatFloat(a.PacketTimeMs, 'f', -1, 64)
	fmt.Fprintf(b, "a=ptime:%s\r\n", ptime)

	// framecount = sample_rate * ptime_ms / 1000
	framecount := int(float64(a.SampleRate) * a.PacketTimeMs / 1000.0)
	fmt.Fprintf(b, "a=framecount:%d\r\n", framecount)

	// Channel order fmtp (optional).
	if a.ChannelOrder != "" {
		fmt.Fprintf(b, "a=fmtp:%d channel-order=%s\r\n", pt, a.ChannelOrder)
	}

	fmt.Fprintf(b, "a=mediaclk:direct=0 rate=%d\r\n", a.SampleRate)
}

func generateAncillarySDP(b *strings.Builder, pt int, params SDPParams) {
	anc := params.Ancillary

	fmt.Fprintf(b, "a=rtpmap:%d smpte291/90000\r\n", pt)

	// Build fmtp with DID_SDID pairs.
	var pairs []string
	for _, ds := range anc.DIDSDIDs {
		pairs = append(pairs, fmt.Sprintf("DID_SDID={0x%02x,0x%02x}", ds[0], ds[1]))
	}
	if len(pairs) > 0 {
		fmt.Fprintf(b, "a=fmtp:%d %s\r\n", pt, strings.Join(pairs, ";"))
	}

	b.WriteString("a=mediaclk:direct=0\r\n")
}

// ParseSDP parses an RFC 4566 SDP string and extracts ST 2110 parameters.
func ParseSDP(raw string) (SDPParams, error) {
	var params SDPParams

	if raw == "" {
		return params, fmt.Errorf("empty SDP")
	}

	// Normalize line endings.
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")

	var (
		hasMLine   bool
		hasRTpmap  bool
		encoding   string
		clockRate  int
		channels   int
		fmtpValue  string
	)

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "s="):
			params.SessionName = strings.TrimPrefix(line, "s=")

		case strings.HasPrefix(line, "o="):
			// o=- {ts} {ts} IN IP4 {source_ip}
			parts := strings.Fields(line[2:])
			if len(parts) >= 6 {
				params.SourceIP = parts[5]
			}

		case strings.HasPrefix(line, "m="):
			hasMLine = true
			// m={type} {port} RTP/AVP {pt}
			parts := strings.Fields(line[2:])
			if len(parts) >= 4 {
				var err error
				params.Port, err = strconv.Atoi(parts[1])
				if err != nil {
					return params, fmt.Errorf("invalid port %q in media line: %w", parts[1], err)
				}
				params.PayloadType, err = strconv.Atoi(parts[3])
				if err != nil {
					return params, fmt.Errorf("invalid payload type %q in media line: %w", parts[3], err)
				}
			}

		case strings.HasPrefix(line, "c=IN IP4 "):
			// c=IN IP4 {multicast_ip}/64
			addr := strings.TrimPrefix(line, "c=IN IP4 ")
			if idx := strings.Index(addr, "/"); idx >= 0 {
				addr = addr[:idx]
			}
			params.MulticastIP = addr

		case strings.HasPrefix(line, "a=source-filter:"):
			// a=source-filter: incl IN IP4 {mcast} {source}
			parts := strings.Fields(line)
			if len(parts) >= 6 {
				params.SourceIP = parts[5]
			}

		case strings.HasPrefix(line, "a=rtpmap:"):
			hasRTpmap = true
			// a=rtpmap:{pt} {encoding}/{clock_rate}[/{channels}]
			rest := strings.TrimPrefix(line, "a=rtpmap:")
			spaceIdx := strings.Index(rest, " ")
			if spaceIdx < 0 {
				continue
			}
			codecPart := rest[spaceIdx+1:]
			parts := strings.Split(codecPart, "/")
			if len(parts) >= 2 {
				encoding = parts[0]
				clockRate, _ = strconv.Atoi(parts[1])
			}
			if len(parts) >= 3 {
				channels, _ = strconv.Atoi(parts[2])
			}

		case strings.HasPrefix(line, "a=fmtp:"):
			// a=fmtp:{pt} {params}
			rest := strings.TrimPrefix(line, "a=fmtp:")
			spaceIdx := strings.Index(rest, " ")
			if spaceIdx >= 0 {
				fmtpValue = rest[spaceIdx+1:]
			}

		case strings.HasPrefix(line, "a=ptime:"):
			val := strings.TrimPrefix(line, "a=ptime:")
			if params.Audio == nil {
				params.Audio = &SDPAudioParams{}
			}
			params.Audio.PacketTimeMs, _ = strconv.ParseFloat(val, 64)

		case strings.HasPrefix(line, "a=ts-refclk:ptp=IEEE1588-2008:"):
			// a=ts-refclk:ptp=IEEE1588-2008:{gm}:{domain}
			rest := strings.TrimPrefix(line, "a=ts-refclk:ptp=IEEE1588-2008:")
			// Split from the last colon to separate domain from grandmaster ID
			// (grandmaster ID itself contains colons/dashes).
			lastColon := strings.LastIndex(rest, ":")
			if lastColon >= 0 {
				params.PTPGrandmaster = rest[:lastColon]
				var err error
				params.PTPDomain, err = strconv.Atoi(rest[lastColon+1:])
				if err != nil {
					return params, fmt.Errorf("invalid PTP domain %q: %w", rest[lastColon+1:], err)
				}
			}
		}
	}

	if !hasMLine {
		return params, fmt.Errorf("no media line found in SDP")
	}
	if !hasRTpmap {
		return params, fmt.Errorf("no rtpmap attribute found in SDP")
	}

	// Determine media type from encoding name and populate type-specific params.
	switch {
	case encoding == "raw":
		params.Video = parseVideoFmtp(fmtpValue)

	case strings.HasPrefix(encoding, "L"):
		if params.Audio == nil {
			params.Audio = &SDPAudioParams{}
		}
		// Bit depth from encoding name (L16, L24, etc.).
		depthStr := strings.TrimPrefix(encoding, "L")
		params.Audio.BitDepth, _ = strconv.Atoi(depthStr)
		params.Audio.SampleRate = clockRate
		params.Audio.Channels = channels
		parseAudioFmtp(fmtpValue, params.Audio)

	case encoding == "smpte291":
		params.Ancillary = parseAncillaryFmtp(fmtpValue)

	default:
		return params, fmt.Errorf("unknown encoding: %s", encoding)
	}

	return params, nil
}

func parseVideoFmtp(fmtp string) *SDPVideoParams {
	v := &SDPVideoParams{}
	if fmtp == "" {
		return v
	}

	// Split on semicolons; each part is "key=value" or a bare flag like "interlace".
	parts := strings.Split(fmtp, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if part == "interlace" {
			v.Interlace = true
			continue
		}

		eqIdx := strings.Index(part, "=")
		if eqIdx < 0 {
			continue
		}
		key := part[:eqIdx]
		val := part[eqIdx+1:]

		switch key {
		case "sampling":
			v.Sampling = val
		case "width":
			v.Width, _ = strconv.Atoi(val)
		case "height":
			v.Height, _ = strconv.Atoi(val)
		case "depth":
			v.Depth, _ = strconv.Atoi(val)
		case "exactframerate":
			v.ExactFPS = val
		case "colorimetry":
			v.Colorimetry = val
		case "PM":
			v.PackingMode = val
		case "TP":
			v.SenderType = val
		}
	}

	return v
}

func parseAudioFmtp(fmtp string, a *SDPAudioParams) {
	if fmtp == "" {
		return
	}

	// Audio fmtp: "channel-order=SMPTE2110.(ST)"
	parts := strings.Split(fmtp, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "channel-order=") {
			a.ChannelOrder = strings.TrimPrefix(part, "channel-order=")
		}
	}
}

func parseAncillaryFmtp(fmtp string) *SDPAncillaryParams {
	anc := &SDPAncillaryParams{}
	if fmtp == "" {
		return anc
	}

	// Parse DID_SDID={0xHH,0xHH} pairs separated by semicolons.
	parts := strings.Split(fmtp, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "DID_SDID=") {
			continue
		}

		// Extract the pair from DID_SDID={0xHH,0xHH}
		val := strings.TrimPrefix(part, "DID_SDID=")
		val = strings.Trim(val, "{}")
		hexParts := strings.Split(val, ",")
		if len(hexParts) != 2 {
			continue
		}

		did, err1 := parseHexByte(strings.TrimSpace(hexParts[0]))
		sdid, err2 := parseHexByte(strings.TrimSpace(hexParts[1]))
		if err1 == nil && err2 == nil {
			anc.DIDSDIDs = append(anc.DIDSDIDs, [2]byte{did, sdid})
		}
	}

	return anc
}

func parseHexByte(s string) (byte, error) {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	val, err := strconv.ParseUint(s, 16, 8)
	if err != nil {
		return 0, err
	}
	return byte(val), nil
}
