package mxl

import (
	"strings"
	"testing"
)

// TestCheckGrainPayloadFits covers finding #72: discreteFlowWriter.WriteGrain
// memcpy'd the caller payload into the MXL shared-memory grain slot without
// validating len(data) against the grain's capacity (grainSize from
// mxlFlowWriterOpenGrain), allowing an oversized payload (e.g. a video frame
// whose WriterConfig resolution disagrees with the flow definition, or an
// oversized data grain) to overwrite adjacent grains/headers consumed by
// other processes. checkGrainPayloadFits is the bounds check WriteGrain now
// applies before the cgo memcpy.
func TestCheckGrainPayloadFits(t *testing.T) {
	tests := []struct {
		name     string
		dataLen  int
		capacity uint32
		wantErr  bool
	}{
		{"fits exactly", 5_529_600, 5_529_600, false},
		{"smaller than capacity", 254, 5_529_600, false},
		{"one byte over", 5_529_601, 5_529_600, true},
		{"resolution mismatch 1080p into 720p flow", 5_529_600, 2_488_320, true},
		{"zero capacity rejects any payload", 1, 0, true},
		{"zero payload always fits", 0, 0, false},
		{"huge payload vs small capacity", 1 << 30, 4096, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkGrainPayloadFits(tt.dataLen, tt.capacity)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("checkGrainPayloadFits(%d, %d) = nil, want error", tt.dataLen, tt.capacity)
				}
				if !strings.Contains(err.Error(), "exceeds grain capacity") {
					t.Fatalf("error %q should mention exceeding grain capacity", err.Error())
				}
			} else if err != nil {
				t.Fatalf("checkGrainPayloadFits(%d, %d) = %v, want nil", tt.dataLen, tt.capacity, err)
			}
		})
	}
}
