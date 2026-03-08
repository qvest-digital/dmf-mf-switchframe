package demo

import (
	"context"
	"fmt"
	"io"
	"os"

	astits "github.com/asticode/go-astits"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

// demuxResult holds pre-parsed video and audio frames from a TS file.
type demuxResult struct {
	Video []media.VideoFrame
	Audio []media.AudioFrame
}

// demuxTSFile reads an MPEG-TS file and extracts video and audio frames.
// Video frames are returned in AVC1 format with SPS/PPS set on keyframes.
// Audio frames have ADTS headers stripped (raw AAC).
func demuxTSFile(path string) (*demuxResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	return demuxTS(f)
}

// demuxTS reads MPEG-TS data from a reader and extracts video and audio frames.
func demuxTS(r io.Reader) (*demuxResult, error) {
	dmx := astits.NewDemuxer(context.Background(), r)

	var (
		result  demuxResult
		lastSPS []byte
		lastPPS []byte
	)

	for {
		d, err := dmx.NextData()
		if err != nil {
			if err == astits.ErrNoMorePackets {
				break
			}
			// io.EOF means we've read all data
			if err.Error() == "astits: fetching next packet failed: EOF" {
				break
			}
			return nil, fmt.Errorf("demux: %w", err)
		}

		if d.PES == nil {
			continue
		}

		oh := d.PES.Header.OptionalHeader

		switch {
		case d.PES.Header.IsVideoStream():
			frame, sps, pps, err := parseVideoFrame(d, oh, lastSPS, lastPPS)
			if err != nil {
				return nil, err
			}
			if frame == nil {
				continue
			}
			if sps != nil {
				lastSPS = sps
			}
			if pps != nil {
				lastPPS = pps
			}
			result.Video = append(result.Video, *frame)

		case isAudioStream(d.PES.Header.StreamID):
			frames := parseAudioFrames(d, oh)
			result.Audio = append(result.Audio, frames...)
		}
	}

	if len(result.Video) == 0 {
		return nil, fmt.Errorf("no video frames found in %v", r)
	}

	return &result, nil
}

// parseVideoFrame converts a PES video packet into a media.VideoFrame.
// Returns updated SPS/PPS if found in this frame's NALUs.
func parseVideoFrame(d *astits.DemuxerData, oh *astits.PESOptionalHeader, lastSPS, lastPPS []byte) (*media.VideoFrame, []byte, []byte, error) {
	if len(d.PES.Data) == 0 {
		return nil, nil, nil, nil
	}

	// PES video data is Annex B format — convert to AVC1.
	avc1 := codec.AnnexBToAVC1(d.PES.Data)
	if len(avc1) == 0 {
		return nil, nil, nil, nil
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
		return nil, sps, pps, nil
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

	var pts, dts int64
	if oh != nil && oh.PTS != nil {
		pts = oh.PTS.Base
	}
	if oh != nil && oh.DTS != nil {
		dts = oh.DTS.Base
	} else {
		dts = pts
	}

	// Check for random access from adaptation field.
	if d.FirstPacket != nil && d.FirstPacket.AdaptationField != nil {
		if d.FirstPacket.AdaptationField.RandomAccessIndicator {
			isKeyframe = true
		}
	}

	frame := &media.VideoFrame{
		PTS:        pts,
		DTS:        dts,
		IsKeyframe: isKeyframe,
		WireData:   wireData,
		Codec:      "h264",
	}
	if isKeyframe {
		frame.SPS = frameSPS
		frame.PPS = framePPS
	}

	return frame, sps, pps, nil
}

// parseAudioFrames splits a PES audio packet into individual AAC frames.
// PES packets often contain multiple concatenated ADTS frames; each is
// returned as a separate media.AudioFrame with correct PTS offsets.
func parseAudioFrames(d *astits.DemuxerData, oh *astits.PESOptionalHeader) []media.AudioFrame {
	if len(d.PES.Data) == 0 {
		return nil
	}

	var basePTS int64
	if oh != nil && oh.PTS != nil {
		basePTS = oh.PTS.Base
	}

	// Split concatenated ADTS frames into individual raw AAC payloads.
	payloads := codec.SplitADTSFrames(d.PES.Data)

	frames := make([]media.AudioFrame, len(payloads))
	for i, payload := range payloads {
		// Each AAC-LC frame is 1024 samples. PTS ticks at 90kHz.
		// Offset = i * 1024 * 90000 / sampleRate
		pts := basePTS + int64(i)*1024*90000/48000
		frames[i] = media.AudioFrame{
			PTS:        pts,
			Data:       payload,
			SampleRate: 48000,
			Channels:   2,
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

// parseSPS extracts width, height, and codec string from an H.264 SPS NALU.
// Returns a basic codec string like "avc1.640028" and the resolution.
func parseSPS(sps []byte) (codecStr string, width, height int) {
	if len(sps) < 4 {
		return "avc1.42C01E", 0, 0
	}

	codecStr = codec.ParseSPSCodecString(sps)

	// Parse width/height from SPS using Exp-Golomb decoding.
	w, h, ok := decodeSPSResolution(sps)
	if ok {
		width = w
		height = h
	}

	return codecStr, width, height
}

// decodeSPSResolution parses width and height from SPS using Exp-Golomb coding.
func decodeSPSResolution(sps []byte) (width, height int, ok bool) {
	if len(sps) < 5 {
		return 0, 0, false
	}

	// Simple bit reader for Exp-Golomb decoding.
	bits := &bitReader{data: sps, pos: 0}

	// Skip NALU type byte (8 bits).
	bits.skip(8)

	profileIDC := bits.readBits(8) // profile_idc
	bits.skip(8)                   // constraint_set flags + reserved
	bits.skip(8)                   // level_idc

	bits.readExpGolomb() // seq_parameter_set_id

	// High profiles have additional chroma/scaling fields.
	if profileIDC == 100 || profileIDC == 110 || profileIDC == 122 ||
		profileIDC == 244 || profileIDC == 44 || profileIDC == 83 ||
		profileIDC == 86 || profileIDC == 118 || profileIDC == 128 {

		chromaFormatIDC := bits.readExpGolomb() // chroma_format_idc
		if chromaFormatIDC == 3 {
			bits.skip(1) // separate_colour_plane_flag
		}
		bits.readExpGolomb() // bit_depth_luma_minus8
		bits.readExpGolomb() // bit_depth_chroma_minus8
		bits.skip(1)         // qpprime_y_zero_transform_bypass_flag

		scalingMatrixPresent := bits.readBits(1) // seq_scaling_matrix_present_flag
		if scalingMatrixPresent != 0 {
			count := 8
			if chromaFormatIDC == 3 {
				count = 12
			}
			for i := 0; i < int(count); i++ {
				if bits.readBits(1) != 0 { // seq_scaling_list_present_flag
					size := 16
					if i >= 6 {
						size = 64
					}
					skipScalingList(bits, size)
				}
			}
		}
	}

	bits.readExpGolomb() // log2_max_frame_num_minus4

	picOrderCntType := bits.readExpGolomb() // pic_order_cnt_type
	switch picOrderCntType {
	case 0:
		bits.readExpGolomb() // log2_max_pic_order_cnt_lsb_minus4
	case 1:
		bits.skip(1)               // delta_pic_order_always_zero_flag
		bits.readSignedExpGolomb() // offset_for_non_ref_pic
		bits.readSignedExpGolomb() // offset_for_top_to_bottom_field
		numRefFrames := bits.readExpGolomb()
		for i := uint64(0); i < numRefFrames; i++ {
			bits.readSignedExpGolomb() // offset_for_ref_frame
		}
	}

	bits.readExpGolomb() // max_num_ref_frames
	bits.skip(1)         // gaps_in_frame_num_value_allowed_flag

	picWidthMBS := bits.readExpGolomb()  // pic_width_in_mbs_minus1
	picHeightMBS := bits.readExpGolomb() // pic_height_in_map_units_minus1

	frameMBSOnly := bits.readBits(1) // frame_mbs_only_flag

	width = int((picWidthMBS + 1) * 16)
	height = int((picHeightMBS + 1) * 16)
	if frameMBSOnly == 0 {
		bits.skip(1) // mb_adaptive_frame_field_flag
		height *= 2
	}

	bits.skip(1) // direct_8x8_inference_flag

	// Frame cropping.
	frameCroppingFlag := bits.readBits(1)
	if frameCroppingFlag != 0 {
		cropLeft := bits.readExpGolomb()
		cropRight := bits.readExpGolomb()
		cropTop := bits.readExpGolomb()
		cropBottom := bits.readExpGolomb()

		// Crop units depend on chroma format; for 4:2:0 (most common): 2 pixels per unit.
		cropUnitX := 2
		cropUnitY := 2
		if frameMBSOnly == 0 {
			cropUnitY = 4
		}
		width -= int(cropLeft+cropRight) * cropUnitX
		height -= int(cropTop+cropBottom) * cropUnitY
	}

	if bits.err {
		return 0, 0, false
	}

	return width, height, true
}

// skipScalingList skips a scaling list in the SPS bitstream.
func skipScalingList(bits *bitReader, size int) {
	lastScale := int64(8)
	nextScale := int64(8)
	for j := 0; j < size; j++ {
		if nextScale != 0 {
			delta := bits.readSignedExpGolomb()
			nextScale = (lastScale + delta + 256) % 256
		}
		if nextScale != 0 {
			lastScale = nextScale
		}
	}
}

// bitReader provides bit-level reading for Exp-Golomb decoding.
type bitReader struct {
	data []byte
	pos  int // bit position
	err  bool
}

func (b *bitReader) readBits(n int) uint64 {
	var val uint64
	for i := 0; i < n; i++ {
		byteIdx := b.pos / 8
		bitIdx := 7 - (b.pos % 8)
		if byteIdx >= len(b.data) {
			b.err = true
			return 0
		}
		val = (val << 1) | uint64((b.data[byteIdx]>>bitIdx)&1)
		b.pos++
	}
	return val
}

func (b *bitReader) skip(n int) {
	b.pos += n
}

func (b *bitReader) readExpGolomb() uint64 {
	leadingZeros := 0
	for {
		bit := b.readBits(1)
		if b.err {
			return 0
		}
		if bit == 1 {
			break
		}
		leadingZeros++
		if leadingZeros > 31 {
			b.err = true
			return 0
		}
	}
	if leadingZeros == 0 {
		return 0
	}
	val := b.readBits(leadingZeros)
	return (1 << leadingZeros) - 1 + val
}

func (b *bitReader) readSignedExpGolomb() int64 {
	v := b.readExpGolomb()
	if v%2 == 0 {
		return -int64(v / 2)
	}
	return int64((v + 1) / 2)
}
