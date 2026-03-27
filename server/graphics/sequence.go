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
	ErrSequenceNoLoopFrames = errors.New("graphics: sequence ZIP must contain root-level loop PNGs")
	ErrSequenceTooLarge     = errors.New("graphics: sequence exceeds memory limit")
	ErrSequenceEmpty        = errors.New("graphics: sequence ZIP contains no PNG files")
	ErrSequenceSizeMismatch = errors.New("graphics: all PNGs must have same dimensions")
)

// seqPhase represents the current phase of an animated PNG sequence.
type seqPhase int

const (
	seqPhaseIdle seqPhase = iota // not playing
	seqPhaseIn                   // playing animate-in segment
	seqPhaseLoop                 // playing loop segment (wraps)
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

	phase    seqPhase
	frameIdx int
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

// startLoop begins playback from the loop segment directly, skipping any
// animate-in. This is used for "cut on" behaviour.
func (s *sequenceState) startLoop() {
	s.phase = seqPhaseLoop
	s.frameIdx = 0
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
	case seqPhaseOut:
		if s.frameIdx < len(s.outFrames) {
			return s.outFrames[s.frameIdx]
		}
	}
	return nil
}

// LoadSequenceZIP decodes a ZIP archive containing PNG frames for an animated
// graphics sequence. PNGs in "in/" become animate-in frames, PNGs in "out/"
// become animate-out frames, and root-level PNGs become loop frames. All PNGs
// must share the same dimensions. The function computes a union bounding box of
// all non-transparent pixels, even-aligns it for YUV420, and crops every frame
// to that region, storing raw RGBA bytes.
func LoadSequenceZIP(zipData []byte) (*sequenceState, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("graphics: open ZIP: %w", err)
	}

	// Categorize PNG files by folder.
	type entry struct {
		name string
		file *zip.File
	}
	var inEntries, loopEntries, outEntries []entry

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(f.Name), ".png") {
			continue
		}

		dir := filepath.Dir(f.Name)
		base := filepath.Base(f.Name)

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
	if len(loopEntries) == 0 {
		return nil, ErrSequenceNoLoopFrames
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

	// Decode all PNGs to NRGBA. Validate dimensions match.
	allEntries := make([]entry, 0, totalCount)
	allEntries = append(allEntries, inEntries...)
	allEntries = append(allEntries, loopEntries...)
	allEntries = append(allEntries, outEntries...)

	decoded := make([]*image.NRGBA, 0, totalCount)
	var refW, refH int

	for i, e := range allEntries {
		rc, err := e.file.Open()
		if err != nil {
			return nil, fmt.Errorf("graphics: open %s: %w", e.file.Name, err)
		}
		img, _, err := image.Decode(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("graphics: decode %s: %w", e.file.Name, err)
		}

		bounds := img.Bounds()
		w, h := bounds.Dx(), bounds.Dy()
		if i == 0 {
			refW, refH = w, h
		} else if w != refW || h != refH {
			return nil, ErrSequenceSizeMismatch
		}

		// Convert to NRGBA if needed.
		nrgba := toNRGBA(img)
		decoded = append(decoded, nrgba)
	}

	// Check memory limit based on source dimensions.
	memPerFrame := int64(refW) * int64(refH) * 4
	totalMem := memPerFrame * int64(totalCount)
	if totalMem > maxSequenceMemory {
		return nil, ErrSequenceTooLarge
	}

	// Compute union bounding box across all frames.
	unionBBox := image.Rectangle{}
	for _, img := range decoded {
		r := frameBBox(img)
		if r.Empty() {
			continue
		}
		if unionBBox.Empty() {
			unionBBox = r
		} else {
			unionBBox = unionBBox.Union(r)
		}
	}

	// If all frames are fully transparent, use a minimal 2x2 region.
	if unionBBox.Empty() {
		unionBBox = image.Rect(0, 0, 2, 2)
	}

	// Even-align the bounding box for YUV420.
	cropX := unionBBox.Min.X &^ 1
	cropY := unionBBox.Min.Y &^ 1
	cropW := unionBBox.Dx()
	cropH := unionBBox.Dy()
	// Adjust width/height for the expanded origin.
	cropW += unionBBox.Min.X - cropX
	cropH += unionBBox.Min.Y - cropY
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
	if cropX+cropW > refW {
		cropW = refW - cropX
		if cropW%2 != 0 {
			cropW--
			if cropX >= 2 {
				cropX -= 2 - (cropW % 2)
			}
		}
	}
	if cropY+cropH > refH {
		cropH = refH - cropY
		if cropH%2 != 0 {
			cropH--
			if cropY >= 2 {
				cropY -= 2 - (cropH % 2)
			}
		}
	}

	// Crop each frame and extract raw RGBA bytes.
	cropFrame := func(img *image.NRGBA) []byte {
		out := make([]byte, cropW*cropH*4)
		for y := 0; y < cropH; y++ {
			srcY := cropY + y
			for x := 0; x < cropW; x++ {
				srcX := cropX + x
				off := (y*cropW + x) * 4
				if srcX >= 0 && srcX < refW && srcY >= 0 && srcY < refH {
					c := img.NRGBAAt(srcX, srcY)
					out[off+0] = c.R
					out[off+1] = c.G
					out[off+2] = c.B
					out[off+3] = c.A
				}
			}
		}
		return out
	}

	inCount := len(inEntries)
	loopCount := len(loopEntries)

	inFrames := make([][]byte, inCount)
	for i := 0; i < inCount; i++ {
		inFrames[i] = cropFrame(decoded[i])
	}
	loopFrames := make([][]byte, loopCount)
	for i := 0; i < loopCount; i++ {
		loopFrames[i] = cropFrame(decoded[inCount+i])
	}
	outFrames := make([][]byte, len(outEntries))
	for i := 0; i < len(outEntries); i++ {
		outFrames[i] = cropFrame(decoded[inCount+loopCount+i])
	}

	return &sequenceState{
		inFrames:   inFrames,
		loopFrames: loopFrames,
		outFrames:  outFrames,
		cropX:      cropX,
		cropY:      cropY,
		cropW:      cropW,
		cropH:      cropH,
	}, nil
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
