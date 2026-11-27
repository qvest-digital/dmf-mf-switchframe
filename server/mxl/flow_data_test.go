//go:build cgo && mxl

package mxl

import (
	"bytes"
	"os"
	"testing"
)

const testDataFlowID = "6f1c3a52-7d4e-4b8a-9c21-3e5f6a7b8c9d"

const testDataFlowDef = `{
  "id": "` + testDataFlowID + `",
  "source_id": "0e635152-e501-4d4e-bb87-9f3fe05eb79a",
  "device_id": "9126cc2f-4c26-4c9b-a6cd-93c4381c9be5",
  "version": "1453880607:123995943",
  "label": "data grain test",
  "description": "data grain test",
  "tags": {"urn:x-nmos:tag:grouphint/v1.0": ["data grain test:Ancillary Data"]},
  "parents": [],
  "format": "urn:x-nmos:format:data",
  "media_type": "video/smpte291",
  "grain_rate": {"numerator": 50, "denominator": 1}
}`

// TestDataGrainCommittedAtSlotSize writes a long data frame, then a short
// one into the same slot a ring revolution later, and reads it back through
// libmxl: the grain is committed at the slot capacity, the short frame at
// its start and the rest zero, with nothing of the longer frame left. A
// grain committed at the frame length reaches a reader on another node
// through a flow mirror with the header's size but only part of the payload.
func TestDataGrainCommittedAtSlotSize(t *testing.T) {
	domain, err := os.MkdirTemp("/dev/shm", "switchframe-mxl-data-*")
	if err != nil {
		t.Skipf("no /dev/shm: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(domain) })

	inst, err := NewInstance(domain)
	if err != nil {
		t.Fatalf("NewInstance: %v", err)
	}
	defer inst.Close()

	w, err := inst.OpenWriter(testDataFlowDef)
	if err != nil {
		t.Fatalf("OpenWriter: %v", err)
	}
	defer w.Close()

	long := append([]byte{0x00, 0x40, 0x02, 0x00, 0x00, 0x00}, bytes.Repeat([]byte{0xAA}, 64)...)
	short := append([]byte{0x00, 0x20, 0x01, 0x00, 0x00, 0x00}, bytes.Repeat([]byte{0x55}, 32)...)
	const first = 1000
	if err := w.WriteGrain(first, long); err != nil {
		t.Fatalf("write long frame: %v", err)
	}

	r, err := inst.OpenReader(testDataFlowID)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()
	cfg := r.ConfigInfo()

	// One ring revolution later the short frame lands in the same slot.
	index := first + uint64(cfg.GrainCount)
	if err := w.WriteGrain(index, short); err != nil {
		t.Fatalf("write short frame: %v", err)
	}

	data, info, err := r.ReadGrain(index, 0)
	if err != nil {
		t.Fatalf("ReadGrain: %v", err)
	}
	if info.Invalid || info.ValidSlices != info.TotalSlices {
		t.Fatalf("grain invalid=%v valid slices %d of %d, want a complete grain",
			info.Invalid, info.ValidSlices, info.TotalSlices)
	}
	capacity := cfg.SliceSizes[0] * uint32(info.TotalSlices)
	if info.GrainSize != capacity || uint32(len(data)) != capacity {
		t.Fatalf("grain size %d, %d bytes read, want the slot capacity %d",
			info.GrainSize, len(data), capacity)
	}
	if !bytes.Equal(data[:len(short)], short) {
		t.Fatalf("grain starts % x, want the short frame % x", data[:len(short)], short)
	}
	for i, b := range data[len(short):] {
		if b != 0 {
			t.Fatalf("byte %d past the frame = %#x, want 0", len(short)+i, b)
		}
	}
}
