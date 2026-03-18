package output

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"testing"

	astits "github.com/asticode/go-astits"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

// ---------------------------------------------------------------------------
// Part 1: Fixture Demuxer
// ---------------------------------------------------------------------------

// fixtureFrames holds the demuxed video and audio frames from a TS fixture.
type fixtureFrames struct {
	Video []*media.VideoFrame
	Audio []*media.AudioFrame
}

// loadFixture demuxes a TS file into media.VideoFrame and media.AudioFrame
// structs suitable for feeding through TSMuxer. It uses go-astits to parse
// the transport stream and extracts:
//   - Video PES: Annex B NALUs parsed to find SPS/PPS on keyframes,
//     converted to AVC1 wire format (4-byte length prefix), with
//     SPS/PPS/AUD stripped from wire data.
//   - Audio PES: raw ADTS data preserved as-is.
//
// Video and audio streams are identified by PES stream_id (0xE0-0xEF = video,
// 0xC0-0xDF = audio) since the fixture may use different PIDs than our muxer.
func loadFixture(t *testing.T, path string) *fixtureFrames {
	t.Helper()

	f, err := os.Open(path)
	require.NoError(t, err, "open fixture file")
	defer f.Close()

	ctx := context.Background()
	dmx := astits.NewDemuxer(ctx, f)

	var result fixtureFrames
	var currentSPS, currentPPS []byte

	for {
		d, err := dmx.NextData()
		if err != nil {
			if err == io.EOF {
				break
			}
			// Some demuxer errors are non-fatal (e.g. incomplete final packet).
			t.Logf("demuxer warning: %v", err)
			break
		}
		if d == nil || d.PES == nil || d.PES.Header == nil {
			continue
		}

		streamID := d.PES.Header.StreamID
		pesData := d.PES.Data

		switch {
		case streamID >= 0xE0 && streamID <= 0xEF:
			// Video stream.
			vf := parseVideoFrame(t, d, pesData, &currentSPS, &currentPPS)
			if vf != nil {
				result.Video = append(result.Video, vf)
			}

		case streamID >= 0xC0 && streamID <= 0xDF:
			// Audio stream — may contain multiple concatenated ADTS frames.
			afs := parseAudioFrames(t, d, pesData)
			result.Audio = append(result.Audio, afs...)
		}
	}

	return &result
}

// parseVideoFrame converts a video PES payload into a media.VideoFrame.
// It parses Annex B NALUs, extracts SPS/PPS, detects keyframes, and
// builds AVC1 wire data with SPS/PPS/AUD stripped.
func parseVideoFrame(t *testing.T, d *astits.DemuxerData, pesData []byte, sps, pps *[]byte) *media.VideoFrame {
	t.Helper()

	if len(pesData) == 0 {
		return nil
	}

	// Extract PTS and DTS from PES header.
	var pts, dts int64
	if d.PES.Header.OptionalHeader != nil {
		if d.PES.Header.OptionalHeader.PTS != nil {
			pts = d.PES.Header.OptionalHeader.PTS.Base
		}
		if d.PES.Header.OptionalHeader.DTS != nil {
			dts = d.PES.Header.OptionalHeader.DTS.Base
		} else {
			dts = pts
		}
	}

	// Detect keyframe via RandomAccessIndicator in the adaptation field
	// of the first TS packet for this PES.
	isKeyframe := false
	if d.FirstPacket != nil && d.FirstPacket.AdaptationField != nil {
		isKeyframe = d.FirstPacket.AdaptationField.RandomAccessIndicator
	}

	// Split Annex B data into individual NALUs.
	nalus := splitAnnexBNALUsFromData(pesData)
	if len(nalus) == 0 {
		return nil
	}

	// Scan NALUs: extract SPS/PPS and build wire data without SPS/PPS/AUD.
	var wireNALUs [][]byte
	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}
		naluType := nalu[0] & 0x1F

		switch naluType {
		case 7: // SPS
			spsCopy := make([]byte, len(nalu))
			copy(spsCopy, nalu)
			*sps = spsCopy
			// Also mark as keyframe if we see SPS (some streams don't set RAI).
			isKeyframe = true
		case 8: // PPS
			ppsCopy := make([]byte, len(nalu))
			copy(ppsCopy, nalu)
			*pps = ppsCopy
		case 9: // AUD — skip, muxer adds its own
			continue
		case 5: // IDR slice
			isKeyframe = true
			wireNALUs = append(wireNALUs, nalu)
		default:
			wireNALUs = append(wireNALUs, nalu)
		}
	}

	if len(wireNALUs) == 0 {
		return nil
	}

	// Build AVC1 wire data: 4-byte big-endian length prefix per NALU.
	var wireBuf []byte
	for _, nalu := range wireNALUs {
		lenBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBytes, uint32(len(nalu)))
		wireBuf = append(wireBuf, lenBytes...)
		wireBuf = append(wireBuf, nalu...)
	}

	vf := &media.VideoFrame{
		PTS:        pts,
		DTS:        dts,
		IsKeyframe: isKeyframe,
		WireData:   wireBuf,
		Codec:      "h264",
	}

	if isKeyframe && len(*sps) > 0 && len(*pps) > 0 {
		vf.SPS = make([]byte, len(*sps))
		copy(vf.SPS, *sps)
		vf.PPS = make([]byte, len(*pps))
		copy(vf.PPS, *pps)
	}

	return vf
}

