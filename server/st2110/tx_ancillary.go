//go:build cgo && st2110

package st2110

/*
#include <mtl/mtl_api.h>
#include <mtl/st40_api.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// AncillaryTx transmits ST 2110-40 ancillary data via MTL raw RTP API.
// Unlike video/audio which use pipeline APIs, ST 2110-40 only has raw-level access.
type AncillaryTx struct {
	handle C.st40_tx_handle
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

	var ops C.struct_st40_tx_ops

	cName := C.CString(fmt.Sprintf("dtx_%s_%d", cfg.MulticastIP, cfg.Port))
	defer C.free(unsafe.Pointer(cName))
	ops.name = cName

	if err := parseIPv4(cfg.MulticastIP, &ops.dip_addr[0][0]); err != nil {
		return nil, fmt.Errorf("st2110: invalid multicast IP: %w", err)
	}
	ops.udp_port[0] = C.uint16_t(cfg.Port)
	pt := cfg.PayloadType
	if pt == 0 {
		pt = 113 // default dynamic PT for ST 2110-40
	}
	ops.payload_type = C.uint8_t(pt)
	ops.num_port = 1

	ops.fps = fpsToMTL(cfg.Format.FPSNum, cfg.Format.FPSDen)
	ops._type = C.ST40_TYPE_RTP_LEVEL
	ops.rtp_ring_size = 128

	handle := C.st40_tx_create(inst.handle, &ops)
	if handle == nil {
		return nil, fmt.Errorf("st2110: st40_tx_create failed for %s:%d", cfg.MulticastIP, cfg.Port)
	}

	return &AncillaryTx{
		handle: handle,
		inst:   inst,
		cfg:    cfg,
	}, nil
}

// PutFrame submits RFC 8331 encoded ancillary data for transmission via RTP.
// The data should be a complete RTP payload (RFC 8331 format).
func (tx *AncillaryTx) PutFrame(data []byte) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return fmt.Errorf("st2110: ancillary tx closed")
	}

	if len(data) == 0 {
		return nil
	}

	var usrptr unsafe.Pointer
	mbuf := C.st40_tx_get_mbuf(tx.handle, &usrptr)
	if mbuf == nil {
		return fmt.Errorf("st2110: st40_tx_get_mbuf returned nil")
	}

	C.memcpy(usrptr, unsafe.Pointer(&data[0]), C.size_t(len(data)))

	ret := C.st40_tx_put_mbuf(tx.handle, mbuf, C.uint16_t(len(data)))
	if ret != 0 {
		return fmt.Errorf("st2110: st40_tx_put_mbuf returned %d", int(ret))
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
	ret := C.st40_tx_free(tx.handle)
	if ret != 0 {
		return fmt.Errorf("st2110: st40_tx_free returned %d", int(ret))
	}
	return nil
}
