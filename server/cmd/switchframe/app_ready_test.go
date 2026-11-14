package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/mxl"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

// /ready tells a controller that the engine carries the program on its own,
// so its peer may be taken down. Each test leaves the engine initialised
// and producing its program but for the one thing it takes away, and
// /ready must then report not ready.

// readyFormats are the programs the timing tests run at: every frame
// rate the engine's formats take, from 23.976, whose ticks lie furthest
// apart, to 60, at 1080 and 2160 lines, and 120 and 240, whose ticks lie
// 8.3 and 4.2 ms apart.
var readyFormats = []string{
	"1080p23.976", "1080p24", "1080p25", "1080p29.97", "1080p30",
	"1080p50", "1080p59.94", "1080p60", "1080p120", "1080p240",
	"2160p25", "2160p59.94",
}

// readyFormat returns the format named: a preset, or 1080p at 120 or 240
// frames a second.
func readyFormat(t *testing.T, name string) switcher.PipelineFormat {
	t.Helper()
	if f, ok := switcher.FormatPresets[name]; ok {
		return f
	}
	f := switcher.FormatPresets["1080p60"]
	switch name {
	case "1080p120":
		f.FPSNum = 120
	case "1080p240":
		f.FPSNum = 240
	default:
		t.Fatalf("no format %s", name)
	}
	f.Name = name
	return f
}

// onAirApp returns an initialised engine running format on air on the
// built-in colour bars, once its reference clock, rc, has ticked.
func onAirApp(t *testing.T, format string) (a *App, rc *clock.ReferenceClock) {
	t.Helper()
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)
	a = &App{sw: sw, outputMgr: output.NewManager()}
	t.Cleanup(func() { _ = a.outputMgr.Close() })
	if err := sw.SetPipelineFormat(readyFormat(t, format)); err != nil {
		t.Fatal(err)
	}
	if err := sw.Cut(context.Background(), switcher.BuiltinSourceKeyColorbars); err != nil {
		t.Fatal(err)
	}
	pf := sw.PipelineFormat()
	rc = clock.NewReferenceClock(pf.FPSNum, pf.FPSDen, 48000, clock.PTSBase, true)
	sw.SetReferenceClock(rc)
	sw.Start()
	deadline := time.Now().Add(2 * time.Second)
	for sw.LastTick().IsZero() {
		if time.Now().After(deadline) {
			t.Fatal("the program clock never ticked")
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.startupDone.Store(true)
	readyFlag.Store(true)
	t.Cleanup(func() { readyFlag.Store(false) })
	return a, rc
}

// readyAnswer is what GET /ready answered.
type readyAnswer struct {
	code   int
	reason string
}

// startReady serves a's /ready and returns a function that reads it.
func startReady(t *testing.T, a *App) func() readyAnswer {
	t.Helper()
	stop, addr := StartAdminServer(context.Background(), "127.0.0.1:0", ":8080", "testhash", false, "", a.producing)
	t.Cleanup(stop)
	return func() readyAnswer {
		t.Helper()
		resp, err := http.Get("http://" + addr + "/ready")
		if err != nil {
			t.Fatalf("GET /ready: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode /ready: %v", err)
		}
		return readyAnswer{resp.StatusCode, body["reason"]}
	}
}

// waitReady waits for get to answer want, failing the test after timeout.
func waitReady(t *testing.T, get func() readyAnswer, want int, timeout time.Duration) readyAnswer {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := get()
		if got.code == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /ready = %d (%q), want %d", got.code, got.reason, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func requireNotReady(t *testing.T, got readyAnswer, reason string) {
	t.Helper()
	if got.code != http.StatusServiceUnavailable {
		t.Fatalf("GET /ready = %d, want 503 (%s)", got.code, reason)
	}
	if !strings.Contains(got.reason, reason) {
		t.Fatalf("not ready for %q, want %q", got.reason, reason)
	}
}

func TestReady_OnAir(t *testing.T) {
	for _, format := range readyFormats {
		t.Run(format, func(t *testing.T) {
			a, _ := onAirApp(t, format)
			get := startReady(t, a)
			waitReady(t, get, http.StatusOK, 2*time.Second)
		})
	}
}

// requireReadyWhileTicking fails unless get answers 200 throughout d, or
// 503 only for a clock that has truly not ticked for readyMaxLag: on a
// loaded host, under the race detector, a 2160p tick can take that long,
// and the engine is then rightly not ready.
func requireReadyWhileTicking(t *testing.T, a *App, get func() readyAnswer, d time.Duration) {
	t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		last := a.sw.LastTick()
		got := get()
		if got.code == http.StatusOK {
			continue
		}
		// /ready answered from a tick no older than last, so it can have
		// seen one more than readyMaxLag old only if last is.
		if strings.Contains(got.reason, "program clock has not ticked") && time.Since(last) > readyMaxLag {
			continue
		}
		t.Fatalf("GET /ready = %d (%q), the last tick %s ago, want 200 until a stall of readyMaxLag",
			got.code, got.reason, time.Since(last).Round(time.Millisecond))
	}
}

