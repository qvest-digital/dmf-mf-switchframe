package switcher

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/colorgrade"
	"github.com/zsiec/switchframe/server/transition"
)

type stubProgramGrader struct {
	g         *colorgrade.Grader
	intensity float32
}

func (s *stubProgramGrader) ProgramGrader() (*colorgrade.Grader, float32) { return s.g, s.intensity }

type stubSourceGrader map[string]*colorgrade.Grader

func (s stubSourceGrader) SourceGrader(key string) *colorgrade.Grader { return s[key] }

func noirGrader(t *testing.T) *colorgrade.Grader {
	t.Helper()
	for _, p := range colorgrade.BuiltinLooks() {
		if p.Name == "noir" {
			lut := colorgrade.GenerateLook(p)
			g, err := colorgrade.NewGrader(lut.Data, lut.Size)
			require.NoError(t, err)
			return g
		}
	}
	t.Fatal("no noir look")
	return nil
}

// barsFrame is a 4:2:0 frame of saturated colour columns.
func barsFrame(w, h int) []byte {
	f := make([]byte, ColorYUV420_8bit.FrameSize(w, h))
	bars := [][3]byte{{168, 44, 136}, {145, 147, 44}, {63, 193, 204}, {51, 109, 212}}
	cw := w / 2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			f[y*w+x] = bars[x*len(bars)/w][0]
		}
	}
	for y := 0; y < h/2; y++ {
		for x := 0; x < cw; x++ {
			b := bars[x*len(bars)/cw]
			f[w*h+y*cw+x] = b[1]
			f[w*h+(w/2)*(h/2)+y*cw+x] = b[2]
		}
	}
	return f
}

func graded(t *testing.T, g *colorgrade.Grader, src []byte, w, h int, intensity float32) []byte {
	t.Helper()
	out := append([]byte(nil), src...)
	require.NoError(t, g.ApplyYUV420(out, w, h, intensity))
	return out
}

func TestColorGradeNode_ActiveWithAGrader(t *testing.T) {
	require.False(t, (&colorGradeNode{}).Active())
	// With grading off the node stays in and passes frames through, so
	// turning grading on needs no rebuild.
	n := &colorGradeNode{grader: &stubProgramGrader{}}
	require.True(t, n.Active())
	data := barsFrame(8, 4)
	pf := &ProcessingFrame{Data: append([]byte(nil), data...), Format: ColorYUV420_8bit, Width: 8, Height: 4}
	require.Same(t, pf, n.Process(nil, pf))
	require.Equal(t, data, pf.Data)
}

// togglingGrader turns grading on and off under a running pipeline.
type togglingGrader struct {
	g atomic.Pointer[colorgrade.Grader]
}

func (t *togglingGrader) ProgramGrader() (*colorgrade.Grader, float32) {
	if g := t.g.Load(); g != nil {
		return g, 1
	}
	return nil, 0
}

// TestCPUPipeline_GradingFollowsToggleWithoutRebuild: the pipeline is built
// once and grading follows the grader frame by frame.
func TestCPUPipeline_GradingFollowsToggleWithoutRebuild(t *testing.T) {
	g := noirGrader(t)
	tg := &togglingGrader{}
	sw, got := gradeTestSwitcher(t, tg, nil)
	pf := sw.PipelineFormat()
	w, h := pf.Width, pf.Height
	src := barsFrame(w, h)
	epoch := sw.pipelineEpoch.Load()

	ingest(sw, append([]byte(nil), src...), w, h, 1)
	require.Eventually(t, func() bool { return len(got()) >= 1 }, time.Second, 5*time.Millisecond)
	require.Equal(t, src, got()[0], "grading off: frame untouched")

	tg.g.Store(g)
	ingest(sw, append([]byte(nil), src...), w, h, 2)
	require.Eventually(t, func() bool { return len(got()) >= 2 }, time.Second, 5*time.Millisecond)
	require.Equal(t, graded(t, g, src, w, h, 1), got()[1], "grading on: frame graded")

	tg.g.Store(nil)
	ingest(sw, append([]byte(nil), src...), w, h, 3)
	require.Eventually(t, func() bool { return len(got()) >= 3 }, time.Second, 5*time.Millisecond)
	require.Equal(t, src, got()[2], "grading off again: frame untouched")
	require.Equal(t, epoch, sw.pipelineEpoch.Load(), "no rebuild")
}

func TestColorGradeNode_Process10Bit(t *testing.T) {
	g := noirGrader(t)
	const w, h = 8, 4
	data := make([]byte, ColorYUV422_10bit.FrameSize(w, h))
	for i := 0; i < len(data); i += 2 {
		data[i], data[i+1] = byte(i*7), 0x01
	}
	want := append([]byte(nil), data...)
	require.NoError(t, g.ApplyYUV422P10(want, w, h, 1))

	n := &colorGradeNode{grader: &stubProgramGrader{g: g, intensity: 1}}
	pf := &ProcessingFrame{Data: data, Format: ColorYUV422_10bit, Width: w, Height: h}
	require.Same(t, pf, n.Process(nil, pf))
	require.Equal(t, want, pf.Data)
	require.NoError(t, n.Err())
}

