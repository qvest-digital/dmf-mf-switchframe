//go:build cgo && st2110

package st2110

/*
#include <mtl/mtl_api.h>
#include <mtl/st_pipeline_api.h>
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

// VideoRx receives uncompressed ST 2110-20 video via MTL pipeline API.
type VideoRx struct {
	handle C.st20p_rx_handle
	inst   *Instance
	cfg    RxVideoConfig
	mu     sync.Mutex
	closed bool
}

// CreateVideoRx creates a video receiver session on the MTL instance.
func (inst *Instance) CreateVideoRx(cfg RxVideoConfig) (*VideoRx, error) {
	if inst.handle == nil {
		return nil, ErrST2110NotAvailable
	}

	var ops C.struct_st20p_rx_ops

	cName := C.CString(fmt.Sprintf("vrx_%s_%d", cfg.MulticastIP, cfg.Port))
	defer C.free(unsafe.Pointer(cName))
	ops.name = cName

	// Set DPDK port BDF (mandatory, must match mtl_init).
	cPort := C.CString(inst.dpdkPort)
	defer C.free(unsafe.Pointer(cPort))
	C.st_rxp_para_port_set(&ops.port, C.MTL_SESSION_PORT_P, cPort)

	// Use MTL helper functions to set port IP (avoids cgo anonymous union issues).
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
		pt = 112 // default dynamic PT for ST 2110-20
	}
	ops.port.payload_type = C.uint8_t(pt)
	ops.port.num_port = 1

	ops.width = C.uint32_t(cfg.Format.Width)
	ops.height = C.uint32_t(cfg.Format.Height)
	ops.fps = fpsToMTL(cfg.Format.FPSNum, cfg.Format.FPSDen)
	ops.transport_fmt = C.ST20_FMT_YUV_422_10BIT
	ops.output_fmt = C.ST_FRAME_FMT_YUV420PLANAR8
	ops.device = C.ST_PLUGIN_DEVICE_AUTO

	frameBufs := cfg.FrameBuffers
	if frameBufs == 0 {
		frameBufs = 3
	}
	ops.framebuff_cnt = C.uint16_t(frameBufs)

	handle := C.st20p_rx_create(inst.handle, &ops)
	if handle == nil {
		return nil, fmt.Errorf("st2110: st20p_rx_create failed for %s:%d", cfg.MulticastIP, cfg.Port)
	}

	return &VideoRx{
		handle: handle,
		inst:   inst,
		cfg:    cfg,
	}, nil
}

// GetFrame blocks until a frame is available or ctx is cancelled.
// Returns YUV420p data, dimensions, PTP-derived timestamp, and error.
// The returned byte slice is a Go-owned copy; the MTL frame is returned immediately.
func (rx *VideoRx) GetFrame(ctx context.Context) ([]byte, int, int, int64, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, 0, 0, 0, ctx.Err()
		default:
		}

		rx.mu.Lock()
		if rx.closed {
			rx.mu.Unlock()
			return nil, 0, 0, 0, fmt.Errorf("st2110: video rx closed")
		}

		frame := C.st20p_rx_get_frame(rx.handle)
		if frame == nil {
			rx.mu.Unlock()
			time.Sleep(1 * time.Millisecond)
			continue
		}

		if frame.status == C.ST_FRAME_STATUS_CORRUPTED {
			C.st20p_rx_put_frame(rx.handle, frame)
			rx.mu.Unlock()
			continue
		}

		w := int(frame.width)
		h := int(frame.height)
		dataSize := int(frame.data_size)
		pts := int64(frame.timestamp)

		data := make([]byte, dataSize)
		C.memcpy(unsafe.Pointer(&data[0]), frame.addr[0], C.size_t(dataSize))

		C.st20p_rx_put_frame(rx.handle, frame)
		rx.mu.Unlock()

		return data, w, h, pts, nil
	}
}

// Close frees the video receiver session.
func (rx *VideoRx) Close() error {
	rx.mu.Lock()
	defer rx.mu.Unlock()
	if rx.closed {
		return nil
	}
	rx.closed = true
	ret := C.st20p_rx_free(rx.handle)
	if ret != 0 {
		return fmt.Errorf("st2110: st20p_rx_free returned %d", int(ret))
	}
	return nil
}
