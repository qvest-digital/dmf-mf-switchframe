//go:build cgo && darwin

package gpu

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// poolFreeListDuplicates returns frames that appear more than once in the
// pool's free list. A duplicate means the same GPU buffer will be handed out
// to two different Acquire() callers (frame aliasing / VRAM corruption).
func poolFreeListDuplicates(p *FramePool) []*GPUFrame {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[*GPUFrame]bool, len(p.free))
	var dups []*GPUFrame
	for _, f := range p.free {
		if seen[f] {
			dups = append(dups, f)
		}
		seen[f] = true
	}
	return dups
}

// poolFreeListContains reports whether frame is currently in the free list.
func poolFreeListContains(p *FramePool, frame *GPUFrame) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, f := range p.free {
		if f == frame {
			return true
		}
	}
	return false
}

// TestGPUSourceManager_GetFrameIngestRace_PoolIntegrity exercises finding #20:
// GetFrame's Load -> Ref -> recheck retry loop shared no lock with IngestYUV's
// Swap+Release, so a reader could resurrect a frame's refcount from zero after
// the ingester had already returned it to the pool, and then double-insert the
// frame into the pool free list on its compensating Release. After the stress
// run, the free list must contain no duplicates and must not contain the
// source's still-current frame.
func TestGPUSourceManager_GetFrameIngestRace_PoolIntegrity(t *testing.T) {
	const w, h = 16, 16
	ctx := testContext(t)
	pool, err := NewFramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	yuv := createTestYUV420p(w, h, 128, 128, 128)
	mgr.IngestYUV("cam1", yuv, w, h, 0)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Single producer: continuously swap in new frames (releases old ones).
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := int64(0)
		for {
			select {
			case <-stop:
				return
			default:
			}
			i++
			mgr.IngestYUV("cam1", yuv, w, h, i)
		}
	}()

	// Many readers: continuously take and drop refs on the current frame.
	readers := runtime.GOMAXPROCS(0)
	if readers < 4 {
		readers = 4
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if f := mgr.GetFrame("cam1"); f != nil {
					f.Release()
				}
			}
		}()
	}

	time.Sleep(3 * time.Second)
	close(stop)
	wg.Wait()

	if dups := poolFreeListDuplicates(pool); len(dups) > 0 {
		t.Fatalf("pool free list contains %d duplicate frame(s): "+
			"the same VRAM buffer would be handed to two Acquire() callers", len(dups))
	}

	// The frame still cached as current must not be in the free list.
	cur := mgr.CurrentFrame("cam1")
	require.NotNil(t, cur)
	require.False(t, poolFreeListContains(pool, cur),
		"source's current frame is simultaneously in the pool free list (use-after-release)")
}

// TestGPUSourceManager_RemoveDuringIngest_NoResourceRace exercises findings
// #71/#86: RemoveSource/RegisterSource ran cleanupEntry (free scalePool,
// nv12Tmp, stmap, colorLUT) outside m.mu while a concurrent IngestYUV — which
// only holds m.mu.RLock long enough to fetch the entry pointer — was actively
// using those per-entry resources. The ingest uses a mismatched source
// resolution so every frame goes through entry.scalePool, the field cleanup
// frees. Run with -race: pre-fix this reports data races on entry.scalePool
// (and friends) and can crash on a freed GPU buffer.
func TestGPUSourceManager_RemoveDuringIngest_NoResourceRace(t *testing.T) {
	const poolW, poolH = 64, 64
	const srcW, srcH = 32, 32 // mismatch forces the entry.scalePool path
	ctx := testContext(t)
	pool, err := NewFramePool(ctx, poolW, poolH, 8)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	yuv := createTestYUV420p(srcW, srcH, 128, 128, 128)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Producer: continuous ingest at non-pool resolution (uses scalePool).
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := int64(0)
		for {
			select {
			case <-stop:
				return
			default:
			}
			i++
			mgr.IngestYUV("cam1", yuv, srcW, srcH, i)
		}
	}()

	// Control-plane goroutine: churn remove/re-register, like an SRT source
	// reconnecting while decoded frames are still flowing.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			mgr.RemoveSource("cam1")
			mgr.RegisterSource("cam1", srcW, srcH, nil)
		}
	}()

	time.Sleep(2 * time.Second)
	close(stop)
	wg.Wait()

	// Pool integrity should survive the churn.
	require.Empty(t, poolFreeListDuplicates(pool),
		"pool free list contains duplicates after remove/ingest churn")

	// The source should still be usable.
	mgr.IngestYUV("cam1", yuv, srcW, srcH, 999)
	if f := mgr.GetFrame("cam1"); f != nil {
		f.Release()
	}
}
