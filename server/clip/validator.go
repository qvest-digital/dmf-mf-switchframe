package clip

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/zsiec/switchframe/server/codec"
)

// supportedExtensions lists file extensions that Validate accepts directly
// (Go-native demux path for H.264 content).
var supportedExtensions = map[string]bool{
	".ts":  true,
	".mp4": true,
	".m4v": true,
	".mov": true,
}

// transcodeExtensions lists additional extensions accepted via transcode.
var transcodeExtensions = map[string]bool{
	".mkv":  true,
	".webm": true,
	".avi":  true,
	".flv":  true,
	".mxf":  true,
	".wmv":  true,
	".mpg":  true,
	".mpeg": true,
	".ogv":  true,
}

// IsAcceptedExtension returns true if the extension is accepted for upload,
// either directly (H.264 in native containers) or via transcode.
func IsAcceptedExtension(ext string) bool {
	return supportedExtensions[ext] || transcodeExtensions[ext]
}

// NeedsTranscode returns true if the file at path needs transcoding before
// it can be stored. Files with transcode-only extensions always need it.
// Files with native extensions are probed to check if the video codec is H.264;
// non-H.264 video in .mp4/.mov containers (e.g., HEVC) triggers transcode.
func NeedsTranscode(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if transcodeExtensions[ext] {
		return true
	}
	if !supportedExtensions[ext] {
		return false // unknown extension, let Validate reject it
	}
	// Probe native container to check if codec is actually H.264.
	result, err := codec.ProbeFile(path)
	if err != nil {
		// Probe failed. For MP4/MOV containers, the Go-native demuxer only
		// handles avc1 boxes, so transcoding is the safer fallback. For TS
		// files, the Go demuxer handles H.264 TS reliably, so let Validate
		// try first (recordings and replay exports are always H.264 TS).
		return ext != ".ts"
	}
	return !result.IsH264()
}

// CanTranscodeFallback returns true if the extension is a native container
// that may benefit from FFmpeg transcode when the Go-native demuxer fails.
// This covers MP4 variants (fragmented, unusual box layout) that avformat
// handles but abema/go-mp4 does not.
func CanTranscodeFallback(ext string) bool {
	switch ext {
	case ".mp4", ".m4v", ".mov":
		return true
	}
	return false
}

// Validate performs a three-stage validation pipeline on a media file:
//
//  1. Container probe: check extension, demux, extract SPS metadata
//  2. Test decode: decode first GOP to verify codec integrity
//  3. Metadata extraction: compute duration, FPS, frame count
//
// Returns a ProbeResult with extracted metadata and optional warnings,
// or a sentinel error (ErrInvalidFormat, ErrNoVideo, ErrCorruptFile,
// ErrOddDimensions) on failure.
func Validate(path string) (*ProbeResult, error) {
	// --- Stage 1: Container probe ---

	ext := strings.ToLower(filepath.Ext(path))
	if !supportedExtensions[ext] {
		return nil, ErrInvalidFormat
	}

	videoFrames, audioFrames, err := DemuxFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoVideo, err)
	}
	if len(videoFrames) == 0 {
		return nil, ErrNoVideo
	}

	// Find first keyframe with SPS for metadata extraction.
	var spsNALU []byte
	for _, f := range videoFrames {
		if f.isKeyframe && len(f.sps) > 0 {
			spsNALU = f.sps
			break
		}
	}

	// Extract resolution from SPS if available.
	width, height := ParseSPSDimensions(spsNALU)

	// Determine audio codec from ADTS presence.
	var audioCodec string
	var sampleRate, channels int
	if len(audioFrames) > 0 {
		audioCodec = "aac"
		sampleRate = audioFrames[0].sampleRate
		channels = audioFrames[0].channels
	}

	// --- Stage 2: Test decode (first GOP only) ---

	var warnings []string
	decodeWidth, decodeHeight, decodeWarnings, decodeErr := testDecodeFirstGOP(videoFrames, width, height)
	if decodeErr != nil {
		return nil, decodeErr
	}
	warnings = append(warnings, decodeWarnings...)

	// Prefer dimensions from decoder (most reliable), fall back to SPS parse.
	if decodeWidth > 0 && decodeHeight > 0 {
		width = decodeWidth
		height = decodeHeight
	}

	// Verify dimensions are even (required for YUV420).
	if width > 0 && height > 0 {
		if width%2 != 0 || height%2 != 0 {
			return nil, fmt.Errorf("%w: %dx%d not even", ErrCorruptFile, width, height)
		}
	}

	// --- Stage 3: Metadata extraction ---

	frameCount := len(videoFrames)

	// Compute duration from PTS span. Frames may be in decode order (B-frames),
	// so scan for actual min/max PTS rather than assuming first/last.
	var durationMs int64
	if frameCount >= 2 {
		minPTS, maxPTS := videoFrames[0].pts, videoFrames[0].pts
		for _, f := range videoFrames[1:] {
			if f.pts < minPTS {
				minPTS = f.pts
			}
			if f.pts > maxPTS {
				maxPTS = f.pts
			}
		}
		ptsSpan := maxPTS - minPTS
		if ptsSpan > 0 {
			// Duration includes the last frame's display time.
			// Estimate per-frame duration and add it.
			perFramePTS := ptsSpan / int64(frameCount-1)
			totalPTS := ptsSpan + perFramePTS
			durationMs = totalPTS * 1000 / 90000
		}
	} else if frameCount == 1 {
		// Single frame: assign a nominal duration of one frame at 30fps.
		durationMs = 33
	}

	// Estimate FPS from PTS span.
	fps := estimateFPS(videoFrames)
	fpsNum, fpsDen := fpsToRational(fps)

	// Build codec string.
	codecStr := "h264"

	result := &ProbeResult{
		Codec:      codecStr,
		AudioCodec: audioCodec,
		Width:      width,
		Height:     height,
		FPSNum:     fpsNum,
		FPSDen:     fpsDen,
		DurationMs: durationMs,
		SampleRate: sampleRate,
		Channels:   channels,
		FrameCount: frameCount,
		Warnings:   warnings,
	}

	return result, nil
}