func TestReady_NotOnAir(t *testing.T) {
	t.Run("startup not settled", func(t *testing.T) {
		a, _ := onAirApp(t, "1080p29.97")
		a.startupDone.Store(false)
		requireNotReady(t, startReady(t, a)(), "startup has not settled")
	})
	t.Run("clock never started", func(t *testing.T) {
		sw := switcher.New(distribution.NewRelay())
		t.Cleanup(sw.Close)
		if err := sw.Cut(context.Background(), switcher.BuiltinSourceKeyColorbars); err != nil {
			t.Fatal(err)
		}
		a := &App{sw: sw}
		a.startupDone.Store(true)
		readyFlag.Store(true)
		t.Cleanup(func() { readyFlag.Store(false) })
		requireNotReady(t, startReady(t, a)(), "program clock has not ticked")
	})
	// Ready while the clock ticks at any rate, not ready readyMaxLag after
	// it stops.
	for _, format := range readyFormats {
		t.Run("clock stopped/"+format, func(t *testing.T) {
			a, rc := onAirApp(t, format)
			get := startReady(t, a)
			waitReady(t, get, http.StatusOK, 10*time.Second)
			requireReadyWhileTicking(t, a, get, readyMaxLag)
			rc.Stop()
			// Ready until readyMaxLag after the last tick, then not ready.
			for {
				got := get()
				answered := time.Now()
				last := a.sw.LastTick()
				if got.code == http.StatusOK {
					if answered.Sub(last) > readyMaxLag+5*time.Second {
						t.Fatalf("still ready %s after the last tick", answered.Sub(last).Round(time.Millisecond))
					}
					time.Sleep(20 * time.Millisecond)
					continue
				}
				requireNotReady(t, got, "program clock has not ticked for")
				if since := answered.Sub(last); since < readyMaxLag {
					t.Fatalf("not ready %s after the last tick, before readyMaxLag", since.Round(time.Millisecond))
				}
				break
			}
		})
	}
}

// gatedGrainWriter takes video grains until refuse is set.
type gatedGrainWriter struct{ refuse atomic.Bool }

func (g *gatedGrainWriter) WriteGrain(uint64, []byte) error {
	if g.refuse.Load() {
		return errors.New("write refused")
	}
	return nil
}

func (*gatedGrainWriter) Close() error { return nil }