// parseAudioFrames converts an audio PES payload into one or more
// media.AudioFrame structs. A single PES may contain multiple concatenated
// ADTS frames; this function splits them and computes per-frame PTS offsets.
func parseAudioFrames(t *testing.T, d *astits.DemuxerData, pesData []byte) []*media.AudioFrame {
	t.Helper()

	if len(pesData) == 0 {
		return nil
	}

	var basePTS int64
	if d.PES.Header.OptionalHeader != nil && d.PES.Header.OptionalHeader.PTS != nil {
		basePTS = d.PES.Header.OptionalHeader.PTS.Base
	}

	// Parse sample rate and channels from the first ADTS header.
	sampleRate, channels := codec.ParseADTSInfo(pesData)
	if sampleRate == 0 {
		sampleRate = 48000
	}
	if channels == 0 {
		channels = 2
	}

	// Split concatenated ADTS frames within this PES.
	var frames []*media.AudioFrame
	data := pesData
	frameIdx := 0

	for len(data) >= 7 && codec.IsADTS(data) {
		frameLen := codec.ADTSFrameLen(data)
		if frameLen < 7 || frameLen > len(data) {
			break
		}

		// Each AAC frame is 1024 samples. PTS offset in 90kHz ticks:
		// offset = frameIdx * 1024 * 90000 / sampleRate
		ptsOffset := int64(frameIdx) * 1024 * 90000 / int64(sampleRate)

		frameCopy := make([]byte, frameLen)
		copy(frameCopy, data[:frameLen])

		frames = append(frames, &media.AudioFrame{
			PTS:        basePTS + ptsOffset,
			Data:       frameCopy,
			SampleRate: sampleRate,
			Channels:   channels,
		})

		data = data[frameLen:]
		frameIdx++
	}

	// If no ADTS frames found, return the whole PES as a single frame.
	if len(frames) == 0 {
		frameCopy := make([]byte, len(pesData))
		copy(frameCopy, pesData)
		frames = append(frames, &media.AudioFrame{
			PTS:        basePTS,
			Data:       frameCopy,
			SampleRate: sampleRate,
			Channels:   channels,
		})
	}

	return frames
}