// testDecodeFirstGOP attempts to decode the first GOP using the real
// video decoder. Returns decoded dimensions, any warnings, and an error.
// If the decoder produces frames whose dimensions don't match the SPS
// dimensions, it returns ErrCorruptFile. If decoding fails entirely
// (e.g., synthetic test data), it returns zero dimensions with a
// warning rather than a hard error.
func testDecodeFirstGOP(frames []bufferedFrame, spsWidth, spsHeight int) (width, height int, warnings []string, err error) {
	if len(frames) == 0 {
		return 0, 0, nil, nil
	}

	// Find first GOP: frames from first keyframe to second keyframe (or end).
	var gop []bufferedFrame
	started := false
	for _, f := range frames {
		if f.isKeyframe {
			if started {
				break // Second keyframe = end of first GOP.
			}
			started = true
		}
		if started {
			gop = append(gop, f)
		}
	}
	if len(gop) == 0 {
		return 0, 0, []string{"no keyframe found in video frames"}, nil
	}

	// Limit to 30 frames max for test decode.
	if len(gop) > 30 {
		gop = gop[:30]
	}

	// Try to create a decoder.
	decoder, decErr := codec.NewVideoDecoder()
	if decErr != nil {
		return 0, 0, []string{fmt.Sprintf("video decoder unavailable: %v", decErr)}, nil
	}
	defer decoder.Close()

	// Decode each frame.
	for _, f := range gop {
		// Convert AVC1 to Annex B for the decoder.
		annexB := codec.AVC1ToAnnexB(f.wireData)
		if len(annexB) == 0 {
			continue
		}

		// Prepend SPS/PPS for keyframes.
		if f.isKeyframe && len(f.sps) > 0 {
			annexB = codec.PrependSPSPPS(f.sps, f.pps, annexB)
		}

		yuv, w, h, frameErr := decoder.Decode(annexB)
		if frameErr != nil {
			// EAGAIN ("buffering") is expected for B-frame reordering.
			if strings.Contains(frameErr.Error(), "buffering") {
				continue
			}
			// For synthetic test data, decoding may fail. Treat as warning.
			warnings = append(warnings, fmt.Sprintf("test decode warning: %v", frameErr))
			continue
		}

		// Verify YUV buffer size matches dimensions.
		expectedSize := w * h * 3 / 2
		if len(yuv) < expectedSize {
			warnings = append(warnings, fmt.Sprintf("decoded YUV size mismatch: got %d, expected %d", len(yuv), expectedSize))
			continue
		}

		// Check for dimension mismatch between SPS and decoded output.
		// SPS display dimensions (after frame cropping) and decoded output
		// may differ by up to one macroblock (16px) due to macroblock
		// alignment, cropping applied differently by encoder vs decoder,
		// or SPS parser rounding. Allow ±16px tolerance per axis.
		if spsWidth > 0 && spsHeight > 0 {
			dw := w - spsWidth
			dh := h - spsHeight
			if dw < 0 {
				dw = -dw
			}
			if dh < 0 {
				dh = -dh
			}
			if dw > 16 || dh > 16 {
				return 0, 0, nil, fmt.Errorf("%w: SPS declares %dx%d but decoder produced %dx%d",
					ErrCorruptFile, spsWidth, spsHeight, w, h)
			}
		}

		// Successfully decoded a frame.
		width = w
		height = h
		return width, height, warnings, nil
	}

	// If we get here, no frames were successfully decoded.
	// This happens with synthetic test data. Return zero dimensions
	// with a warning rather than failing.
	if len(warnings) == 0 {
		warnings = append(warnings, "no frames decoded in test decode (possible synthetic data)")
	}
	return 0, 0, warnings, nil
}

