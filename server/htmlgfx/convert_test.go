package htmlgfx

import (
	"testing"
)

func TestBGRAToRGBA(t *testing.T) {
	tests := []struct {
		name string
		src  []byte
		want []byte
	}{
		{
			name: "opaque red",
			src:  []byte{0, 0, 255, 255}, // BGRA: B=0, G=0, R=255, A=255
			want: []byte{255, 0, 0, 255}, // RGBA: R=255, G=0, B=0, A=255
		},
		{
			name: "fully transparent",
			src:  []byte{0, 0, 0, 0},
			want: []byte{0, 0, 0, 0},
		},
		{
			name: "50% white premultiplied",
			src:  []byte{128, 128, 128, 128}, // premul BGRA
			want: []byte{255, 255, 255, 128}, // straight RGBA
		},
		{
			name: "25% green premultiplied",
			src:  []byte{0, 64, 0, 64},  // premul BGRA: B=0, G=64, R=0, A=64
			want: []byte{0, 255, 0, 64}, // straight RGBA: R=0, G=255, B=0, A=64
		},
		{
			name: "opaque blue",
			src:  []byte{255, 0, 0, 255}, // BGRA: B=255, G=0, R=0, A=255
			want: []byte{0, 0, 255, 255}, // RGBA: R=0, G=0, B=255, A=255
		},
		{
			name: "multiple pixels in sequence",
			src: []byte{
				0, 0, 255, 255, // opaque red (BGRA)
				0, 0, 0, 0, // transparent
				128, 128, 128, 128, // 50% white premul
			},
			want: []byte{
				255, 0, 0, 255, // opaque red (RGBA)
				0, 0, 0, 0, // transparent
				255, 255, 255, 128, // 50% white straight
			},
		},
		{
			name: "semi-transparent color",
			// premul BGRA: B=32, G=64, R=96, A=128
			// straight: R = min((96*255 + 64) / 128, 255) = min(191, 255) = 191
			//           G = min((64*255 + 64) / 128, 255) = min(128, 255) = 128
			//           B = min((32*255 + 64) / 128, 255) = min(64, 255) = 64
			src:  []byte{32, 64, 96, 128},
			want: []byte{191, 128, 64, 128},
		},
		{
			name: "nearly transparent",
			// premul BGRA: B=1, G=1, R=1, A=1
			// straight: each = min((1*255 + 0) / 1, 255) = 255
			src:  []byte{1, 1, 1, 1},
			want: []byte{255, 255, 255, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := make([]byte, len(tt.src))
			bgraToRGBA(dst, tt.src)

			if len(dst) != len(tt.want) {
				t.Fatalf("length mismatch: got %d, want %d", len(dst), len(tt.want))
			}

			for i, b := range dst {
				if b != tt.want[i] {
					t.Errorf("byte[%d] = %d, want %d (pixel %d, component %d)",
						i, b, tt.want[i], i/4, i%4)
				}
			}
		})
	}
}

func TestBGRAToRGBAInPlace(t *testing.T) {
	// In-place conversion: dst and src are the same slice.
	buf := []byte{
		0, 0, 255, 255, // opaque red (BGRA)
		128, 128, 128, 128, // 50% white premul (BGRA)
		0, 0, 0, 0, // transparent
	}
	want := []byte{
		255, 0, 0, 255, // opaque red (RGBA)
		255, 255, 255, 128, // 50% white straight (RGBA)
		0, 0, 0, 0, // transparent
	}

	bgraToRGBA(buf, buf) // dst == src

	for i, b := range buf {
		if b != want[i] {
			t.Errorf("byte[%d] = %d, want %d (pixel %d, component %d)",
				i, b, want[i], i/4, i%4)
		}
	}
}

func BenchmarkBGRAToRGBA1080p(b *testing.B) {
	const width, height = 1920, 1080
	size := width * height * 4
	src := make([]byte, size)
	dst := make([]byte, size)

	// Fill with semi-transparent pixels to exercise the un-premultiply path.
	for i := 0; i < size; i += 4 {
		src[i] = 100   // B
		src[i+1] = 150 // G
		src[i+2] = 200 // R
		src[i+3] = 200 // A
	}

	b.SetBytes(int64(size))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		bgraToRGBA(dst, src)
	}
}
