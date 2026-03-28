package graphics

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"
)

// TestKeyMaskBlend10Row_DirectKernel tests the SIMD kernel against Go reference.
func TestKeyMaskBlend10Row_DirectKernel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))

	for _, count := range []int{1, 3, 4, 7, 8, 9, 15, 16, 17, 32, 64, 100, 960} {
		for _, maskVal := range []byte{0, 1, 85, 128, 200, 255} {
			t.Run(fmt.Sprintf("count=%d/mask=%d", count, maskVal), func(t *testing.T) {
				bg := make([]byte, count*2)
				fill := make([]byte, count*2)
				mask := make([]byte, count)
				for i := 0; i < count; i++ {
					binary.LittleEndian.PutUint16(bg[i*2:], uint16(rng.Intn(1024)))
					binary.LittleEndian.PutUint16(fill[i*2:], uint16(rng.Intn(1024)))
					mask[i] = maskVal
				}

				// Go reference
				refBg := make([]byte, count*2)
				copy(refBg, bg)
				keyMaskBlend10RowGo(&refBg[0], &fill[0], &mask[0], count)

				// SIMD kernel
				gotBg := make([]byte, count*2)
				copy(gotBg, bg)
				keyMaskBlend10Row(&gotBg[0], &fill[0], &mask[0], count)

				for i := 0; i < count; i++ {
					rv := binary.LittleEndian.Uint16(refBg[i*2:])
					gv := binary.LittleEndian.Uint16(gotBg[i*2:])
					if rv != gv {
						bgVal := binary.LittleEndian.Uint16(bg[i*2:])
						fillVal := binary.LittleEndian.Uint16(fill[i*2:])
						t.Fatalf("sample %d: ref=%d, got=%d (bg=%d, fill=%d, mask=%d)",
							i, rv, gv, bgVal, fillVal, mask[i])
					}
				}
			})
		}
	}
}

// TestKeyMaskBlend10Row_RandomMasks tests with random per-pixel masks.
func TestKeyMaskBlend10Row_RandomMasks(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(99))
	count := 256

	bg := make([]byte, count*2)
	fill := make([]byte, count*2)
	mask := make([]byte, count)
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(bg[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(fill[i*2:], uint16(rng.Intn(1024)))
		mask[i] = byte(rng.Intn(256))
	}

	refBg := make([]byte, count*2)
	copy(refBg, bg)
	keyMaskBlend10RowGo(&refBg[0], &fill[0], &mask[0], count)

	gotBg := make([]byte, count*2)
	copy(gotBg, bg)
	keyMaskBlend10Row(&gotBg[0], &fill[0], &mask[0], count)

	for i := 0; i < count; i++ {
		rv := binary.LittleEndian.Uint16(refBg[i*2:])
		gv := binary.LittleEndian.Uint16(gotBg[i*2:])
		if rv != gv {
			t.Fatalf("sample %d: ref=%d, got=%d (mask=%d)", i, rv, gv, mask[i])
		}
	}
}

// TestKeyMaskBlend10Row_Transparent verifies transparent mask doesn't modify bg.
func TestKeyMaskBlend10Row_Transparent(t *testing.T) {
	t.Parallel()
	count := 32
	bg := make([]byte, count*2)
	fill := make([]byte, count*2)
	mask := make([]byte, count)
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(bg[i*2:], 500)
		binary.LittleEndian.PutUint16(fill[i*2:], 800)
	}

	origBg := make([]byte, count*2)
	copy(origBg, bg)

	keyMaskBlend10Row(&bg[0], &fill[0], &mask[0], count)

	for i := 0; i < count; i++ {
		ov := binary.LittleEndian.Uint16(origBg[i*2:])
		gv := binary.LittleEndian.Uint16(bg[i*2:])
		if ov != gv {
			t.Fatalf("transparent: sample %d changed from %d to %d", i, ov, gv)
		}
	}
}

// TestKeyMaskBlend10Row_FullOpaque verifies full opaque mask replaces bg with fill.
func TestKeyMaskBlend10Row_FullOpaque(t *testing.T) {
	t.Parallel()
	count := 32
	bg := make([]byte, count*2)
	fill := make([]byte, count*2)
	mask := make([]byte, count)
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(bg[i*2:], 100)
		binary.LittleEndian.PutUint16(fill[i*2:], 800)
		mask[i] = 255
	}

	keyMaskBlend10Row(&bg[0], &fill[0], &mask[0], count)

	// mask=255 → w=256, inv=0 → result = (100*0 + 800*256 + 128) >> 8 = 800
	for i := 0; i < count; i++ {
		gv := binary.LittleEndian.Uint16(bg[i*2:])
		if gv != 800 {
			t.Fatalf("full opaque: sample %d: expected 800, got %d", i, gv)
		}
	}
}

// TestKeyMaskBlend10Row_MaxValues tests no overflow at maximum values.
func TestKeyMaskBlend10Row_MaxValues(t *testing.T) {
	t.Parallel()
	count := 16
	bg := make([]byte, count*2)
	fill := make([]byte, count*2)
	mask := make([]byte, count)
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(bg[i*2:], 1023)
		binary.LittleEndian.PutUint16(fill[i*2:], 1023)
		mask[i] = 255
	}

	refBg := make([]byte, count*2)
	copy(refBg, bg)
	keyMaskBlend10RowGo(&refBg[0], &fill[0], &mask[0], count)

	gotBg := make([]byte, count*2)
	copy(gotBg, bg)
	keyMaskBlend10Row(&gotBg[0], &fill[0], &mask[0], count)

	for i := 0; i < count; i++ {
		rv := binary.LittleEndian.Uint16(refBg[i*2:])
		gv := binary.LittleEndian.Uint16(gotBg[i*2:])
		if rv != gv {
			t.Fatalf("max values: sample %d: ref=%d, got=%d", i, rv, gv)
		}
		if gv > 1023 {
			t.Fatalf("max values: sample %d exceeds 10-bit: %d", i, gv)
		}
	}
}

func BenchmarkKeyMaskBlend10Row_Go_1080p(b *testing.B) {
	count := 1920 * 1080
	bg := make([]byte, count*2)
	fill := make([]byte, count*2)
	mask := make([]byte, count)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(bg[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(fill[i*2:], uint16(rng.Intn(1024)))
		mask[i] = byte(rng.Intn(256))
	}
	bgCopy := make([]byte, count*2)

	b.SetBytes(int64(count * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(bgCopy, bg)
		keyMaskBlend10RowGo(&bgCopy[0], &fill[0], &mask[0], count)
	}
}

func BenchmarkKeyMaskBlend10Row_SIMD_1080p(b *testing.B) {
	count := 1920 * 1080
	bg := make([]byte, count*2)
	fill := make([]byte, count*2)
	mask := make([]byte, count)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(bg[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(fill[i*2:], uint16(rng.Intn(1024)))
		mask[i] = byte(rng.Intn(256))
	}
	bgCopy := make([]byte, count*2)

	b.SetBytes(int64(count * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(bgCopy, bg)
		keyMaskBlend10Row(&bgCopy[0], &fill[0], &mask[0], count)
	}
}