// splitAnnexBNALUsFromData splits Annex B byte stream into individual NALUs.
// Handles both 3-byte (0x000001) and 4-byte (0x00000001) start codes.
func splitAnnexBNALUsFromData(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}

	var nalus [][]byte
	naluStart := -1

	i := 0
	for i < len(data)-2 {
		if data[i] == 0x00 && data[i+1] == 0x00 {
			scLen := 0
			if i+3 < len(data) && data[i+2] == 0x00 && data[i+3] == 0x01 {
				scLen = 4
			} else if data[i+2] == 0x01 {
				scLen = 3
			}
			if scLen > 0 {
				if naluStart >= 0 {
					nalu := data[naluStart:i]
					if len(nalu) > 0 {
						nalus = append(nalus, nalu)
					}
				}
				naluStart = i + scLen
				i += scLen
				continue
			}
		}
		i++
	}

	if naluStart >= 0 && naluStart < len(data) {
		nalu := data[naluStart:]
		if len(nalu) > 0 {
			nalus = append(nalus, nalu)
		}
	}

	return nalus
}

func TestLoadFixture(t *testing.T) {
	const fixturePath = "testdata/fixture.ts"
	if _, err := os.Stat(fixturePath); os.IsNotExist(err) {
		t.Skip("fixture.ts not found in testdata/")
	}

	ff := loadFixture(t, fixturePath)

	t.Run("video_frame_count", func(t *testing.T) {
		require.Greater(t, len(ff.Video), 100,
			"fixture should contain >100 video frames (5s at 24fps = ~120)")
	})

	t.Run("audio_frame_count", func(t *testing.T) {
		require.Greater(t, len(ff.Audio), 200,
			"fixture should contain >200 audio frames (5s at ~46.9fps = ~234)")
	})

	t.Run("first_frame_is_keyframe", func(t *testing.T) {
		require.True(t, ff.Video[0].IsKeyframe,
			"first video frame should be a keyframe")
	})

	t.Run("first_keyframe_has_sps_pps", func(t *testing.T) {
		require.NotEmpty(t, ff.Video[0].SPS,
			"first keyframe should have SPS")
		require.NotEmpty(t, ff.Video[0].PPS,
			"first keyframe should have PPS")
	})

	t.Run("video_pts_reasonable", func(t *testing.T) {
		// PTS should be non-negative and within a reasonable range.
		for i, vf := range ff.Video {
			require.GreaterOrEqual(t, vf.PTS, int64(0),
				"video frame %d PTS should be non-negative", i)
		}
	})

	t.Run("wire_data_is_avc1", func(t *testing.T) {
		// Verify wire data uses 4-byte length prefix (AVC1 format).
		for i, vf := range ff.Video[:min(5, len(ff.Video))] {
			require.GreaterOrEqual(t, len(vf.WireData), 5,
				"video frame %d wire data too short", i)
			naluLen := binary.BigEndian.Uint32(vf.WireData[:4])
			require.Greater(t, naluLen, uint32(0),
				"video frame %d first NALU length should be >0", i)
			require.LessOrEqual(t, int(naluLen)+4, len(vf.WireData),
				"video frame %d first NALU length exceeds wire data", i)
		}
	})

	t.Run("audio_has_adts", func(t *testing.T) {
		for i, af := range ff.Audio[:min(5, len(ff.Audio))] {
			require.True(t, codec.IsADTS(af.Data),
				"audio frame %d should have ADTS header", i)
		}
	})

	t.Run("multiple_keyframes", func(t *testing.T) {
		keyframeCount := 0
		for _, vf := range ff.Video {
			if vf.IsKeyframe {
				keyframeCount++
			}
		}
		require.Greater(t, keyframeCount, 1,
			"fixture should contain multiple keyframes (every 48 frames)")
	})
}

// ---------------------------------------------------------------------------
// Part 2: Capture + Scanner
// ---------------------------------------------------------------------------

