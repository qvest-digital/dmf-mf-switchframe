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
	"context"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// AncillaryRx receives ST 2110-40 ancillary data via MTL raw RTP API.
// Unlike video/audio which use pipeline APIs, ST 2110-40 only has RTP-level access.
type AncillaryRx struct {
	handle C.st40_rx_handle
	inst   *Instance
	cfg    RxAncillaryConfig
	mu     sync.Mutex
	closed bool
}

// CreateAncillaryRx creates an ancillary data receiver session on the MTL instance.
func (inst *Instance) CreateAncillaryRx(cfg RxAncillaryConfig) (*AncillaryRx, error) {
	if inst.handle == nil {
		return nil, ErrST2110NotAvailable
	}

	var ops C.struct_st40_rx_ops

	cName := C.CString(fmt.Sprintf("drx_%s_%d", cfg.MulticastIP, cfg.Port))
	defer C.free(unsafe.Pointer(cName))
	ops.name = cName

	if err := parseIPv4(cfg.MulticastIP, &ops.ip_addr[0][0]); err != nil {
		return nil, fmt.Errorf("st2110: invalid multicast IP: %w", err)
	}
	if cfg.SourceIP != "" {
		if err := parseIPv4(cfg.SourceIP, &ops.mcast_sip_addr[0][0]); err != nil {
			return nil, fmt.Errorf("st2110: invalid source IP filter: %w", err)
		}
	}
	ops.udp_port[0] = C.uint16_t(cfg.Port)
	pt := cfg.PayloadType
	if pt == 0 {
		pt = 113 // default dynamic PT for ST 2110-40
	}
	ops.payload_type = C.uint8_t(pt)
	ops.num_port = 1

	// RTP ring must be power of 2
	ops.rtp_ring_size = 128

	handle := C.st40_rx_create(inst.handle, &ops)
	if handle == nil {
		return nil, fmt.Errorf("st2110: st40_rx_create failed for %s:%d", cfg.MulticastIP, cfg.Port)
	}

	return &AncillaryRx{
		handle: handle,
		inst:   inst,
		cfg:    cfg,
	}, nil
}

// GetFrame blocks until an ancillary RTP packet is available or ctx is cancelled.
// Returns raw RTP payload bytes containing RFC 8331 ancillary data.
func (rx *AncillaryRx) GetFrame(ctx context.Context) ([]byte, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		rx.mu.Lock()
		if rx.closed {
			rx.mu.Unlock()
			return nil, fmt.Errorf("st2110: ancillary rx closed")
		}

		var usrptr unsafe.Pointer
		var pktLen C.uint16_t
		mbuf := C.st40_rx_get_mbuf(rx.handle, &usrptr, &pktLen)
		if mbuf == nil {
			rx.mu.Unlock()
			time.Sleep(1 * time.Millisecond)
			continue
		}

		dataSize := int(pktLen)
		if dataSize == 0 {
			C.st40_rx_put_mbuf(rx.handle, mbuf)
			rx.mu.Unlock()
			continue
		}

		data := make([]byte, dataSize)
		C.memcpy(unsafe.Pointer(&data[0]), usrptr, C.size_t(dataSize))

		C.st40_rx_put_mbuf(rx.handle, mbuf)
		rx.mu.Unlock()

		return data, nil
	}
}

// Close frees the ancillary receiver session.
func (rx *AncillaryRx) Close() error {
	rx.mu.Lock()
	defer rx.mu.Unlock()
	if rx.closed {
		return nil
	}
	rx.closed = true
	ret := C.st40_rx_free(rx.handle)
	if ret != 0 {
		return fmt.Errorf("st2110: st40_rx_free returned %d", int(ret))
	}
	return nil
}
