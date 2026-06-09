package mxl

import "fmt"

// checkGrainPayloadFits validates a grain payload length against the grain
// slot capacity reported by mxlFlowWriterOpenGrain (mxlGrainInfo.grainSize).
//
// discreteFlowWriter.WriteGrain memcpys the payload into the MXL shared-memory
// ring before CommitGrain, so an oversized payload would silently overwrite
// adjacent grains/headers consumed by other processes — SDK-side commit
// validation cannot prevent it. This check must run before the copy.
//
// Kept in a file without the cgo/mxl build tags so it is unit-testable in
// default builds.
func checkGrainPayloadFits(dataLen int, grainCapacity uint32) error {
	if dataLen < 0 || uint64(dataLen) > uint64(grainCapacity) {
		return fmt.Errorf("mxl: grain payload %d bytes exceeds grain capacity %d bytes", dataLen, grainCapacity)
	}
	return nil
}