// The MXL writer's own check at every frame rate, then /ready through it.
func TestReady_MXLWriterStalled(t *testing.T) {
	const width, height = 12, 2
	start := func(t *testing.T, rate mxl.Rational) (*mxl.Output, *gatedGrainWriter) {
		out := mxl.NewOutput(mxl.OutputConfig{Width: width, Height: height, VideoRate: rate})
		grains := &gatedGrainWriter{}
		out.StartLifecycle(context.Background(), grains, nil)
		t.Cleanup(out.Stop)
		yuv := make([]byte, width*height*3/2)
		out.Writer().WriteVideo(yuv, width, height,
			clock.IndexPTS(clock.Rate{Num: rate.Numerator, Den: rate.Denominator}, mxl.CurrentIndex(rate)))
		return out, grains
	}
	// waitAtClock waits until AtClock gives what want says.
	waitAtClock := func(t *testing.T, w *mxl.Writer, want func(error) bool, timeout time.Duration) error {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for {
			err := w.AtClock(readyMaxLag)
			if want(err) {
				return err
			}
			if time.Now().After(deadline) {
				t.Fatalf("AtClock = %v after %s", err, timeout)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, format := range readyFormats {
		t.Run(format, func(t *testing.T) {
			f := readyFormat(t, format)
			out, grains := start(t, mxl.Rational{Numerator: int64(f.FPSNum), Denominator: int64(f.FPSDen)})
			w := out.Writer()
			_ = waitAtClock(t, w, func(err error) bool { return err == nil }, 2*time.Second)
			// No frame fed for readyMaxLag: the writer repeats the last.
			for end := time.Now().Add(readyMaxLag); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
				if err := w.AtClock(readyMaxLag); err != nil {
					t.Fatalf("a stall of the frames fed counts as a stalled writer: %v", err)
				}
			}
			grains.refuse.Store(true)
			refused := time.Now()
			err := waitAtClock(t, w, func(err error) bool { return err != nil }, readyMaxLag+2*time.Second)
			if !strings.Contains(err.Error(), "behind the clock") {
				t.Fatalf("AtClock = %v, want behind the clock", err)
			}
			if since := time.Since(refused); since < readyMaxLag-grainPeriod(f) {
				t.Fatalf("behind the clock %s after the writes stopped, before readyMaxLag", since)
			}
		})
	}
	t.Run("ready", func(t *testing.T) {
		a, _ := onAirApp(t, "1080p29.97")
		pf := a.sw.PipelineFormat()
		rate := mxl.Rational{Numerator: int64(pf.FPSNum), Denominator: int64(pf.FPSDen)}
		a.mxlOutput = mxl.NewOutput(mxl.OutputConfig{Width: width, Height: height, VideoRate: rate})
		grains := &gatedGrainWriter{}
		a.mxlOutput.StartLifecycle(context.Background(), grains, nil)
		t.Cleanup(a.mxlOutput.Stop)
		get := startReady(t, a)
		requireNotReady(t, get(), "mxl output: program video: nothing written")
		yuv := make([]byte, width*height*3/2)
		a.mxlOutput.Writer().WriteVideo(yuv, width, height,
			clock.IndexPTS(clock.Rate{Num: rate.Numerator, Den: rate.Denominator}, mxl.CurrentIndex(rate)))
		waitReady(t, get, http.StatusOK, 10*time.Second)
		grains.refuse.Store(true)
		_ = waitAtClock(t, a.mxlOutput.Writer(), func(err error) bool { return err != nil }, readyMaxLag+2*time.Second)
		if err := a.producing(); err == nil || !strings.Contains(err.Error(), "mxl output") &&
			!strings.Contains(err.Error(), "program clock has not ticked") {
			t.Fatalf("producing() = %v with the program flow behind the clock", err)
		}
	})
}

// grainPeriod is one frame of f.
func grainPeriod(f switcher.PipelineFormat) time.Duration {
	return time.Duration(int64(time.Second) * int64(f.FPSDen) / int64(f.FPSNum))
}

// An MXL output whose flows did not open, which initMXL only logs, is not
// a program this engine writes: video always, audio when one is configured.
func TestReady_MXLFlowNotOpened(t *testing.T) {
	a, _ := onAirApp(t, "1080p29.97")
	a.mxlOutput = mxl.NewOutput(mxl.OutputConfig{Width: 12, Height: 2, VideoRate: mxl.Rational{Numerator: 30000, Denominator: 1001}})
	a.mxlOutput.StartLifecycle(context.Background(), nil, nil)
	t.Cleanup(a.mxlOutput.Stop)
	requireNotReady(t, startReady(t, a)(), "mxl output: program video: no flow")

	b, _ := onAirApp(t, "1080p29.97")
	b.cfg.MXLOutputAudioDef = "/config/program-audio.json"
	b.mxlOutput = mxl.NewOutput(mxl.OutputConfig{Width: 12, Height: 2, VideoRate: mxl.Rational{Numerator: 30000, Denominator: 1001}})
	b.mxlOutput.StartLifecycle(context.Background(), &gatedGrainWriter{}, nil)
	t.Cleanup(b.mxlOutput.Stop)
	requireNotReady(t, startReady(t, b)(), "mxl output: program audio: no flow")
}

func TestReady_DestinationNotStarted(t *testing.T) {
	a, _ := onAirApp(t, "1080p29.97")
	a.outputMgr.SetEngineLabel("a")
	a.outputMgr.EnablePeerFailover()
	err := a.outputMgr.ApplyStartup(output.StartupConfig{
		Destinations: []output.StartupDestination{
			{DestinationConfig: output.DestinationConfig{Type: "srt-caller", Address: "192.0.2.1", Port: 9000}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	get := startReady(t, a)

	// Armed, and held until the engine has a view of its peer.
	requireNotReady(t, get(), "destinations not started")

	a.outputMgr.ReconcileDestinations(output.PeerView{Known: true, Label: "b", Grace: time.Hour})
	waitReady(t, get, http.StatusOK, 2*time.Second)
}

// absentPeerURL returns the URL of a port nothing listens on.
func absentPeerURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr
}

// An engine of a pair whose peer never answers, as when the peer's node is
// gone, holds the destinations --outputs-config declares until it counts the
// peer down, then sends them and becomes ready: within the takeover window,
// the peer's failure threshold of polls, plus a reconcile tick. The engine
// that owns a destination starts it; the other takes it over.
func TestReady_PeerAbsentDestinationDeclared(t *testing.T) {
	const poll, tick = 100 * time.Millisecond, 50 * time.Millisecond
	for _, label := range []string{"a", "b"} {
		t.Run(label, func(t *testing.T) {
			a, _ := onAirApp(t, "1080p29.97")
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			p := peer.NewHealthPoller(absentPeerURL(t), peer.WithPollInterval(poll),
				peer.WithLocalIdentity(label))
			a.outputMgr.SetEngineLabel(label)
			a.outputMgr.EnablePeerFailover()
			a.outputMgr.SetPeerProbe(peerProbe(ctx, p))
			err := a.outputMgr.ApplyStartup(output.StartupConfig{
				Destinations: []output.StartupDestination{
					{DestinationConfig: output.DestinationConfig{Type: "srt-caller", Address: "192.0.2.1", Port: 9000}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			get := startReady(t, a)
			if label == "a" {
				requireNotReady(t, get(), "destinations not started")
			}

			start := time.Now()
			p.Start()
			t.Cleanup(p.Stop)
			go runDestinationFailover(ctx, p, a.outputMgr, tick)

			window := p.DownAfter() + 2*tick
			deadline := time.Now().Add(window + 2*time.Second)
			for {
				sending := a.outputMgr.SendingDestinationIDs()
				if len(sending) == 1 && get().code == http.StatusOK {
					t.Logf("sending and ready %s after the polls started, window %s",
						time.Since(start).Round(time.Millisecond), window)
					break
				}
				if time.Now().After(deadline) {
					got := get()
					t.Fatalf("after %s: sending %v, /ready %d %q", time.Since(start).Round(time.Millisecond),
						sending, got.code, got.reason)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
