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
	"context"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// AncillaryRx receives ST 2110-40 ancillary data via MTL pipeline API.
type AncillaryRx struct {
	handle C.st40p_rx_handle
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

	var ops C.struct_st40p_rx_ops

	cName := C.CString(fmt.Sprintf("drx_%s_%d", cfg.MulticastIP, cfg.Port))
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
		pt = 113 // default dynamic PT for ST 2110-40
	}
	ops.port.payload_type = C.uint16_t(pt)
	ops.port.num_port = 1

	ops.fps = fpsToMTL(cfg.Format.FPSNum, cfg.Format.FPSDen)

	handle := C.st40p_rx_create(inst.handle, &ops)
	if handle == nil {
		return nil, fmt.Errorf("st2110: st40p_rx_create failed for %s:%d", cfg.MulticastIP, cfg.Port)
	}

	return &AncillaryRx{
		handle: handle,
		inst:   inst,
		cfg:    cfg,
	}, nil
}

// GetFrame blocks until an ancillary frame is available or ctx is cancelled.
// Returns raw ST 291 VANC bytes (suitable for scte104.ParseST291).
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

		frame := C.st40p_rx_get_frame(rx.handle)
		if frame == nil {
			rx.mu.Unlock()
			time.Sleep(1 * time.Millisecond)
			continue
		}

		// Skip corrupted frames (same check as video RX).
		if frame.status == C.ST_FRAME_STATUS_CORRUPTED {
			C.st40p_rx_put_frame(rx.handle, frame)
			rx.mu.Unlock()
			continue
		}

		dataSize := int(frame.data_size)
		if dataSize == 0 {
			C.st40p_rx_put_frame(rx.handle, frame)
			rx.mu.Unlock()
			continue
		}

		data := make([]byte, dataSize)
		C.memcpy(unsafe.Pointer(&data[0]), frame.addr[0], C.size_t(dataSize))

		C.st40p_rx_put_frame(rx.handle, frame)
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
	ret := C.st40p_rx_free(rx.handle)
	if ret != 0 {
		return fmt.Errorf("st2110: st40p_rx_free returned %d", int(ret))
	}
	return nil
}
