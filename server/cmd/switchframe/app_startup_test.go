package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zsiec/switchframe/server/internal"
)

func TestInitialProgramSource(t *testing.T) {
	src := func(status string, position int) internal.SourceInfo {
		return internal.SourceInfo{Status: status, Position: position}
	}
	sources := map[string]internal.SourceInfo{
		"source:black":   {Status: "healthy", IsVirtual: true},
		"replay":         {Status: "healthy", IsVirtual: true},
		"clip-player-1":  src("healthy", 0),
		"playout:1":      src("healthy", 0),
		"mxl:cam3":       src("healthy", 3),
		"mxl:cam2":       src("healthy", 2),
		"mxl:cam1":       src("no_signal", 1),
		"srt:contrib":    src("offline", 0),
		"mxl:unswitched": src("stale", 0),
	}

	tests := []struct {
		name       string
		configured string
		sources    map[string]internal.SourceInfo
		graceOver  bool
		want       string
	}{
		{"first input delivering frames, in source order", "", sources, false, "mxl:cam2"},
		{"configured input delivering frames", "mxl:cam3", sources, false, "mxl:cam3"},
		// The configured input is waited for, so two engines given the
		// same one do not go live on different sources while it connects.
		{"configured input not delivering yet", "mxl:cam1", sources, false, ""},
		{"configured input still silent after the grace", "mxl:cam1", sources, true, "mxl:cam2"},
		{"configured key no input has", "mxl:typo", sources, true, "mxl:cam2"},
		{"configured built-in", "source:black", sources, false, "source:black"},
		{"no input delivering", "", map[string]internal.SourceInfo{"mxl:cam1": src("offline", 0), "source:colorbars": {Status: "healthy", IsVirtual: true}}, true, ""},
		{"key breaks a position tie", "", map[string]internal.SourceInfo{"srt:b": src("healthy", 0), "mxl:a": src("healthy", 0), "cam1": src("healthy", 1)}, false, "mxl:a"},
		{"demo camera", "", map[string]internal.SourceInfo{"cam1": src("healthy", 0)}, false, "cam1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, initialProgramSource(tt.configured, tt.sources, tt.graceOver))
		})
	}
}
