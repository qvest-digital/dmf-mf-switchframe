// Package aacfixture writes clips with real AAC audio for tests, with the
// ffmpeg CLI, and the PCM they were encoded from.
package aacfixture

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/cmplx"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Clip describes a clip to write.
type Clip struct {
	Ext      string  // ".ts" or ".mp4"
	Channels int     // 1, 2, 6 or 8
	Rate     int     // sample rate, 48000 when 0
	Seconds  float64 // 1 when 0
	FPS      int     // 25 when 0
	// NoEditList writes an MP4 without an edit list, so nothing in it
	// says where the encoder priming ends.
	NoEditList bool
	// Lead is silence encoded ahead of the sound, so the stream's priming
	// is 1024+Lead samples, and ITunSMPB tags an MP4 written without an
	// edit list with that priming and its padding, as Apple's encoder
	// does.
	Lead     int
	ITunSMPB bool
	// Seed picks other pictures and another sound, for a clip told apart
	// from another.
	Seed int
}

var pictures = [2]string{"testsrc", "testsrc2"}

// ffmpeg encodes 7.1 as AAC channel configuration 12, which ADTS, and so
// MPEG-TS, cannot carry; other 8-channel layouts it signals with a
// program config element, which the engine's ADTS framing does not carry.
var layouts = map[int]string{1: "mono", 2: "stereo", 6: "5.1", 8: "7.1"}

// Write writes c into dir and returns its path and the sound it carries
// as interleaved float32 PCM at its rate, without the lead: sample 0 is
// what a player has to put on the first picture.
func Write(t testing.TB, dir string, c Clip) (string, []float32) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		// CI installs the CLI (setup-cgo-deps): a skip there would pass
		// every test built on a fixture without running it.
		if os.Getenv("CI") != "" {
			t.Fatal("aacfixture: the ffmpeg CLI is not installed")
		}
		t.Skip("ffmpeg not available")
	}
	if c.Rate == 0 {
		c.Rate = 48000
	}
	if c.Seconds == 0 {
		c.Seconds = 1
	}
	if c.FPS == 0 {
		c.FPS = 25
	}
	layout, ok := layouts[c.Channels]
	if !ok {
		t.Fatalf("aacfixture: no layout for %d channels", c.Channels)
	}
	// A clip is encoded once per test binary: tests that share one would
	// otherwise each spend an encode on it.
	cacheMu.Lock()
	defer cacheMu.Unlock()
	f, ok := cache[c]
	if !ok {
		tmp, err := os.MkdirTemp("", "aacfixture-")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		path, ref := encode(t, ffmpeg, tmp, c, layout)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f = fixture{name: filepath.Base(path), data: data, ref: ref}
		cache[c] = f
	}
	path := filepath.Join(dir, f.name)
	if err := os.WriteFile(path, f.data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, f.ref
}

type fixture struct {
	name string
	data []byte
	ref  []float32
}

var (
	cacheMu sync.Mutex
	cache   = map[Clip]fixture{}
)

