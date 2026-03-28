package clip

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	astits "github.com/asticode/go-astits"
	"github.com/zsiec/switchframe/server/codec"
)

// DemuxFile reads a media file and extracts H.264/HEVC video frames and AAC
// audio frames. The format is detected by file extension (.ts or .mp4).
// Video frames are returned in wire format (4-byte length-prefixed NALUs) with
// parameter sets (SPS/PPS for H.264, VPS/SPS/PPS for HEVC) set on keyframes,
// in decode order (not display/PTS order) so that the decoder receives
// reference frames before B-frames that depend on them.
// Audio frames contain raw AAC payloads (ADTS headers stripped), sorted by PTS.
func DemuxFile(path string) ([]BufferedFrame, []BufferedAudioFrame, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".ts", ".mts", ".m2ts":
		return demuxTSFile(path)
	case ".mp4", ".m4v", ".mov":
		return demuxMP4(path)
	default:
		return nil, nil, fmt.Errorf("unsupported format: %s", ext)
	}
}

// demuxTSFile opens an MPEG-TS file and demuxes it into video and audio frames.
func demuxTSFile(path string) ([]BufferedFrame, []BufferedAudioFrame, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Check for empty file.
	info, err := f.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() == 0 {
		return nil, nil, fmt.Errorf("empty file: %s", path)
	}

	return demuxTS(f)
}

// demuxTS reads MPEG-TS data from a reader and extracts video and audio frames.
func demuxTS(r io.Reader) ([]BufferedFrame, []BufferedAudioFrame, error) {
	dmx := astits.NewDemuxer(context.Background(), r)

	const maxDemuxErrors = 100 // give up after this many non-fatal errors

	var (
		videoFrames []BufferedFrame
		audioFrames []BufferedAudioFrame
		lastVPS     []byte
		lastSPS     []byte
		lastPPS     []byte
		videoCodec  string // "h264" or "h265", detected from PMT or first NALU
		packetCount int
		errCount    int
	)

	for {
		d, err := dmx.NextData()
		if err != nil {
			if err == astits.ErrNoMorePackets {
				break
			}
			// io.EOF means we've read all data.
			if err.Error() == "astits: fetching next packet failed: EOF" {
				break
			}
			// If we haven't seen any packets at all, the file is likely invalid.
			if packetCount == 0 {
				return nil, nil, fmt.Errorf("invalid TS data: %w", err)
			}
			// Skip non-fatal errors (CRC mismatches, malformed PSI tables,
			// corrupted PES headers). These are common in damaged streams
			// and should not discard all previously parsed frames.
			errCount++
			if errCount > maxDemuxErrors {
				return videoFrames, audioFrames, fmt.Errorf("demux: too many errors (%d): last: %w", errCount, err)
			}
			continue
		}
		packetCount++

		// Detect video codec from PMT stream type.
		if d.PMT != nil && videoCodec == "" {
			for _, es := range d.PMT.ElementaryStreams {
				switch es.StreamType {
				case astits.StreamTypeH264Video:
					videoCodec = "h264"
				case astits.StreamTypeH265Video:
					videoCodec = "h265"
				}
			}
		}

		if d.PES == nil {
			continue
		}

		oh := d.PES.Header.OptionalHeader

		switch {
		case d.PES.Header.IsVideoStream():
			frame, vps, sps, pps := parseVideoFrameForClip(d, oh, lastVPS, lastSPS, lastPPS, videoCodec)
			if frame == nil {
				if vps != nil {
					lastVPS = vps
				}
				if sps != nil {
					lastSPS = sps
				}
				if pps != nil {
					lastPPS = pps
				}
				continue
			}
			if vps != nil {
				lastVPS = vps
			}
			if sps != nil {
				lastSPS = sps
			}
			if pps != nil {
				lastPPS = pps
			}
			// Auto-detect codec from first frame if PMT didn't tell us.
			if videoCodec == "" {
				videoCodec = frame.codec
			}
			videoFrames = append(videoFrames, *frame)

		case isAudioStream(d.PES.Header.StreamID):
			frames := parseAudioFramesForClip(d, oh)
			audioFrames = append(audioFrames, frames...)
		}
	}

	// Validate we got some video data.
	if len(videoFrames) == 0 {
		if packetCount == 0 {
			return nil, nil, fmt.Errorf("invalid TS file: no packets found")
		}
		return nil, nil, fmt.Errorf("no video frames found in TS data")
	}

	// Video frames are kept in PES arrival order (decode order) because the
	// H.264 decoder needs reference frames before B-frames that depend on them.
	// Sorting by PTS would reorder to display order, breaking B-frame decoding.

	// Audio frames have no B-frame reordering — sort by PTS for proper playback.
	sort.Slice(audioFrames, func(i, j int) bool {
		return audioFrames[i].pts < audioFrames[j].pts
	})

	return videoFrames, audioFrames, nil
}

