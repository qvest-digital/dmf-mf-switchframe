package switcher

import (
	"sync"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

const gopBufCap = 65536 // 64KB default buffer capacity for GOP pool

var gopBufPool = sync.Pool{
	New: func() any {
		return make([]byte, 0, gopBufCap)
	},
}

func getGOPBuf(size int) []byte {
	b, ok := gopBufPool.Get().([]byte)
	if !ok || cap(b) < size {
		return make([]byte, size)
	}
	return b[:size]
}

func putGOPBuf(b []byte) {
	if b == nil {
		return
	}
	gopBufPool.Put(b[:0]) //nolint:staticcheck // slice value is intentional
}

// cachedFrame stores a deep-copied frame for GOP replay.
type cachedFrame struct {
	annexB     []byte            // Annex B format with SPS/PPS prepended for keyframes
	original   *media.VideoFrame // Deep copy of original frame for program relay replay
	isKeyframe bool
}

// defaultMaxFrames is the maximum number of frames cached per source.
// 120 frames = 4 seconds at 30fps. This prevents unbounded growth when
// a source never sends keyframes or sends them very rarely.
const defaultMaxFrames = 120

// gopCache maintains a per-source cache of the current GOP (keyframe +
// subsequent delta frames). Used to warm up transition decoders so the
// first blended frame appears immediately without waiting for a keyframe.
//
// Only frames from "active" sources (program + preview) are recorded.
// This avoids expensive deep-copy allocations for sources that are never
// consumed. When no active sources are set (zero-value), all sources are
// recorded for backward compatibility.
type gopCache struct {
	mu            sync.Mutex
	caches        map[string][]cachedFrame
	maxFrames     int
	activeSources map[string]bool // nil = record all (backward compat)
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

// SetActiveSources updates which sources should be recorded. Only the
// program and preview sources need caching — all others are skipped.
// When a source is removed from the active set, its cache is cleared to
// free memory. Passing empty strings is safe (they are ignored).
func (g *gopCache) SetActiveSources(program, preview string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	active := make(map[string]bool, 2)
	if program != "" {
		active[program] = true
	}
	if preview != "" {
		active[preview] = true
	}

	for key, frames := range g.caches {
		if !active[key] {
			for i := range frames {
				putGOPBuf(frames[i].annexB)
				if frames[i].original != nil {
					putGOPBuf(frames[i].original.WireData)
				}
				frames[i] = cachedFrame{}
			}
			delete(g.caches, key)
		}
	}

	g.activeSources = active
}

// RecordFrame records a video frame into the cache for the given source.
// On keyframe: resets the cache and stores the keyframe with SPS/PPS prepended.
// On delta: appends to the existing cache.
// All data is deep-copied; the caller's buffers are not retained.
//
// If precomputedAnnexB is non-nil, it is used directly (deep-copied) instead
// of converting from AVC1. This avoids duplicate conversion on the hot path
// when the caller has already computed AnnexB for other purposes.
//
// Frames from sources not in the active set are skipped to avoid unnecessary
// deep-copy allocations on the hot path.
func (g *gopCache) RecordFrame(sourceKey string, frame *media.VideoFrame, precomputedAnnexB []byte) {
	// Prepare data outside lock to minimize critical section.
	var annexB []byte
	if len(precomputedAnnexB) > 0 {
		annexB = getGOPBuf(len(precomputedAnnexB))
		copy(annexB, precomputedAnnexB)
	} else {
		// Convert AVC1→AnnexB directly into a pool buffer to avoid
		// an intermediate allocation.
		poolBuf := getGOPBuf(len(frame.WireData))
		converted := codec.AVC1ToAnnexBInto(frame.WireData, poolBuf[:0])
		if len(converted) == 0 {
			putGOPBuf(poolBuf)
			return
		}
		if frame.IsKeyframe {
			withSPSPPS := codec.PrependSPSPPSInto(frame.SPS, frame.PPS, converted, nil)
			putGOPBuf(poolBuf)
			annexB = getGOPBuf(len(withSPSPPS))
			copy(annexB, withSPSPPS)
		} else {
			annexB = converted
		}
	}

	orig := &media.VideoFrame{
		PTS:        frame.PTS,
		DTS:        frame.DTS,
		IsKeyframe: frame.IsKeyframe,
		Codec:      frame.Codec,
		GroupID:    frame.GroupID,
	}
	if len(frame.WireData) > 0 {
		orig.WireData = getGOPBuf(len(frame.WireData))
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

	// Single lock acquisition for both active-source check and cache write.
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.activeSources != nil && !g.activeSources[sourceKey] {
		putGOPBuf(annexB)
		if orig != nil && len(orig.WireData) > 0 {
			putGOPBuf(orig.WireData)
		}
		return
	}

	if frame.IsKeyframe {
		old := g.caches[sourceKey]
		for i := range old {
			putGOPBuf(old[i].annexB)
			if old[i].original != nil {
				putGOPBuf(old[i].original.WireData)
			}
			old[i].annexB = nil
			old[i].original = nil
		}
		cache := make([]cachedFrame, 1, g.maxFrames)
		cache[0] = cf
		g.caches[sourceKey] = cache
	} else {
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
			GroupID:    cf.original.GroupID,
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

// trimCache trims a cache slice to at most maxFrames entries in-place.
// If a keyframe exists in the cache, it is retained along with the most
// recent delta frames that fit within the limit. If no keyframe exists,
// only the most recent maxFrames entries are kept. Evicted frames have
// their pooled buffers (annexB and WireData) returned to the pool.
func trimCache(cache []cachedFrame, maxFrames int) []cachedFrame {
	keyframeIdx := -1
	for i := len(cache) - 1; i >= 0; i-- {
		if cache[i].isKeyframe {
			keyframeIdx = i
			break
		}
	}

	if keyframeIdx < 0 {
		start := len(cache) - maxFrames
		for i := 0; i < start; i++ {
			putGOPBuf(cache[i].annexB)
			if cache[i].original != nil {
				putGOPBuf(cache[i].original.WireData)
			}
			cache[i] = cachedFrame{}
		}
		return cache[start:]
	}

	tailCount := len(cache) - keyframeIdx
	if tailCount <= maxFrames {
		for i := 0; i < keyframeIdx; i++ {
			putGOPBuf(cache[i].annexB)
			if cache[i].original != nil {
				putGOPBuf(cache[i].original.WireData)
			}
			cache[i] = cachedFrame{}
		}
		return cache[keyframeIdx:]
	}

	keepStart := len(cache) - (maxFrames - 1)
	for i := 0; i < keyframeIdx; i++ {
		putGOPBuf(cache[i].annexB)
		if cache[i].original != nil {
			putGOPBuf(cache[i].original.WireData)
		}
		cache[i] = cachedFrame{}
	}
	for i := keyframeIdx + 1; i < keepStart; i++ {
		putGOPBuf(cache[i].annexB)
		if cache[i].original != nil {
			putGOPBuf(cache[i].original.WireData)
		}
		cache[i] = cachedFrame{}
	}
	cache[0] = cache[keyframeIdx]
	if keyframeIdx != 0 {
		cache[keyframeIdx] = cachedFrame{}
	}
	copy(cache[1:], cache[keepStart:])
	for i := maxFrames; i < len(cache); i++ {
		cache[i] = cachedFrame{}
	}
	return cache[:maxFrames]
}

// RemoveSource removes the cached GOP for the given source.
func (g *gopCache) RemoveSource(sourceKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if frames, ok := g.caches[sourceKey]; ok {
		for i := range frames {
			putGOPBuf(frames[i].annexB)
			if frames[i].original != nil {
				putGOPBuf(frames[i].original.WireData)
			}
			frames[i] = cachedFrame{}
		}
		delete(g.caches, sourceKey)
	}
}
