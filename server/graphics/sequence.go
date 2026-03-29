package graphics

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxSequenceMemory = 512 << 20 // 512 MB total RGBA limit
	maxSequenceFrames = 7200      // 2 min at 60fps
)

var (
	ErrSequenceTooLarge = errors.New("graphics: sequence exceeds memory limit")
	ErrSequenceEmpty        = errors.New("graphics: sequence ZIP contains no PNG files")
	ErrSequenceSizeMismatch = errors.New("graphics: all PNGs must have same dimensions")
)

// seqPhase represents the current phase of an animated PNG sequence.
type seqPhase int

const (
	seqPhaseIdle seqPhase = iota // not playing
	seqPhaseIn                   // playing animate-in segment
	seqPhaseLoop                 // playing loop segment (wraps)
	seqPhaseHold                 // holding last in-frame (no loop frames)
	seqPhaseOut                  // playing animate-out segment
	seqPhaseDone                 // finished playing
)

// sequenceState holds the frame data and playback state for an animated PNG
// sequence on a graphics layer. It is advanced once per ProcessYUV call,
// giving frame-locked playback at the pipeline frame rate.
type sequenceState struct {
	inFrames   [][]byte // cropped RGBA frames for animate-in
	loopFrames [][]byte // cropped RGBA frames for loop
	outFrames  [][]byte // cropped RGBA frames for animate-out

	cropX, cropY int // offset within program frame
	cropW, cropH int // cropped frame dimensions

	phase      seqPhase
	frameIdx   int
	speed      float64 // playback rate: 1.0 = normal, 0.5 = half, 2.0 = double
	frameAccum float64 // fractional frame accumulator for sub-1x speeds
}

// startIn begins playback from the animate-in segment. If there are no in
// frames, it transitions directly to the loop phase.
func (s *sequenceState) startIn() {
	if len(s.inFrames) > 0 {
		s.phase = seqPhaseIn
		s.frameIdx = 0
		return
	}
	s.startLoop()
}

// startLoop begins the loop or hold phase. If there are loop frames, they
// cycle. If not, holds the last frame of the in segment (static bug).
// Also used for "cut on" behaviour.
func (s *sequenceState) startLoop() {
	if len(s.loopFrames) > 0 {
		s.phase = seqPhaseLoop
		s.frameIdx = 0
	} else {
		s.phase = seqPhaseHold
	}
}

// startOut begins playback of the animate-out segment. If there are no out
// frames, it transitions directly to the done phase.
func (s *sequenceState) startOut() {
	if len(s.outFrames) > 0 {
		s.phase = seqPhaseOut
		s.frameIdx = 0
		return
	}
	s.phase = seqPhaseDone
}

// tick accumulates one pipeline frame's worth of playback at the current speed.
// Returns true if the frame changed (ProcessYUV should update the overlay).
func (s *sequenceState) tick() bool {
	if s.phase == seqPhaseIdle || s.phase == seqPhaseDone || s.phase == seqPhaseHold {
		return false
	}
	spd := s.speed
	if spd <= 0 {
		spd = 1.0
	}
	s.frameAccum += spd
	changed := false
	for s.frameAccum >= 1.0 {
		s.frameAccum -= 1.0
		s.advance()
		changed = true
	}
	return changed
}

// advance moves to the next frame within the current phase. Phase transitions
// are handled automatically: in→loop, loop wraps, out→done.
func (s *sequenceState) advance() {
	switch s.phase {
	case seqPhaseIn:
		s.frameIdx++
		if s.frameIdx >= len(s.inFrames) {
			s.startLoop()
		}
	case seqPhaseLoop:
		s.frameIdx++
		if s.frameIdx >= len(s.loopFrames) {
			s.frameIdx = 0
		}
	case seqPhaseOut:
		s.frameIdx++
		if s.frameIdx >= len(s.outFrames) {
			s.phase = seqPhaseDone
			s.frameIdx = 0
		}
	case seqPhaseIdle, seqPhaseDone:
		// no-op
	}
}

