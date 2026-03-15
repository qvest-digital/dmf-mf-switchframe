//go:build cgo && mxl

package mxl

/*
#include <mxl/mxl.h>
#include <mxl/flow.h>
#include <mxl/flowinfo.h>
#include <mxl/time.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"fmt"
	"sync"
	"time"
	"unsafe"
)

func init() {
	// Wire CurrentIndex to the real MXL time function.
	CurrentIndex = func(rate Rational) uint64 {
		if rate.Denominator == 0 || rate.Numerator == 0 {
			return 0
		}
		cr := C.mxlRational{
			numerator:   C.int64_t(rate.Numerator),
			denominator: C.int64_t(rate.Denominator),
		}
		return uint64(C.mxlGetCurrentIndex(&cr))
	}
}

// statusError converts an mxlStatus to a Go error.
// Transient errors (too late, too early, timeout) wrap the corresponding
// sentinel errors so callers can use errors.Is for classification.
func statusError(status C.mxlStatus, context string) error {
	switch status {
	case C.MXL_STATUS_OK:
		return nil
	case C.MXL_ERR_FLOW_NOT_FOUND:
		return fmt.Errorf("mxl: %s: flow not found", context)
	case C.MXL_ERR_OUT_OF_RANGE_TOO_LATE:
		return fmt.Errorf("%w: %s: grain expired", ErrTooLate, context)
	case C.MXL_ERR_OUT_OF_RANGE_TOO_EARLY:
		return fmt.Errorf("%w: %s: grain not yet available", ErrTooEarly, context)
	case C.MXL_ERR_INVALID_FLOW_READER:
		return fmt.Errorf("mxl: %s: invalid flow reader", context)
	case C.MXL_ERR_INVALID_FLOW_WRITER:
		return fmt.Errorf("mxl: %s: invalid flow writer", context)
	case C.MXL_ERR_TIMEOUT:
		return fmt.Errorf("%w: %s", ErrTimeout, context)
	case C.MXL_ERR_INVALID_ARG:
		return fmt.Errorf("mxl: %s: invalid argument", context)
	case C.MXL_ERR_CONFLICT:
		return fmt.Errorf("mxl: %s: conflict", context)
	case C.MXL_ERR_PERMISSION_DENIED:
		return fmt.Errorf("mxl: %s: permission denied", context)
	case C.MXL_ERR_FLOW_INVALID:
		return fmt.Errorf("mxl: %s: flow invalid (writer crashed?)", context)
	default:
		return fmt.Errorf("mxl: %s: error code %d", context, int(status))
	}
}

// Instance wraps an MXL SDK instance handle.
type Instance struct {
	handle    C.mxlInstance
	stopGC    chan struct{}    // signals the GC goroutine to stop
	gcWG      sync.WaitGroup  // waits for gcLoop to exit before destroying handle
	closeOnce sync.Once       // ensures Close is safe for concurrent calls
	closeErr  error           // captured error from the single Close execution
}

// NewInstance creates an MXL instance for the given domain path
// (e.g. "/dev/shm/mxl" on Linux).
func NewInstance(domain string) (*Instance, error) {
	cDomain := C.CString(domain)
	defer C.free(unsafe.Pointer(cDomain))

	handle := C.mxlCreateInstance(cDomain, nil)
	if handle == nil {
		return nil, fmt.Errorf("mxl: failed to create instance for domain %q", domain)
	}
	inst := &Instance{handle: handle, stopGC: make(chan struct{})}
	inst.gcWG.Add(1)
	go inst.gcLoop()
	return inst, nil
}

// gcLoop periodically calls mxlGarbageCollectFlows to clean up stale flows
// from crashed writers. The SDK runs GC once at instance creation, but the
// docs say it "should be called periodically on a long running application."
func (inst *Instance) gcLoop() {
	defer inst.gcWG.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-inst.stopGC:
			return
		case <-ticker.C:
			C.mxlGarbageCollectFlows(inst.handle)
		}
	}
}

// OpenReader opens a discrete (video/data) flow for reading.
func (inst *Instance) OpenReader(flowID string) (DiscreteReader, error) {
	cFlowID := C.CString(flowID)
	defer C.free(unsafe.Pointer(cFlowID))

	var reader C.mxlFlowReader
	status := C.mxlCreateFlowReader(inst.handle, cFlowID, nil, &reader)
	if err := statusError(status, "open reader"); err != nil {
		return nil, err
	}

	var configInfo C.mxlFlowConfigInfo
	status = C.mxlFlowReaderGetConfigInfo(reader, &configInfo)
	if err := statusError(status, "get reader config"); err != nil {
		C.mxlReleaseFlowReader(inst.handle, reader)
		return nil, err
	}

	return &discreteFlowReader{
		instance: inst.handle,
		reader:   reader,
		config:   convertFlowConfig(&configInfo),
	}, nil
}

// OpenAudioReader opens a continuous (audio) flow for reading.
func (inst *Instance) OpenAudioReader(flowID string) (ContinuousReader, error) {
	cFlowID := C.CString(flowID)
	defer C.free(unsafe.Pointer(cFlowID))

	var reader C.mxlFlowReader
	status := C.mxlCreateFlowReader(inst.handle, cFlowID, nil, &reader)
	if err := statusError(status, "open audio reader"); err != nil {
		return nil, err
	}

	var configInfo C.mxlFlowConfigInfo
	status = C.mxlFlowReaderGetConfigInfo(reader, &configInfo)
	if err := statusError(status, "get audio reader config"); err != nil {
		C.mxlReleaseFlowReader(inst.handle, reader)
		return nil, err
	}

	return &continuousFlowReader{
		instance: inst.handle,
		reader:   reader,
		config:   convertFlowConfig(&configInfo),
	}, nil
}

// OpenWriter opens a discrete (video/data) flow for writing.
func (inst *Instance) OpenWriter(flowDef string) (DiscreteWriter, error) {
	cFlowDef := C.CString(flowDef)
	defer C.free(unsafe.Pointer(cFlowDef))

	var writer C.mxlFlowWriter
	var configInfo C.mxlFlowConfigInfo
	var created C.bool
	status := C.mxlCreateFlowWriter(inst.handle, cFlowDef, nil, &writer, &configInfo, &created)
	if err := statusError(status, "open writer"); err != nil {
		return nil, err
	}

	return &discreteFlowWriter{
		instance: inst.handle,
		writer:   writer,
	}, nil
}

// OpenAudioWriter opens a continuous (audio) flow for writing.
func (inst *Instance) OpenAudioWriter(flowDef string) (ContinuousWriter, error) {
	cFlowDef := C.CString(flowDef)
	defer C.free(unsafe.Pointer(cFlowDef))

	var writer C.mxlFlowWriter
	var configInfo C.mxlFlowConfigInfo
	var created C.bool
	status := C.mxlCreateFlowWriter(inst.handle, cFlowDef, nil, &writer, &configInfo, &created)
	if err := statusError(status, "open audio writer"); err != nil {
		return nil, err
	}

	return &continuousFlowWriter{
		instance: inst.handle,
		writer:   writer,
	}, nil
}

// GetFlowConfig returns configuration for a flow by UUID.
func (inst *Instance) GetFlowConfig(flowID string) (FlowConfig, error) {
	cFlowID := C.CString(flowID)
	defer C.free(unsafe.Pointer(cFlowID))

	var reader C.mxlFlowReader
	status := C.mxlCreateFlowReader(inst.handle, cFlowID, nil, &reader)
	if err := statusError(status, "get flow config"); err != nil {
		return FlowConfig{}, err
	}
	defer C.mxlReleaseFlowReader(inst.handle, reader)

	var configInfo C.mxlFlowConfigInfo
	status = C.mxlFlowReaderGetConfigInfo(reader, &configInfo)
	if err := statusError(status, "get flow config info"); err != nil {
		return FlowConfig{}, err
	}

	return convertFlowConfig(&configInfo), nil
}

// IsFlowActive checks whether a flow has an active writer.
func (inst *Instance) IsFlowActive(flowID string) (bool, error) {
	cFlowID := C.CString(flowID)
	defer C.free(unsafe.Pointer(cFlowID))

	var active C.bool
	status := C.mxlIsFlowActive(inst.handle, cFlowID, &active)
	if err := statusError(status, "is flow active"); err != nil {
		return false, err
	}
	return bool(active), nil
}

// Close releases the MXL instance and stops the GC goroutine.
// It is safe to call Close concurrently or multiple times.
func (inst *Instance) Close() error {
	inst.closeOnce.Do(func() {
		if inst.handle != nil {
			close(inst.stopGC)
			inst.gcWG.Wait() // ensure gcLoop exits before destroying handle
			status := C.mxlDestroyInstance(inst.handle)
			inst.handle = nil
			inst.closeErr = statusError(status, "destroy instance")
		}
	})
	return inst.closeErr
}

// convertFlowConfig converts a C mxlFlowConfigInfo to Go FlowConfig.
func convertFlowConfig(c *C.mxlFlowConfigInfo) FlowConfig {
	fc := FlowConfig{
		Format: DataFormat(c.common.format),
		GrainRate: Rational{
			Numerator:   int64(c.common.grainRate.numerator),
			Denominator: int64(c.common.grainRate.denominator),
		},
	}

	// Access the union fields based on format.
	// C union is laid out at the same memory offset.
	switch fc.Format {
	case DataFormatVideo, DataFormatData:
		// Discrete flow — access as discrete union member.
		discrete := (*C.mxlDiscreteFlowConfigInfo)(unsafe.Pointer(&c.anon0[0]))
		for i := 0; i < 4; i++ {
			fc.SliceSizes[i] = uint32(discrete.sliceSizes[i])
		}
		fc.GrainCount = uint32(discrete.grainCount)
	case DataFormatAudio:
		// Continuous flow — access as continuous union member.
		continuous := (*C.mxlContinuousFlowConfigInfo)(unsafe.Pointer(&c.anon0[0]))
		fc.ChannelCount = uint32(continuous.channelCount)
		fc.BufferLength = uint32(continuous.bufferLength)
	}

	return fc
}

// --- Discrete (video/data) reader ---

type discreteFlowReader struct {
	instance C.mxlInstance
	reader   C.mxlFlowReader
	config   FlowConfig
}

func (r *discreteFlowReader) ReadGrain(index uint64, timeoutNs uint64) ([]byte, GrainInfo, error) {
	var grain C.mxlGrainInfo
	var payload *C.uint8_t

	status := C.mxlFlowReaderGetGrain(r.reader, C.uint64_t(index),
		C.uint64_t(timeoutNs), &grain, &payload)
	if err := statusError(status, "read grain"); err != nil {
		return nil, GrainInfo{}, err
	}

	info := GrainInfo{
		Index:       uint64(grain.index),
		GrainSize:   uint32(grain.grainSize),
		TotalSlices: uint16(grain.totalSlices),
		ValidSlices: uint16(grain.validSlices),
		Invalid:     grain.flags&0x01 != 0, // MXL_GRAIN_FLAG_INVALID
	}

	// Copy payload from shared memory into Go-owned slice.
	size := int(grain.grainSize)
	if size == 0 {
		return nil, info, nil
	}
	data := make([]byte, size)
	C.memcpy(unsafe.Pointer(&data[0]), unsafe.Pointer(payload), C.size_t(size))

	return data, info, nil
}

func (r *discreteFlowReader) ConfigInfo() FlowConfig {
	return r.config
}

func (r *discreteFlowReader) HeadIndex() (uint64, error) {
	var info C.mxlFlowRuntimeInfo
	status := C.mxlFlowReaderGetRuntimeInfo(r.reader, &info)
	if err := statusError(status, "head index"); err != nil {
		return 0, err
	}
	return uint64(info.headIndex), nil
}

func (r *discreteFlowReader) Close() error {
	status := C.mxlReleaseFlowReader(r.instance, r.reader)
	return statusError(status, "release reader")
}

// --- Continuous (audio) reader ---

type continuousFlowReader struct {
	instance C.mxlInstance
	reader   C.mxlFlowReader
	config   FlowConfig
}

func (r *continuousFlowReader) ReadSamples(index uint64, count int, timeoutNs uint64) ([][]float32, error) {
	var slices C.mxlWrappedMultiBufferSlice

	status := C.mxlFlowReaderGetSamples(r.reader, C.uint64_t(index),
		C.size_t(count), C.uint64_t(timeoutNs), &slices)
	if err := statusError(status, "read samples"); err != nil {
		return nil, err
	}

	channels := int(slices.count)
	stride := int(slices.stride)
	result := make([][]float32, channels)

	for ch := 0; ch < channels; ch++ {
		result[ch] = make([]float32, count)
		// Each channel's data may be split across two ring buffer fragments.
		offset := ch * stride
		copied := 0

		for frag := 0; frag < 2; frag++ {
			fragPtr := slices.base.fragments[frag].pointer
			fragSize := int(slices.base.fragments[frag].size)
			if fragPtr == nil || fragSize == 0 {
				continue
			}
			// Adjust pointer for this channel's offset within the fragment.
			// Each channel has its own ring buffer; stride is the byte offset
			// between channels. fragSize is bytes for ONE channel's region.
			chPtr := unsafe.Add(fragPtr, offset)
			samplesInFrag := fragSize / 4 // float32 = 4 bytes per sample
			if copied+samplesInFrag > count {
				samplesInFrag = count - copied
			}
			src := unsafe.Slice((*float32)(chPtr), samplesInFrag)
			copy(result[ch][copied:], src)
			copied += samplesInFrag
		}
	}

	return result, nil
}

func (r *continuousFlowReader) ConfigInfo() FlowConfig {
	return r.config
}

func (r *continuousFlowReader) HeadIndex() (uint64, error) {
	var info C.mxlFlowRuntimeInfo
	status := C.mxlFlowReaderGetRuntimeInfo(r.reader, &info)
	if err := statusError(status, "audio head index"); err != nil {
		return 0, err
	}
	return uint64(info.headIndex), nil
}

func (r *continuousFlowReader) Close() error {
	status := C.mxlReleaseFlowReader(r.instance, r.reader)
	return statusError(status, "release audio reader")
}

// --- Discrete (video/data) writer ---

type discreteFlowWriter struct {
	instance C.mxlInstance
	writer   C.mxlFlowWriter
}

func (w *discreteFlowWriter) WriteGrain(index uint64, data []byte) error {
	if len(data) == 0 {
		return nil
	}

	var grain C.mxlGrainInfo
	var payload *C.uint8_t

	status := C.mxlFlowWriterOpenGrain(w.writer, C.uint64_t(index), &grain, &payload)
	if err := statusError(status, "open grain for write"); err != nil {
		return err
	}

	// Copy Go data into shared memory.
	C.memcpy(unsafe.Pointer(payload), unsafe.Pointer(&data[0]), C.size_t(len(data)))

	// Commit — mark all slices valid, clear invalid flag.
	grain.flags = 0
	grain.validSlices = grain.totalSlices
	status = C.mxlFlowWriterCommitGrain(w.writer, &grain)
	return statusError(status, "commit grain")
}

func (w *discreteFlowWriter) Close() error {
	status := C.mxlReleaseFlowWriter(w.instance, w.writer)
	return statusError(status, "release writer")
}

// --- Continuous (audio) writer ---

type continuousFlowWriter struct {
	instance C.mxlInstance
	writer   C.mxlFlowWriter
}

func (w *continuousFlowWriter) WriteSamples(index uint64, channels [][]float32) error {
	if len(channels) == 0 {
		return nil
	}
	count := len(channels[0])

	var slices C.mxlMutableWrappedMultiBufferSlice
	status := C.mxlFlowWriterOpenSamples(w.writer, C.uint64_t(index),
		C.size_t(count), &slices)
	if err := statusError(status, "open samples for write"); err != nil {
		return err
	}

	stride := int(slices.stride)
	for ch := 0; ch < len(channels); ch++ {
		offset := ch * stride
		copied := 0

		for frag := 0; frag < 2; frag++ {
			fragPtr := slices.base.fragments[frag].pointer
			fragSize := int(slices.base.fragments[frag].size)
			if fragPtr == nil || fragSize == 0 {
				continue
			}
			// Each channel has its own ring buffer; stride is the byte offset
			// between channels. fragSize is bytes for ONE channel's region.
			chPtr := unsafe.Add(fragPtr, offset)
			samplesInFrag := fragSize / 4 // float32 = 4 bytes per sample
			if copied+samplesInFrag > count {
				samplesInFrag = count - copied
			}
			dst := unsafe.Slice((*float32)(chPtr), samplesInFrag)
			copy(dst, channels[ch][copied:])
			copied += samplesInFrag
		}
	}

	status = C.mxlFlowWriterCommitSamples(w.writer)
	return statusError(status, "commit samples")
}

func (w *continuousFlowWriter) Close() error {
	status := C.mxlReleaseFlowWriter(w.instance, w.writer)
	return statusError(status, "release audio writer")
}
