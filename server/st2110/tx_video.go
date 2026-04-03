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

	"github.com/zsiec/switchframe/server/internal/video"
)

// VideoTx transmits uncompressed ST 2110-20 video via MTL pipeline API.
// Input is YUV420p (from the switcher pipeline); internally converts to
// YUV422 planar before passing to MTL which handles the RFC4175 packing.
type VideoTx struct {
	handle C.st20p_tx_handle
	inst   *Instance
	cfg    TxVideoConfig
	mu     sync.Mutex
	closed bool
	yuv422 []byte // reusable 4:2:2 buffer
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

	cPort := C.CString(inst.dpdkPort)
	defer C.free(unsafe.Pointer(cPort))
	C.st_txp_para_port_set(&ops.port, C.MTL_SESSION_PORT_P, cPort)

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
	// Use YUV422PLANAR10LE as input — MTL has a built-in converter to
	// the ST 2110-20 wire format (RFC4175 YUV422 10-bit BE).
	// PutFrame converts YUV420p 8-bit → YUV422 planar 10-bit LE internally.
	ops.input_fmt = C.ST_FRAME_FMT_YUV422PLANAR10LE
	ops.transport_fmt = C.ST20_FMT_YUV_422_10BIT
	ops.transport_pacing = C.ST21_PACING_NARROW
	ops.transport_packing = C.ST20_PACKING_BPM
	ops.device = C.ST_PLUGIN_DEVICE_AUTO

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
// Internally converts YUV420p → YUV422 planar (chroma row duplication)
// before copying into the MTL frame buffer.
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

	w := tx.cfg.Format.Width
	h := tx.cfg.Format.Height

	// Convert YUV420p 8-bit → YUV422 planar 10-bit LE.
	// YUV422 10-bit planar: Y=w*h*2, U=w/2*h*2, V=w/2*h*2 → total = w*h*4
	// (each sample is uint16 LE, 10-bit value left-shifted by 6 for 16-bit alignment,
	//  or stored as raw 10-bit value — MTL expects raw 10-bit in uint16 LE)
	yuv422Size := video.YUV422_10bitFrameSize(w, h) // 3 planes, all 16-bit samples
	if cap(tx.yuv422) < yuv422Size {
		tx.yuv422 = make([]byte, yuv422Size)
	}
	tx.yuv422 = tx.yuv422[:yuv422Size]
	yuv420p8ToYUV422Planar10LE(yuv, tx.yuv422, w, h)

	copySize := yuv422Size
	if int(frame.data_size) < copySize {
		copySize = int(frame.data_size)
	}
	C.memcpy(frame.addr[0], unsafe.Pointer(&tx.yuv422[0]), C.size_t(copySize))

	ret := C.st20p_tx_put_frame(tx.handle, frame)
	if ret != 0 {
		return fmt.Errorf("st2110: st20p_tx_put_frame returned %d", int(ret))
	}
	return nil
}

// yuv420p8ToYUV422Planar10LE converts YUV420p 8-bit to YUV422 planar 10-bit LE.
// Each 8-bit sample is scaled to 10-bit (val << 2) and stored as uint16 LE.
// Chroma is vertically upsampled by row duplication (nearest-neighbor).
//
// Input layout (YUV420p 8-bit):
//   Y: w*h bytes, U: w/2*h/2 bytes, V: w/2*h/2 bytes
//
// Output layout (YUV422 planar 10-bit LE):
//   Y: w*h*2 bytes (uint16 LE), U: w/2*h*2 bytes, V: w/2*h*2 bytes
func yuv420p8ToYUV422Planar10LE(src, dst []byte, w, h int) {
	ySize8 := w * h
	chromaW := w / 2
	chromaH420 := h / 2

	ySize16 := ySize8 * 2
	chromaPlane16 := chromaW * h * 2

	// Y plane: 8-bit → 10-bit LE.
	for i := 0; i < ySize8; i++ {
		val := uint16(src[i]) << 2
		dst[i*2] = byte(val)
		dst[i*2+1] = byte(val >> 8)
	}

	// U plane: 8-bit → 10-bit LE with vertical row duplication.
	srcU := src[ySize8:]
	dstU := dst[ySize16:]
	for row := 0; row < chromaH420; row++ {
		for col := 0; col < chromaW; col++ {
			val := uint16(srcU[row*chromaW+col]) << 2
			lo := byte(val)
			hi := byte(val >> 8)
			// Write to two output rows (row*2 and row*2+1).
			dstU[(row*2)*chromaW*2+col*2] = lo
			dstU[(row*2)*chromaW*2+col*2+1] = hi
			dstU[(row*2+1)*chromaW*2+col*2] = lo
			dstU[(row*2+1)*chromaW*2+col*2+1] = hi
		}
	}

	// V plane: same as U.
	srcV := src[ySize8+chromaW*chromaH420:]
	dstV := dst[ySize16+chromaPlane16:]
	for row := 0; row < chromaH420; row++ {
		for col := 0; col < chromaW; col++ {
			val := uint16(srcV[row*chromaW+col]) << 2
			lo := byte(val)
			hi := byte(val >> 8)
			dstV[(row*2)*chromaW*2+col*2] = lo
			dstV[(row*2)*chromaW*2+col*2+1] = hi
			dstV[(row*2+1)*chromaW*2+col*2] = lo
			dstV[(row*2+1)*chromaW*2+col*2+1] = hi
		}
	}
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
