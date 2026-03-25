//go:build cgo && st2110

package st2110

/*
#include <mtl/mtl_api.h>
#include <mtl/st40_pipeline_api.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// AncillaryTx transmits ST 2110-40 ancillary data via MTL pipeline API.
type AncillaryTx struct {
	handle C.st40p_tx_handle
	inst   *Instance
	cfg    TxAncillaryConfig
	mu     sync.Mutex
	closed bool
}

// CreateAncillaryTx creates an ancillary data transmitter session.
func (inst *Instance) CreateAncillaryTx(cfg TxAncillaryConfig) (*AncillaryTx, error) {
	if inst.handle == nil {
		return nil, ErrST2110NotAvailable
	}

	var ops C.struct_st40p_tx_ops

	cName := C.CString(fmt.Sprintf("dtx_%s_%d", cfg.MulticastIP, cfg.Port))
	defer C.free(unsafe.Pointer(cName))
	C.strncpy(&ops.name[0], cName, 32-1)

	if err := parseIPv4(cfg.MulticastIP, &ops.port.dip_addr[0][0]); err != nil {
		return nil, fmt.Errorf("st2110: invalid multicast IP: %w", err)
	}
	ops.port.udp_port[0] = C.uint16_t(cfg.Port)
	ops.port.payload_type = 113
	ops.port.num_port = 1

	ops.fps = fpsToMTL(cfg.Format.FPSNum, cfg.Format.FPSDen)

	handle := C.st40p_tx_create(inst.handle, &ops)
	if handle == nil {
		return nil, fmt.Errorf("st2110: st40p_tx_create failed for %s:%d", cfg.MulticastIP, cfg.Port)
	}

	return &AncillaryTx{
		handle: handle,
		inst:   inst,
		cfg:    cfg,
	}, nil
}

// PutFrame submits ST 291 VANC data for transmission.
func (tx *AncillaryTx) PutFrame(data []byte) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return fmt.Errorf("st2110: ancillary tx closed")
	}

	frame := C.st40p_tx_get_frame(tx.handle)
	if frame == nil {
		return fmt.Errorf("st2110: st40p_tx_get_frame returned nil")
	}

	copySize := len(data)
	if int(frame.data_size) < copySize {
		copySize = int(frame.data_size)
	}
	if copySize > 0 {
		C.memcpy(frame.addr[0], unsafe.Pointer(&data[0]), C.size_t(copySize))
	}

	ret := C.st40p_tx_put_frame(tx.handle, frame)
	if ret != 0 {
		return fmt.Errorf("st2110: st40p_tx_put_frame returned %d", int(ret))
	}
	return nil
}

// Close frees the ancillary transmitter session.
func (tx *AncillaryTx) Close() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return nil
	}
	tx.closed = true
	ret := C.st40p_tx_free(tx.handle)
	if ret != 0 {
		return fmt.Errorf("st2110: st40p_tx_free returned %d", int(ret))
	}
	return nil
}