// currentFrame returns the RGBA pixel data for the current frame, or nil if
// the sequence is idle or done.
func (s *sequenceState) currentFrame() []byte {
	switch s.phase {
	case seqPhaseIn:
		if s.frameIdx < len(s.inFrames) {
			return s.inFrames[s.frameIdx]
		}
	case seqPhaseLoop:
		if s.frameIdx < len(s.loopFrames) {
			return s.loopFrames[s.frameIdx]
		}
	case seqPhaseHold:
		// Hold the last frame of the in segment.
		if len(s.inFrames) > 0 {
			return s.inFrames[len(s.inFrames)-1]
		}
	case seqPhaseOut:
		if s.frameIdx < len(s.outFrames) {
			return s.outFrames[s.frameIdx]
		}
	}
	return nil
}

// holdFrame returns the frame to display during hold or as initial overlay.
// Prefers first loop frame; falls back to last in-frame.
func (s *sequenceState) holdFrame() []byte {
	if len(s.loopFrames) > 0 {
		return s.loopFrames[0]
	}
	if len(s.inFrames) > 0 {
		return s.inFrames[len(s.inFrames)-1]
	}
	return nil
}

// LoadSequenceZIP decodes a ZIP archive containing PNG frames for an animated
// graphics sequence. PNGs in "in/" become animate-in frames, PNGs in "out/"
// become animate-out frames, and root-level PNGs become loop frames. All PNGs
// must share the same dimensions.
//
// Uses a two-pass approach to keep peak memory low:
//   - Pass 1: decode each PNG one at a time, compute union bounding box, discard.
//   - Pass 2: re-decode each PNG, crop to the union bbox, store cropped RGBA.
//
// This means a 1322-frame 1080p sequence uses ~8MB peak (one frame) + final
// cropped storage, rather than ~11GB (all frames decoded simultaneously).
// ProgressFunc is called during ZIP processing with a value from 0.0 to 1.0.
type ProgressFunc func(progress float64)

func LoadSequenceZIP(zipData []byte) (*sequenceState, error) {
	return LoadSequenceZIPWithProgress(zipData, nil)
}

