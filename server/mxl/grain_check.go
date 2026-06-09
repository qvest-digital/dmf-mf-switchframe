package mxl

import "fmt"

// checkGrainPayloadFits validates a grain payload length against the grain
// slot capacity (see discreteGrainCapacity).
//
// discreteFlowWriter.WriteGrain memcpys the payload into the MXL shared-memory
// ring before CommitGrain, so an oversized payload would silently overwrite
// adjacent grains/headers consumed by other processes — SDK-side commit
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
// back whatever header the writer passes in. WriteGrain deliberately commits
// grainSize = len(data) — readers depend on it as the actual payload length
// of variable-length data grains — so once the ring wraps, the grainSize
// returned by openGrain is the previous occupant's payload length, NOT the
// slot capacity. Treating it as capacity would ratchet each slot's usable
// size monotonically downward, breaking variable-length data flows (e.g.
// SCTE-104) after one ring revolution.
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
// capacity = sliceBytes × totalSlices, matching the SDK's own payload-size
// computation (FlowParser::getPayloadSize): for video/v210 it is
// line-bytes × lines, for v210a (fill+key line bytes) × lines, and for data
// flows 1 byte × MXL_DATA_FORMAT_GRAIN_SIZE (4096).
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
