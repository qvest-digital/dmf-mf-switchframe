package stinger

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/zsiec/switchframe/server/transition"
)

var (
	ErrNotFound          = errors.New("stinger: not found")
	ErrInvalidName       = errors.New("stinger: invalid name")
	ErrInvalidCutPoint   = errors.New("stinger: cut point must be between 0 and 1")
	ErrAlreadyExists     = errors.New("stinger: already exists")
	ErrMaxClipsReached   = errors.New("stinger: maximum clips reached")
	ErrTooManyFrames     = errors.New("stinger: too many frames in clip")
	ErrTotalSizeExceeded = errors.New("stinger: total extracted size exceeds limit")
)

// maxZipEntrySize is the maximum allowed size for a single extracted zip entry.
// 50 MB is generous for stinger PNG frames (a 1080p RGBA PNG is ~8MB uncompressed).
const maxZipEntrySize = 50 << 20 // 50 MB

// maxFramesPerClip limits the number of PNG frames in a single stinger clip.
// 300 frames = 10 seconds at 30fps, which is generous for any stinger transition.
const maxFramesPerClip = 300

// maxTotalExtractedBytes limits the total size of all extracted files from a zip upload.
// 500 MB prevents zip bombs from exhausting disk space.
const maxTotalExtractedBytes = 500 << 20 // 500 MB

// validateName rejects names that could cause path traversal or are otherwise invalid.
func validateName(name string) error {
	if name == "" || name == "." || name == ".." {
		return ErrInvalidName
	}
	if strings.ContainsAny(name, "/\\") {
		return ErrInvalidName
	}
	// Ensure the cleaned name matches the original (catches things like "a/../b")
	if filepath.Base(name) != name {
		return ErrInvalidName
	}
	return nil
}

// Frame holds a pre-decoded frame with YUV420 data and a per-pixel alpha map.
type Frame struct {
	YUV   []byte // YUV420 planar: Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2]
	Alpha []byte // per-luma-pixel alpha [0-255], w*h bytes
}

// Clip is a pre-loaded stinger transition clip.
type Clip struct {
	Name            string
	Frames          []Frame
	Width           int
	Height          int
	CutPoint        float64   // 0.0-1.0, where A->B switch happens (default 0.5)
	Audio           []float32 // interleaved PCM float32 (optional, nil = no audio)
	AudioSampleRate int       // e.g. 48000
	AudioChannels   int       // e.g. 2 (stereo)
}

// FrameAt returns the stinger frame for a given transition position [0.0, 1.0].
func (c *Clip) FrameAt(position float64) *Frame {
	if len(c.Frames) == 0 {
		return nil
	}
	idx := int(position * float64(len(c.Frames)))
	if idx >= len(c.Frames) {
		idx = len(c.Frames) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return &c.Frames[idx]
}

// Store manages stinger clips stored as PNG sequences on disk.
type Store struct {
	dir      string
	mu       sync.RWMutex
	clips    map[string]*Clip
	pending  map[string]struct{} // slots reserved by in-progress uploads
	maxClips int
}

// NewStore creates a store backed by the given directory.
// Pre-loads any existing stinger directories.
// maxClips limits the number of clips that can be loaded; <= 0 defaults to 16.
func NewStore(dir string, maxClips int) (*Store, error) {
	if maxClips <= 0 {
		maxClips = 16
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create stinger dir: %w", err)
	}
	s := &Store{
		dir:      dir,
		clips:    make(map[string]*Clip),
		maxClips: maxClips,
	}
	// Pre-load existing stingers
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read stinger dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if len(s.clips) >= s.maxClips {
			break // stop loading when limit reached
		}
		clip, err := s.loadClip(e.Name())
		if err != nil {
			continue // skip invalid stingers
		}
		s.clips[e.Name()] = clip
	}
	return s, nil
}

// List returns all loaded stinger names.
func (s *Store) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.clips))
	for name := range s.clips {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Get returns a stinger clip by name.
func (s *Store) Get(name string) (*Clip, bool) {
	if validateName(name) != nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.clips[name]
	return c, ok
}

// Delete removes a stinger clip and its directory.
func (s *Store) Delete(name string) error {
	if err := validateName(name); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidName, name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clips[name]; !ok {
		return ErrNotFound
	}
	delete(s.clips, name)
	return os.RemoveAll(filepath.Join(s.dir, name))
}

// SetCutPoint updates the cut point for a stinger clip.
func (s *Store) SetCutPoint(name string, cutPoint float64) error {
	if err := validateName(name); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidName, name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clips[name]
	if !ok {
		return ErrNotFound
	}
	if cutPoint < 0 || cutPoint > 1 {
		return ErrInvalidCutPoint
	}
	c.CutPoint = cutPoint
	return nil
}

// LoadFromDir loads a stinger from a directory of PNG files.
// The directory must already exist under the store's base dir.
func (s *Store) LoadFromDir(name string) error {
	if err := validateName(name); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidName, name)
	}
	clip, err := s.loadClip(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Allow replacing an existing clip without counting against the limit.
	if _, exists := s.clips[name]; !exists && len(s.clips) >= s.maxClips {
		return ErrMaxClipsReached
	}
	s.clips[name] = clip
	return nil
}