// feedAndCapture loads the fixture, feeds all frames through a real TSMuxer
// (interleaved by PTS), and returns all captured output bytes.
func feedAndCapture(t *testing.T, path string) []byte {
	t.Helper()

	ff := loadFixture(t, path)
	require.NotEmpty(t, ff.Video, "fixture must have video frames")
	require.NotEmpty(t, ff.Audio, "fixture must have audio frames")

	muxer := NewTSMuxer()
	var output []byte
	muxer.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	// Interleave video and audio by PTS for realistic muxing order.
	type taggedFrame struct {
		pts     int64
		isVideo bool
		vidIdx  int
		audIdx  int
	}
	var frames []taggedFrame
	for i := range ff.Video {
		frames = append(frames, taggedFrame{pts: ff.Video[i].PTS, isVideo: true, vidIdx: i})
	}
	for i := range ff.Audio {
		frames = append(frames, taggedFrame{pts: ff.Audio[i].PTS, isVideo: false, audIdx: i})
	}
	sort.SliceStable(frames, func(i, j int) bool {
		return frames[i].pts < frames[j].pts
	})

	for _, f := range frames {
		if f.isVideo {
			err := muxer.WriteVideo(ff.Video[f.vidIdx])
			require.NoError(t, err, "WriteVideo failed at PTS %d", f.pts)
		} else {
			err := muxer.WriteAudio(ff.Audio[f.audIdx])
			require.NoError(t, err, "WriteAudio failed at PTS %d", f.pts)
		}
	}

	require.NotEmpty(t, output, "muxer should produce output")
	return output
}

// tsAnalysis holds the results of a single-pass TS stream analysis.
type tsAnalysis struct {
	// Structural
	TotalPackets     int
	PIDs             map[uint16]int // PID → packet count
	ContinuityErrors []string
	SyncErrors       int

	// PMT
	HasPAT     bool
	HasPMT     bool
	PMTStreams []pmtStream

	// Codec
	FirstVideoHasSPS bool
	IDRCount         int
	IDRsWithoutSPS   int
	ADTSFrames       int
	ADTSSyncErrors   int

	// Timing
	VideoPTS         []int64
	AudioPTS         []int64
	PCRValues        []pcrSample
	MaxPCRGapMs      float64
	MaxVideoPTSGapMs float64
	MaxAudioPTSGapMs float64
	AVDriftMs        float64
}

type pmtStream struct {
	PID        uint16
	StreamType uint8
}

type pcrSample struct {
	PacketIndex int
	Base        int64
}

