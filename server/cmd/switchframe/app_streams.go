package main

import (
	"log/slog"
	"strings"

	"github.com/zsiec/prism/distribution"
)

// onStreamRegistered handles a new stream from the Prism distribution server.
// It skips "program" and "replay" (internal relays) and includes a nil guard
// for BUG detection — all init methods complete before Run() starts the Prism
// server, so sw/mixer should never be nil.
func (a *App) onStreamRegistered(key string, relay *distribution.Relay) {
	if key == "program" || key == "replay" {
		return
	}
	if strings.HasPrefix(key, "mxl:") {
		return // MXL sources are manually wired in initMXL()
	}

	if a.sw == nil || a.mixer == nil {
		slog.Error("BUG: stream registered before switcher/mixer initialized", "key", key)
		return
	}

	slog.Info("stream registered, adding source", "key", key)
	a.sw.RegisterSource(key, relay)
	a.mixer.AddChannel(key)
	_ = a.mixer.SetAFV(key, true) // cameras default to audio-follows-video

	// Register replay viewer on the source relay.
	if a.replayMgr != nil {
		if err := a.replayMgr.AddSource(key); err != nil {
			slog.Warn("replay: could not add source", "key", key, "err", err)
		} else if v := a.replayMgr.Viewer(key); v != nil {
			relay.AddViewer(v)
		}
	}
}

// onStreamUnregistered handles a removed stream. It skips "program" and
// "replay" (internal relays) and includes a nil guard for BUG detection.
func (a *App) onStreamUnregistered(key string) {
	if key == "program" || key == "replay" {
		return
	}

	if a.sw == nil || a.mixer == nil {
		slog.Error("BUG: stream unregistered before switcher/mixer initialized", "key", key)
		return
	}

	slog.Info("stream unregistered, removing source", "key", key)
	a.sw.UnregisterSource(key)
	a.mixer.RemoveChannel(key)

	// Remove replay viewer from the source relay.
	if a.replayMgr != nil {
		a.replayMgr.RemoveSource(key)
	}
}