func LoadSequenceZIPWithProgress(zipData []byte, onProgress ProgressFunc) (*sequenceState, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("graphics: open ZIP: %w", err)
	}

	// Categorize PNG files by folder, skipping macOS resource forks.
	type entry struct {
		name string
		file *zip.File
	}
	var inEntries, loopEntries, outEntries []entry

	// Detect common root folder: if ALL entries share the same top-level directory
	// prefix (e.g., "my-animation/in/001.png"), strip it so in/out matching works.
	// Don't strip "in" or "out" themselves — those are the meaningful folder names.
	var commonPrefix string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.HasPrefix(f.Name, "__MACOSX") || strings.HasPrefix(filepath.Base(f.Name), "._") {
			continue
		}
		parts := strings.SplitN(f.Name, "/", 2)
		if len(parts) < 2 {
			commonPrefix = ""
			break
		}
		top := parts[0]
		// Don't strip "in" or "out" — these are real folder names, not a wrapper.
		if top == "in" || top == "out" {
			commonPrefix = ""
			break
		}
		if commonPrefix == "" {
			commonPrefix = top
		} else if top != commonPrefix {
			commonPrefix = ""
			break
		}
	}

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// Skip macOS resource forks and metadata.
		if strings.HasPrefix(f.Name, "__MACOSX") || strings.HasPrefix(filepath.Base(f.Name), "._") {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(f.Name), ".png") {
			continue
		}

		// Strip common root folder prefix if present.
		name := f.Name
		if commonPrefix != "" {
			name = strings.TrimPrefix(name, commonPrefix+"/")
		}

		dir := filepath.Dir(name)
		base := filepath.Base(name)

		switch {
		case dir == "in" || strings.HasPrefix(dir, "in/"):
			inEntries = append(inEntries, entry{name: base, file: f})
		case dir == "out" || strings.HasPrefix(dir, "out/"):
			outEntries = append(outEntries, entry{name: base, file: f})
		case dir == ".":
			loopEntries = append(loopEntries, entry{name: base, file: f})
		}
	}

	totalCount := len(inEntries) + len(loopEntries) + len(outEntries)
	if totalCount == 0 {
		return nil, ErrSequenceEmpty
	}
	// Loop frames are optional. Without them, the sequence holds the last
	// in-frame during the steady state (static bug/watermark behaviour).
	if len(loopEntries) == 0 && len(inEntries) == 0 {
		return nil, ErrSequenceEmpty
	}
	if totalCount > maxSequenceFrames {
		return nil, ErrSequenceTooLarge
	}

	// Sort each group alphabetically.
	sortEntries := func(e []entry) {
		sort.Slice(e, func(i, j int) bool { return e[i].name < e[j].name })
	}
	sortEntries(inEntries)
	sortEntries(loopEntries)
	sortEntries(outEntries)

	// Build ordered list of all entries (in, loop, out).
	allEntries := make([]entry, 0, totalCount)
	allEntries = append(allEntries, inEntries...)
	allEntries = append(allEntries, loopEntries...)
	allEntries = append(allEntries, outEntries...)

	// --- Pass 1: compute union bounding box (one frame in memory at a time) ---
	var refW, refH int
	unionBBox := image.Rectangle{}

	totalWork := float64(totalCount * 2) // two passes
	done := 0
	report := func() {
		if onProgress != nil {
			onProgress(float64(done) / totalWork)
		}
	}

	for i, e := range allEntries {
		nrgba, err := decodeZipPNG(e.file)
		if err != nil {
			return nil, err
		}
		w, h := nrgba.Bounds().Dx(), nrgba.Bounds().Dy()
		if i == 0 {
			refW, refH = w, h
		} else if w != refW || h != refH {
			return nil, ErrSequenceSizeMismatch
		}

		r := frameBBox(nrgba)
		if !r.Empty() {
			if unionBBox.Empty() {
				unionBBox = r
			} else {
				unionBBox = unionBBox.Union(r)
			}
		}
		// nrgba is discarded here — only one frame in memory at a time.
		done++
		report()
	}

	// If all frames are fully transparent, use a minimal 2x2 region.
	if unionBBox.Empty() {
		unionBBox = image.Rect(0, 0, 2, 2)
	}

	// Even-align the bounding box for YUV420.
	cropX, cropY, cropW, cropH := evenAlignBBox(unionBBox, refW, refH)

	// Check memory limit using CROPPED dimensions (the actual storage cost).
	frameBytes := int64(cropW) * int64(cropH) * 4
	totalMem := frameBytes * int64(totalCount)
	if totalMem > maxSequenceMemory {
		return nil, fmt.Errorf("%w: cropped %dx%d × %d frames = %dMB exceeds %dMB limit",
			ErrSequenceTooLarge, cropW, cropH, totalCount,
			totalMem/(1<<20), maxSequenceMemory/(1<<20))
	}

	// --- Pass 2: re-decode each PNG, crop to bbox, store cropped RGBA ---
	cropFrame := func(e entry) ([]byte, error) {
		nrgba, err := decodeZipPNG(e.file)
		if err != nil {
			return nil, err
		}
		out := make([]byte, cropW*cropH*4)
		for y := 0; y < cropH; y++ {
			srcY := cropY + y
			for x := 0; x < cropW; x++ {
				srcX := cropX + x
				off := (y*cropW + x) * 4
				if srcX >= 0 && srcX < refW && srcY >= 0 && srcY < refH {
					c := nrgba.NRGBAAt(srcX, srcY)
					out[off+0] = c.R
					out[off+1] = c.G
					out[off+2] = c.B
					out[off+3] = c.A
				}
			}
		}
		return out, nil
	}

	inCount := len(inEntries)
	loopCount := len(loopEntries)

	inFrames := make([][]byte, inCount)
	for i := 0; i < inCount; i++ {
		inFrames[i], err = cropFrame(allEntries[i])
		if err != nil {
			return nil, err
		}
		done++
		report()
	}
	loopFrames := make([][]byte, loopCount)
	for i := 0; i < loopCount; i++ {
		loopFrames[i], err = cropFrame(allEntries[inCount+i])
		if err != nil {
			return nil, err
		}
		done++
		report()
	}
	outFrames := make([][]byte, len(outEntries))
	for i := 0; i < len(outEntries); i++ {
		outFrames[i], err = cropFrame(allEntries[inCount+loopCount+i])
		if err != nil {
			return nil, err
		}
		done++
		report()
	}

	return &sequenceState{
		inFrames:   inFrames,
		loopFrames: loopFrames,
		outFrames:  outFrames,
		cropX:      cropX,
		cropY:      cropY,
		cropW:      cropW,
		cropH:      cropH,
		speed:      1.0,
	}, nil
}