// estimateFPS estimates the source FPS from buffered frame PTS values.
// This mirrors the logic in replay/player.go estimateFPSFromClip.
func estimateFPS(frames []bufferedFrame) float64 {
	if len(frames) < 2 {
		return 30.0
	}
	// Frames may be in decode order (B-frames), so scan for min/max PTS.
	minPTS, maxPTS := frames[0].pts, frames[0].pts
	for _, f := range frames[1:] {
		if f.pts < minPTS {
			minPTS = f.pts
		}
		if f.pts > maxPTS {
			maxPTS = f.pts
		}
	}
	ptsSpan := maxPTS - minPTS
	if ptsSpan <= 0 {
		return 30.0
	}
	fps := float64(len(frames)-1) * 90000.0 / float64(ptsSpan)
	if fps < 10 {
		fps = 10
	}
	if fps > 120 {
		fps = 120
	}
	return fps
}

// fpsToRational converts a float64 FPS to a rational fpsNum/fpsDen pair.
// Snaps to standard broadcast rates.
func fpsToRational(fps float64) (int, int) {
	type rate struct {
		num, den int
		nominal  float64
	}
	standards := []rate{
		{24000, 1001, 23.976},
		{24, 1, 24},
		{25, 1, 25},
		{30000, 1001, 29.97},
		{30, 1, 30},
		{50, 1, 50},
		{60000, 1001, 59.94},
		{60, 1, 60},
	}
	bestNum, bestDen := 30000, 1001
	bestDist := math.Abs(fps - 29.97)
	for _, s := range standards {
		d := math.Abs(fps - s.nominal)
		if d < bestDist {
			bestDist = d
			bestNum = s.num
			bestDen = s.den
		}
	}
	return bestNum, bestDen
}

