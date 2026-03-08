package switcher

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
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
	}, nil)

	// Record two deltas
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x41, 0x01}),
		IsKeyframe: false,
	}, nil)
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x41, 0x02}),
		IsKeyframe: false,
	}, nil)

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
	}, nil)

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
	}, nil)

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
	}, nil)

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
			}, nil)
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
			}, nil)
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
	}, nil)

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
		GroupID:    5,
	}, nil)
	gc.RecordFrame("cam1", &media.VideoFrame{
		PTS:        1033,
		WireData:   makeAVC1Frame([]byte{0x41, 0x01}),
		IsKeyframe: false,
		Codec:      "h264",
		GroupID:    5,
	}, nil)

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
	}, nil)

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
		}, nil)
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
	}, nil)

	// Feed 14 more delta frames (total 15 frames, limit is 10)
	for i := 1; i <= 14; i++ {
		gc.RecordFrame("cam1", &media.VideoFrame{
			PTS:        int64(i * 33000),
			WireData:   makeAVC1Frame([]byte{0x41, byte(i)}),
			IsKeyframe: false,
		}, nil)
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
	}, nil)

	gop := gc.GetGOP("cam1")
	require.Equal(t, 1, len(gop), "single keyframe should fit")
	require.True(t, gop[0].isKeyframe)

	// Add a delta — should trim to just the delta (no keyframe to keep)
	gc.RecordFrame("cam1", &media.VideoFrame{
		PTS:        33000,
		WireData:   makeAVC1Frame([]byte{0x41, 0x01}),
		IsKeyframe: false,
	}, nil)

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
	}, nil)
	gc2.RecordFrame("cam1", &media.VideoFrame{
		PTS:        33000,
		WireData:   makeAVC1Frame([]byte{0x41, 0x01}),
		IsKeyframe: false,
	}, nil)
	gc2.RecordFrame("cam1", &media.VideoFrame{
		PTS:        66000,
		WireData:   makeAVC1Frame([]byte{0x41, 0x02}),
		IsKeyframe: false,
	}, nil)

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
		}, nil)
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
		}, nil)
	}

	// Keyframe at position 3 — this will reset the cache (normal keyframe behavior)
	gc.RecordFrame("cam1", &media.VideoFrame{
		PTS:        int64(3 * 33000),
		WireData:   makeAVC1Frame([]byte{0x65, 0xBB}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	}, nil)

	// 10 more deltas after the keyframe (total 11: 1 keyframe + 10 deltas)
	for i := 4; i <= 13; i++ {
		gc.RecordFrame("cam1", &media.VideoFrame{
			PTS:        int64(i * 33000),
			WireData:   makeAVC1Frame([]byte{0x41, byte(i)}),
			IsKeyframe: false,
		}, nil)
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

func TestGOPCacheSetActiveSources(t *testing.T) {
	gc := newGOPCache()

	sps := []byte{0x67, 0x42, 0x00, 0x0a}
	pps := []byte{0x68, 0xce, 0x38, 0x80}

	// Record frames for 4 sources (no active filter yet — all recorded)
	for _, src := range []string{"cam1", "cam2", "cam3", "cam4"} {
		gc.RecordFrame(src, &media.VideoFrame{
			WireData:   makeAVC1Frame([]byte{0x65, 0xAA}),
			IsKeyframe: true,
			SPS:        sps,
			PPS:        pps,
		}, nil)
	}
	require.NotNil(t, gc.GetGOP("cam1"))
	require.NotNil(t, gc.GetGOP("cam2"))
	require.NotNil(t, gc.GetGOP("cam3"))
	require.NotNil(t, gc.GetGOP("cam4"))

	// Set active sources — caches for cam3 and cam4 should be cleared
	gc.SetActiveSources("cam1", "cam2")
	require.NotNil(t, gc.GetGOP("cam1"), "program source cache should be kept")
	require.NotNil(t, gc.GetGOP("cam2"), "preview source cache should be kept")
	require.Nil(t, gc.GetGOP("cam3"), "non-active source cache should be cleared")
	require.Nil(t, gc.GetGOP("cam4"), "non-active source cache should be cleared")

	// New frames for non-active sources should be skipped
	gc.RecordFrame("cam3", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x65, 0xBB}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	}, nil)
	require.Nil(t, gc.GetGOP("cam3"), "non-active source should not be recorded")

	// Active sources should still record
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x41, 0x01}),
		IsKeyframe: false,
	}, nil)
	gop := gc.GetGOP("cam1")
	require.Equal(t, 2, len(gop), "active source should record new frames")
}

func TestGOPCacheSetActiveSourcesSameSource(t *testing.T) {
	gc := newGOPCache()

	sps := []byte{0x67, 0x42}
	pps := []byte{0x68, 0xce}

	// Program and preview can be the same source
	gc.SetActiveSources("cam1", "cam1")

	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x65, 0xAA}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	}, nil)
	require.NotNil(t, gc.GetGOP("cam1"))
}

