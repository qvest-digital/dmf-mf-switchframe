package switcher

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/transition"
)

// TileCache holds the latest downsampled YUV420 tile for each registered
// source, keyed by source key. It is the server-side backing store for the
// multiview mosaic compositor (DEBT-002).
//
// Thread model:
//   - The `slots` map is written under `mu` (writer-serialized) and read
//     lock-free via atomic.Pointer CoW, matching the `sourcesAtomic`
//     pattern already in use for Switcher.
//   - Each slot stores the latest tile as an atomic.Pointer to an
//     immutable YUV420 byte slice; publishers allocate a fresh buffer
//     per frame and atomically swap. Readers load the pointer and copy
//     from an immutable buffer, so there is never a write-during-read
//     hazard and the race detector stays quiet.
//   - Tile buffers are sized at tile resolution (tileW*tileH*3/2). At
//     32 sources × 30 fps × ~48 KB the allocation rate is ~46 MB/s,
//     comfortably within the Go GC's steady-state budget. We do not
//     reclaim prior buffers into a sync.Pool — a reader may still hold
//     the prior pointer when Swap returns it, and any Put-into-pool
//     would race with that reader. Defer pool reclamation until a
//     benchmark demands it and an epoch-reclamation scheme is wired.
type TileCache struct {
	tileW, tileH int
	tileSize     int // cached tileFrameSize(tileW, tileH)

	mu    sync.Mutex // writer serialization for slots map
	slots atomic.Pointer[map[string]*tileSlot]

	staleThresholdNs atomic.Int64
}

// ErrOddDimensions is returned by NewTileCache for zero or odd width/height.
// YUV420 requires even dimensions so the 4:2:0 chroma planes divide cleanly.
var ErrOddDimensions = errors.New("TileCache: tile dimensions must be positive and even")

// tileSlot holds the latest tile for a single source. `current` points to
// an immutable, fully-written YUV420 byte slice; swapping the pointer is
// the only way to update.
type tileSlot struct {
	key     string
	label   atomic.Pointer[string]
	current atomic.Pointer[[]byte]

	lastUpdateNano atomic.Int64
}

const defaultStaleThresholdNs = int64(500 * time.Millisecond)

// NewTileCache constructs a TileCache that stores tiles at (tileW, tileH).
// Returns ErrOddDimensions if tileW or tileH are zero or odd.
func NewTileCache(tileW, tileH int) (*TileCache, error) {
	if tileW <= 0 || tileH <= 0 || tileW&1 == 1 || tileH&1 == 1 {
		return nil, ErrOddDimensions
	}
	tc := &TileCache{
		tileW:    tileW,
		tileH:    tileH,
		tileSize: tileFrameSize(tileW, tileH),
	}
	empty := make(map[string]*tileSlot)
	tc.slots.Store(&empty)
	tc.staleThresholdNs.Store(defaultStaleThresholdNs)
	return tc, nil
}

// SetStaleThreshold overrides the "no update in X" threshold used by
// ReadInto to report stale tiles. Default is 500 ms.
func (tc *TileCache) SetStaleThreshold(d time.Duration) {
	tc.staleThresholdNs.Store(int64(d))
}

// RegisterTile allocates a slot for the given source key. Safe to call
// repeatedly; a re-register with the same key keeps the existing slot
// but updates the label.
func (tc *TileCache) RegisterTile(key, label string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	current := *tc.slots.Load()
	if existing, ok := current[key]; ok {
		existing.setLabel(label)
		return
	}

	slot := &tileSlot{key: key}
	slot.setLabel(label)

	next := make(map[string]*tileSlot, len(current)+1)
	for k, v := range current {
		next[k] = v
	}
	next[key] = slot
	tc.slots.Store(&next)
}

// UnregisterTile removes a source's tile. Readers that hold a pointer
// obtained before this call keep working on their loaded buffer — the
// slot's last buffer is returned to the pool once no one holds it
// (via Go GC; we do not reclaim eagerly).
func (tc *TileCache) UnregisterTile(key string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	current := *tc.slots.Load()
	if _, ok := current[key]; !ok {
		return
	}
	next := make(map[string]*tileSlot, len(current)-1)
	for k, v := range current {
		if k == key {
			continue
		}
		next[k] = v
	}
	tc.slots.Store(&next)
}

// SetLabel updates the label shown in the mosaic for the given source.
// No-op if the source is not registered.
func (tc *TileCache) SetLabel(key, label string) {
	current := *tc.slots.Load()
	if slot, ok := current[key]; ok {
		slot.setLabel(label)
	}
}

// HasTile reports whether a tile is registered for the given source key.
func (tc *TileCache) HasTile(key string) bool {
	current := *tc.slots.Load()
	_, ok := current[key]
	return ok
}

// Label returns the current label for the given source, or "" if not
// registered. Safe for concurrent use.
func (tc *TileCache) Label(key string) string {
	current := *tc.slots.Load()
	slot, ok := current[key]
	if !ok {
		return ""
	}
	if lbl := slot.label.Load(); lbl != nil {
		return *lbl
	}
	return ""
}

// PublishYUV420 downsamples the source YUV420 frame into a fresh tile
// buffer and atomically publishes it to the slot. No-op if the source
// is not registered, the source dimensions are invalid, or `src` is
// shorter than the expected YUV420 frame size for (srcW, srcH). The
// caller may reuse `src` immediately after return.
func (tc *TileCache) PublishYUV420(key string, src []byte, srcW, srcH int) {
	if srcW <= 0 || srcH <= 0 || srcW&1 == 1 || srcH&1 == 1 {
		return
	}
	expectedSize := tileFrameSize(srcW, srcH)
	if len(src) < expectedSize {
		return
	}

	current := *tc.slots.Load()
	slot, ok := current[key]
	if !ok {
		return
	}

	buf := make([]byte, tc.tileSize)
	transition.ScaleYUV420(src[:expectedSize], srcW, srcH, buf, tc.tileW, tc.tileH)

	slot.current.Store(&buf)
	slot.lastUpdateNano.Store(time.Now().UnixNano())
}

// ReadInto copies the current tile for the given source into dst.
// Returns (true, stale) on success where stale=true if the tile has not
// been updated within the configured stale threshold. Returns (false,
// false) if the source is not registered, dst is too small, or no tile
// has been published yet.
func (tc *TileCache) ReadInto(key string, dst []byte) (ok, stale bool) {
	current := *tc.slots.Load()
	slot, exists := current[key]
	if !exists {
		return false, false
	}
	bufPtr := slot.current.Load()
	if bufPtr == nil {
		return false, false
	}
	if len(dst) < len(*bufPtr) {
		return false, false
	}
	copy(dst[:len(*bufPtr)], *bufPtr)

	// Stale check: treat negative deltas (wall-clock jumped backward —
	// NTP step, VM pause, container migration) as stale so we never
	// report a slot fresh for the duration of the backward jump.
	threshold := tc.staleThresholdNs.Load()
	last := slot.lastUpdateNano.Load()
	delta := time.Now().UnixNano() - last
	if delta < 0 || delta > threshold {
		stale = true
	}
	return true, stale
}

// tileFrameSize returns the byte size of a YUV420 tile at (w, h).
func tileFrameSize(w, h int) int {
	return w*h + 2*(w/2)*(h/2)
}

// setLabel atomically replaces the label pointer.
func (s *tileSlot) setLabel(label string) {
	clone := label
	s.label.Store(&clone)
}