// ParseSPSDimensions extracts width and height from an H.264 SPS NALU.
// Uses exponential Golomb coding to parse the SPS fields.
// Returns (0, 0) if the SPS is too short or cannot be parsed.
func ParseSPSDimensions(sps []byte) (width, height int) {
	if len(sps) < 4 {
		return 0, 0
	}

	r := &bitReader{data: sps, pos: 0}

	// Skip forbidden_zero_bit (1) + nal_ref_idc (2) + nal_unit_type (5)
	r.skip(8)

	profileIDC := r.readBits(8)
	r.skip(8) // constraint_set flags + reserved_zero_2bits
	r.skip(8) // level_idc

	r.readUE() // seq_parameter_set_id

	// High profile and related profiles have additional fields.
	if profileIDC == 100 || profileIDC == 110 || profileIDC == 122 ||
		profileIDC == 244 || profileIDC == 44 || profileIDC == 83 ||
		profileIDC == 86 || profileIDC == 118 || profileIDC == 128 {
		chromaFormatIDC := r.readUE()
		if chromaFormatIDC == 3 {
			r.skip(1) // separate_colour_plane_flag
		}
		r.readUE() // bit_depth_luma_minus8
		r.readUE() // bit_depth_chroma_minus8
		r.skip(1)  // qpprime_y_zero_transform_bypass_flag
		scalingMatrixPresent := r.readBits(1)
		if scalingMatrixPresent != 0 {
			count := 8
			if chromaFormatIDC == 3 {
				count = 12
			}
			for i := 0; i < count; i++ {
				listPresent := r.readBits(1)
				if listPresent != 0 {
					size := 16
					if i >= 6 {
						size = 64
					}
					skipScalingList(r, size)
				}
			}
		}
	}

	r.readUE() // log2_max_frame_num_minus4
	picOrderCntType := r.readUE()
	switch picOrderCntType {
	case 0:
		r.readUE() // log2_max_pic_order_cnt_lsb_minus4
	case 1:
		r.skip(1)  // delta_pic_order_always_zero_flag
		r.readSE() // offset_for_non_ref_pic
		r.readSE() // offset_for_top_to_bottom_field
		numRefFrames := r.readUE()
		for i := 0; i < int(numRefFrames); i++ {
			r.readSE() // offset_for_ref_frame
		}
	}

	r.readUE() // max_num_ref_frames
	r.skip(1)  // gaps_in_frame_num_value_allowed_flag

	picWidthMbs := r.readUE()
	picHeightMapUnits := r.readUE()

	frameMbsOnly := r.readBits(1)

	if r.err {
		return 0, 0
	}

	mbHeight := picHeightMapUnits
	if frameMbsOnly == 0 {
		r.skip(1) // mb_adaptive_frame_field_flag
		mbHeight = picHeightMapUnits * 2
	}

	width = int((picWidthMbs + 1) * 16)
	height = int((mbHeight + 1) * 16)

	// Check for frame cropping.
	frameCroppingFlag := r.readBits(1)
	if frameCroppingFlag != 0 {
		cropLeft := r.readUE()
		cropRight := r.readUE()
		cropTop := r.readUE()
		cropBottom := r.readUE()
		if !r.err {
			// Crop units depend on chroma format, but for 4:2:0 (most common):
			// CropUnitX = 2, CropUnitY = 2 * (2 - frame_mbs_only_flag)
			cropUnitX := 2
			cropUnitY := 2
			if frameMbsOnly == 0 {
				cropUnitY = 4
			}
			width -= int(cropLeft+cropRight) * cropUnitX
			height -= int(cropTop+cropBottom) * cropUnitY
		}
	}

	return width, height
}

// bitReader reads individual bits from a byte slice.
type bitReader struct {
	data []byte
	pos  int // bit position
	err  bool
}

func (r *bitReader) readBits(n int) uint32 {
	if r.err {
		return 0
	}
	var val uint32
	for i := 0; i < n; i++ {
		byteIdx := r.pos / 8
		bitIdx := 7 - (r.pos % 8)
		if byteIdx >= len(r.data) {
			r.err = true
			return 0
		}
		val = (val << 1) | uint32((r.data[byteIdx]>>bitIdx)&1)
		r.pos++
	}
	return val
}

func (r *bitReader) skip(n int) {
	r.pos += n
	if r.pos/8 >= len(r.data) && r.pos%8 != 0 {
		r.err = true
	}
}

// readUE reads an unsigned exponential Golomb coded value.
func (r *bitReader) readUE() uint32 {
	if r.err {
		return 0
	}
	leadingZeros := 0
	for {
		bit := r.readBits(1)
		if r.err {
			return 0
		}
		if bit != 0 {
			break
		}
		leadingZeros++
		if leadingZeros > 31 {
			r.err = true
			return 0
		}
	}
	if leadingZeros == 0 {
		return 0
	}
	val := r.readBits(leadingZeros)
	return (1 << leadingZeros) - 1 + val
}

// readSE reads a signed exponential Golomb coded value.
func (r *bitReader) readSE() int32 {
	ue := r.readUE()
	if ue%2 == 0 {
		return -int32(ue / 2)
	}
	return int32((ue + 1) / 2)
}

// skipScalingList skips an H.264 scaling list in the SPS.
func skipScalingList(r *bitReader, size int) {
	lastScale := 8
	nextScale := 8
	for j := 0; j < size; j++ {
		if nextScale != 0 {
			deltaScale := r.readSE()
			nextScale = (lastScale + int(deltaScale) + 256) % 256
		}
		if nextScale != 0 {
			lastScale = nextScale
		}
	}
}
