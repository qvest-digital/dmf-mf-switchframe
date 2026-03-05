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
