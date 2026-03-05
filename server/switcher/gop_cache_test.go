package switcher

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// makeAVC1Frame creates a minimal AVC1-formatted frame (4-byte length prefix + NALU data).
func makeAVC1Frame(naluData []byte) []byte {
	buf := make([]byte, 4+len(naluData))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(naluData)))
	copy(buf[4:], naluData)
	return buf
}

func TestGOPCacheKeyframeResets(t *testing.T) {
	gc := newGOPCache()

	sps := []byte{0x67, 0x42, 0x00, 0x0a}
	pps := []byte{0x68, 0xce, 0x38, 0x80}

	// Record keyframe
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x65, 0xAA, 0xBB}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	})

	// Record two deltas
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x41, 0x01}),
		IsKeyframe: false,
	})
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x41, 0x02}),
		IsKeyframe: false,
	})

	gop := gc.GetGOP("cam1")
	require.Equal(t, 3, len(gop), "should have keyframe + 2 deltas")
	require.True(t, gop[0].isKeyframe)
	require.False(t, gop[1].isKeyframe)
	require.False(t, gop[2].isKeyframe)

	// New keyframe should reset the cache
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x65, 0xCC}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	})

	gop = gc.GetGOP("cam1")
	require.Equal(t, 1, len(gop), "keyframe should reset cache")
	require.True(t, gop[0].isKeyframe)
}

func TestGOPCacheDeepCopy(t *testing.T) {
	gc := newGOPCache()

	original := makeAVC1Frame([]byte{0x65, 0xAA, 0xBB})
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   original,
		IsKeyframe: true,
		SPS:        []byte{0x67},
		PPS:        []byte{0x68},
	})

	gop := gc.GetGOP("cam1")
	require.Equal(t, 1, len(gop))

	// Mutate the original — should not affect cache
	original[4] = 0xFF

	gop2 := gc.GetGOP("cam1")
	require.Equal(t, gop[0].annexB, gop2[0].annexB, "cache should be independent of original")

	// Mutate the returned copy — should not affect cache
	gop[0].annexB[0] = 0xFF
	gop3 := gc.GetGOP("cam1")
	require.NotEqual(t, byte(0xFF), gop3[0].annexB[0], "GetGOP should return independent copies")
}

func TestGOPCacheSPSPPSPrepended(t *testing.T) {
	gc := newGOPCache()

	sps := []byte{0x67, 0x42, 0x00, 0x0a}
	pps := []byte{0x68, 0xce, 0x38, 0x80}

	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x65, 0xAA}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	})

	gop := gc.GetGOP("cam1")
	require.Equal(t, 1, len(gop))

	annexB := gop[0].annexB
	// Should start with SPS: 00 00 00 01 + SPS data
	require.Equal(t, []byte{0x00, 0x00, 0x00, 0x01}, annexB[:4])
	require.Equal(t, sps, annexB[4:4+len(sps)])
	// Then PPS: 00 00 00 01 + PPS data
	offset := 4 + len(sps)
	require.Equal(t, []byte{0x00, 0x00, 0x00, 0x01}, annexB[offset:offset+4])
	require.Equal(t, pps, annexB[offset+4:offset+4+len(pps)])
}

func TestGOPCacheConcurrentAccess(t *testing.T) {
	gc := newGOPCache()

	var wg sync.WaitGroup
	wg.Add(3)

	// Writer 1
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			gc.RecordFrame("cam1", &media.VideoFrame{
				WireData:   makeAVC1Frame([]byte{0x65, byte(i)}),
				IsKeyframe: i%10 == 0,
				SPS:        []byte{0x67},
				PPS:        []byte{0x68},
			})
		}
	}()

	// Writer 2
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			gc.RecordFrame("cam2", &media.VideoFrame{
				WireData:   makeAVC1Frame([]byte{0x65, byte(i)}),
				IsKeyframe: i%10 == 0,
				SPS:        []byte{0x67},
				PPS:        []byte{0x68},
			})
		}
	}()

	// Reader
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			gc.GetGOP("cam1")
			gc.GetGOP("cam2")
		}
	}()

	wg.Wait()
}

