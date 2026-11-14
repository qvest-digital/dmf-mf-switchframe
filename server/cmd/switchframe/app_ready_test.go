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

// onAirApp returns an initialised engine on air on the built-in colour
// bars, once its reference clock, rc, has ticked.
func onAirApp(t *testing.T) (a *App, rc *clock.ReferenceClock) {
	t.Helper()
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)
	a = &App{sw: sw, outputMgr: output.NewManager()}
	t.Cleanup(func() { _ = a.outputMgr.Close() })
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
	a, _ := onAirApp(t)
	get := startReady(t, a)
	waitReady(t, get, http.StatusOK, 2*time.Second)
}

func TestReady_NotOnAir(t *testing.T) {
	t.Run("startup not settled", func(t *testing.T) {
		a, _ := onAirApp(t)
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
	t.Run("clock stopped", func(t *testing.T) {
		a, rc := onAirApp(t)
		get := startReady(t, a)
		waitReady(t, get, http.StatusOK, 2*time.Second)
		rc.Stop()
		got := waitReady(t, get, http.StatusServiceUnavailable, readyMaxLag+2*time.Second)
		requireNotReady(t, got, "program clock has not ticked for")
	})
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
	rate := mxl.Rational{Numerator: 25, Denominator: 1}
	a, _ := onAirApp(t)
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

	grains.refuse.Store(true)
	got := waitReady(t, get, http.StatusServiceUnavailable, readyMaxLag+2*time.Second)
	requireNotReady(t, got, "behind the clock")
}

func TestReady_DestinationNotStarted(t *testing.T) {
	a, _ := onAirApp(t)
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
			a, _ := onAirApp(t)
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