// analyzeTSStream performs a single-pass scan of raw TS bytes, extracting
// structural, codec, and timing information.
func analyzeTSStream(data []byte) *tsAnalysis {
	a := &tsAnalysis{
		PIDs: make(map[uint16]int),
	}

	const pktSize = 188
	if len(data)%pktSize != 0 {
		// Not packet-aligned — count the aligned portion anyway.
	}

	// Continuity counter tracking per PID.
	ccTracker := make(map[uint16]int) // PID → last CC seen (-1 = not seen)
	firstVideoSeen := false
	pktIdx := 0

	for offset := 0; offset+pktSize <= len(data); offset += pktSize {
		pkt := data[offset : offset+pktSize]
		a.TotalPackets++

		// Sync byte check.
		if pkt[0] != 0x47 {
			a.SyncErrors++
			continue
		}

		// Parse header.
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		pusi := pkt[1]&0x40 != 0
		afc := (pkt[3] >> 4) & 0x03
		cc := pkt[3] & 0x0F
		hasPayload := afc == 0x01 || afc == 0x03
		hasAdaptation := afc == 0x02 || afc == 0x03

		a.PIDs[pid]++

		// Continuity counter validation (only for PIDs with payload, skip null PID).
		if hasPayload && pid != 0x1FFF {
			if lastCC, ok := ccTracker[pid]; ok {
				expectedCC := (lastCC + 1) & 0x0F
				if int(cc) != expectedCC {
					a.ContinuityErrors = append(a.ContinuityErrors,
						fmt.Sprintf("PID 0x%04X packet %d: expected CC %d, got %d",
							pid, pktIdx, expectedCC, cc))
				}
			}
			ccTracker[pid] = int(cc)
		}

		// Parse adaptation field.
		headerLen := 4
		var rai bool
		if hasAdaptation && headerLen < pktSize {
			afLen := int(pkt[headerLen])
			if afLen > 0 && headerLen+1 < pktSize {
				flags := pkt[headerLen+1]
				rai = flags&0x40 != 0
				hasPCR := flags&0x10 != 0

				if hasPCR && afLen >= 7 {
					// PCR is 6 bytes starting at headerLen+2.
					pcrBase := int64(pkt[headerLen+2])<<25 |
						int64(pkt[headerLen+3])<<17 |
						int64(pkt[headerLen+4])<<9 |
						int64(pkt[headerLen+5])<<1 |
						int64(pkt[headerLen+6]>>7)
					a.PCRValues = append(a.PCRValues, pcrSample{
						PacketIndex: pktIdx,
						Base:        pcrBase,
					})
				}
			}
			headerLen += 1 + afLen
		}

		if headerLen >= pktSize {
			pktIdx++
			continue
		}

		payload := pkt[headerLen:]

		// PAT detection (PID 0).
		if pid == 0x0000 && pusi {
			a.HasPAT = true
		}

		// PMT detection (PID 0x1000) and stream parsing.
		if pid == 0x1000 && pusi {
			a.HasPMT = true
			parsePMTStreams(payload, a)
		}

		// Video PES analysis (PID 0x100).
		if pid == videoPID && pusi {
			pesPayload := extractPESPayload(payload)
			if len(pesPayload) > 0 {
				hasSPS := false
				hasIDR := false

				// Scan for Annex B start codes and NALU types.
				for i := 0; i < len(pesPayload)-4; i++ {
					if pesPayload[i] == 0x00 && pesPayload[i+1] == 0x00 {
						scLen := 0
						if i+3 < len(pesPayload) && pesPayload[i+2] == 0x00 && pesPayload[i+3] == 0x01 {
							scLen = 4
						} else if pesPayload[i+2] == 0x01 {
							scLen = 3
						}
						if scLen > 0 && i+scLen < len(pesPayload) {
							naluType := pesPayload[i+scLen] & 0x1F
							if naluType == 7 { // SPS
								hasSPS = true
							}
							if naluType == 5 { // IDR
								hasIDR = true
							}
						}
					}
				}

				if !firstVideoSeen {
					a.FirstVideoHasSPS = hasSPS
					firstVideoSeen = true
				}

				if rai || hasIDR {
					a.IDRCount++
					if !hasSPS {
						a.IDRsWithoutSPS++
					}
				}
			}

			// Extract PTS from PES header.
			pts := extractPESPTS(payload)
			if pts >= 0 {
				a.VideoPTS = append(a.VideoPTS, pts)
			}
		}

		// Audio PES analysis (PID 0x101).
		if pid == audioPID && pusi {
			pesPayload := extractPESPayload(payload)
			if len(pesPayload) > 0 {
				if codec.IsADTS(pesPayload) {
					a.ADTSFrames++
				} else {
					a.ADTSSyncErrors++
				}
			}

			pts := extractPESPTS(payload)
			if pts >= 0 {
				a.AudioPTS = append(a.AudioPTS, pts)
			}
		}

		pktIdx++
	}

	// Compute timing statistics.
	a.MaxPCRGapMs = computeMaxGapMs(pcrSamplesToInt64(a.PCRValues))
	a.MaxVideoPTSGapMs = computeMaxGapMs(a.VideoPTS)
	a.MaxAudioPTSGapMs = computeMaxGapMs(a.AudioPTS)
	a.AVDriftMs = computeAVDrift(a.VideoPTS, a.AudioPTS)

	return a
}

