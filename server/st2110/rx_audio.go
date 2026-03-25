//go:build cgo && st2110

package st2110

/*
#include <mtl/mtl_api.h>
#include <mtl/st30_pipeline_api.h>
#include <stdlib.h>
#include <string.h>
#include "mtl_helpers.h"
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
	ops.name = cName

	cPort := C.CString(inst.dpdkPort)
	defer C.free(unsafe.Pointer(cPort))
	C.st_rxp_para_port_set(&ops.port, C.MTL_SESSION_PORT_P, cPort)

	cMcast := C.CString(cfg.MulticastIP)
	defer C.free(unsafe.Pointer(cMcast))
	if ret := C.st_rxp_para_ip_set(&ops.port, C.MTL_PORT_P, cMcast); ret != 0 {
		return nil, fmt.Errorf("st2110: invalid multicast IP %q", cfg.MulticastIP)
	}
	if cfg.SourceIP != "" {
		a, b, c, d, err := parseIPv4(cfg.SourceIP)
		if err != nil {
			return nil, fmt.Errorf("st2110: invalid source IP filter: %w", err)
		}
		C.st2110_rx_port_set_mcast_sip(&ops.port, 0, a, b, c, d)
	}
	C.st_rxp_para_udp_port_set(&ops.port, C.MTL_PORT_P, C.uint16_t(cfg.Port))
	pt := cfg.PayloadType
	if pt == 0 {
		pt = 111 // default dynamic PT for ST 2110-30
	}
	ops.port.payload_type = C.uint8_t(pt)
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

	// Calculate frame buffer size (must be multiple of packet size).
	var fps C.double
	fbSize := C.st30_calculate_framebuff_size(ops.fmt, ops.ptime, ops.sampling,
		ops.channel, 10_000_000, &fps)
	if fbSize > 0 {
		ops.framebuff_size = C.uint32_t(fbSize)
	}

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

		channels := rx.cfg.Format.Channels
		dataSize := int(frame.data_size)
		totalSamples := dataSize / 3
		samplesPerCh := totalSamples / channels

		rawL24 := make([]byte, dataSize)
		C.memcpy(unsafe.Pointer(&rawL24[0]), frame.addr, C.size_t(dataSize))

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