// Upload extracts a zip of PNG files into the store directory and loads the clip.
// The zip must contain PNG files at the root level (no subdirectories).
func (s *Store) Upload(name string, zipData []byte) error {
	if err := validateName(name); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidName, name)
	}

	// Reserve a pending slot under lock to prevent TOCTOU races where
	// concurrent uploads could exceed maxClips.
	s.mu.Lock()
	if _, exists := s.clips[name]; exists {
		s.mu.Unlock()
		return ErrAlreadyExists
	}
	if _, pendingExists := s.pending[name]; pendingExists {
		s.mu.Unlock()
		return ErrAlreadyExists
	}
	if len(s.clips)+len(s.pending) >= s.maxClips {
		s.mu.Unlock()
		return ErrMaxClipsReached
	}
	if s.pending == nil {
		s.pending = make(map[string]struct{})
	}
	s.pending[name] = struct{}{}
	s.mu.Unlock()

	// From here, all error paths must clean up the pending reservation.
	cleanupPending := func() {
		s.mu.Lock()
		delete(s.pending, name)
		s.mu.Unlock()
	}

	// Create the directory
	dir := filepath.Join(s.dir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		cleanupPending()
		return fmt.Errorf("create stinger dir: %w", err)
	}

	// Extract PNGs and optional WAV from zip
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		_ = os.RemoveAll(dir) // clean up on failure
		cleanupPending()
		return fmt.Errorf("open zip: %w", err)
	}

	pngCount := 0
	wavFound := false
	var totalExtractedBytes int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		lower := strings.ToLower(f.Name)
		isPNG := strings.HasSuffix(lower, ".png")
		isWAV := strings.HasSuffix(lower, ".wav") && !wavFound // keep first WAV only

		if !isPNG && !isWAV {
			continue
		}

		// Check frame count limit before extracting
		if isPNG && pngCount >= maxFramesPerClip {
			_ = os.RemoveAll(dir)
			cleanupPending()
			return fmt.Errorf("%w: limit is %d", ErrTooManyFrames, maxFramesPerClip)
		}

		// Use only the base name to prevent zip path traversal
		baseName := filepath.Base(f.Name)

		rc, err := f.Open()
		if err != nil {
			_ = os.RemoveAll(dir)
			cleanupPending()
			return fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}

		outPath := filepath.Join(dir, baseName)
		outFile, err := os.Create(outPath)
		if err != nil {
			_ = rc.Close()
			_ = os.RemoveAll(dir)
			cleanupPending()
			return fmt.Errorf("create file %s: %w", baseName, err)
		}

		limited := io.LimitReader(rc, maxZipEntrySize+1)
		n, copyErr := io.Copy(outFile, limited)
		_ = rc.Close()
		_ = outFile.Close()
		if copyErr != nil {
			_ = os.RemoveAll(dir)
			cleanupPending()
			return fmt.Errorf("write file %s: %w", baseName, copyErr)
		}
		if n > maxZipEntrySize {
			_ = os.RemoveAll(dir)
			cleanupPending()
			return fmt.Errorf("zip entry %s exceeds maximum size (%d bytes)", baseName, maxZipEntrySize)
		}

		totalExtractedBytes += n
		if totalExtractedBytes > maxTotalExtractedBytes {
			_ = os.RemoveAll(dir)
			cleanupPending()
			return fmt.Errorf("%w: limit is %d bytes", ErrTotalSizeExceeded, maxTotalExtractedBytes)
		}

		if isPNG {
			pngCount++
		}
		if isWAV {
			wavFound = true
		}
	}

	if pngCount == 0 {
		_ = os.RemoveAll(dir)
		cleanupPending()
		return errors.New("zip contains no PNG files")
	}

	// Load the clip
	clip, err := s.loadClip(name)
	if err != nil {
		_ = os.RemoveAll(dir)
		cleanupPending()
		return fmt.Errorf("load clip: %w", err)
	}

	s.mu.Lock()
	delete(s.pending, name)
	s.clips[name] = clip
	s.mu.Unlock()

	return nil
}

