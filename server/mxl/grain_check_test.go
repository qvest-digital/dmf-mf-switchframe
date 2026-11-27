package mxl

import (
	"bytes"
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
		capacity uint64
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

// simSlot models a discrete grain slot header exactly as the MXL SDK manages
// it (FlowManager.cpp / PosixDiscreteFlowWriter.cpp):
//
//   - At flow creation, grainSize is set to the slot capacity and totalSlices
//     to the slice count. This is the ONLY time the SDK itself writes
//     grainSize.
//   - openGrain returns the stored header verbatim (only index is updated).
//   - commit overwrites the stored header verbatim with whatever the writer
//     passes back. WriteGrain passes grainSize = len(data) for a video grain,
//     and builds before data grains took the whole slot did the same for
//     data grains.
type simSlot struct {
	grainSize   uint32
	totalSlices uint16
}

// writeGrain mirrors discreteFlowWriter.WriteGrain's capacity check and its
// commit of a grain at its frame length against the simulated slot header.
func (s *simSlot) writeGrain(sliceBytes uint64, data []byte) error {
	// openGrain: header returned verbatim.
	capacity := discreteGrainCapacity(sliceBytes, s.totalSlices, s.grainSize)
	if err := checkGrainPayloadFits(len(data), capacity); err != nil {
		return err // CancelGrain: header unchanged.
	}
	// CommitGrain at the frame length.
	s.grainSize = uint32(len(data))
	return nil
}

// TestWriteGrainCapacityNotRatchetedByPreviousPayload is the regression test
// for the capacity-ratchet bug: WriteGrain used the grainSize returned by
// mxlFlowWriterOpenGrain as the slot capacity, but it also commits
// grainSize = len(data) into the same header -- and openGrain returns the
// stored header verbatim. After the ring wrapped, the check compared against
// the previous payload's size instead of the slot capacity, so each slot's
// usable size ratcheted monotonically downward. A slot last committed at a
// frame length, as data grains were before they took the whole slot, must
// still take a longer frame; capacity derives from the immutable flow config
// (sliceBytes) and the immutable totalSlices.
func TestWriteGrainCapacityNotRatchetedByPreviousPayload(t *testing.T) {
	// A "video/smpte291" data flow: slice size 1 byte, totalSlices =
	// MXL_DATA_FORMAT_GRAIN_SIZE = 4096, so true capacity = 4096 bytes.
	const sliceBytes = 1
	slot := &simSlot{grainSize: 4096, totalSlices: 4096}

	// 1. Write a small SCTE-104-sized data grain into the slot.
	small := make([]byte, 16)
	if err := slot.writeGrain(sliceBytes, small); err != nil {
		t.Fatalf("small grain write: %v", err)
	}
	if slot.grainSize != 16 {
		t.Fatalf("committed grainSize = %d, want 16", slot.grainSize)
	}

	// 2. The ring wraps: the same slot index is reopened, and openGrain
	// reports the previous payload's length (16) in grainSize. The old check
	// treated that as capacity and rejected anything larger.
	larger := make([]byte, 4000) // well within the true 4096-byte capacity
	if err := checkGrainPayloadFits(len(larger), uint64(slot.grainSize)); err == nil {
		t.Fatal("precondition: old behaviour (header grainSize as capacity) should reject the larger grain")
	}

	// 3. With the fix, the larger within-capacity grain must succeed.
	if err := slot.writeGrain(sliceBytes, larger); err != nil {
		t.Fatalf("larger grain write after ring wrap: %v (capacity ratcheted to previous payload size)", err)
	}
	if slot.grainSize != 4000 {
		t.Fatalf("committed grainSize = %d, want 4000", slot.grainSize)
	}

	// 4. Repeated wraps with shrinking-then-growing payloads keep working.
	for _, n := range []int{1, 4096, 254, 4096} {
		if err := slot.writeGrain(sliceBytes, make([]byte, n)); err != nil {
			t.Fatalf("write of %d bytes after wraps: %v", n, err)
		}
	}

	// 5. Oversized payloads are still rejected before the memcpy.
	if err := slot.writeGrain(sliceBytes, make([]byte, 4097)); err == nil {
		t.Fatal("oversized grain (4097 > 4096 capacity) must be rejected")
	}
}

// TestDiscreteGrainCapacity covers the capacity derivation for the supported
// discrete flow shapes and the fallback path.
func TestDiscreteGrainCapacity(t *testing.T) {
	tests := []struct {
		name            string
		sliceBytes      uint64
		totalSlices     uint16
		headerGrainSize uint32
		want            uint64
	}{
		// data flow (video/smpte291): 1-byte slices x 4096.
		{"data flow", 1, 4096, 4096, 4096},
		// data flow after a 16-byte payload was committed: header grainSize
		// is stale but capacity must not change.
		{"data flow with stale header", 1, 4096, 16, 4096},
		// 1080p video/v210: 5120-byte lines x 1080 = 5_529_600.
		{"v210 1080p", 5120, 1080, 5_529_600, 5_529_600},
		// v210a: fill plane line + key (alpha) plane line per slice, summed
		// across planes by OpenWriter.
		{"v210a sums planes", 5120 + 2560, 1080, 8_294_400, 8_294_400},
		// Unknown config (sliceBytes 0): fall back to the header grainSize so
		// the bounds check never becomes weaker than the pre-fix behaviour.
		{"fallback to header grainSize", 0, 4096, 1234, 1234},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := discreteGrainCapacity(tt.sliceBytes, tt.totalSlices, tt.headerGrainSize)
			if got != tt.want {
				t.Fatalf("discreteGrainCapacity(%d, %d, %d) = %d, want %d",
					tt.sliceBytes, tt.totalSlices, tt.headerGrainSize, got, tt.want)
			}
		})
	}
}

// TestFillDataGrain pins the data grain layout: committed at the full slot
// size, the frame at the start and the rest zero. Committed at the frame
// length, a reader on another node through a flow mirror receives the
// header's size with only part of the payload, and a strict RFC 8331 parser
// fails on the missing packet bytes. The slot here held a longer frame
// before, so a tail left uncleared would show its bytes.
func TestFillDataGrain(t *testing.T) {
	const capacity = 4096 // video/smpte291: 1-byte slices x 4096
	slot := bytes.Repeat([]byte{0xAA}, capacity)

	// An RFC 8331 frame with one packet's worth of bytes after the header.
	frame := []byte{0x00, 0x20, 0x01, 0x00, 0x00, 0x00}
	frame = append(frame, bytes.Repeat([]byte{0x55}, 32)...)

	if got := fillDataGrain(slot, frame); got != capacity {
		t.Fatalf("committed grain size = %d, want the slot capacity %d", got, capacity)
	}
	if !bytes.Equal(slot[:len(frame)], frame) {
		t.Fatalf("slot starts % x, want the frame % x", slot[:len(frame)], frame)
	}
	for i, b := range slot[len(frame):] {
		if b != 0 {
			t.Fatalf("byte %d past the frame = %#x, want 0", len(frame)+i, b)
		}
	}

	// The empty frame written for most pictures takes the whole slot too.
	empty := []byte{0, 0, 0, 0, 0, 0}
	if got := fillDataGrain(slot, empty); got != capacity {
		t.Fatalf("empty frame committed at %d bytes, want %d", got, capacity)
	}
	if !bytes.Equal(slot, make([]byte, capacity)) {
		t.Fatal("slot holds bytes of the previous frame after an empty frame")
	}
}