// parsePMTStreams extracts elementary stream entries from a PMT payload.
func parsePMTStreams(payload []byte, a *tsAnalysis) {
	// Skip pointer field if present (first byte after header).
	if len(payload) == 0 {
		return
	}
	ptr := int(payload[0])
	pos := 1 + ptr
	if pos >= len(payload) {
		return
	}

	// table_id = payload[pos]
	if pos >= len(payload) || payload[pos] != 0x02 {
		return
	}
	pos++

	// section_syntax_indicator(1) + '0'(1) + reserved(2) + section_length(12)
	if pos+1 >= len(payload) {
		return
	}
	sectionLen := int(payload[pos]&0x0F)<<8 | int(payload[pos+1])
	pos += 2
	sectionEnd := pos + sectionLen
	if sectionEnd > len(payload) {
		sectionEnd = len(payload)
	}

	// program_number(2) + reserved_version_current(1) + section_num(1) + last_section_num(1)
	pos += 5
	if pos+2 > sectionEnd {
		return
	}

	// reserved(3) + PCR_PID(13)
	pos += 2

	// reserved(4) + program_info_length(12)
	if pos+1 >= sectionEnd {
		return
	}
	progInfoLen := int(payload[pos]&0x0F)<<8 | int(payload[pos+1])
	pos += 2 + progInfoLen

	// Elementary stream loop (stop 4 bytes before end for CRC32).
	for pos+4 < sectionEnd-4 {
		if pos >= sectionEnd {
			break
		}
		streamType := payload[pos]
		pos++
		if pos+1 >= sectionEnd {
			break
		}
		esPID := uint16(payload[pos]&0x1F)<<8 | uint16(payload[pos+1])
		pos += 2
		if pos+1 >= sectionEnd {
			break
		}
		esInfoLen := int(payload[pos]&0x0F)<<8 | int(payload[pos+1])
		pos += 2 + esInfoLen

		a.PMTStreams = append(a.PMTStreams, pmtStream{
			PID:        esPID,
			StreamType: streamType,
		})
	}
}

// extractPESPayload extracts the payload data from a PES packet header
// that starts at the beginning of payload (after pointer field processing
// for PSI, but PES doesn't use pointer field — it starts directly).
func extractPESPayload(payload []byte) []byte {
	// PES packet: 00 00 01 [stream_id] [length_hi] [length_lo] ...
	if len(payload) < 9 {
		return nil
	}
	if payload[0] != 0x00 || payload[1] != 0x00 || payload[2] != 0x01 {
		return nil
	}
	// stream_id at [3], length at [4:5]
	// Optional header starts at [6] if stream_id has optional header.
	streamID := payload[3]
	if streamID < 0xC0 && streamID != 0xBD {
		return nil // not audio/video/private_stream_1
	}

	// PES optional header: [6] has flags, [7] has flags2, [8] has header_data_length.
	if len(payload) < 9 {
		return nil
	}
	headerDataLen := int(payload[8])
	pesHeaderLen := 9 + headerDataLen
	if pesHeaderLen > len(payload) {
		return nil
	}
	return payload[pesHeaderLen:]
}

// extractPESPTS extracts the PTS value from a PES header.
// Returns -1 if no PTS is present.
func extractPESPTS(payload []byte) int64 {
	if len(payload) < 14 {
		return -1
	}
	if payload[0] != 0x00 || payload[1] != 0x00 || payload[2] != 0x01 {
		return -1
	}
	// Byte 7: PTS/DTS flags in bits 7-6.
	ptsDtsFlags := (payload[7] >> 6) & 0x03
	if ptsDtsFlags < 2 {
		return -1 // no PTS
	}

	// PTS is 5 bytes starting at offset 9.
	if len(payload) < 14 {
		return -1
	}
	b := payload[9:14]
	pts := (int64(b[0]>>1) & 0x07) << 30
	pts |= int64(b[1]) << 22
	pts |= (int64(b[2]>>1) & 0x7F) << 15
	pts |= int64(b[3]) << 7
	pts |= int64(b[4]>>1) & 0x7F

	return pts
}

// pcrSamplesToInt64 extracts just the base values from PCR samples.
func pcrSamplesToInt64(samples []pcrSample) []int64 {
	vals := make([]int64, len(samples))
	for i, s := range samples {
		vals[i] = s.Base
	}
	return vals
}

// computeMaxGapMs computes the maximum gap between consecutive values
// in 90kHz clock ticks, returned in milliseconds.
func computeMaxGapMs(pts []int64) float64 {
	if len(pts) < 2 {
		return 0
	}
	var maxGap float64
	for i := 1; i < len(pts); i++ {
		gap := float64(pts[i]-pts[i-1]) / 90.0 // 90kHz → ms
		if gap > maxGap {
			maxGap = gap
		}
	}
	return maxGap
}