// decodeZipPNG opens a zip entry, decodes the PNG, and returns an NRGBA image.
func decodeZipPNG(f *zip.File) (*image.NRGBA, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("graphics: open %s: %w", f.Name, err)
	}
	defer rc.Close()

	img, _, err := image.Decode(rc)
	if err != nil {
		return nil, fmt.Errorf("graphics: decode %s: %w", f.Name, err)
	}
	return toNRGBA(img), nil
}

// evenAlignBBox even-aligns a bounding box for YUV420 compatibility and clamps
// to source dimensions. Returns cropX, cropY, cropW, cropH.
func evenAlignBBox(bbox image.Rectangle, srcW, srcH int) (cropX, cropY, cropW, cropH int) {
	cropX = bbox.Min.X &^ 1
	cropY = bbox.Min.Y &^ 1
	cropW = bbox.Dx() + (bbox.Min.X - cropX)
	cropH = bbox.Dy() + (bbox.Min.Y - cropY)

	// Round up to even.
	if cropW%2 != 0 {
		cropW++
	}
	if cropH%2 != 0 {
		cropH++
	}
	// Minimum 2x2.
	if cropW < 2 {
		cropW = 2
	}
	if cropH < 2 {
		cropH = 2
	}
	// Clamp to source dimensions.
	if cropX+cropW > srcW {
		cropW = (srcW - cropX) &^ 1
	}
	if cropY+cropH > srcH {
		cropH = (srcH - cropY) &^ 1
	}
	return
}

// frameBBox computes the bounding box of all non-transparent pixels in img.
// Returns an empty rectangle if all pixels are fully transparent.
func frameBBox(img *image.NRGBA) image.Rectangle {
	bounds := img.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		rowOff := (y - bounds.Min.Y) * img.Stride
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			a := img.Pix[rowOff+(x-bounds.Min.X)*4+3]
			if a == 0 {
				continue
			}
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}

	if minX > maxX || minY > maxY {
		return image.Rectangle{}
	}
	// Max is exclusive.
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

// toNRGBA converts any image.Image to *image.NRGBA.
func toNRGBA(img image.Image) *image.NRGBA {
	if nrgba, ok := img.(*image.NRGBA); ok {
		return nrgba
	}
	bounds := img.Bounds()
	dst := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			dst.SetNRGBA(x, y, nrgbaFromRGBA(r, g, b, a))
		}
	}
	return dst
}

// nrgbaFromRGBA converts pre-multiplied 16-bit RGBA to non-pre-multiplied 8-bit NRGBA.
func nrgbaFromRGBA(r, g, b, a uint32) color.NRGBA {
	if a == 0 {
		return color.NRGBA{}
	}
	return color.NRGBA{
		R: uint8((r * 0xff) / a),
		G: uint8((g * 0xff) / a),
		B: uint8((b * 0xff) / a),
		A: uint8(a >> 8),
	}
}