func TestGOPCacheRemoveSource(t *testing.T) {
	gc := newGOPCache()

	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x65, 0xAA}),
		IsKeyframe: true,
		SPS:        []byte{0x67},
		PPS:        []byte{0x68},
	})

	require.NotNil(t, gc.GetGOP("cam1"))

	gc.RemoveSource("cam1")
	require.Nil(t, gc.GetGOP("cam1"))
}

func TestGOPCacheEmptySource(t *testing.T) {
	gc := newGOPCache()
	require.Nil(t, gc.GetGOP("nonexistent"))
}

func TestGetOriginalGOPReturnsFrames(t *testing.T) {
	gc := newGOPCache()

	sps := []byte{0x67, 0x42, 0x00, 0x0a}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	wireData := makeAVC1Frame([]byte{0x65, 0xAA, 0xBB})

	gc.RecordFrame("cam1", &media.VideoFrame{
		PTS:        1000,
		WireData:   wireData,
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
		Codec:      "h264",
		GroupID:     5,
	})
	gc.RecordFrame("cam1", &media.VideoFrame{
		PTS:        1033,
		WireData:   makeAVC1Frame([]byte{0x41, 0x01}),
		IsKeyframe: false,
		Codec:      "h264",
		GroupID:     5,
	})

	frames := gc.GetOriginalGOP("cam1")
	require.Equal(t, 2, len(frames))

	// Keyframe should have all fields preserved
	require.True(t, frames[0].IsKeyframe)
	require.Equal(t, int64(1000), frames[0].PTS)
	require.Equal(t, "h264", frames[0].Codec)
	require.Equal(t, uint32(5), frames[0].GroupID)
	require.Equal(t, wireData, frames[0].WireData)
	require.Equal(t, sps, frames[0].SPS)
	require.Equal(t, pps, frames[0].PPS)

	// Delta should have basic fields
	require.False(t, frames[1].IsKeyframe)
	require.Equal(t, int64(1033), frames[1].PTS)
}

func TestGetOriginalGOPDeepCopy(t *testing.T) {
	gc := newGOPCache()

	wireData := makeAVC1Frame([]byte{0x65, 0xAA})
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   wireData,
		IsKeyframe: true,
		SPS:        []byte{0x67},
		PPS:        []byte{0x68},
	})

	frames1 := gc.GetOriginalGOP("cam1")
	require.Equal(t, 1, len(frames1))

	// Mutate returned frame — should not affect cache
	frames1[0].WireData[0] = 0xFF

	frames2 := gc.GetOriginalGOP("cam1")
	require.NotEqual(t, byte(0xFF), frames2[0].WireData[0],
		"GetOriginalGOP should return independent copies")
}

func TestGetOriginalGOPEmpty(t *testing.T) {
	gc := newGOPCache()
	require.Nil(t, gc.GetOriginalGOP("nonexistent"))
}

func TestGOPCacheMaxFrames(t *testing.T) {
	// Use a small maxFrames to test trimming behavior with no keyframe
	gc := newGOPCacheWithMax(10)

	// Feed 15 delta frames (no keyframe at all)
	for i := 0; i < 15; i++ {
		gc.RecordFrame("cam1", &media.VideoFrame{
			PTS:        int64(i * 33000),
			WireData:   makeAVC1Frame([]byte{0x41, byte(i)}),
			IsKeyframe: false,
		})
	}

	gop := gc.GetGOP("cam1")
	require.LessOrEqual(t, len(gop), 10, "cache should be trimmed to maxFrames")
	require.Equal(t, 10, len(gop), "cache should keep exactly maxFrames when no keyframe")

	// Verify we kept the most recent frames (last 10 of 15)
	// The first frame in the trimmed cache should have PTS corresponding to frame index 5
	origGOP := gc.GetOriginalGOP("cam1")
	require.Equal(t, int64(5*33000), origGOP[0].PTS, "should have dropped oldest frames")
	require.Equal(t, int64(14*33000), origGOP[9].PTS, "last frame should be most recent")
}