// parseVideoFrameForClip converts a PES video packet into a BufferedFrame.
// Returns updated VPS/SPS/PPS if found in this frame's NALUs. Returns nil frame
// if the packet contains only parameter sets or is empty.
// videoCodec is "h264", "h265", or "" (auto-detect from NALU types).
func parseVideoFrameForClip(d *astits.DemuxerData, oh *astits.PESOptionalHeader, lastVPS, lastSPS, lastPPS []byte, videoCodec string) (*BufferedFrame, []byte, []byte, []byte) {
	if len(d.PES.Data) == 0 {
		return nil, nil, nil, nil
	}

	// PES video data is Annex B format -- convert to wire format (4-byte length prefix).
	wireFormatData := codec.AnnexBToWire(d.PES.Data)
	if len(wireFormatData) == 0 {
		return nil, nil, nil, nil
	}

	// Extract NALUs to find parameter sets and keyframe status.
	nalus := codec.ExtractNALUs(wireFormatData)

	// Auto-detect codec from NALU types if not specified.
	isHEVC := videoCodec == "h265"
	if videoCodec == "" {
		for _, nalu := range nalus {
			if len(nalu) < 1 {
				continue
			}
			if isHEVC {
				break
			}
			// Check for HEVC VPS (type 32) — unique to HEVC, never appears in H.264.
			hevcType := codec.HEVCNALUType(nalu[0])
			if hevcType == codec.HEVCNALVPS {
				isHEVC = true
				break
			}
			// Check for H.264 SPS (type 7) or IDR (type 5) — confirms H.264.
			h264Type := nalu[0] & 0x1F
			if h264Type == 7 || h264Type == 5 {
				break // confirmed H.264
			}
		}
	}

	var (
		vps        []byte
		sps        []byte
		pps        []byte
		isKeyframe bool
	)

	// Build wire data excluding parameter set NALUs (they go in separate fields).
	var wireNALUs [][]byte

	if isHEVC {
		for _, nalu := range nalus {
			if len(nalu) < 2 { // HEVC NALUs need at least 2-byte header
				continue
			}
			naluType := codec.HEVCNALUType(nalu[0])
			switch naluType {
			case codec.HEVCNALVPS:
				vps = make([]byte, len(nalu))
				copy(vps, nalu)
			case codec.HEVCNALSPS:
				sps = make([]byte, len(nalu))
				copy(sps, nalu)
			case codec.HEVCNALPPS:
				pps = make([]byte, len(nalu))
				copy(pps, nalu)
			case codec.HEVCNALAUD, codec.HEVCNALFillerData:
				// Skip AUD and filler data.
			case codec.HEVCNALSEIPrefix, codec.HEVCNALSEISuffix:
				wireNALUs = append(wireNALUs, nalu)
			default:
				if codec.IsHEVCKeyframe(naluType) {
					isKeyframe = true
				}
				if codec.IsHEVCVCL(naluType) || naluType >= 32 {
					wireNALUs = append(wireNALUs, nalu)
				}
			}
		}
	} else {
		for _, nalu := range nalus {
			if len(nalu) == 0 {
				continue
			}
			naluType := nalu[0] & 0x1F
			switch naluType {
			case 7: // SPS
				sps = make([]byte, len(nalu))
				copy(sps, nalu)
			case 8: // PPS
				pps = make([]byte, len(nalu))
				copy(pps, nalu)
			case 5: // IDR
				isKeyframe = true
				wireNALUs = append(wireNALUs, nalu)
			default:
				wireNALUs = append(wireNALUs, nalu)
			}
		}
	}

	if len(wireNALUs) == 0 {
		return nil, vps, sps, pps
	}

	// Rebuild wire data from non-parameter-set NALUs.
	wireData := nalusToAVC1(wireNALUs)

	// Use last known parameter sets if not in this frame.
	frameVPS := vps
	if frameVPS == nil {
		frameVPS = lastVPS
	}
	frameSPS := sps
	if frameSPS == nil {
		frameSPS = lastSPS
	}
	framePPS := pps
	if framePPS == nil {
		framePPS = lastPPS
	}

	var pts int64
	if oh != nil && oh.PTS != nil {
		pts = oh.PTS.Base
	}

	// Check for random access from adaptation field.
	if d.FirstPacket != nil && d.FirstPacket.AdaptationField != nil {
		if d.FirstPacket.AdaptationField.RandomAccessIndicator {
			isKeyframe = true
		}
	}

	codecStr := "h264"
	if isHEVC {
		codecStr = "h265"
	}

	frame := &BufferedFrame{
		wireData:   wireData,
		pts:        pts,
		isKeyframe: isKeyframe,
		codec:      codecStr,
	}
	if isKeyframe {
		frame.vps = frameVPS
		frame.sps = frameSPS
		frame.pps = framePPS
	}

	return frame, vps, sps, pps
}

