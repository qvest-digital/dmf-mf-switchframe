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
	dpdkPort  string // stored for per-session port config
	closeOnce sync.Once
	closeErr  error
}

// NewInstance creates an MTL instance with the given configuration.
func NewInstance(cfg InstanceConfig) (*Instance, error) {
	var params C.struct_mtl_init_params

	// Use MTL helper functions to set port/IP (avoids cgo anonymous union issues).
	cIP := C.CString(cfg.SourceIP)
	defer C.free(unsafe.Pointer(cIP))
	if ret := C.mtl_para_sip_set(&params, C.MTL_PORT_P, cIP); ret != 0 {
		return nil, fmt.Errorf("st2110: invalid source IP %q", cfg.SourceIP)
	}

	cPort := C.CString(cfg.Port)
	defer C.free(unsafe.Pointer(cPort))
	C.mtl_para_port_set(&params, C.MTL_PORT_P, cPort)

	params.num_ports = 1
	switch cfg.PMDType {
	case "kernel":
		C.mtl_para_pmd_set(&params, C.MTL_PORT_P, C.MTL_PMD_KERNEL_SOCKET)
	case "af_xdp":
		C.mtl_para_pmd_set(&params, C.MTL_PORT_P, C.MTL_PMD_NATIVE_AF_XDP)
	default:
		C.mtl_para_pmd_set(&params, C.MTL_PORT_P, C.MTL_PMD_DPDK_USER)
	}
	C.mtl_para_tx_queues_cnt_set(&params, C.MTL_PORT_P, C.uint16_t(cfg.TxQueues))
	C.mtl_para_rx_queues_cnt_set(&params, C.MTL_PORT_P, C.uint16_t(cfg.RxQueues))
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

	return &Instance{handle: handle, dpdkPort: cfg.Port}, nil
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
// Note: MTL's kernel socket mode does not provide true PTP sync status.
// Locked is set to false since we cannot determine grandmaster sync state
// from mtl_ptp_read_time alone (it returns wall clock time, not offset).
func (inst *Instance) PTPStatus() (PTPStatus, error) {
	if inst.handle == nil {
		return PTPStatus{}, ErrST2110NotAvailable
	}
	ptpNs := C.mtl_ptp_read_time(inst.handle)
	return PTPStatus{
		Locked:   false, // cannot determine sync state from timestamp alone
		OffsetNs: int64(ptpNs),
	}, nil
}

// parseIPv4 parses a dotted-quad IPv4 string and returns the 4 bytes.
func parseIPv4(s string) (a, b, c, d C.uint8_t, err error) {
	ip := net.ParseIP(s)
	if ip == nil {
		return 0, 0, 0, 0, fmt.Errorf("invalid IP: %s", s)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return 0, 0, 0, 0, fmt.Errorf("not IPv4: %s", s)
	}
	return C.uint8_t(ip4[0]), C.uint8_t(ip4[1]), C.uint8_t(ip4[2]), C.uint8_t(ip4[3]), nil
}

// fpsToMTL converts a rational frame rate to the MTL fps enum.
// Uses exact rational matching to avoid float comparison ambiguity.
// Panics on unsupported frame rates to prevent silent misconfiguration.
func fpsToMTL(num, den int) C.enum_st_fps {
	if den == 0 {
		panic(fmt.Sprintf("st2110: fpsToMTL: zero denominator (num=%d)", num))
	}

	// Match by exact rational value (num/den) to avoid float ambiguity.
	// Normalize: 30000/1001 and 60000/2002 should both match.
	// Use cross-multiplication: a/b == c/d iff a*d == b*c.
	type rate struct{ num, den int }
	table := []struct {
		r   rate
		fps C.enum_st_fps
	}{
		{rate{24000, 1001}, C.ST_FPS_P23_98},
		{rate{24, 1}, C.ST_FPS_P24},
		{rate{25, 1}, C.ST_FPS_P25},
		{rate{30000, 1001}, C.ST_FPS_P29_97},
		{rate{30, 1}, C.ST_FPS_P30},
		{rate{50, 1}, C.ST_FPS_P50},
		{rate{60000, 1001}, C.ST_FPS_P59_94},
		{rate{60, 1}, C.ST_FPS_P60},
		{rate{120000, 1001}, C.ST_FPS_P119_88},
		{rate{120, 1}, C.ST_FPS_P120},
	}

	for _, entry := range table {
		if num*entry.r.den == den*entry.r.num {
			return entry.fps
		}
	}

	panic(fmt.Sprintf("st2110: fpsToMTL: unsupported frame rate %d/%d", num, den))
}