// computeAVDrift computes the maximum absolute drift between the nearest
// audio and video PTS timestamps.
func computeAVDrift(videoPTS, audioPTS []int64) float64 {
	if len(videoPTS) == 0 || len(audioPTS) == 0 {
		return 0
	}

	var maxDrift float64
	ai := 0

	for _, vpts := range videoPTS {
		// Advance audio index to the nearest audio PTS.
		for ai < len(audioPTS)-1 && audioPTS[ai+1] <= vpts {
			ai++
		}

		// Check drift to current and next audio PTS.
		drift := math.Abs(float64(vpts-audioPTS[ai])) / 90.0
		if drift > maxDrift {
			maxDrift = drift
		}
		if ai+1 < len(audioPTS) {
			drift2 := math.Abs(float64(vpts-audioPTS[ai+1])) / 90.0
			if drift2 > maxDrift {
				maxDrift = drift2
			}
		}
	}

	return maxDrift
}

// ---------------------------------------------------------------------------
// Part 3: Validation Subtests
// ---------------------------------------------------------------------------

func TestTSOutputValidation(t *testing.T) {
	const fixturePath = "testdata/fixture.ts"
	if _, err := os.Stat(fixturePath); os.IsNotExist(err) {
		t.Skip("fixture.ts not found in testdata/")
	}

	output := feedAndCapture(t, fixturePath)
	a := analyzeTSStream(output)

	// Log summary for debugging.
	t.Logf("Total packets: %d", a.TotalPackets)
	t.Logf("PIDs: %v", a.PIDs)
	t.Logf("Video PTS count: %d, Audio PTS count: %d", len(a.VideoPTS), len(a.AudioPTS))
	t.Logf("PCR count: %d", len(a.PCRValues))
	t.Logf("PMT streams: %v", a.PMTStreams)
	t.Logf("IDR count: %d, IDRs without SPS: %d", a.IDRCount, a.IDRsWithoutSPS)
	t.Logf("ADTS frames: %d, ADTS sync errors: %d", a.ADTSFrames, a.ADTSSyncErrors)
	t.Logf("Max PCR gap: %.2fms", a.MaxPCRGapMs)
	t.Logf("Max video PTS gap: %.2fms", a.MaxVideoPTSGapMs)
	t.Logf("Max audio PTS gap: %.2fms", a.MaxAudioPTSGapMs)
	t.Logf("A/V drift: %.2fms", a.AVDriftMs)

	// === Structural ===

	t.Run("packet_alignment", func(t *testing.T) {
		require.Equal(t, 0, len(output)%188,
			"output must be a multiple of 188 bytes (got %d bytes)", len(output))
	})

	t.Run("sync_bytes", func(t *testing.T) {
		require.Equal(t, 0, a.SyncErrors,
			"all packets must start with 0x47 sync byte")
	})

	t.Run("pat_present", func(t *testing.T) {
		require.True(t, a.HasPAT,
			"PAT must be present at PID 0x0000")
	})

	t.Run("pmt_present", func(t *testing.T) {
		require.True(t, a.HasPMT,
			"PMT must be present at PID 0x1000")
	})

	t.Run("pmt_streams", func(t *testing.T) {
		require.GreaterOrEqual(t, len(a.PMTStreams), 2,
			"PMT must declare at least video and audio streams")

		foundVideo := false
		foundAudio := false
		for _, s := range a.PMTStreams {
			if s.PID == videoPID && s.StreamType == 0x1B {
				foundVideo = true
			}
			if s.PID == audioPID && s.StreamType == 0x0F {
				foundAudio = true
			}
		}
		require.True(t, foundVideo,
			"PMT must declare video stream (PID 0x%04X, stream_type 0x1B)", videoPID)
		require.True(t, foundAudio,
			"PMT must declare audio stream (PID 0x%04X, stream_type 0x0F)", audioPID)
	})

	t.Run("continuity_counters", func(t *testing.T) {
		if len(a.ContinuityErrors) > 0 {
			for _, err := range a.ContinuityErrors {
				t.Errorf("continuity counter error: %s", err)
			}
		}
	})

	t.Run("no_orphan_pids", func(t *testing.T) {
		allowedPIDs := map[uint16]string{
			0x0000:   "PAT",
			0x1000:   "PMT",
			videoPID: "video",
			audioPID: "audio",
			0x1FFF:   "null",
		}
		for pid := range a.PIDs {
			_, ok := allowedPIDs[pid]
			if !ok {
				t.Errorf("unexpected PID 0x%04X with %d packets", pid, a.PIDs[pid])
			}
		}
	})

	// === Codec ===

	t.Run("first_video_has_sps", func(t *testing.T) {
		require.True(t, a.FirstVideoHasSPS,
			"first video PES must contain SPS NALU")
	})

	t.Run("idr_frames_have_sps", func(t *testing.T) {
		require.Equal(t, 0, a.IDRsWithoutSPS,
			"all IDR frames (RAI=1) must contain SPS NALU; found %d without SPS out of %d IDRs",
			a.IDRsWithoutSPS, a.IDRCount)
	})

	t.Run("adts_headers_valid", func(t *testing.T) {
		require.Greater(t, a.ADTSFrames, 0,
			"should find audio PES frames with valid ADTS sync word")
		require.Equal(t, 0, a.ADTSSyncErrors,
			"all audio PES frames must have valid ADTS sync word (0xFFF)")
	})

	// === Timing ===

	t.Run("video_pts_monotonic", func(t *testing.T) {
		for i := 1; i < len(a.VideoPTS); i++ {
			if a.VideoPTS[i] <= a.VideoPTS[i-1] {
				t.Errorf("video PTS not strictly increasing at index %d: %d <= %d",
					i, a.VideoPTS[i], a.VideoPTS[i-1])
				break
			}
		}
	})

	t.Run("audio_pts_monotonic", func(t *testing.T) {
		for i := 1; i < len(a.AudioPTS); i++ {
			if a.AudioPTS[i] <= a.AudioPTS[i-1] {
				t.Errorf("audio PTS not strictly increasing at index %d: %d <= %d",
					i, a.AudioPTS[i], a.AudioPTS[i-1])
				break
			}
		}
	})

	t.Run("pcr_interval", func(t *testing.T) {
		require.Greater(t, len(a.PCRValues), 0, "must have at least one PCR value")
		require.LessOrEqual(t, a.MaxPCRGapMs, 100.0,
			"max PCR gap must be <= 100ms (MPEG-TS spec requires <= 40ms, "+
				"we allow 100ms for muxer implementation tolerance); got %.2fms", a.MaxPCRGapMs)
	})

	t.Run("av_drift", func(t *testing.T) {
		require.LessOrEqual(t, a.AVDriftMs, 100.0,
			"max A/V PTS drift must be <= 100ms; got %.2fms", a.AVDriftMs)
	})

	t.Run("no_video_pts_gaps", func(t *testing.T) {
		// At 24fps, one frame = 90000/24 = 3750 ticks = 41.67ms.
		// Allow up to 2x frame duration = 83.33ms.
		require.LessOrEqual(t, a.MaxVideoPTSGapMs, 84.0,
			"max video PTS gap must be <= 84ms (2x 41.67ms at 24fps); got %.2fms",
			a.MaxVideoPTSGapMs)
	})

	t.Run("no_audio_pts_gaps", func(t *testing.T) {
		// At 48kHz with 1024-sample AAC frames: 1024/48000 = 21.33ms.
		// Allow up to 2x = 42.67ms.
		require.LessOrEqual(t, a.MaxAudioPTSGapMs, 43.0,
			"max audio PTS gap must be <= 43ms (2x 21.33ms at 48kHz/1024); got %.2fms",
			a.MaxAudioPTSGapMs)
	})
}
