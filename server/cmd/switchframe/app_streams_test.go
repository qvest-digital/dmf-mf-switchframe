package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/zsiec/switchframe/server/switcher"
)

// captureSlog redirects the default slog logger to a buffer for the duration
// of the test and returns the buffer.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// TestOnStreamRegistered_SkipsBuiltinSourceRelays verifies that the built-in
// "source:" relays (source:black, source:colorbars — registered by
// initBuiltinSourcePreview via a.server.RegisterStream) are skipped by
// onStreamRegistered, like every other internally wired relay family
// (mxl:, srt:, clip:, playout:).
//
// Without the skip, the handler would re-register the built-in virtual source
// as a relay-fed source (clobbering its BLACK/BARS label and isVirtual flag),
// attach an H.264 decoder to the relay that the built-in's own preview encoder
// broadcasts into (a permanent 480p encode→decode→re-ingest loop competing
// with the full-res producer frames), and allocate a phantom mixer channel
// and replay buffer.
//
// The test runs with a nil switcher/mixer: if the key is NOT skipped, the
// handler reaches the nil guard and logs a "BUG:" error, which the test
// rejects. A properly skipped key produces no log output at all.
func TestOnStreamRegistered_SkipsBuiltinSourceRelays(t *testing.T) {
	buf := captureSlog(t)

	app := &App{}
	app.onStreamRegistered(switcher.BuiltinSourceKeyBlack, nil)
	app.onStreamRegistered(switcher.BuiltinSourceKeyColorbars, nil)

	if out := buf.String(); strings.Contains(out, "BUG") || strings.Contains(out, "adding source") {
		t.Errorf("onStreamRegistered did not skip built-in source: relays:\n%s", out)
	}
}

// TestOnStreamUnregistered_SkipsBuiltinSourceRelays is the unregister-side
// counterpart of TestOnStreamRegistered_SkipsBuiltinSourceRelays.
func TestOnStreamUnregistered_SkipsBuiltinSourceRelays(t *testing.T) {
	buf := captureSlog(t)

	app := &App{}
	app.onStreamUnregistered(switcher.BuiltinSourceKeyBlack)
	app.onStreamUnregistered(switcher.BuiltinSourceKeyColorbars)

	if out := buf.String(); strings.Contains(out, "BUG") || strings.Contains(out, "removing source") {
		t.Errorf("onStreamUnregistered did not skip built-in source: relays:\n%s", out)
	}
}
