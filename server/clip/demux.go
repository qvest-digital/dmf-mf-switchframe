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

// DemuxFile reads a media file and extracts H.264 video frames and AAC
// audio frames. The format is detected by file extension (.ts or .mp4).
// Video frames are returned in AVC1 format with SPS/PPS set on keyframes.
// Audio frames contain raw AAC payloads (ADTS headers stripped).
// Both slices are sorted by PTS in ascending order.
func DemuxFile(path string) ([]bufferedFrame, []bufferedAudioFrame, error) {
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
func demuxTSFile(path string) ([]bufferedFrame, []bufferedAudioFrame, error) {
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
func demuxTS(r io.Reader) ([]bufferedFrame, []bufferedAudioFrame, error) {
	dmx := astits.NewDemuxer(context.Background(), r)

	var (
		videoFrames []bufferedFrame
		audioFrames []bufferedAudioFrame
		lastSPS     []byte
		lastPPS     []byte
		packetCount int
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
			return nil, nil, fmt.Errorf("demux: %w", err)
		}
		packetCount++

		if d.PES == nil {
			continue
		}

		oh := d.PES.Header.OptionalHeader

		switch {
		case d.PES.Header.IsVideoStream():
			frame, sps, pps := parseVideoFrameForClip(d, oh, lastSPS, lastPPS)
			if frame == nil {
				if sps != nil {
					lastSPS = sps
				}
				if pps != nil {
					lastPPS = pps
				}
				continue
			}
			if sps != nil {
				lastSPS = sps
			}
			if pps != nil {
				lastPPS = pps
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

	// Sort frames by PTS.
	sort.Slice(videoFrames, func(i, j int) bool {
		return videoFrames[i].pts < videoFrames[j].pts
	})
	sort.Slice(audioFrames, func(i, j int) bool {
		return audioFrames[i].pts < audioFrames[j].pts
	})

	return videoFrames, audioFrames, nil
}

// parseVideoFrameForClip converts a PES video packet into a bufferedFrame.
// Returns updated SPS/PPS if found in this frame's NALUs. Returns nil frame
// if the packet contains only parameter sets or is empty.
func parseVideoFrameForClip(d *astits.DemuxerData, oh *astits.PESOptionalHeader, lastSPS, lastPPS []byte) (*bufferedFrame, []byte, []byte) {
	if len(d.PES.Data) == 0 {
		return nil, nil, nil
	}

	// PES video data is Annex B format -- convert to AVC1.
	avc1 := codec.AnnexBToAVC1(d.PES.Data)
	if len(avc1) == 0 {
		return nil, nil, nil
	}

	// Extract NALUs to find SPS, PPS, and keyframe status.
	nalus := codec.ExtractNALUs(avc1)

	var (
		sps        []byte
		pps        []byte
		isKeyframe bool
	)

	// Build wire data excluding SPS/PPS NALUs (they go in separate fields).
	var wireNALUs [][]byte

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

	if len(wireNALUs) == 0 {
		return nil, sps, pps
	}

	// Rebuild AVC1 wire data from non-parameter-set NALUs.
	wireData := nalusToAVC1(wireNALUs)

	// Use last known SPS/PPS if not in this frame.
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

	frame := &bufferedFrame{
		wireData:   wireData,
		pts:        pts,
		isKeyframe: isKeyframe,
	}
	if isKeyframe {
		frame.sps = frameSPS
		frame.pps = framePPS
	}

	return frame, sps, pps
}

// parseAudioFramesForClip splits a PES audio packet into individual
// bufferedAudioFrame entries. PES packets often contain multiple
// concatenated ADTS frames; each is returned separately with correct PTS.
func parseAudioFramesForClip(d *astits.DemuxerData, oh *astits.PESOptionalHeader) []bufferedAudioFrame {
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

	frames := make([]bufferedAudioFrame, len(payloads))
	for i, payload := range payloads {
		// Each AAC-LC frame is 1024 samples. PTS ticks at 90kHz.
		pts := basePTS + int64(i)*1024*90000/int64(sampleRate)
		frames[i] = bufferedAudioFrame{
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