func TestGOPCacheMaxFramesKeepsKeyframe(t *testing.T) {
	// Use a small maxFrames to test that keyframes are retained
	gc := newGOPCacheWithMax(10)

	sps := []byte{0x67, 0x42, 0x00, 0x0a}
	pps := []byte{0x68, 0xce, 0x38, 0x80}

	// Record a keyframe first
	gc.RecordFrame("cam1", &media.VideoFrame{
		PTS:        0,
		WireData:   makeAVC1Frame([]byte{0x65, 0xAA}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	})

	// Feed 14 more delta frames (total 15 frames, limit is 10)
	for i := 1; i <= 14; i++ {
		gc.RecordFrame("cam1", &media.VideoFrame{
			PTS:        int64(i * 33000),
			WireData:   makeAVC1Frame([]byte{0x41, byte(i)}),
			IsKeyframe: false,
		})
	}

	gop := gc.GetGOP("cam1")
	require.LessOrEqual(t, len(gop), 10, "cache should be trimmed to maxFrames")

	// The keyframe is at index 0 of the original 15 frames.
	// When trimming, we should find the most recent keyframe and keep it + everything after.
	// Since the only keyframe is at the start and there are 15 frames total,
	// we need to drop the oldest non-keyframe frames after the keyframe.
	// Result: keyframe + last 9 delta frames = 10 frames
	require.Equal(t, 10, len(gop))
	require.True(t, gop[0].isKeyframe, "first frame should be the keyframe")

	// Verify the delta frames are the most recent ones
	origGOP := gc.GetOriginalGOP("cam1")
	require.True(t, origGOP[0].IsKeyframe)
	require.Equal(t, int64(0), origGOP[0].PTS, "keyframe PTS preserved")
	// The deltas should be frames 6-14 (the 9 most recent deltas)
	require.Equal(t, int64(6*33000), origGOP[1].PTS, "should keep most recent deltas after keyframe")
	require.Equal(t, int64(14*33000), origGOP[9].PTS)
}

func TestGOPCacheMaxFramesSmallLimit(t *testing.T) {
	// Edge case: maxFrames = 1
	gc := newGOPCacheWithMax(1)

	sps := []byte{0x67, 0x42, 0x00, 0x0a}
	pps := []byte{0x68, 0xce, 0x38, 0x80}

	// Record a keyframe
	gc.RecordFrame("cam1", &media.VideoFrame{
		PTS:        0,
		WireData:   makeAVC1Frame([]byte{0x65, 0xAA}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	})

	gop := gc.GetGOP("cam1")
	require.Equal(t, 1, len(gop), "single keyframe should fit")
	require.True(t, gop[0].isKeyframe)

	// Add a delta — should trim to just the delta (no keyframe to keep)
	gc.RecordFrame("cam1", &media.VideoFrame{
		PTS:        33000,
		WireData:   makeAVC1Frame([]byte{0x41, 0x01}),
		IsKeyframe: false,
	})

	gop = gc.GetGOP("cam1")
	require.Equal(t, 1, len(gop), "should trim to maxFrames=1")
	// With maxFrames=1, after adding keyframe+delta (2 frames), we trim.
	// The keyframe is at index 0. We keep keyframe + deltas after it, but
	// that's still 2. So we must drop deltas between keyframe and tail.
	// Actually with only 1 frame allowed and keyframe at index 0,
	// keyframe + 0 deltas = 1 frame. But we added a delta making it 2.
	// Trimming: keep keyframe, then keep last (maxFrames - 1) = 0 deltas.
	// So the result is just the keyframe.
	require.True(t, gop[0].isKeyframe, "should keep keyframe even at limit=1")

	// Edge case: maxFrames = 2
	gc2 := newGOPCacheWithMax(2)

	gc2.RecordFrame("cam1", &media.VideoFrame{
		PTS:        0,
		WireData:   makeAVC1Frame([]byte{0x65, 0xAA}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	})
	gc2.RecordFrame("cam1", &media.VideoFrame{
		PTS:        33000,
		WireData:   makeAVC1Frame([]byte{0x41, 0x01}),
		IsKeyframe: false,
	})
	gc2.RecordFrame("cam1", &media.VideoFrame{
		PTS:        66000,
		WireData:   makeAVC1Frame([]byte{0x41, 0x02}),
		IsKeyframe: false,
	})

	gop2 := gc2.GetGOP("cam1")
	require.Equal(t, 2, len(gop2), "should trim to maxFrames=2")
	require.True(t, gop2[0].isKeyframe, "should keep keyframe")

	origGOP2 := gc2.GetOriginalGOP("cam1")
	require.Equal(t, int64(66000), origGOP2[1].PTS, "should keep the most recent delta")

	// Edge case: maxFrames = 5 with no keyframes at all
	gc3 := newGOPCacheWithMax(5)
	for i := 0; i < 8; i++ {
		gc3.RecordFrame("cam1", &media.VideoFrame{
			PTS:        int64(i * 33000),
			WireData:   makeAVC1Frame([]byte{0x41, byte(i)}),
			IsKeyframe: false,
		})
	}

	gop3 := gc3.GetGOP("cam1")
	require.Equal(t, 5, len(gop3), "should trim to maxFrames=5 with no keyframe")
	// Should keep frames 3-7 (the last 5)
	origGOP3 := gc3.GetOriginalGOP("cam1")
	require.Equal(t, int64(3*33000), origGOP3[0].PTS)
	require.Equal(t, int64(7*33000), origGOP3[4].PTS)
}

func TestGOPCacheMaxFramesKeyframeInMiddle(t *testing.T) {
	// When trimming, the most recent keyframe should be found and preserved
	gc := newGOPCacheWithMax(8)

	sps := []byte{0x67, 0x42, 0x00, 0x0a}
	pps := []byte{0x68, 0xce, 0x38, 0x80}

	// 3 deltas, then a keyframe, then 7 more deltas = 11 total
	for i := 0; i < 3; i++ {
		gc.RecordFrame("cam1", &media.VideoFrame{
			PTS:        int64(i * 33000),
			WireData:   makeAVC1Frame([]byte{0x41, byte(i)}),
			IsKeyframe: false,
		})
	}

	// Keyframe at position 3 — this will reset the cache (normal keyframe behavior)
	gc.RecordFrame("cam1", &media.VideoFrame{
		PTS:        int64(3 * 33000),
		WireData:   makeAVC1Frame([]byte{0x65, 0xBB}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	})

	// 10 more deltas after the keyframe (total 11: 1 keyframe + 10 deltas)
	for i := 4; i <= 13; i++ {
		gc.RecordFrame("cam1", &media.VideoFrame{
			PTS:        int64(i * 33000),
			WireData:   makeAVC1Frame([]byte{0x41, byte(i)}),
			IsKeyframe: false,
		})
	}

	// Cache has: keyframe(3) + deltas(4..13) = 11 frames, limit is 8
	// Trimming: keep keyframe + last 7 deltas = 8 frames
	gop := gc.GetGOP("cam1")
	require.Equal(t, 8, len(gop))
	require.True(t, gop[0].isKeyframe)

	origGOP := gc.GetOriginalGOP("cam1")
	require.Equal(t, int64(3*33000), origGOP[0].PTS, "keyframe preserved")
	require.Equal(t, int64(7*33000), origGOP[1].PTS, "oldest kept delta")
	require.Equal(t, int64(13*33000), origGOP[7].PTS, "most recent delta")
}

func TestGOPCacheDefaultMaxFrames(t *testing.T) {
	// newGOPCache() should use the default max of 120
	gc := newGOPCache()
	require.Equal(t, 120, gc.maxFrames, "default maxFrames should be 120")
}
