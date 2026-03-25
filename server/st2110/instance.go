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
	"net"
	"sync"
	"unsafe"
)

// Instance wraps an MTL library handle.
type Instance struct {
	handle    C.mtl_handle
	closeOnce sync.Once
	closeErr  error
}

// NewInstance creates an MTL instance with the given configuration.
func NewInstance(cfg InstanceConfig) (*Instance, error) {
	var params C.struct_mtl_init_params

	if err := parseIPv4(cfg.SourceIP, &params.sip_addr[0]); err != nil {
		return nil, fmt.Errorf("st2110: invalid source IP %q: %w", cfg.SourceIP, err)
	}

	cPort := C.CString(cfg.Port)
	defer C.free(unsafe.Pointer(cPort))
	C.strncpy(&params.port[0][0], cPort, C.MTL_PORT_MAX_LEN-1)

	params.num_ports = 1
	params.pmd[0] = C.MTL_PMD_DPDK_USER
	params.tx_queues_cnt[0] = C.uint16_t(cfg.TxQueues)
	params.rx_queues_cnt[0] = C.uint16_t(cfg.RxQueues)
	params.flags = C.MTL_FLAG_BIND_NUMA | C.MTL_FLAG_TX_VIDEO_MIGRATE
	params.log_level = C.enum_mtl_log_level(cfg.LogLevel)

	if cfg.PTPMode != "" && cfg.PTPMode != "none" {
		params.flags |= C.MTL_FLAG_PTP_ENABLE
	}

	if cfg.LCores != "" {
		cLcores := C.CString(cfg.LCores)
		defer C.free(unsafe.Pointer(cLcores))
		params.lcores = cLcores
	}

	handle := C.mtl_init(&params)
	if handle == nil {
		return nil, fmt.Errorf("st2110: mtl_init failed for port %q ip %q", cfg.Port, cfg.SourceIP)
	}

	return &Instance{handle: handle}, nil
}

// Close releases the MTL instance. Safe to call concurrently or multiple times.
func (inst *Instance) Close() error {
	inst.closeOnce.Do(func() {
		if inst.handle != nil {
			ret := C.mtl_uninit(inst.handle)
			inst.handle = nil
			if ret != 0 {
				inst.closeErr = fmt.Errorf("st2110: mtl_uninit returned %d", int(ret))
			}
		}
	})
	return inst.closeErr
}

// PTPStatus returns the current PTP synchronization state.
func (inst *Instance) PTPStatus() (PTPStatus, error) {
	if inst.handle == nil {
		return PTPStatus{}, ErrST2110NotAvailable
	}
	var spec C.struct_timespec
	ret := C.mtl_ptp_read_time(inst.handle, &spec)
	if ret != 0 {
		return PTPStatus{Locked: false}, nil
	}
	return PTPStatus{
		Locked:   spec.tv_sec > 0 || spec.tv_nsec > 0,
		OffsetNs: int64(spec.tv_nsec),
	}, nil
}

// parseIPv4 parses a dotted-quad IPv4 string into a C uint8_t[4] array.
func parseIPv4(s string, out *C.uint8_t) error {
	ip := net.ParseIP(s)
	if ip == nil {
		return fmt.Errorf("invalid IP: %s", s)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return fmt.Errorf("not IPv4: %s", s)
	}
	arr := (*[4]C.uint8_t)(unsafe.Pointer(out))
	arr[0] = C.uint8_t(ip4[0])
	arr[1] = C.uint8_t(ip4[1])
	arr[2] = C.uint8_t(ip4[2])
	arr[3] = C.uint8_t(ip4[3])
	return nil
}

// fpsToMTL converts a rational frame rate to the MTL fps enum.
func fpsToMTL(num, den int) C.enum_st_fps {
	fps := float64(num) / float64(den)
	switch {
	case fps > 59.9 && fps <= 60.0:
		if den == 1 {
			return C.ST_FPS_P60
		}
		return C.ST_FPS_P59_94
	case fps > 49.9 && fps <= 50.0:
		return C.ST_FPS_P50
	case fps > 29.9 && fps <= 30.0:
		if den == 1 {
			return C.ST_FPS_P30
		}
		return C.ST_FPS_P29_97
	case fps > 24.9 && fps <= 25.0:
		return C.ST_FPS_P25
	case fps > 23.9 && fps <= 24.0:
		if den == 1 {
			return C.ST_FPS_P24
		}
		return C.ST_FPS_P23_98
	default:
		return C.ST_FPS_P29_97
	}
}
