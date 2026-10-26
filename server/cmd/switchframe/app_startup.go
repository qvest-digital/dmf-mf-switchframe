package main

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

// initialSourceGrace is how long an engine going live waits for
// --initial-program-source to deliver frames before it takes another source.
const initialSourceGrace = 10 * time.Second

// startupEngine is the App as peer.RunStartup drives it.
type startupEngine struct {
	app *App

	// goLiveSince is when the engine first tried to go live on its own.
	goLiveSince time.Time
	warnedGrace bool
}

var _ peer.StartupEngine = (*startupEngine)(nil)

func (e *startupEngine) ProgramSource() string {
	return e.app.sw.ProgramSource()
}

func (e *startupEngine) Cut(ctx context.Context, source string) error {
	return e.app.sw.Cut(ctx, source)
}

// ApplyPeerSnapshot applies the peer's state the way a snapshot pushed to
// POST /api/sync/state-snapshot is applied, seq included.
func (e *startupEngine) ApplyPeerSnapshot(ctx context.Context, snap internal.FullStateSnapshot, seq uint64) {
	if warnings := internal.ApplyFullSnapshot(ctx, snap, e.app); len(warnings) > 0 {
		slog.Warn("startup: peer state applied in part", "warnings", warnings)
	}
	if seq > 0 {
		e.app.sw.TrackCommandSeq(seq)
	}
}

func (e *startupEngine) GoLive(ctx context.Context) bool {
	if e.goLiveSince.IsZero() {
		e.goLiveSince = time.Now()
	}
	configured := e.app.cfg.InitialProgramSource
	graceOver := time.Since(e.goLiveSince) >= initialSourceGrace
	key := initialProgramSource(configured, e.app.sw.State().Sources, graceOver)
	if key == "" {
		return false
	}
	if configured != "" && key != configured && !e.warnedGrace {
		e.warnedGrace = true
		slog.Warn("startup: initial program source delivers no frames; going live on another",
			"configured", configured, "source", key, "waited", initialSourceGrace)
	}
	if err := e.app.sw.Cut(ctx, key); err != nil {
		slog.Warn("startup: initial cut failed", "source", key, "error", err)
		return false
	}
	return true
}

// initialProgramSource picks the source an engine with no peer to follow
// goes live on: configured once it delivers frames, else, with configured
// empty or graceOver, the first input delivering frames in source order. An
// input is an MXL, SRT or demo source; the built-ins, which are virtual,
// clips, replay and playout are not. "" while there is none.
func initialProgramSource(configured string, sources map[string]internal.SourceInfo, graceOver bool) string {
	healthy := func(key string, s internal.SourceInfo) bool {
		return s.Status == string(switcher.SourceHealthy) && !s.IsVirtual && isInputSource(key)
	}
	if configured != "" {
		if s, ok := sources[configured]; ok && healthy(configured, s) {
			return configured
		}
		if !graceOver {
			return ""
		}
	}
	var keys []string
	for key, s := range sources {
		if healthy(key, s) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		pi, pj := sources[keys[i]].Position, sources[keys[j]].Position
		if pi != pj {
			return pi < pj
		}
		return keys[i] < keys[j]
	})
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func isInputSource(key string) bool {
	switch sourceType(key) {
	case "mxl", "srt", "demo":
		return true
	}
	return false
}
