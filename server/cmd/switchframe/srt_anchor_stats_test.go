package main

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/metrics"
)

// A connected SRT source's anchor reports how far its offset moved since
// the connection was placed and how often it was raised, in the debug
// snapshot and as metrics, so a soak tells a sender's late frames from
// the engine's.
func TestSRTAnchorStatsCountRaises(t *testing.T) {
	// The real clock: other tests' switchers read it from goroutines
	// that outlive them. Each timestamp after the first arrives 10 ms
	// later against the one before, and raises the offset by that much.
	anchor := &clock.TrackingAnchor{}
	src := int64(1_000_000)
	for range 3 {
		anchor.PTS(src)
		src -= 900
	}
	a := &App{srtSources: map[string]*srtSourceState{
		"srt:cam": {anchor: anchor},
		"srt:new": {anchor: &clock.TrackingAnchor{}},
	}}

	snap := srtAnchorProvider{app: a}.DebugSnapshot()["sources"].(map[string]any)
	cam := snap["srt:cam"].(map[string]any)
	require.Equal(t, int64(2), cam["raises"])
	require.InDelta(t, 20.0, cam["offset_ms"], 5) // and the time the calls took
	require.Equal(t, int64(0), snap["srt:new"].(map[string]any)["raises"])

	label := metrics.SourceLabel("srt:cam")
	want := `
# HELP switchframe_srt_anchor_offset_raises_total Timestamps of a connected SRT source that raised its TrackingAnchor offset, each moving its later frames that much later. Starts at 0 on every connection.
# TYPE switchframe_srt_anchor_offset_raises_total counter
switchframe_srt_anchor_offset_raises_total{source="` + label + `"} 2
switchframe_srt_anchor_offset_raises_total{source="` + metrics.SourceLabel("srt:new") + `"} 0
`
	require.NoError(t, testutil.CollectAndCompare(srtAnchorCollector{app: a}, strings.NewReader(want),
		"switchframe_srt_anchor_offset_raises_total"))
	problems, err := testutil.CollectAndLint(srtAnchorCollector{app: a})
	require.NoError(t, err)
	require.Empty(t, problems)
}