// parseAudioFramesForClip splits a PES audio packet into individual
// BufferedAudioFrame entries. PES packets often contain multiple
// concatenated ADTS frames; each is returned separately with correct PTS.
func parseAudioFramesForClip(d *astits.DemuxerData, oh *astits.PESOptionalHeader) []BufferedAudioFrame {
	if len(d.PES.Data) == 0 {
		return nil
	}

	var basePTS int64
	if oh != nil && oh.PTS != nil {
		basePTS = oh.PTS.Base
	}

	// Extract real sample rate and channel count from ADTS header.
	sampleRate, channels := codec.ParseADTSInfo(d.PES.Data)
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if channels <= 0 {
		channels = 2
	}

	// Split concatenated ADTS frames into individual raw AAC payloads.
	payloads := codec.SplitADTSFrames(d.PES.Data)

	frames := make([]BufferedAudioFrame, len(payloads))
	for i, payload := range payloads {
		// Each AAC-LC frame is 1024 samples. PTS ticks at 90kHz.
		pts := basePTS + int64(i)*1024*90000/int64(sampleRate)
		frames[i] = BufferedAudioFrame{
			data:       payload,
			pts:        pts,
			sampleRate: sampleRate,
			channels:   channels,
		}
	}
	return frames
}

// isAudioStream checks if a PES stream ID is an audio stream (0xC0-0xDF).
func isAudioStream(streamID uint8) bool {
	return streamID >= 0xC0 && streamID <= 0xDF
}

// nalusToAVC1 converts a slice of raw NALUs to AVC1 format (4-byte length prefix).
func nalusToAVC1(nalus [][]byte) []byte {
	size := 0
	for _, n := range nalus {
		size += 4 + len(n)
	}
	out := make([]byte, size)
	pos := 0
	for _, n := range nalus {
		out[pos] = byte(len(n) >> 24)
		out[pos+1] = byte(len(n) >> 16)
		out[pos+2] = byte(len(n) >> 8)
		out[pos+3] = byte(len(n))
		copy(out[pos+4:], n)
		pos += 4 + len(n)
	}
	return out
}
