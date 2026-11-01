package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/srt"
	"github.com/zsiec/switchframe/server/switcher"
)

// An SRT source whose caller never comes back keeps only its
// registration. Its listener key comes from the caller's stream ID, so
// such sources have no bound: the frames it holds for the program's
// freeze are released once the freeze has turned to black, and its
// preview encoder, which has nothing left to encode, when its connection
// ends. A card has a limited number of encode sessions, so the encoder
// is closed before a new connection under the key opens its own. The
// source stays on program, showing black, and a new connection brings
// its frames back. A drop shorter than the freeze releases nothing.
func TestSRTSourceGoneReleasesItsFramesAndPreviewEncoder(t *testing.T) {
	const key = "srt:gone"
	const w, h = 320, 240
	const releaseAfter = 300 * time.Millisecond
	saved := srtSourceReleaseAfter
	srtSourceReleaseAfter = releaseAfter
	t.Cleanup(func() { srtSourceReleaseAfter = saved })

	relay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, relay)
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	sw.SetMixer(m)
	gpuSources := &gpuRemovals{}
	sw.SetGPUSourceManager(gpuSources)
	a := &App{sw: sw, mixer: m, srtSources: map[string]*srtSourceState{}}
	rec := &programLumaRecorder{}
	relay.AddViewer(rec)

	a.bindSRTSource(key)
	if err := sw.Cut(context.Background(), key); err != nil {
		t.Fatal(err)
	}

	period := 1001 * time.Millisecond / 30
	// send feeds n frames of luma y through a new connection's video
	// ingest and closes it.
	send := func(n int, y byte, srcPTS int64) {
		video := make(chan srtVideoJob, 4)
		a.startSRTPipelineIngest(key, a.bindSRTSource(key), video, nil)
		for i := range n {
			yuv := make([]byte, w*h*3/2)
			for j := range yuv[:w*h] {
				yuv[j] = y
			}
			select {
			case video <- srtVideoJob{yuv: yuv, w: w, h: h, pts: srcPTS + int64(i)*3003}:
			case <-time.After(time.Second):
				t.Fatalf("video ingest took no frame for 1s at frame %d", i)
			}
			time.Sleep(period)
		}
		close(video)
	}
	// shown waits up to 2s for the program to show luma y from frame
	// index from on, and returns the index of the first such frame.
	shown := func(y byte, from int) int {
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			rec.mu.Lock()
			i := slices.Index(rec.luma[min(from, len(rec.luma)):], y)
			rec.mu.Unlock()
			if i >= 0 {
				return from + i
			}
		}
		t.Fatalf("program showed no frame of luma %d within 2s", y)
		return 0
	}
	frames := func() int {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return len(rec.luma)
	}

	send(10, 50, 20*90000)
	shown(50, 0)

	// The connection ends.
	a.releaseSRTConnection(key, srt.ModeListener)
	dropped := time.Now()
	dropFrame := frames()
	if got := gpuSources.logged(key); !slices.Contains(got, "stop-preview "+key) {
		t.Fatalf("GPU calls after the connection ended: %v, want the preview stopped", got)
	}
	if slices.Contains(gpuSources.logged(key), "remove "+key) {
		t.Fatal("GPU entry removed as the connection ended, want it kept for the freeze")
	}

	// The freeze holds the last frame, then turns to black once the
	// frames are released.
	black := shown(16, dropFrame)
	if d := time.Since(dropped); d < releaseAfter {
		t.Fatalf("program went black %v after the connection ended, want the freeze for %v", d, releaseAfter)
	}
	rec.mu.Lock()
	for i := dropFrame; i < black; i++ {
		if rec.luma[i] != 50 {
			rec.mu.Unlock()
			t.Fatalf("program frame %d of the freeze has luma %d, want the held 50", i, rec.luma[i])
		}
	}
	rec.mu.Unlock()
	if got := gpuSources.logged(key); !slices.Contains(got, "remove "+key) {
		t.Fatalf("GPU calls once the program went black: %v, want the entry removed", got)
	}
	if !sw.HasSource(key) || sw.ProgramSource() != key {
		t.Fatalf("source registered %v, program %q once its frames were released; want it kept on program",
			sw.HasSource(key), sw.ProgramSource())
	}
	time.Sleep(5 * period)
	rec.mu.Lock()
	for i := black; i < len(rec.luma); i++ {
		if rec.luma[i] != 16 {
			rec.mu.Unlock()
			t.Fatalf("program frame %d after the release has luma %d, want black", i, rec.luma[i])
		}
	}
	rec.mu.Unlock()

	// A new connection under the key: live video, and frames on the GPU
	// again.
	removed := len(gpuSources.logged(key))
	send(10, 90, 40*90000)
	shown(90, black)
	if got := gpuSources.logged(key)[removed:]; !slices.Contains(got, "ingest "+key) {
		t.Fatalf("GPU calls after the new connection: %v, want its frames ingested", got)
	}

	// A connection that ends and comes back within the freeze keeps the
	// frames: the release its end armed must not fire into the new one.
	a.releaseSRTConnection(key, srt.ModeListener)
	resumed := frames()
	send(int(2*releaseAfter/period), 120, 60*90000)
	first := shown(120, resumed)
	rec.mu.Lock()
	for i := first; i < len(rec.luma); i++ {
		if rec.luma[i] != 120 {
			rec.mu.Unlock()
			t.Fatalf("program frame %d of the connection after a short drop has luma %d, want 120", i, rec.luma[i])
		}
	}
	rec.mu.Unlock()
	gpuSources.mu.Lock()
	n := len(gpuSources.removed)
	gpuSources.mu.Unlock()
	if n != 1 {
		t.Fatalf("GPU entry removed %d times, want once: not after the drop a new connection followed within %v",
			n, releaseAfter)
	}
}

// A GPU rebuild stops every SRT source, and each stop arms the release of
// that source's frames as any ended connection does. A listener whose
// caller never comes back must still have them released after the
// freeze; only shutdown cancels the releases.
func TestSRTSourcesStoppedForAGPURebuildStillReleaseTheirFrames(t *testing.T) {
	const gone, down = "srt:gone", "srt:down"
	const releaseAfter = 100 * time.Millisecond
	saved := srtSourceReleaseAfter
	srtSourceReleaseAfter = releaseAfter
	t.Cleanup(func() { srtSourceReleaseAfter = saved })

	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	sw.SetMixer(m)
	gpuSources := &gpuRemovals{}
	sw.SetGPUSourceManager(gpuSources)
	a := &App{sw: sw, mixer: m, srtSources: map[string]*srtSourceState{}}
	removed := func(key string) bool {
		gpuSources.mu.Lock()
		defer gpuSources.mu.Unlock()
		return slices.Contains(gpuSources.removed, key)
	}

	// As rebuildGPUPipelineForFormat does: the connection ends, then the
	// sources are stopped.
	a.bindSRTSource(gone)
	a.releaseSRTConnection(gone, srt.ModeListener)
	a.stopSRTSources()
	for end := time.Now().Add(releaseAfter + 2*time.Second); !removed(gone); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("frames of %s not released %v after a rebuild stopped the sources, want after %v",
				gone, releaseAfter+2*time.Second, releaseAfter)
		}
	}

	// Shutdown cancels what is still pending.
	a.bindSRTSource(down)
	a.releaseSRTConnection(down, srt.ModeListener)
	a.stopSRTSources()
	a.cancelSRTReleases()
	time.Sleep(3 * releaseAfter)
	if removed(down) {
		t.Fatalf("frames of %s released after shutdown cancelled the releases", down)
	}
}
