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

// requireStaysReady fails unless get answers 200 throughout d.
func requireStaysReady(t *testing.T, get func() readyAnswer, d time.Duration) {
	t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if got := get(); got.code != http.StatusOK {
			t.Fatalf("GET /ready = %d (%q) within %s of the stall, want 200 until readyMaxLag", got.code, got.reason, d)
		}
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
			waitReady(t, get, http.StatusOK, 2*time.Second)
			requireStaysReady(t, get, readyMaxLag)
			rc.Stop()
			stopped := time.Now()
			got := waitReady(t, get, http.StatusServiceUnavailable, readyMaxLag+2*time.Second)
			requireNotReady(t, got, "program clock has not ticked for")
			if since := time.Since(stopped); since < readyMaxLag/2 {
				t.Fatalf("not ready %s after the clock stopped, before readyMaxLag", since)
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

func TestReady_MXLWriterStalled(t *testing.T) {
	const width, height = 12, 2
	for _, format := range readyFormats {
		t.Run(format, func(t *testing.T) {
			a, _ := onAirApp(t, format)
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
			waitReady(t, get, http.StatusOK, 2*time.Second)
			// No frame fed for readyMaxLag: the writer repeats the last.
			requireStaysReady(t, get, readyMaxLag)

			grains.refuse.Store(true)
			got := waitReady(t, get, http.StatusServiceUnavailable, readyMaxLag+2*time.Second)
			requireNotReady(t, got, "behind the clock")
		})
	}
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
