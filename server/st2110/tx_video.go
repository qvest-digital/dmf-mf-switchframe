//go:build cgo && st2110

package st2110

/*
#include <mtl/mtl_api.h>
#include <mtl/st_pipeline_api.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// VideoTx transmits uncompressed ST 2110-20 video via MTL pipeline API.
type VideoTx struct {
	handle C.st20p_tx_handle
	inst   *Instance
	cfg    TxVideoConfig
	mu     sync.Mutex
	closed bool
}

// CreateVideoTx creates a video transmitter session on the MTL instance.
func (inst *Instance) CreateVideoTx(cfg TxVideoConfig) (*VideoTx, error) {
	if inst.handle == nil {
		return nil, ErrST2110NotAvailable
	}

	var ops C.struct_st20p_tx_ops

	cName := C.CString(fmt.Sprintf("vtx_%s_%d", cfg.MulticastIP, cfg.Port))
	defer C.free(unsafe.Pointer(cName))
	ops.name = cName

	cDest := C.CString(cfg.MulticastIP)
	defer C.free(unsafe.Pointer(cDest))
	if ret := C.st_txp_para_dip_set(&ops.port, C.MTL_PORT_P, cDest); ret != 0 {
		return nil, fmt.Errorf("st2110: invalid multicast IP %q", cfg.MulticastIP)
	}
	C.st_txp_para_udp_port_set(&ops.port, C.MTL_PORT_P, C.uint16_t(cfg.Port))
	pt := cfg.PayloadType
	if pt == 0 {
		pt = 112 // default dynamic PT for ST 2110-20
	}
	ops.port.payload_type = C.uint8_t(pt)
	ops.port.num_port = 1

	ops.width = C.uint32_t(cfg.Format.Width)
	ops.height = C.uint32_t(cfg.Format.Height)
	ops.fps = fpsToMTL(cfg.Format.FPSNum, cfg.Format.FPSDen)
	ops.input_fmt = C.ST_FRAME_FMT_YUV420PLANAR8
	ops.transport_fmt = C.ST20_FMT_YUV_422_10BIT

	frameBufs := cfg.FrameBuffers
	if frameBufs == 0 {
		frameBufs = 3
	}
	ops.framebuff_cnt = C.uint16_t(frameBufs)

	handle := C.st20p_tx_create(inst.handle, &ops)
	if handle == nil {
		return nil, fmt.Errorf("st2110: st20p_tx_create failed for %s:%d", cfg.MulticastIP, cfg.Port)
	}

	return &VideoTx{
		handle: handle,
		inst:   inst,
		cfg:    cfg,
	}, nil
}

// PutFrame submits a YUV420p frame for transmission.
func (tx *VideoTx) PutFrame(yuv []byte) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return fmt.Errorf("st2110: video tx closed")
	}

	frame := C.st20p_tx_get_frame(tx.handle)
	if frame == nil {
		return fmt.Errorf("st2110: st20p_tx_get_frame returned nil")
	}

	copySize := len(yuv)
	if int(frame.data_size) < copySize {
		copySize = int(frame.data_size)
	}
	C.memcpy(frame.addr[0], unsafe.Pointer(&yuv[0]), C.size_t(copySize))

	ret := C.st20p_tx_put_frame(tx.handle, frame)
	if ret != 0 {
		return fmt.Errorf("st2110: st20p_tx_put_frame returned %d", int(ret))
	}
	return nil
}

// Close frees the video transmitter session.
func (tx *VideoTx) Close() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return nil
	}
	tx.closed = true
	ret := C.st20p_tx_free(tx.handle)
	if ret != 0 {
		return fmt.Errorf("st2110: st20p_tx_free returned %d", int(ret))
	}
	return nil
}
