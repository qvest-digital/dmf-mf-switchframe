package video

import "testing"

func TestFrameSize_1080p(t *testing.T) {
	// 1920x1080 YUV420 8-bit: 1920*1080*3/2 = 3,110,400
	if got := YUV420_8bit.FrameSize(1920, 1080); got != 3110400 {
		t.Errorf("YUV420_8bit.FrameSize(1920,1080) = %d, want 3110400", got)
	}

	// 1920x1080 YUV422 10-bit: 1920*1080*4 = 8,294,400
	// Y: 1920*1080*2 = 4,147,200  Cb: 960*1080*2 = 2,073,600  Cr: same
	if got := YUV422_10bit.FrameSize(1920, 1080); got != 8294400 {
		t.Errorf("YUV422_10bit.FrameSize(1920,1080) = %d, want 8294400", got)
	}
}

func TestYUV420FrameSize(t *testing.T) {
	got := YUV420FrameSize(1920, 1080)
	want := 3110400
	if got != want {
		t.Errorf("YUV420FrameSize(1920,1080) = %d, want %d", got, want)
	}

	// Verify it matches the method version.
	if got != YUV420_8bit.FrameSize(1920, 1080) {
		t.Error("YUV420FrameSize disagrees with YUV420_8bit.FrameSize")
	}
}

func TestBytesPerSample(t *testing.T) {
	if got := YUV420_8bit.BytesPerSample(); got != 1 {
		t.Errorf("YUV420_8bit.BytesPerSample() = %d, want 1", got)
	}
	if got := YUV422_10bit.BytesPerSample(); got != 2 {
		t.Errorf("YUV422_10bit.BytesPerSample() = %d, want 2", got)
	}
}

func TestChromaDimensions(t *testing.T) {
	// 420: half width, half height
	if got := YUV420_8bit.ChromaWidth(1920); got != 960 {
		t.Errorf("YUV420_8bit.ChromaWidth(1920) = %d, want 960", got)
	}
	if got := YUV420_8bit.ChromaHeight(1080); got != 540 {
		t.Errorf("YUV420_8bit.ChromaHeight(1080) = %d, want 540", got)
	}

	// 422: half width, full height
	if got := YUV422_10bit.ChromaWidth(1920); got != 960 {
		t.Errorf("YUV422_10bit.ChromaWidth(1920) = %d, want 960", got)
	}
	if got := YUV422_10bit.ChromaHeight(1080); got != 1080 {
		t.Errorf("YUV422_10bit.ChromaHeight(1080) = %d, want 1080", got)
	}
}

func TestPlaneSizes_1080p(t *testing.T) {
	// YUV420 8-bit @ 1080p
	// Y: 1920*1080*1 = 2,073,600
	if got := YUV420_8bit.YPlaneSize(1920, 1080); got != 2073600 {
		t.Errorf("YUV420_8bit.YPlaneSize = %d, want 2073600", got)
	}
	// Cb/Cr: 960*540*1 = 518,400
	if got := YUV420_8bit.ChromaPlaneSize(1920, 1080); got != 518400 {
		t.Errorf("YUV420_8bit.ChromaPlaneSize = %d, want 518400", got)
	}

	// YUV422 10-bit @ 1080p
	// Y: 1920*1080*2 = 4,147,200
	if got := YUV422_10bit.YPlaneSize(1920, 1080); got != 4147200 {
		t.Errorf("YUV422_10bit.YPlaneSize = %d, want 4147200", got)
	}
	// Cb/Cr: 960*1080*2 = 2,073,600
	if got := YUV422_10bit.ChromaPlaneSize(1920, 1080); got != 2073600 {
		t.Errorf("YUV422_10bit.ChromaPlaneSize = %d, want 2073600", got)
	}
}

