package gpu

import "testing"

func TestGPUFrameFormat_NV12_Size(t *testing.T) {
	f := &GPUFrame{Width: 1920, Height: 1080, Pitch: 1920, Format: GPUFormatNV12}
	// NV12: pitch * h * 3/2
	want := 1920 * 1080 * 3 / 2
	if got := f.FrameSize(); got != want {
		t.Errorf("NV12 size: got %d, want %d", got, want)
	}
}

func TestGPUFrameFormat_P210_Size(t *testing.T) {
	// P210: pitch = width*2 (uint16 per sample), total = pitch * height * 2
	f := &GPUFrame{Width: 1920, Height: 1080, Pitch: 3840, Format: GPUFormatP210}
	want := 3840 * 1080 * 2 // Y plane + UV plane (both full height for 4:2:2)
	if got := f.FrameSize(); got != want {
		t.Errorf("P210 size: got %d, want %d", got, want)
	}
}

func TestGPUFrameFormat_UVPlaneOffset(t *testing.T) {
	// UV offset is pitch * height for both formats
	nv12 := &GPUFrame{Width: 1920, Height: 1080, Pitch: 1920, Format: GPUFormatNV12}
	if got := nv12.UVPlaneOffset(); got != 1920*1080 {
		t.Errorf("NV12 UV offset: got %d, want %d", got, 1920*1080)
	}

	p210 := &GPUFrame{Width: 1920, Height: 1080, Pitch: 3840, Format: GPUFormatP210}
	if got := p210.UVPlaneOffset(); got != 3840*1080 {
		t.Errorf("P210 UV offset: got %d, want %d", got, 3840*1080)
	}
}

func TestGPUFrameFormat_DefaultIsNV12(t *testing.T) {
	f := &GPUFrame{}
	if f.Format != GPUFormatNV12 {
		t.Error("default format should be NV12 (zero value)")
	}
}

func TestGPUFrameFormat_String(t *testing.T) {
	if GPUFormatNV12.String() != "NV12" {
		t.Errorf("NV12 string: got %q, want %q", GPUFormatNV12.String(), "NV12")
	}
	if GPUFormatP210.String() != "P210" {
		t.Errorf("P210 string: got %q, want %q", GPUFormatP210.String(), "P210")
	}
}

func TestGPUFrameFormat_UnknownString(t *testing.T) {
	unknown := GPUFrameFormat(99)
	if got := unknown.String(); got != "Unknown(99)" {
		t.Errorf("unknown format string: got %q, want %q", got, "Unknown(99)")
	}
}

func TestGPUFrameFormat_P210_UVPlaneHeight(t *testing.T) {
	// P210 (4:2:2): UV plane is full height
	f := &GPUFrame{Width: 1920, Height: 1080, Pitch: 3840, Format: GPUFormatP210}
	if got := f.UVPlaneHeight(); got != 1080 {
		t.Errorf("P210 UV height: got %d, want 1080", got)
	}

	// NV12 (4:2:0): UV plane is half height
	nv12 := &GPUFrame{Width: 1920, Height: 1080, Pitch: 1920, Format: GPUFormatNV12}
	if got := nv12.UVPlaneHeight(); got != 540 {
		t.Errorf("NV12 UV height: got %d, want 540", got)
	}
}

func TestGPUFrameFormat_NV12Size_BackwardsCompat(t *testing.T) {
	// NV12Size should still work and return the same value as FrameSize for NV12
	f := &GPUFrame{Width: 1920, Height: 1080, Pitch: 2048, Format: GPUFormatNV12}
	if f.NV12Size() != f.FrameSize() {
		t.Errorf("NV12Size() = %d, FrameSize() = %d — should match for NV12 format",
			f.NV12Size(), f.FrameSize())
	}
}

func TestGPUFrameFormat_AlignedPitch(t *testing.T) {
	// Test with 256-byte aligned pitch (typical GPU alignment)
	// NV12: 1920 width → pitch 2048 (256-aligned)
	nv12 := &GPUFrame{Width: 1920, Height: 1080, Pitch: 2048, Format: GPUFormatNV12}
	wantNV12 := 2048 * 1080 * 3 / 2
	if got := nv12.FrameSize(); got != wantNV12 {
		t.Errorf("aligned NV12 size: got %d, want %d", got, wantNV12)
	}

	// P210: 1920 width → pitch 3840 bytes (1920 * 2 for uint16), 256-aligned → 3840 (already aligned)
	// Actually 3840 / 256 = 15, so already aligned. Use 4096 for a misaligned example.
	p210 := &GPUFrame{Width: 1920, Height: 1080, Pitch: 3840, Format: GPUFormatP210}
	wantP210 := 3840 * 1080 * 2
	if got := p210.FrameSize(); got != wantP210 {
		t.Errorf("aligned P210 size: got %d, want %d", got, wantP210)
	}
}

func TestGPUFrameFormat_SmallResolution(t *testing.T) {
	// Test with small resolution (preview/thumbnail)
	nv12 := &GPUFrame{Width: 320, Height: 240, Pitch: 512, Format: GPUFormatNV12}
	if got := nv12.FrameSize(); got != 512*240*3/2 {
		t.Errorf("small NV12: got %d, want %d", got, 512*240*3/2)
	}
	if got := nv12.UVPlaneHeight(); got != 120 {
		t.Errorf("small NV12 UV height: got %d, want 120", got)
	}

	p210 := &GPUFrame{Width: 320, Height: 240, Pitch: 640, Format: GPUFormatP210}
	if got := p210.FrameSize(); got != 640*240*2 {
		t.Errorf("small P210: got %d, want %d", got, 640*240*2)
	}
	if got := p210.UVPlaneHeight(); got != 240 {
		t.Errorf("small P210 UV height: got %d, want 240", got)
	}
}
