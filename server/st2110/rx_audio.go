//go:build cgo && st2110

package st2110

/*
#include <mtl/mtl_api.h>
#include <mtl/st30_pipeline_api.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"context"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// AudioRx receives ST 2110-30 audio via MTL pipeline API.
type AudioRx struct {
	handle C.st30p_rx_handle
	inst   *Instance
	cfg    RxAudioConfig
	mu     sync.Mutex
	closed bool
}

// CreateAudioRx creates an audio receiver session on the MTL instance.
func (inst *Instance) CreateAudioRx(cfg RxAudioConfig) (*AudioRx, error) {
	if inst.handle == nil {
		return nil, ErrST2110NotAvailable
	}

	var ops C.struct_st30p_rx_ops

	cName := C.CString(fmt.Sprintf("arx_%s_%d", cfg.MulticastIP, cfg.Port))
	defer C.free(unsafe.Pointer(cName))
	C.strncpy(&ops.name[0], cName, 32-1)

	if err := parseIPv4(cfg.MulticastIP, &ops.port.ip_addr[0][0]); err != nil {
		return nil, fmt.Errorf("st2110: invalid multicast IP: %w", err)
	}
	if cfg.SourceIP != "" {
		if err := parseIPv4(cfg.SourceIP, &ops.port.mcast_sip_addr[0][0]); err != nil {
			return nil, fmt.Errorf("st2110: invalid source IP filter: %w", err)
		}
	}
	ops.port.udp_port[0] = C.uint16_t(cfg.Port)
	pt := cfg.PayloadType
	if pt == 0 {
		pt = 111 // default dynamic PT for ST 2110-30
	}
	ops.port.payload_type = C.uint16_t(pt)
	ops.port.num_port = 1

	ops.fmt = C.ST30_FMT_PCM24
	ops.channel = C.uint16_t(cfg.Format.Channels)
	ops.sampling = C.ST30_SAMPLING_48K
	ops.ptime = C.ST30_PTIME_1MS
	if cfg.Format.PacketTimeUs == 125 {
		ops.ptime = C.ST30_PTIME_125US
	}

	frameBufs := cfg.FrameBuffers
	if frameBufs == 0 {
		frameBufs = 3
	}
	ops.framebuff_cnt = C.uint16_t(frameBufs)

	handle := C.st30p_rx_create(inst.handle, &ops)
	if handle == nil {
		return nil, fmt.Errorf("st2110: st30p_rx_create failed for %s:%d", cfg.MulticastIP, cfg.Port)
	}

	return &AudioRx{
		handle: handle,
		inst:   inst,
		cfg:    cfg,
	}, nil
}

// GetFrame blocks until an audio frame is available or ctx is cancelled.
// Returns interleaved float32 PCM (converted from L24 big-endian), sample count per channel, and error.
func (rx *AudioRx) GetFrame(ctx context.Context) ([]float32, int, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		default:
		}

		rx.mu.Lock()
		if rx.closed {
			rx.mu.Unlock()
			return nil, 0, fmt.Errorf("st2110: audio rx closed")
		}

		frame := C.st30p_rx_get_frame(rx.handle)
		if frame == nil {
			rx.mu.Unlock()
			time.Sleep(500 * time.Microsecond)
			continue
		}

		// Skip corrupted frames (same check as video RX).
		if frame.status == C.ST_FRAME_STATUS_CORRUPTED {
			C.st30p_rx_put_frame(rx.handle, frame)
			rx.mu.Unlock()
			continue
		}

		channels := rx.cfg.Format.Channels
		dataSize := int(frame.data_size)
		totalSamples := dataSize / 3
		samplesPerCh := totalSamples / channels

		rawL24 := make([]byte, dataSize)
		C.memcpy(unsafe.Pointer(&rawL24[0]), frame.addr[0], C.size_t(dataSize))

		C.st30p_rx_put_frame(rx.handle, frame)
		rx.mu.Unlock()

		pcm := make([]float32, totalSamples)
		ConvertInt24BEToFloat32(rawL24, pcm, channels, samplesPerCh)

		return pcm, samplesPerCh, nil
	}
}

// Close frees the audio receiver session.
func (rx *AudioRx) Close() error {
	rx.mu.Lock()
	defer rx.mu.Unlock()
	if rx.closed {
		return nil
	}
	rx.closed = true
	ret := C.st30p_rx_free(rx.handle)
	if ret != 0 {
		return fmt.Errorf("st2110: st30p_rx_free returned %d", int(ret))
	}
	return nil
}