// encode writes c into dir with ffmpeg and returns its path and sound.
func encode(t testing.TB, ffmpeg, dir string, c Clip, layout string) (string, []float32) {
	t.Helper()
	// Every channel a chirp of its own: a correlation finds one place.
	exprs := make([]string, c.Channels)
	for ch := range exprs {
		exprs[ch] = fmt.Sprintf("0.2*sin(2*PI*(%d+%d*t)*t+%d)+0.1*sin(2*PI*%d*t)", 150+70*ch+400*c.Seed, 900+250*c.Seed, ch, 2000+300*ch+700*c.Seed)
	}
	sound := fmt.Sprintf("aevalsrc=exprs=%s:s=%d:c=%s:d=%g", strings.Join(exprs, "|"), c.Rate, layout, c.Seconds)
	encoded := sound
	if c.Lead > 0 {
		encoded = sound + fmt.Sprintf(",adelay=delays=%dS:all=1", c.Lead)
	}

	path := filepath.Join(dir, fmt.Sprintf("clip-%dch-%d-%d%s", c.Channels, c.Rate, c.Seed, c.Ext))
	args := []string{"-v", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("%s=size=64x48:rate=%d:d=%g", pictures[c.Seed%2], c.FPS, c.Seconds),
		"-f", "lavfi", "-i", encoded,
		"-c:v", "libx264", "-g", fmt.Sprint(c.FPS), "-bf", "0", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", 96*c.Channels)}
	if c.NoEditList || c.ITunSMPB {
		args = append(args, "-use_editlist", "0")
	}
	if out, err := exec.Command(ffmpeg, append(args, path)...).CombinedOutput(); err != nil {
		t.Fatalf("aacfixture: ffmpeg: %v\n%s", err, out)
	}

	raw, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", sound, "-f", "f32le", "-").Output()
	if err != nil {
		t.Fatalf("aacfixture: ffmpeg reference: %v", err)
	}
	ref := make([]float32, len(raw)/4)
	for i := range ref {
		ref[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}

	if c.ITunSMPB {
		frames := countAACFrames(t, ffmpeg, path)
		delay := 1024 + c.Lead
		padding := frames*1024 - delay - len(ref)/c.Channels
		tag := fmt.Sprintf(" 00000000 %08X %08X %016X 00000000 00000000", delay, padding, len(ref)/c.Channels)
		addITunSMPB(t, path, tag)
	}
	return path, ref
}

// countAACFrames returns how many AAC frames the clip's sound track has.
func countAACFrames(t testing.TB, ffmpeg, path string) int {
	t.Helper()
	ffprobe := filepath.Join(filepath.Dir(ffmpeg), "ffprobe")
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "a:0",
		"-count_packets", "-show_entries", "stream=nb_read_packets", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("aacfixture: ffprobe: %v", err)
	}
	var n int
	if _, err := fmt.Sscan(string(bytes.TrimSpace(out)), &n); err != nil {
		t.Fatalf("aacfixture: ffprobe packets %q: %v", out, err)
	}
	return n
}

// addITunSMPB appends a udta box carrying the iTunes gapless tag to the
// moov box of the MP4 at path, which ffmpeg writes last, after the media.
func addITunSMPB(t testing.TB, path, value string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	box := func(typ string, payload ...[]byte) []byte {
		body := bytes.Join(payload, nil)
		out := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
		return append(append(out, typ...), body...)
	}
	full := []byte{0, 0, 0, 0}
	data32 := []byte{0, 0, 0, 1, 0, 0, 0, 0} // UTF-8, no locale
	udta := box("udta", box("meta", full,
		box("hdlr", full, []byte{0, 0, 0, 0}, []byte("mdir"), make([]byte, 12), []byte{0}),
		box("ilst", box("----",
			box("mean", full, []byte("com.apple.iTunes")),
			box("name", full, []byte("iTunSMPB")),
			box("data", data32, []byte(value))))))
	for off := 0; off+8 <= len(data); {
		size := int(binary.BigEndian.Uint32(data[off:]))
		if size < 8 || off+size > len(data) {
			t.Fatalf("aacfixture: bad box at %d", off)
		}
		if string(data[off+4:off+8]) == "moov" {
			if off+size != len(data) {
				t.Fatal("aacfixture: moov is not the last box")
			}
			binary.BigEndian.PutUint32(data[off:], uint32(size+len(udta)))
			if err := os.WriteFile(path, append(data, udta...), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		off += size
	}
	t.Fatal("aacfixture: no moov box")
}

// Lag returns the shift, within maxLag samples, at which got best matches
// ref on channel ch: got[i+lag] is ref[i]. Both are interleaved with
// channels channels; the match runs over the first window samples of
// ref past maxLag. The correlation is taken by FFT: summed directly it
// costs window products per lag, which the race detector slows to
// minutes.
func Lag(got, ref []float32, channels, ch, maxLag, window int) int {
	sample := func(pcm []float32, i int) float64 {
		if i < 0 || (i+1)*channels > len(pcm) {
			return 0
		}
		return float64(pcm[i*channels+ch])
	}
	// c[k] = sum over i of a[i]*b[i+k], k = lag+maxLag.
	n := 1
	for n < window+2*maxLag {
		n <<= 1
	}
	a := make([]complex128, n)
	b := make([]complex128, n)
	for i := range window {
		a[i] = complex(sample(ref, i+maxLag), 0)
	}
	for j := range window + 2*maxLag {
		b[j] = complex(sample(got, j), 0)
	}
	fft(a, false)
	fft(b, false)
	for i := range a {
		a[i] = cmplx.Conj(a[i]) * b[i]
	}
	fft(a, true)
	best, bestLag := math.Inf(-1), 0
	for k := 0; k <= 2*maxLag; k++ {
		if v := real(a[k]); v > best {
			best, bestLag = v, k-maxLag
		}
	}
	return bestLag
}

// fft transforms x in place, its length a power of two; inverse
// transforms back without the 1/n scale, which Lag does not need.
func fft(x []complex128, inverse bool) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	sign := -1.0
	if inverse {
		sign = 1
	}
	for size := 2; size <= n; size <<= 1 {
		w := cmplx.Rect(1, sign*2*math.Pi/float64(size))
		for start := 0; start < n; start += size {
			t := complex(1, 0)
			for k := range size / 2 {
				u, v := x[start+k], x[start+k+size/2]*t
				x[start+k], x[start+k+size/2] = u+v, u-v
				t *= w
			}
		}
	}
}

// RMS returns the root mean square difference of got and ref over their
// common length, channel ch, got shifted by lag as Lag returns it.
func RMS(got, ref []float32, channels, ch, lag int) float64 {
	var s float64
	n := 0
	for i := 0; ; i++ {
		j := i + lag
		if (i+1)*channels > len(ref) || (j+1)*channels > len(got) {
			break
		}
		if j < 0 {
			continue
		}
		d := float64(got[j*channels+ch]) - float64(ref[i*channels+ch])
		s += d * d
		n++
	}
	if n == 0 {
		return math.Inf(1)
	}
	return math.Sqrt(s / float64(n))
}
