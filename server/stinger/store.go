package stinger

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var (
	ErrNotFound        = errors.New("stinger not found")
	ErrInvalidName     = errors.New("invalid stinger name")
	ErrInvalidCutPoint = errors.New("cut point must be between 0 and 1")
)

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

// StingerFrame holds a pre-decoded frame with YUV420 data and a per-pixel alpha map.
type StingerFrame struct {
	YUV   []byte // YUV420 planar: Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2]
	Alpha []byte // per-luma-pixel alpha [0-255], w*h bytes
}

// StingerClip is a pre-loaded stinger transition clip.
type StingerClip struct {
	Name     string
	Frames   []StingerFrame
	Width    int
	Height   int
	CutPoint float64 // 0.0-1.0, where A->B switch happens (default 0.5)
}

// FrameAt returns the stinger frame for a given transition position [0.0, 1.0].
func (c *StingerClip) FrameAt(position float64) *StingerFrame {
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

// StingerStore manages stinger clips stored as PNG sequences on disk.
type StingerStore struct {
	dir   string
	mu    sync.RWMutex
	clips map[string]*StingerClip
}

// NewStingerStore creates a store backed by the given directory.
// Pre-loads any existing stinger directories.
func NewStingerStore(dir string) (*StingerStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create stinger dir: %w", err)
	}
	s := &StingerStore{
		dir:   dir,
		clips: make(map[string]*StingerClip),
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
		clip, err := s.loadClip(e.Name())
		if err != nil {
			continue // skip invalid stingers
		}
		s.clips[e.Name()] = clip
	}
	return s, nil
}

// List returns all loaded stinger names.
func (s *StingerStore) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.clips))
	for name := range s.clips {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Get returns a stinger clip by name.
func (s *StingerStore) Get(name string) (*StingerClip, bool) {
	if validateName(name) != nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.clips[name]
	return c, ok
}

// Delete removes a stinger clip and its directory.
func (s *StingerStore) Delete(name string) error {
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
func (s *StingerStore) SetCutPoint(name string, cutPoint float64) error {
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
func (s *StingerStore) LoadFromDir(name string) error {
	if err := validateName(name); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidName, name)
	}
	clip, err := s.loadClip(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.clips[name] = clip
	s.mu.Unlock()
	return nil
}

func (s *StingerStore) loadClip(name string) (*StingerClip, error) {
	dir := filepath.Join(s.dir, name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read stinger dir %q: %w", name, err)
	}

	// Filter and sort PNG files
	var pngFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lower := strings.ToLower(e.Name())
		if strings.HasSuffix(lower, ".png") {
			pngFiles = append(pngFiles, e.Name())
		}
	}
	sort.Strings(pngFiles)

	if len(pngFiles) == 0 {
		return nil, fmt.Errorf("stinger %q has no PNG files", name)
	}

	clip := &StingerClip{
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

	return clip, nil
}

// loadPNGFrame loads a PNG file and converts it to a StingerFrame (YUV420 + alpha).
func loadPNGFrame(path string) (*StingerFrame, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()

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

	frame := RGBAToStingerFrame(img, w, h)
	return frame, w, h, nil
}

// RGBAToStingerFrame converts an image to a StingerFrame with YUV420 + alpha.
// Exported for testing.
func RGBAToStingerFrame(img image.Image, w, h int) *StingerFrame {
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)
	alpha := make([]byte, ySize)

	// Convert RGBA to YUV420 (BT.709) + extract alpha plane
	halfW := w / 2
	uOffset := ySize
	vOffset := ySize + uvSize

	bounds := img.Bounds()
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			r, g, b, a := img.At(bounds.Min.X+col, bounds.Min.Y+row).RGBA()
			// RGBA() returns 16-bit values, scale to 8-bit
			rf := float64(r >> 8)
			gf := float64(g >> 8)
			bf := float64(b >> 8)
			af := uint8(a >> 8)

			idx := row*w + col
			// BT.709 luma
			y := 0.2126*rf + 0.7152*gf + 0.0722*bf
			yuv[idx] = clampByte(y)
			alpha[idx] = af
		}
	}

	// Compute chroma by averaging 2x2 blocks
	for row := 0; row < h; row += 2 {
		for col := 0; col < w; col += 2 {
			var sumR, sumG, sumB float64
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					r, g, b, _ := img.At(bounds.Min.X+col+dx, bounds.Min.Y+row+dy).RGBA()
					sumR += float64(r >> 8)
					sumG += float64(g >> 8)
					sumB += float64(b >> 8)
				}
			}
			avgR := sumR / 4
			avgG := sumG / 4
			avgB := sumB / 4

			y := 0.2126*avgR + 0.7152*avgG + 0.0722*avgB
			uvIdx := (row/2)*halfW + col/2
			yuv[uOffset+uvIdx] = clampByte((avgB-y)/1.8556 + 128)
			yuv[vOffset+uvIdx] = clampByte((avgR-y)/1.5748 + 128)
		}
	}

	return &StingerFrame{YUV: yuv, Alpha: alpha}
}

func clampByte(v float64) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v + 0.5)
}