func TestGOPCacheSetActiveSourcesEmptyStrings(t *testing.T) {
	gc := newGOPCache()

	sps := []byte{0x67, 0x42}
	pps := []byte{0x68, 0xce}

	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x65, 0xAA}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	}, nil)

	// Empty strings should be ignored — results in empty active set
	gc.SetActiveSources("", "")
	require.Nil(t, gc.GetGOP("cam1"), "all caches cleared when no active sources")

	// Nothing should be recorded
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x65, 0xBB}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	}, nil)
	require.Nil(t, gc.GetGOP("cam1"), "nothing recorded with empty active set")
}

func TestGOPCacheSetActiveSourcesTransition(t *testing.T) {
	gc := newGOPCache()

	sps := []byte{0x67, 0x42}
	pps := []byte{0x68, 0xce}

	// Simulate cut: cam1 program, cam2 preview
	gc.SetActiveSources("cam1", "cam2")

	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x65, 0xAA}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	}, nil)
	gc.RecordFrame("cam2", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x65, 0xBB}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	}, nil)

	// Cut to cam2: now cam2 is program, cam3 is preview
	gc.SetActiveSources("cam2", "cam3")

	require.Nil(t, gc.GetGOP("cam1"), "old program cache should be cleared after cut")
	require.NotNil(t, gc.GetGOP("cam2"), "new program cache should be kept")

	// cam3 should now accept frames
	gc.RecordFrame("cam3", &media.VideoFrame{
		WireData:   makeAVC1Frame([]byte{0x65, 0xCC}),
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
	}, nil)
	require.NotNil(t, gc.GetGOP("cam3"), "new preview should accept frames")
}

func TestGOPCacheNilActiveSourcesRecordsAll(t *testing.T) {
	// Backward compat: nil activeSources means record everything
	gc := newGOPCache()
	require.Nil(t, gc.activeSources, "new cache should have nil activeSources")

	sps := []byte{0x67, 0x42}
	pps := []byte{0x68, 0xce}

	for _, src := range []string{"cam1", "cam2", "cam3"} {
		gc.RecordFrame(src, &media.VideoFrame{
			WireData:   makeAVC1Frame([]byte{0x65, 0xAA}),
			IsKeyframe: true,
			SPS:        sps,
			PPS:        pps,
		}, nil)
	}

	require.NotNil(t, gc.GetGOP("cam1"))
	require.NotNil(t, gc.GetGOP("cam2"))
	require.NotNil(t, gc.GetGOP("cam3"))
}

func TestGOPCacheSetActiveSourcesConcurrent(t *testing.T) {
	gc := newGOPCache()

	sps := []byte{0x67, 0x42}
	pps := []byte{0x68, 0xce}

	var wg sync.WaitGroup
	wg.Add(3)

	// Writer: records frames to multiple sources
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			src := []string{"cam1", "cam2", "cam3", "cam4"}[i%4]
			gc.RecordFrame(src, &media.VideoFrame{
				WireData:   makeAVC1Frame([]byte{0x65, byte(i)}),
				IsKeyframe: i%10 == 0,
				SPS:        sps,
				PPS:        pps,
			}, nil)
		}
	}()

	// Active source updater: simulates cuts
	go func() {
		defer wg.Done()
		pairs := [][2]string{
			{"cam1", "cam2"},
			{"cam2", "cam3"},
			{"cam3", "cam4"},
			{"cam4", "cam1"},
		}
		for i := 0; i < 100; i++ {
			p := pairs[i%len(pairs)]
			gc.SetActiveSources(p[0], p[1])
		}
	}()

	// Reader
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			src := []string{"cam1", "cam2", "cam3", "cam4"}[i%4]
			gc.GetGOP(src)
		}
	}()

	wg.Wait()
}

