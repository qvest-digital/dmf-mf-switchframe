//go:build cgo && !noffmpeg

package codec

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	astits "github.com/asticode/go-astits"
	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// The program encode at 1080p50: what NewVideoEncoder opens for the
// pipeline with no --output-* flags.
const (
	benchW, benchH        = 1920, 1080
	benchFPSNum           = 50
	benchFPSDen           = 1
	benchFrameInterval    = time.Second * benchFPSDen / benchFPSNum
	benchSourceFrameCount = 100
	// switcher.DefaultBitrateForResolution at 1080p, which the program
	// encode runs at without --output-bitrate.
	benchBitrate = 10_000_000
	benchNoise   = 3
)

// loadProgramFrames decodes benchSourceFrameCount pictures of live-action
// footage and scales them to 1080p, so the benchmark encodes picture detail
// and motion rather than a pattern x264 compresses for free. The 1080p demo
// clip is no use here: it is a title on black for its first eight seconds.
func loadProgramFrames(tb testing.TB) [][]byte {
	tb.Helper()
	data, err := os.ReadFile("../../test/clips/tears_of_steel.ts")
	if err != nil {
		tb.Fatalf("read clip: %v", err)
	}
	dec, err := NewFFmpegDecoderWithThreads(nil, 1)
	if err != nil {
		tb.Fatalf("decoder: %v", err)
	}
	defer dec.Close()

	dmx := astits.NewDemuxer(context.Background(), bytes.NewReader(data))
	var frames [][]byte
	skipped := 0
	for len(frames) < benchSourceFrameCount {
		d, err := dmx.NextData()
		if err != nil {
			break
		}
		if d.PES == nil || d.PES.Header == nil || d.PES.Header.StreamID != 0xE0 {
			continue
		}
		yuv, w, h, err := dec.Decode(d.PES.Data)
		if err != nil || yuv == nil {
			continue
		}
		// The first second is a fade from black.
		if skipped++; skipped <= 24 {
			continue
		}
		frame := make([]byte, video.YUV420FrameSize(benchW, benchH))
		transition.ScaleYUV420Lanczos(yuv, w, h, frame, benchW, benchH)
		addSensorNoise(frame, uint64(len(frames))+1)
		frames = append(frames, frame)
	}
	if len(frames) < benchSourceFrameCount {
		tb.Fatalf("decoded %d frames, want %d", len(frames), benchSourceFrameCount)
	}
	return frames
}

// addSensorNoise adds the temporal noise of a camera sensor, a triangular
// distribution of +-benchNoise code values that differs every frame. An
// upscaled film is smoother than any camera, and without it x264 skips most
// of each picture.
func addSensorNoise(frame []byte, seed uint64) {
	x := seed * 0x9E3779B97F4A7C15
	for i := range frame {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		n := int(x&0xff)%(benchNoise+1) - int((x>>8)&0xff)%(benchNoise+1)
		frame[i] = byte(min(255, max(0, int(frame[i])+n)))
	}
}

func processCPU() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}

// psnrY is the luma PSNR of b against a.
func psnrY(a, b []byte) float64 {
	n := benchW * benchH
	var sse float64
	for i := range n {
		d := float64(a[i]) - float64(b[i])
		sse += d * d
	}
	if sse == 0 {
		return 99
	}
	return 10 * math.Log10(255*255*float64(n)/sse)
}

