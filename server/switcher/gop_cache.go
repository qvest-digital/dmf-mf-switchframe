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

// defaultMaxFrames is the maximum number of frames cached per source.
// 120 frames = 4 seconds at 30fps. This prevents unbounded growth when
// a source never sends keyframes or sends them very rarely.
const defaultMaxFrames = 120

// gopCache maintains a per-source cache of the current GOP (keyframe +
// subsequent delta frames). Used to warm up transition decoders so the
// first blended frame appears immediately without waiting for a keyframe.
type gopCache struct {
	mu        sync.Mutex
	caches    map[string][]cachedFrame
	maxFrames int
}

func newGOPCache() *gopCache {
	return newGOPCacheWithMax(defaultMaxFrames)
}

func newGOPCacheWithMax(maxFrames int) *gopCache {
	if maxFrames < 1 {
		maxFrames = 1
	}
	return &gopCache{
		caches:    make(map[string][]cachedFrame),
		maxFrames: maxFrames,
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
		DTS:        frame.DTS,
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

	// Enforce maximum frame count to prevent unbounded growth
	if cache := g.caches[sourceKey]; len(cache) > g.maxFrames {
		g.caches[sourceKey] = trimCache(cache, g.maxFrames)
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

// trimCache trims a cache slice to at most maxFrames entries. If a keyframe
// exists in the cache, it is retained along with the most recent delta frames
// that fit within the limit. If no keyframe exists, only the most recent
// maxFrames entries are kept.
func trimCache(cache []cachedFrame, maxFrames int) []cachedFrame {
	// Find the most recent keyframe
	keyframeIdx := -1
	for i := len(cache) - 1; i >= 0; i-- {
		if cache[i].isKeyframe {
			keyframeIdx = i
			break
		}
	}

	if keyframeIdx < 0 {
		// No keyframe — keep the most recent maxFrames entries
		return cache[len(cache)-maxFrames:]
	}

	// Keep the keyframe plus the most recent (maxFrames - 1) deltas after it.
	// If there are fewer deltas than (maxFrames - 1), the cache from keyframe
	// onward is already within the limit (this shouldn't happen since we only
	// trim when len > maxFrames, but handle it defensively).
	tailCount := len(cache) - keyframeIdx // keyframe + deltas after it
	if tailCount <= maxFrames {
		return cache[keyframeIdx:]
	}

	// More frames from keyframe onward than maxFrames allows.
	// Keep keyframe + last (maxFrames - 1) frames.
	trimmed := make([]cachedFrame, maxFrames)
	trimmed[0] = cache[keyframeIdx]
	copy(trimmed[1:], cache[len(cache)-(maxFrames-1):])
	return trimmed
}

// RemoveSource removes the cached GOP for the given source.
func (g *gopCache) RemoveSource(sourceKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.caches, sourceKey)
}