func BenchmarkGOPCacheRecordFrame(b *testing.B) {
	sps := []byte{0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x40, 0x04}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	wireData := makeAVC1Frame(make([]byte, 4096)) // Realistic ~4KB NALU

	b.Run("active_source", func(b *testing.B) {
		gc := newGOPCache()
		gc.SetActiveSources("cam1", "cam2")
		frame := &media.VideoFrame{
			PTS:        1000,
			WireData:   wireData,
			IsKeyframe: false,
			Codec:      "h264",
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if i%30 == 0 {
				// Keyframe every 30 frames
				gc.RecordFrame("cam1", &media.VideoFrame{
					PTS:        int64(i * 3000),
					WireData:   wireData,
					IsKeyframe: true,
					SPS:        sps,
					PPS:        pps,
					Codec:      "h264",
				}, nil)
			} else {
				frame.PTS = int64(i * 3000)
				gc.RecordFrame("cam1", frame, nil)
			}
		}
	})

	b.Run("delta_only", func(b *testing.B) {
		gc := newGOPCache()
		gc.SetActiveSources("cam1", "cam2")
		// Seed with a keyframe so subsequent deltas append
		gc.RecordFrame("cam1", &media.VideoFrame{
			PTS:        0,
			WireData:   wireData,
			IsKeyframe: true,
			SPS:        sps,
			PPS:        pps,
			Codec:      "h264",
		}, nil)
		frame := &media.VideoFrame{
			PTS:        1000,
			WireData:   wireData,
			IsKeyframe: false,
			Codec:      "h264",
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			frame.PTS = int64((i + 1) * 3000)
			gc.RecordFrame("cam1", frame, nil)
		}
	})

	b.Run("skipped_source", func(b *testing.B) {
		gc := newGOPCache()
		gc.SetActiveSources("cam1", "cam2")
		frame := &media.VideoFrame{
			PTS:        1000,
			WireData:   wireData,
			IsKeyframe: false,
			Codec:      "h264",
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			frame.PTS = int64(i * 3000)
			gc.RecordFrame("cam3", frame, nil) // cam3 is NOT active
		}
	})

	b.Run("no_filter_all_recorded", func(b *testing.B) {
		gc := newGOPCache()
		// No SetActiveSources — backward compat, records all
		frame := &media.VideoFrame{
			PTS:        1000,
			WireData:   wireData,
			IsKeyframe: false,
			Codec:      "h264",
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if i%30 == 0 {
				gc.RecordFrame("cam1", &media.VideoFrame{
					PTS:        int64(i * 3000),
					WireData:   wireData,
					IsKeyframe: true,
					SPS:        sps,
					PPS:        pps,
					Codec:      "h264",
				}, nil)
			} else {
				frame.PTS = int64(i * 3000)
				gc.RecordFrame("cam1", frame, nil)
			}
		}
	})

	b.Run("trim_triggered", func(b *testing.B) {
		gc := newGOPCacheWithMax(30)
		gc.SetActiveSources("cam1", "cam2")
		frame := &media.VideoFrame{
			PTS:        1000,
			WireData:   wireData,
			IsKeyframe: false,
			Codec:      "h264",
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if i%30 == 0 {
				gc.RecordFrame("cam1", &media.VideoFrame{
					PTS:        int64(i * 3000),
					WireData:   wireData,
					IsKeyframe: true,
					SPS:        sps,
					PPS:        pps,
					Codec:      "h264",
				}, nil)
			} else {
				frame.PTS = int64(i * 3000)
				gc.RecordFrame("cam1", frame, nil)
			}
		}
	})

	b.Run("realistic_1080p", func(b *testing.B) {
		largeWire := makeAVC1Frame(make([]byte, 80*1024)) // ~80KB 1080p frame
		gc := newGOPCache()
		gc.SetActiveSources("cam1", "cam2")
		frame := &media.VideoFrame{
			PTS:        1000,
			WireData:   largeWire,
			IsKeyframe: false,
			Codec:      "h264",
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if i%30 == 0 {
				gc.RecordFrame("cam1", &media.VideoFrame{
					PTS:        int64(i * 3000),
					WireData:   largeWire,
					IsKeyframe: true,
					SPS:        sps,
					PPS:        pps,
					Codec:      "h264",
				}, nil)
			} else {
				frame.PTS = int64(i * 3000)
				gc.RecordFrame("cam1", frame, nil)
			}
		}
	})
}

func TestAnnexBComputedOnce(t *testing.T) {
	gc := newGOPCache()
	sps := []byte{0x67, 0x42, 0x00, 0x0a}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	wireData := makeAVC1Frame([]byte{0x65, 0xAA, 0xBB})

	// Pre-compute AnnexB with SPS/PPS
	annexB := codec.AVC1ToAnnexB(wireData)
	annexB = codec.PrependSPSPPS(sps, pps, annexB)

	// Record with pre-computed AnnexB
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData: wireData, IsKeyframe: true, SPS: sps, PPS: pps,
	}, annexB)

	gopPrecomputed := gc.GetGOP("cam1")
	require.Equal(t, 1, len(gopPrecomputed))

	// Record without pre-computed AnnexB (nil) — resets cache on keyframe
	gc.RecordFrame("cam1", &media.VideoFrame{
		WireData: wireData, IsKeyframe: true, SPS: sps, PPS: pps,
	}, nil)

	gopComputed := gc.GetGOP("cam1")
	require.Equal(t, 1, len(gopComputed))

	// Both paths should produce identical AnnexB data
	expectedAnnexB := codec.AVC1ToAnnexB(wireData)
	expectedAnnexB = codec.PrependSPSPPS(sps, pps, expectedAnnexB)
	require.Equal(t, expectedAnnexB, gopPrecomputed[0].annexB)
	require.Equal(t, expectedAnnexB, gopComputed[0].annexB)
}

func BenchmarkTrimCache(b *testing.B) {
	makeCache := func(n int) []cachedFrame {
		cache := make([]cachedFrame, n)
		for i := range cache {
			cache[i] = cachedFrame{
				annexB:     make([]byte, 4096),
				original:   &media.VideoFrame{PTS: int64(i * 3000)},
				isKeyframe: i == 0,
			}
		}
		return cache
	}

	b.Run("with_keyframe", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cache := makeCache(60)
			trimCache(cache, 30)
		}
	})

	b.Run("no_keyframe", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cache := make([]cachedFrame, 60)
			for j := range cache {
				cache[j] = cachedFrame{
					annexB:     make([]byte, 4096),
					original:   &media.VideoFrame{PTS: int64(j * 3000)},
					isKeyframe: false,
				}
			}
			trimCache(cache, 30)
		}
	})
}