// runBackgroundSources encodes the same frames at the MXL source preset
// (ultrafast, the encoder default thread count), paced at the frame rate,
// as the relay/replay encodes of n MXL sources do beside the program.
func runBackgroundSources(tb testing.TB, n int, frames [][]byte) (stop func()) {
	tb.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for s := range n {
		enc, err := NewFFmpegEncoder("libx264", benchW, benchH,
			benchBitrate, benchFPSNum, benchFPSDen, 2, nil,
			&EncoderOptions{Preset: OutputPresetFastest})
		if err != nil {
			tb.Fatalf("source encoder: %v", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer enc.Close()
			start := time.Now()
			for i := 0; ctx.Err() == nil; i++ {
				if d := time.Until(start.Add(time.Duration(i) * benchFrameInterval)); d > 0 {
					time.Sleep(d)
				}
				_, _ = enc.EncodePackets(frames[(i+s*7)%len(frames)], int64(i)*1800, false)
			}
		}()
	}
	return func() { cancel(); wg.Wait() }
}

// BenchmarkX264ProgramThreads measures the per-frame latency of the program
// encode against its thread count. Each b.N is one frame, submitted on the
// 50 fps grid the program clock ticks on; an encode longer than a frame
// interval delays the next one, as on the program path.
//
//	go test -run '^$' -bench X264ProgramThreads -benchtime 1000x ./codec/
//
// Run it under a CPU quota to mimic a pod's limit, in a pod or with
// systemd-run --user --scope -p CPUQuota=2800%. SWITCHFRAME_BENCH_SOURCES=4
// adds four MXL source encodes beside it.
func BenchmarkX264ProgramThreads(b *testing.B) {
	frames := loadProgramFrames(b)
	sources := 0
	if v := os.Getenv("SWITCHFRAME_BENCH_SOURCES"); v != "" {
		if _, err := fmt.Sscan(v, &sources); err != nil {
			b.Fatalf("SWITCHFRAME_BENCH_SOURCES=%q: %v", v, err)
		}
	}
	for _, threads := range []int{4, 6, 8, 10, 12, 14, 16, 24} {
		b.Run(fmt.Sprintf("threads=%d/sources=%d", threads, sources), func(b *testing.B) {
			enc, err := NewFFmpegEncoder("libx264", benchW, benchH, benchBitrate,
				benchFPSNum, benchFPSDen, 2, nil, &EncoderOptions{Threads: threads})
			if err != nil {
				b.Fatal(err)
			}
			defer enc.Close()
			stop := runBackgroundSources(b, sources, frames)
			defer stop()

			lat := make([]time.Duration, 0, b.N)
			var out [][]byte
			var outBytes int
			late := 0
			b.ResetTimer()
			cpu0 := processCPU()
			start := time.Now()
			for i := range b.N {
				due := start.Add(time.Duration(i) * benchFrameInterval)
				if d := time.Until(due); d > 0 {
					time.Sleep(d)
				}
				t0 := time.Now()
				pkts, err := enc.EncodePackets(frames[i%len(frames)], int64(i)*1800, false)
				el := time.Since(t0)
				if err != nil {
					b.Fatal(err)
				}
				lat = append(lat, el)
				if el > benchFrameInterval {
					late++
				}
				for _, p := range pkts {
					outBytes += len(p.Data)
					out = append(out, p.Data)
				}
			}
			wall := time.Since(start)
			cpu := processCPU() - cpu0
			b.StopTimer()
			stop()

			slices.Sort(lat)
			var sum time.Duration
			for _, l := range lat {
				sum += l
			}
			ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
			b.ReportMetric(ms(sum/time.Duration(len(lat))), "ms-mean")
			b.ReportMetric(ms(percentile(lat, 0.90)), "ms-p90")
			b.ReportMetric(ms(percentile(lat, 0.99)), "ms-p99")
			b.ReportMetric(100*float64(late)/float64(b.N), "%late")
			b.ReportMetric(ms(cpu)/float64(b.N), "cpu-ms/frame")
			b.ReportMetric(float64(cpu)/float64(wall), "cores")
			b.ReportMetric(float64(outBytes)*8/1000/(float64(b.N)/benchFPSNum), "kbit/s")
			b.ReportMetric(meanPSNR(b, out, frames), "psnr-y")
		})
	}
}

// meanPSNR decodes the encoded stream and compares it with the frames it
// was encoded from, so a thread count that buys speed with quality shows.
func meanPSNR(tb testing.TB, out [][]byte, frames [][]byte) float64 {
	tb.Helper()
	dec, err := NewFFmpegDecoderWithThreads(nil, 1)
	if err != nil {
		tb.Fatal(err)
	}
	defer dec.Close()
	var sum float64
	n := 0
	for i, au := range out {
		yuv, _, _, err := dec.Decode(au)
		if err != nil || yuv == nil || len(yuv) != video.YUV420FrameSize(benchW, benchH) {
			continue
		}
		sum += psnrY(frames[i%len(frames)], yuv)
		n++
	}
	if n == 0 {
		tb.Fatal("no decodable frames in the encoded stream")
	}
	return sum / float64(n)
}
