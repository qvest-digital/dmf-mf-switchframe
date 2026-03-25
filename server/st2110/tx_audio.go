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
	"fmt"
	"sync"
	"unsafe"
)

// AudioTx transmits ST 2110-30 audio via MTL pipeline API.
type AudioTx struct {
	handle C.st30p_tx_handle
	inst   *Instance
	cfg    TxAudioConfig
	mu     sync.Mutex
	closed bool
	l24Buf []byte // reusable L24 buffer
}

// CreateAudioTx creates an audio transmitter session on the MTL instance.
func (inst *Instance) CreateAudioTx(cfg TxAudioConfig) (*AudioTx, error) {
	if inst.handle == nil {
		return nil, ErrST2110NotAvailable
	}

	var ops C.struct_st30p_tx_ops

	cName := C.CString(fmt.Sprintf("atx_%s_%d", cfg.MulticastIP, cfg.Port))
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

	handle := C.st30p_tx_create(inst.handle, &ops)
	if handle == nil {
		return nil, fmt.Errorf("st2110: st30p_tx_create failed for %s:%d", cfg.MulticastIP, cfg.Port)
	}

	return &AudioTx{
		handle: handle,
		inst:   inst,
		cfg:    cfg,
	}, nil
}

// PutFrame submits interleaved float32 PCM for transmission.
// Converts float32 → L24 big-endian before copying into MTL frame buffer.
func (tx *AudioTx) PutFrame(pcm []float32) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return fmt.Errorf("st2110: audio tx closed")
	}

	frame := C.st30p_tx_get_frame(tx.handle)
	if frame == nil {
		return fmt.Errorf("st2110: st30p_tx_get_frame returned nil")
	}

	l24Size := len(pcm) * 3
	if cap(tx.l24Buf) < l24Size {
		tx.l24Buf = make([]byte, l24Size)
	}
	tx.l24Buf = tx.l24Buf[:l24Size]
	ConvertFloat32ToInt24BE(pcm, tx.l24Buf)

	copySize := l24Size
	if int(frame.data_size) < copySize {
		copySize = int(frame.data_size)
	}
	C.memcpy(frame.addr, unsafe.Pointer(&tx.l24Buf[0]), C.size_t(copySize))

	ret := C.st30p_tx_put_frame(tx.handle, frame)
	if ret != 0 {
		return fmt.Errorf("st2110: st30p_tx_put_frame returned %d", int(ret))
	}
	return nil
}

// Close frees the audio transmitter session.
func (tx *AudioTx) Close() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return nil
	}
	tx.closed = true
	ret := C.st30p_tx_free(tx.handle)
	if ret != 0 {
		return fmt.Errorf("st2110: st30p_tx_free returned %d", int(ret))
	}
	return nil
}