func (s *Store) loadClip(name string) (*Clip, error) {
	dir := filepath.Join(s.dir, name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read stinger dir %q: %w", name, err)
	}

	// Filter and sort PNG files, find first WAV file
	var pngFiles []string
	var wavFile string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lower := strings.ToLower(e.Name())
		if strings.HasSuffix(lower, ".png") {
			pngFiles = append(pngFiles, e.Name())
		} else if strings.HasSuffix(lower, ".wav") && wavFile == "" {
			wavFile = e.Name()
		}
	}
	slices.Sort(pngFiles)

	if len(pngFiles) == 0 {
		return nil, fmt.Errorf("stinger %q has no PNG files", name)
	}

	clip := &Clip{
		Name:     name,
		CutPoint: 0.5,
	}

	for _, fname := range pngFiles {
		frame, w, h, err := loadPNGFrame(filepath.Join(dir, fname))
		if err != nil {
			return nil, fmt.Errorf("load frame %s: %w", fname, err)
		}
		if clip.Width == 0 {
			clip.Width = w
			clip.Height = h
		} else if w != clip.Width || h != clip.Height {
			return nil, fmt.Errorf("frame %s has dimensions %dx%d, expected %dx%d", fname, w, h, clip.Width, clip.Height)
		}
		clip.Frames = append(clip.Frames, *frame)
	}

	// Load optional audio from WAV file
	if wavFile != "" {
		wavData, err := os.ReadFile(filepath.Join(dir, wavFile))
		if err == nil {
			pcm, sampleRate, channels, parseErr := ParseWAV(wavData)
			if parseErr == nil {
				clip.Audio = pcm
				clip.AudioSampleRate = sampleRate
				clip.AudioChannels = channels
			}
			// Silently ignore WAV parse errors — audio is optional
		}
		// Silently ignore file read errors — audio is optional
	}

	return clip, nil
}

// loadPNGFrame loads a PNG file and converts it to a Frame (YUV420 + alpha).
func loadPNGFrame(path string) (*Frame, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = f.Close() }()

	img, err := png.Decode(f)
	if err != nil {
		return nil, 0, 0, err
	}

	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	// Ensure even dimensions for YUV420
	if w%2 != 0 || h%2 != 0 {
		return nil, 0, 0, fmt.Errorf("dimensions must be even for YUV420, got %dx%d", w, h)
	}

	frame := RGBAToFrame(img, w, h)
	return frame, w, h, nil
}

// RGBAToFrame converts an image to a Frame with YUV420 + alpha.
// Uses limited-range BT.709 colorspace (Y [16,235], Cb/Cr [16,240]) to match
// the pipeline's limited-range convention.
// Exported for testing.
func RGBAToFrame(img image.Image, w, h int) *Frame {
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)
	alpha := make([]byte, ySize)

	// Convert RGBA to limited-range BT.709 YUV420 + extract alpha plane
	halfW := w / 2
	uOffset := ySize
	vOffset := ySize + uvSize

	bounds := img.Bounds()
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			r, g, b, a := img.At(bounds.Min.X+col, bounds.Min.Y+row).RGBA()
			// RGBA() returns pre-multiplied 16-bit values. Un-premultiply to get
			// straight RGB before YUV conversion, otherwise alpha gets applied twice
			// (once here via darkened RGB, once in the blend kernel).
			af := uint8(a >> 8)
			var r8, g8, b8 uint8
			if a > 0 {
				r8 = uint8((r * 0xFFFF / a) >> 8)
				g8 = uint8((g * 0xFFFF / a) >> 8)
				b8 = uint8((b * 0xFFFF / a) >> 8)
			}

			idx := row*w + col
			// Limited-range BT.709 luma
			yVal, _, _ := transition.RGBToYUV_BT709Limited(r8, g8, b8)
			yuv[idx] = yVal
			alpha[idx] = af
		}
	}

	// Compute chroma by averaging 2x2 blocks of un-premultiplied RGB,
	// then converting to limited-range Cb/Cr
	for row := 0; row < h; row += 2 {
		for col := 0; col < w; col += 2 {
			var sumR, sumG, sumB float64
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					r, g, b, a := img.At(bounds.Min.X+col+dx, bounds.Min.Y+row+dy).RGBA()
					// Un-premultiply before averaging
					if a > 0 {
						sumR += float64((r * 0xFFFF / a) >> 8)
						sumG += float64((g * 0xFFFF / a) >> 8)
						sumB += float64((b * 0xFFFF / a) >> 8)
					}
				}
			}
			avgR := uint8(sumR/4 + 0.5)
			avgG := uint8(sumG/4 + 0.5)
			avgB := uint8(sumB/4 + 0.5)

			// Limited-range BT.709 chroma from averaged RGB
			_, cb, cr := transition.RGBToYUV_BT709Limited(avgR, avgG, avgB)

			uvIdx := (row/2)*halfW + col/2
			yuv[uOffset+uvIdx] = cb
			yuv[vOffset+uvIdx] = cr
		}
	}

	return &Frame{YUV: yuv, Alpha: alpha}
}
