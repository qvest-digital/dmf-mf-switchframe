package sidecar

// Protocol message type constants. These are the Type field values in
// sidecar-to-host messages, forming the wire protocol contract.
const (
	// MsgMaskReady is sent when the sidecar exports a segmentation mask IPC handle.
	MsgMaskReady = "mask_ready"

	// MsgSlotDone is sent when the sidecar finishes processing a segmentation frame slot.
	MsgSlotDone = "slot_done"

	// MsgARSlotDone is sent when the sidecar finishes processing an AR inference frame slot.
	MsgARSlotDone = "ar_slot_done"

	// MsgTrackSlotDone is sent when the sidecar finishes processing a tracking frame slot.
	MsgTrackSlotDone = "track_slot_done"

	// MsgOcclusionReady is sent when the sidecar exports depth/SAM IPC handles.
	MsgOcclusionReady = "occlusion_ready"

	// MsgPoseResult is sent when the sidecar produces a camera pose from visual tracking.
	MsgPoseResult = "pose_result"

	// MsgStatus is the response to a "status" command.
	MsgStatus = "status"

	// MsgOK is a generic success response.
	MsgOK = "ok"

	// MsgError is a generic error response.
	MsgError = "error"
)

// Protocol command constants. These are the Cmd field values in
// host-to-sidecar messages.
const (
	CmdStatus             = "status"
	CmdEnableSegmentation = "enable_segmentation"
	CmdDisableSegmentation = "disable_segmentation"
	CmdEnableDepth        = "enable_depth"
	CmdDisableDepth       = "disable_depth"
	CmdEnableSAM          = "enable_sam"
	CmdDisableSAM         = "disable_sam"
	CmdAddPrompt          = "add_prompt"
	CmdRemovePrompt       = "remove_prompt"
	CmdEnableTracking     = "enable_tracking"
	CmdDisableTracking    = "disable_tracking"
	CmdGPUPoolInit        = "gpu_pool_init"
	CmdShutdown           = "shutdown"
)

// Source key prefixes used in shared memory slot source key field.
const (
	// SourceKeyAR indicates an AR inference request (depth + SAM).
	SourceKeyAR = "__ar__"

	// SourceKeyTrackPrefix is the prefix for visual tracking requests.
	// The full key is "__track:" + sourceKey.
	SourceKeyTrackPrefix = "__track:"
)

// BuildDevPtrToSlotMap creates a mapping from GPU pool device pointers to slot indices.
// Used by all proxy types for zero-copy frame submission.
func BuildDevPtrToSlotMap(pool *CUDAGPUPool) map[uintptr]int {
	if pool == nil {
		return nil
	}
	slotCount := pool.SlotCount()
	m := make(map[uintptr]int, slotCount)
	for i := 0; i < slotCount; i++ {
		ptr := pool.FrameDevPtr(i)
		if ptr != 0 {
			m[ptr] = i
		}
	}
	return m
}