func TestPlaneOffsets(t *testing.T) {
	// YUV420 8-bit @ 1080p
	// CbOffset = Y plane size = 2,073,600
	if got := YUV420_8bit.CbOffset(1920, 1080); got != 2073600 {
		t.Errorf("YUV420_8bit.CbOffset = %d, want 2073600", got)
	}
	// CrOffset = Y + Cb = 2,073,600 + 518,400 = 2,592,000
	if got := YUV420_8bit.CrOffset(1920, 1080); got != 2592000 {
		t.Errorf("YUV420_8bit.CrOffset = %d, want 2592000", got)
	}

	// YUV422 10-bit @ 1080p
	// CbOffset = 4,147,200
	if got := YUV422_10bit.CbOffset(1920, 1080); got != 4147200 {
		t.Errorf("YUV422_10bit.CbOffset = %d, want 4147200", got)
	}
	// CrOffset = 4,147,200 + 2,073,600 = 6,220,800
	if got := YUV422_10bit.CrOffset(1920, 1080); got != 6220800 {
		t.Errorf("YUV422_10bit.CrOffset = %d, want 6220800", got)
	}
}

func TestBT709Constants(t *testing.T) {
	// 8-bit
	if got := YUV420_8bit.BlackY(); got != 16 {
		t.Errorf("YUV420_8bit.BlackY() = %d, want 16", got)
	}
	if got := YUV420_8bit.WhiteY(); got != 235 {
		t.Errorf("YUV420_8bit.WhiteY() = %d, want 235", got)
	}
	if got := YUV420_8bit.NeutralChroma(); got != 128 {
		t.Errorf("YUV420_8bit.NeutralChroma() = %d, want 128", got)
	}
	if got := YUV420_8bit.MaxSampleValue(); got != 255 {
		t.Errorf("YUV420_8bit.MaxSampleValue() = %d, want 255", got)
	}

	// 10-bit
	if got := YUV422_10bit.BlackY(); got != 64 {
		t.Errorf("YUV422_10bit.BlackY() = %d, want 64", got)
	}
	if got := YUV422_10bit.WhiteY(); got != 940 {
		t.Errorf("YUV422_10bit.WhiteY() = %d, want 940", got)
	}
	if got := YUV422_10bit.NeutralChroma(); got != 512 {
		t.Errorf("YUV422_10bit.NeutralChroma() = %d, want 512", got)
	}
	if got := YUV422_10bit.MaxSampleValue(); got != 1023 {
		t.Errorf("YUV422_10bit.MaxSampleValue() = %d, want 1023", got)
	}
}

func TestString(t *testing.T) {
	if got := YUV420_8bit.String(); got != "yuv420p" {
		t.Errorf("YUV420_8bit.String() = %q, want %q", got, "yuv420p")
	}
	if got := YUV422_10bit.String(); got != "yuv422p10le" {
		t.Errorf("YUV422_10bit.String() = %q, want %q", got, "yuv422p10le")
	}
	// Unknown format
	unknown := Format(99)
	if got := unknown.String(); got != "unknown(99)" {
		t.Errorf("Format(99).String() = %q, want %q", got, "unknown(99)")
	}
}

func TestPlaneOffsetsConsistency(t *testing.T) {
	// For any format, CbOffset + ChromaPlaneSize == CrOffset
	for _, f := range []Format{YUV420_8bit, YUV422_10bit} {
		cb := f.CbOffset(1920, 1080)
		cr := f.CrOffset(1920, 1080)
		cs := f.ChromaPlaneSize(1920, 1080)
		if cb+cs != cr {
			t.Errorf("%s: CbOffset(%d) + ChromaPlaneSize(%d) != CrOffset(%d)", f, cb, cs, cr)
		}
	}

	// CrOffset + ChromaPlaneSize == FrameSize
	for _, f := range []Format{YUV420_8bit, YUV422_10bit} {
		cr := f.CrOffset(1920, 1080)
		cs := f.ChromaPlaneSize(1920, 1080)
		fs := f.FrameSize(1920, 1080)
		if cr+cs != fs {
			t.Errorf("%s: CrOffset(%d) + ChromaPlaneSize(%d) != FrameSize(%d)", f, cr, cs, fs)
		}
	}
}
