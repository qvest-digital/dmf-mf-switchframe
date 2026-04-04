//go:build cgo && darwin

package gpu

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testFrame creates a GPUFrame suitable for Ref/Release without a real Metal buffer.
func testFrame(pts int64) *GPUFrame {
	f := &GPUFrame{PTS: pts}
	f.refs.Store(1) // owned by caller
	return f
}

func TestGPUEncodeNode_NonBlockingDrop(t *testing.T) {
	blocker := make(chan struct{})

	node := &gpuEncodeNode{
		encoder:   &GPUEncoder{}, // non-nil to pass guard check
		onEncoded: func(data []byte, isIDR bool, pts int64) {},
	}
	node.frameCh = make(chan vtEncodeReq, 2)
	node.done = make(chan struct{})
	go func() {
		defer close(node.done)
		for req := range node.frameCh {
			<-blocker
			req.frame.Release()
		}
	}()

	// Fill the channel: 2 frames fill the buffer, goroutine is blocked.
	for i := 0; i < 2; i++ {
		f := testFrame(int64(i))
		require.NoError(t, node.ProcessGPU(f))
	}

	// 3rd frame should NOT block — it drops oldest and sends.
	done := make(chan struct{})
	go func() {
		f := testFrame(99)
		_ = node.ProcessGPU(f)
		close(done)
	}()

	select {
	case <-done:
		// ProcessGPU returned without blocking.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ProcessGPU blocked — should have dropped oldest frame")
	}

	assert.Equal(t, int64(1), node.framesDropped.Load(), "should have dropped 1 frame")
	assert.Equal(t, int64(3), node.framesIn.Load(), "should have seen 3 frames")

	// Unblock encoder and clean up.
	close(blocker)
	close(node.frameCh)
	<-node.done
}

func TestGPUEncodeNode_IDRPreservedOnDrop(t *testing.T) {
	// When a frame that had forceIDR=true is dropped, the replacement
	// frame should inherit the IDR flag.

	var receivedIDR atomic.Bool
	blocker := make(chan struct{}, 3)

	node := &gpuEncodeNode{
		encoder:  &GPUEncoder{},
		forceIDR: &atomic.Bool{},
		onEncoded: func(data []byte, isIDR bool, pts int64) {
			if isIDR {
				receivedIDR.Store(true)
			}
		},
	}
	node.frameCh = make(chan vtEncodeReq, 2)
	node.done = make(chan struct{})
	go func() {
		defer close(node.done)
		for req := range node.frameCh {
			<-blocker
			if req.forceIDR && node.onEncoded != nil {
				node.onEncoded(nil, true, req.frame.PTS)
			}
			req.frame.Release()
		}
	}()

	// Frame 1: normal (fills slot 1).
	f1 := testFrame(1)
	require.NoError(t, node.ProcessGPU(f1))

	// Frame 2: set forceIDR before sending (fills slot 2).
	node.forceIDR.Store(true)
	f2 := testFrame(2)
	require.NoError(t, node.ProcessGPU(f2))
	// forceIDR was consumed by ProcessGPU (swapped to false).
	assert.False(t, node.forceIDR.Load(), "forceIDR should be consumed")

	// Frame 3: normal — drops f1 (oldest). Channel now has [f2(IDR), f3].
	f3 := testFrame(3)
	require.NoError(t, node.ProcessGPU(f3))
	// f1 was the dropped one (no IDR), so no IDR propagation needed.
	// f2 still has forceIDR=true in the channel.

	// Unblock all 2 remaining encodes.
	blocker <- struct{}{}
	blocker <- struct{}{}
	close(node.frameCh)
	<-node.done

	// f2 should have delivered the IDR.
	assert.True(t, receivedIDR.Load(), "IDR from f2 should have been encoded")
}
