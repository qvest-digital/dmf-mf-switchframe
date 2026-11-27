package mxl

import "fmt"

// checkGrainPayloadFits validates a grain payload length against the grain
// slot capacity (see discreteGrainCapacity).
//
// discreteFlowWriter.WriteGrain memcpys the payload into the MXL shared-memory
// ring before CommitGrain, so an oversized payload would silently overwrite
// adjacent grains/headers consumed by other processes -- SDK-side commit
// validation cannot prevent it. This check must run before the copy.
//
// Kept in a file without the cgo/mxl build tags so it is unit-testable in
// default builds.
func checkGrainPayloadFits(dataLen int, grainCapacity uint64) error {
	if dataLen < 0 || uint64(dataLen) > grainCapacity {
		return fmt.Errorf("mxl: grain payload %d bytes exceeds grain capacity %d bytes", dataLen, grainCapacity)
	}
	return nil
}

// discreteGrainCapacity returns the true byte capacity of a discrete grain
// slot.
//
// The SDK sets a slot header's grainSize to the slot capacity exactly once,
// at flow creation (FlowManager.cpp). mxlFlowWriterOpenGrain returns the
// stored header verbatim (only the index is updated) and CommitGrain writes
// back whatever header the writer passes in. WriteGrain commits a video
// grain at grainSize = len(data), and a flow written by an older build may
// hold data grains committed at their frame length, so once the ring wraps
// the grainSize returned by openGrain can be the previous occupant's payload
// length, NOT the slot capacity. Treating it as capacity would ratchet each
// slot's usable size monotonically downward.
//
// The true capacity is instead derived from two values the SDK never mutates
// after flow creation:
//
//   - sliceBytes: the per-plane slice sizes from the immutable flow config
//     (mxlDiscreteFlowConfigInfo.sliceSizes), summed across planes. Captured
//     by OpenWriter from mxlCreateFlowWriter's config-info out-parameter.
//   - totalSlices: from the grain header, set once at flow creation and
//     passed through unmodified by every commit.
//
// capacity = sliceBytes x totalSlices, matching the SDK's own payload-size
// computation (FlowParser::getPayloadSize): for video/v210 it is
// line-bytes x lines, for v210a (fill+key line bytes) x lines, and for data
// flows 1 byte x MXL_DATA_FORMAT_GRAIN_SIZE (4096).
//
// headerGrainSize (the grainSize reported by openGrain) is used only as a
// fallback when sliceBytes is unavailable, preserving the previous
// bounds-check behaviour for unexpected configs.
func discreteGrainCapacity(sliceBytes uint64, totalSlices uint16, headerGrainSize uint32) uint64 {
	if sliceBytes == 0 {
		return uint64(headerGrainSize)
	}
	return sliceBytes * uint64(totalSlices)
}

// fillDataGrain copies frame into slot, the whole payload of a data grain,
// zeroes the rest of the slot and returns the grain size to commit: the
// whole slot. A reader on another node through a flow mirror receives a
// data grain whole only when it is committed at the slot size; committed
// at the frame length, the mirror copy carries the header's size with only
// part of the payload. The RFC 8331 Length field says where the frame ends,
// and the zeroed tail leaves nothing of a previous, longer frame behind.
func fillDataGrain(slot, frame []byte) uint32 {
	n := copy(slot, frame)
	clear(slot[n:])
	return uint32(len(slot))
}
