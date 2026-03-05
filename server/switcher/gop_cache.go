package switcher

import (
	"sync"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

// cachedFrame stores a deep-copied frame for GOP replay.
type cachedFrame struct {
	annexB     []byte             // Annex B format with SPS/PPS prepended for keyframes
	original   *media.VideoFrame  // Deep copy of original frame for program relay replay
	isKeyframe bool
}

// gopCache maintains a per-source cache of the current GOP (keyframe +
// subsequent delta frames). Used to warm up transition decoders so the
// first blended frame appears immediately without waiting for a keyframe.
type gopCache struct {
	mu     sync.Mutex
	caches map[string][]cachedFrame
}

func newGOPCache() *gopCache {
	return &gopCache{
		caches: make(map[string][]cachedFrame),
	}
}

// RecordFrame records a video frame into the cache for the given source.
// On keyframe: resets the cache and stores the keyframe with SPS/PPS prepended.
// On delta: appends to the existing cache.
// All data is deep-copied; the caller's buffers are not retained.
func (g *gopCache) RecordFrame(sourceKey string, frame *media.VideoFrame) {
	// Convert AVC1 WireData to Annex B (deep copy)
	annexB := codec.AVC1ToAnnexB(frame.WireData)
	if len(annexB) == 0 {
		return
	}

	// For keyframes, prepend SPS/PPS as Annex B NALUs
	if frame.IsKeyframe && len(frame.SPS) > 0 {
		var buf []byte
		buf = append(buf, 0x00, 0x00, 0x00, 0x01)
		buf = append(buf, frame.SPS...)
		buf = append(buf, 0x00, 0x00, 0x00, 0x01)
		buf = append(buf, frame.PPS...)
		buf = append(buf, annexB...)
		annexB = buf
	}

	// Deep-copy the original frame for program relay replay
	orig := &media.VideoFrame{
		PTS:        frame.PTS,
		IsKeyframe: frame.IsKeyframe,
		Codec:      frame.Codec,
		GroupID:     frame.GroupID,
	}
	if len(frame.WireData) > 0 {
		orig.WireData = make([]byte, len(frame.WireData))
		copy(orig.WireData, frame.WireData)
	}
	if frame.IsKeyframe {
		if len(frame.SPS) > 0 {
			orig.SPS = make([]byte, len(frame.SPS))
			copy(orig.SPS, frame.SPS)
		}
		if len(frame.PPS) > 0 {
			orig.PPS = make([]byte, len(frame.PPS))
			copy(orig.PPS, frame.PPS)
		}
	}

	cf := cachedFrame{
		annexB:     annexB,
		original:   orig,
		isKeyframe: frame.IsKeyframe,
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if frame.IsKeyframe {
		// Reset cache on keyframe
		g.caches[sourceKey] = []cachedFrame{cf}
	} else {
		// Append delta frame
		g.caches[sourceKey] = append(g.caches[sourceKey], cf)
	}
}

// GetGOP returns a copy of the cached GOP for the given source.
// Returns nil if no cache exists. The returned slices are independent
// copies safe for concurrent use.
func (g *gopCache) GetGOP(sourceKey string) []cachedFrame {
	g.mu.Lock()
	defer g.mu.Unlock()

	cached := g.caches[sourceKey]
	if len(cached) == 0 {
		return nil
	}

	// Deep copy so the caller doesn't hold references to our buffers
	result := make([]cachedFrame, len(cached))
	for i, cf := range cached {
		data := make([]byte, len(cf.annexB))
		copy(data, cf.annexB)
		result[i] = cachedFrame{
			annexB:     data,
			isKeyframe: cf.isKeyframe,
		}
	}
	return result
}

// GetOriginalGOP returns deep copies of the original *media.VideoFrame
// objects for the cached GOP. Used to replay frames to the program relay
// at transition completion, avoiding the keyframe gate gap.
func (g *gopCache) GetOriginalGOP(sourceKey string) []*media.VideoFrame {
	g.mu.Lock()
	defer g.mu.Unlock()

	cached := g.caches[sourceKey]
	if len(cached) == 0 {
		return nil
	}

	result := make([]*media.VideoFrame, len(cached))
	for i, cf := range cached {
		f := &media.VideoFrame{
			PTS:        cf.original.PTS,
			IsKeyframe: cf.original.IsKeyframe,
			Codec:      cf.original.Codec,
			GroupID:     cf.original.GroupID,
		}
		if len(cf.original.WireData) > 0 {
			f.WireData = make([]byte, len(cf.original.WireData))
			copy(f.WireData, cf.original.WireData)
		}
		if len(cf.original.SPS) > 0 {
			f.SPS = make([]byte, len(cf.original.SPS))
			copy(f.SPS, cf.original.SPS)
		}
		if len(cf.original.PPS) > 0 {
			f.PPS = make([]byte, len(cf.original.PPS))
			copy(f.PPS, cf.original.PPS)
		}
		result[i] = f
	}
	return result
}

// RemoveSource removes the cached GOP for the given source.
func (g *gopCache) RemoveSource(sourceKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.caches, sourceKey)
}