// gradeTestSwitcher builds a switcher with cam1 on program and returns the
// frames the MXL raw sink sees.
func gradeTestSwitcher(t *testing.T, program ProgramGrader, sources SourceGrader) (*Switcher, func() [][]byte) {
	t.Helper()
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	var mu sync.Mutex
	var got [][]byte
	sw.SetRawVideoSink(func(pf *ProcessingFrame) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, append([]byte(nil), pf.Data...))
	})
	sw.SetColorGraders(program, sources)
	require.NoError(t, sw.BuildPipeline())
	sw.RegisterSource("cam1", newTestRelay())
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	return sw, func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return append([][]byte(nil), got...)
	}
}

func ingest(sw *Switcher, data []byte, w, h int, pts int64) {
	sw.IngestRawVideo("cam1", &ProcessingFrame{
		Data: data, Format: ColorYUV420_8bit, Width: w, Height: h, PTS: pts, Codec: "h264",
	})
}

// TestCPUPipeline_GradesProgram is the CPU-build failure an operator saw: a
// look selected on an engine without a GPU changed nothing on program.
func TestCPUPipeline_GradesProgram(t *testing.T) {
	g := noirGrader(t)
	sw, got := gradeTestSwitcher(t, &stubProgramGrader{g: g, intensity: 1}, nil)
	pf := sw.PipelineFormat()
	w, h := pf.Width, pf.Height
	src := barsFrame(w, h)
	ingest(sw, append([]byte(nil), src...), w, h, 1)

	require.Eventually(t, func() bool { return len(got()) >= 1 }, time.Second, 5*time.Millisecond)
	out := got()[0]
	require.Equal(t, graded(t, g, src, w, h, 1), out, "program output must carry the look")
	require.False(t, bytes.Equal(src, out), "noir must change saturated bars")
}

func TestCPUPipeline_GradesSourceOnACopy(t *testing.T) {
	g := noirGrader(t)
	sw, got := gradeTestSwitcher(t, nil, stubSourceGrader{"cam1": g})
	pf := sw.PipelineFormat()
	w, h := pf.Width, pf.Height
	src := barsFrame(w, h)
	in := append([]byte(nil), src...)
	ingest(sw, in, w, h, 1)

	require.Eventually(t, func() bool { return len(got()) >= 1 }, time.Second, 5*time.Millisecond)
	require.Equal(t, graded(t, g, src, w, h, 1), got()[0], "program output must carry the source correction")
	// A replay producer hands the same buffer in again for a slow-motion
	// repeat; grading it in place would grade the repeat twice.
	require.Equal(t, src, in, "the producer's buffer must be left as it was")

	ingest(sw, in, w, h, 2)
	require.Eventually(t, func() bool { return len(got()) >= 2 }, time.Second, 5*time.Millisecond)
	require.Equal(t, got()[0], got()[1], "a repeated buffer must grade the same")
}

func TestCPUPipeline_UngradedSourcePassesThrough(t *testing.T) {
	sw, got := gradeTestSwitcher(t, nil, stubSourceGrader{"cam2": noirGrader(t)})
	pf := sw.PipelineFormat()
	src := barsFrame(pf.Width, pf.Height)
	ingest(sw, append([]byte(nil), src...), pf.Width, pf.Height, 1)
	require.Eventually(t, func() bool { return len(got()) >= 1 }, time.Second, 5*time.Millisecond)
	require.Equal(t, src, got()[0])
}

type failingSourceGrader struct{ g *colorgrade.Grader }

func (f failingSourceGrader) SourceGrader(string) *colorgrade.Grader { return f.g }

func TestCPUPipeline_SourceGradeFailureLogsOnce(t *testing.T) {
	var buf bytes.Buffer
	sw, _ := gradeTestSwitcher(t, nil, failingSourceGrader{noirGrader(t)})
	sw.log = slog.New(slog.NewTextHandler(&buf, nil))
	// Odd dimensions make the 4:2:0 grade fail.
	for i := 0; i < 5; i++ {
		pf := &ProcessingFrame{Data: make([]byte, 64), Format: ColorYUV420_8bit, Width: 7, Height: 3, PTS: int64(i)}
		out := sw.gradeSourceFrame("cam1", pf)
		require.Same(t, pf, out, "an ungradable frame passes on unchanged")
	}
	require.Equal(t, 1, strings.Count(buf.String(), "source color correction skipped"))
}
