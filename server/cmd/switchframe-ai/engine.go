//go:build cgo && cuda && tensorrt

package main

/*
#include <cuda.h>
#include <cuda_runtime.h>
*/
import "C"

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/zsiec/switchframe/server/arinfer"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/sidecar"
	"github.com/zsiec/switchframe/server/tracking"
)

// engine dispatches control messages and processes GPU frames.
// Per-source segmentation sessions use the gpu.SegmentationEngine for
// real TRT inference. The engine allocates per-source CUDA streams,
// buffers, and TRT execution contexts internally.
type engine struct {
	pool   *sidecar.CUDAGPUPool
	worker *sidecar.Worker

	// GPU context and TRT segmentation engine (nil if --model not provided).
	gpuCtx    *gpu.Context
	modelPath string
	segEngine *gpu.SegmentationEngine
	segReady  chan struct{} // closed when TRT engine finishes loading (async build)

	// AR inference engine for depth + SAM (nil if --models-dir not provided).
	inferEngine *arinfer.Engine

	wg sync.WaitGroup // tracks frameLoop goroutine for clean shutdown
	mu sync.Mutex
	// Per-source segmentation sessions (for processFrame dispatch).
	segSessions map[string]*segSession
	// Slot → source key mapping for infer commands.
	slotSource map[int]string
	// Depth estimation state.
	depthEnabled bool
	depthBufPtr  unsafe.Pointer // float16 depth map buffer
	depthW, depthH int
	depthHandleExported bool

	// SAM state.
	samEnabled bool
	samMaskPtr unsafe.Pointer // uint8 SAM mask buffer
	samMaskW, samMaskH int
	samHandleExported bool

	// Visual tracking state.
	trackingSources map[string]bool                    // source_id → enabled
	trackingLocals  map[string]*tracking.VisualLocalizer // source_id → localizer (nil if models unavailable)
	modelsDir       string                              // path to TRT .plan files
	trackingMapDir  string                              // path to studio map data
}

// segSession holds per-source segmentation tracking state in the sidecar.
// The actual GPU buffers are managed by gpu.SegmentationEngine.
type segSession struct {
	sourceID      string
	width, height int
	stubMaskPtr   unsafe.Pointer // non-nil only when no TRT model (fallback stub mask)
}

func newEngine(pool *sidecar.CUDAGPUPool, worker *sidecar.Worker, gpuCtx *gpu.Context, modelPath string, inferEngine *arinfer.Engine, modelsDir, trackingMapDir string) *engine {
	e := &engine{
		pool:            pool,
		worker:          worker,
		gpuCtx:          gpuCtx,
		modelPath:       modelPath,
		inferEngine:     inferEngine,
		modelsDir:       modelsDir,
		trackingMapDir:  trackingMapDir,
		segSessions:     make(map[string]*segSession),
		slotSource:      make(map[int]string),
		trackingSources: make(map[string]bool),
		trackingLocals:  make(map[string]*tracking.VisualLocalizer),
		segReady:        make(chan struct{}),
	}

	// Build TRT engine asynchronously so the command loop starts immediately.
	// The engine build from ONNX takes 30-60s on first run (cached .plan after).
	// enable_segmentation waits on segReady before proceeding.
	if modelPath != "" && gpuCtx != nil {
		go func() {
			defer close(e.segReady)
			segEng, err := gpu.NewSegmentationEngine(gpuCtx, modelPath)
			if err != nil {
				slog.Error("TRT segmentation engine failed to load", "model", modelPath, "error", err)
				return
			}
			e.mu.Lock()
			e.segEngine = segEng
			e.mu.Unlock()
			slog.Info("TRT segmentation engine loaded", "model", modelPath)
		}()
	} else {
		close(e.segReady) // no model to load
	}

	return e
}

func (e *engine) close() {
	// Wait for frameLoop goroutine to exit before destroying resources.
	e.wg.Wait()

	e.mu.Lock()
	// Nil out depth/SAM pointers BEFORE closing the inference engine.
	// When inferEngine is present, these point to engine-managed memory
	// that will be freed by inferEngine.Close(). Only cudaFree pointers
	// we allocated ourselves (stub path, inferEngine==nil).
	hadInferEngine := e.inferEngine != nil
	if hadInferEngine {
		// Engine-managed: just nil our references, engine.Close frees them.
		e.depthBufPtr = nil
		e.samMaskPtr = nil
	}
	e.mu.Unlock()

	// Close AR inference engine (depth + SAM). Frees all model GPU resources.
	if e.inferEngine != nil {
		e.inferEngine.Close()
		e.inferEngine = nil
	}
	// Close TRT segmentation engine (frees all per-source GPU resources).
	if e.segEngine != nil {
		e.segEngine.Close()
		e.segEngine = nil
	}
	if e.gpuCtx != nil {
		e.gpuCtx.Close()
		e.gpuCtx = nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Free any stub mask buffers (allocated when no TRT model is loaded).
	for _, sess := range e.segSessions {
		if sess.stubMaskPtr != nil {
			C.cudaFree(sess.stubMaskPtr)
		}
	}
	e.segSessions = nil

	// Only free depth/SAM if they were stub-allocated (no inference engine).
	if !hadInferEngine {
		if e.depthBufPtr != nil {
			C.cudaFree(e.depthBufPtr)
			e.depthBufPtr = nil
		}
		if e.samMaskPtr != nil {
			C.cudaFree(e.samMaskPtr)
			e.samMaskPtr = nil
		}
	}

	// Close all visual tracking localizers.
	for _, loc := range e.trackingLocals {
		if loc != nil {
			loc.Close()
		}
	}
	e.trackingLocals = nil
}

// handleMessage dispatches a control message to the appropriate handler.
func (e *engine) handleMessage(msg *sidecar.Message) *sidecar.Message {
	switch msg.Cmd {
	case "status":
		return e.handleStatus()
	case "enable_segmentation":
		return e.handleEnableSegmentation(msg)
	case "disable_segmentation":
		return e.handleDisableSegmentation(msg)
	case "infer":
		return e.handleInfer(msg)
	case "infer_ar":
		return e.handleInferAR(msg)
	case "enable_depth":
		return e.handleEnableDepth(msg)
	case "disable_depth":
		return e.handleDisableDepth(msg)
	case "enable_sam":
		return e.handleEnableSAM(msg)
	case "disable_sam":
		return e.handleDisableSAM(msg)
	case "add_prompt":
		return e.handleAddPrompt(msg)
	case "remove_prompt":
		return e.handleRemovePrompt(msg)
	case "process_frame":
		return e.handleProcessFrame(msg)
	case "enable_tracking":
		return e.handleEnableTracking(msg)
	case "disable_tracking":
		return e.handleDisableTracking(msg)
	case "track_frame":
		return e.handleTrackFrame(msg)
	default:
		return &sidecar.Message{Type: sidecar.MsgError, Error: fmt.Sprintf("unknown command: %s", msg.Cmd)}
	}
}

// --- Status ---

func (e *engine) handleStatus() *sidecar.Message {
	e.mu.Lock()
	defer e.mu.Unlock()

	sources := make([]string, 0, len(e.segSessions))
	for src := range e.segSessions {
		sources = append(sources, src)
	}

	depthReady := false
	samReady := false
	if e.inferEngine != nil {
		st := e.inferEngine.Status()
		depthReady = st.DepthReady
		samReady = st.SAMReady
	}

	return &sidecar.Message{
		Type: sidecar.MsgStatus,
		Data: map[string]any{
			"segmentation_ready":   true,
			"segmentation_sources": sources,
			"depth_ready":          depthReady,
			"depth_enabled":        e.depthEnabled,
			"sam_ready":            samReady,
			"sam_enabled":          e.samEnabled,
		},
	}
}

// --- Segmentation ---

func (e *engine) handleEnableSegmentation(msg *sidecar.Message) *sidecar.Message {
	// Wait for TRT engine to finish loading (async build from ONNX).
	<-e.segReady

	sourceID, err := extractSourceID(msg)
	if err != nil {
		return &sidecar.Message{Type: sidecar.MsgError, Error: err.Error()}
	}

	// Extract width/height from config.
	w, h := extractDimensions(msg)
	if w == 0 || h == 0 {
		// Use pool dimensions as fallback.
		if e.pool != nil {
			cfg := e.pool.Config()
			w, h = cfg.Width, cfg.Height
		}
	}
	if w == 0 || h == 0 {
		return &sidecar.Message{Type: sidecar.MsgError, Error: "missing dimensions and no pool available"}
	}

	var maskPtr uintptr

	if e.segEngine != nil {
		// Real TRT path: enable on the engine (allocates CUDA stream, TRT context, all buffers).
		if enableErr := e.segEngine.EnableSource(sourceID, w, h, 0); enableErr != nil {
			return &sidecar.Message{Type: sidecar.MsgError, Error: fmt.Sprintf("enable source: %v", enableErr)}
		}
		maskPtr = e.segEngine.MaskDevPtrForSource(sourceID)
	} else {
		// No model loaded: allocate a bare mask buffer for IPC lifecycle testing.
		// The mask stays zero-filled (all background). Real inference requires --model.
		maskSize := C.size_t(w * h * 3 / 2)
		var maskDevPtr unsafe.Pointer
		if rc := C.cudaMalloc(&maskDevPtr, maskSize); rc != C.cudaSuccess {
			return &sidecar.Message{Type: sidecar.MsgError, Error: fmt.Sprintf("cudaMalloc mask: error %d", rc)}
		}
		C.cudaMemset(maskDevPtr, 0, maskSize)
		// Set UV plane to neutral chroma (128).
		uvOffset := C.size_t(w * h)
		uvSize := C.size_t(w * h / 2)
		C.cudaMemset(unsafe.Pointer(uintptr(maskDevPtr)+uintptr(uvOffset)), C.int(128), uvSize)
		maskPtr = uintptr(maskDevPtr)
		slog.Warn("segmentation enabled WITHOUT TRT model (stub mask)", "source", sourceID)
	}

	if maskPtr == 0 {
		return &sidecar.Message{Type: sidecar.MsgError, Error: "mask buffer not available after enable"}
	}

	// Track the session locally (for processFrame dispatch).
	e.mu.Lock()
	// Clean up old session if exists.
	if old, exists := e.segSessions[sourceID]; exists && old.stubMaskPtr != nil {
		C.cudaFree(old.stubMaskPtr)
	}
	e.segSessions[sourceID] = &segSession{
		sourceID:    sourceID,
		width:       w,
		height:      h,
		stubMaskPtr: nil, // set below if stub path
	}
	if e.segEngine == nil {
		e.segSessions[sourceID].stubMaskPtr = unsafe.Pointer(maskPtr)
	}
	e.mu.Unlock()

	// Export IPC handle for the mask buffer and send to host.
	if e.worker != nil {
		handle, exportErr := sidecar.ExportIPCHandle(maskPtr)
		if exportErr != nil {
			slog.Error("export mask IPC handle", "source", sourceID, "error", exportErr)
			return &sidecar.Message{Type: sidecar.MsgError, Error: fmt.Sprintf("export mask handle: %v", exportErr)}
		}

		maskMsg := &sidecar.Message{
			Type: sidecar.MsgMaskReady,
			Data: &sidecar.MaskHandleMessage{
				SourceID: sourceID,
				Handle:   handle,
				Width:    w,
				Height:   h,
			},
		}
		if sendErr := e.worker.Send(maskMsg); sendErr != nil {
			slog.Error("send mask_ready", "source", sourceID, "error", sendErr)
		}
	}

	slog.Info("segmentation enabled (TRT)", "source", sourceID, "size", [2]int{w, h})
	return &sidecar.Message{Type: sidecar.MsgOK}
}

func (e *engine) handleDisableSegmentation(msg *sidecar.Message) *sidecar.Message {
	sourceID, err := extractSourceID(msg)
	if err != nil {
		return &sidecar.Message{Type: sidecar.MsgError, Error: err.Error()}
	}

	if e.segEngine != nil {
		e.segEngine.DisableSource(sourceID)
	}

	e.mu.Lock()
	if sess, exists := e.segSessions[sourceID]; exists {
		if sess.stubMaskPtr != nil {
			C.cudaFree(sess.stubMaskPtr)
		}
		delete(e.segSessions, sourceID)
	}
	e.mu.Unlock()

	slog.Info("segmentation disabled", "source", sourceID)
	return &sidecar.Message{Type: sidecar.MsgOK}
}

// --- Infer (per-frame source assignment) ---

// handleInfer associates a GPU pool slot with a source key.
// The frameLoop will pick up the slot and process it.
func (e *engine) handleInfer(msg *sidecar.Message) *sidecar.Message {
	cfg, ok := msg.Config.(map[string]any)
	if !ok {
		return nil // silent — infer is fire-and-forget
	}
	slotF, _ := cfg["slot"].(float64)
	sourceID, _ := cfg["source_id"].(string)
	if sourceID == "" {
		return nil
	}

	slot := int(slotF)
	e.mu.Lock()
	e.slotSource[slot] = sourceID
	e.mu.Unlock()

	return nil // no response needed
}

// handleInferAR processes an AR inference request (depth + SAM) for a slot.
// Fire-and-forget: the sidecar processes the slot and sends ar_slot_done.
func (e *engine) handleInferAR(msg *sidecar.Message) *sidecar.Message {
	cfg, ok := msg.Config.(map[string]any)
	if !ok {
		return nil
	}
	slotF, _ := cfg["slot"].(float64)
	slot := int(slotF)

	if e.pool == nil {
		return nil
	}
	ctrl := e.pool.Control()
	if ctrl == nil || !ctrl.MarkProcessing(slot) {
		return nil
	}

	// Wait for host GPU work to complete before reading the frame.
	if err := e.pool.WaitReadyEvent(slot); err != nil {
		slog.Warn("infer_ar: wait ready event", "slot", slot, "error", err)
	}

	framePtr := e.pool.FrameDevPtr(slot)

	e.runARInference(framePtr)

	// Record done event so host knows our GPU work is complete.
	if err := e.pool.RecordDoneEvent(slot); err != nil {
		slog.Warn("infer_ar: record done event", "slot", slot, "error", err)
	}

	ctrl.MarkDone(slot)
	if e.worker != nil {
		_ = e.worker.Send(&sidecar.Message{
			Type: sidecar.MsgARSlotDone,
			Data: map[string]any{"slot": slot},
		})
	}

	return nil
}

// --- Depth ---

// Depth Anything V2 native output resolution.
const (
	depthModelW = 518
	depthModelH = 294
)

func (e *engine) handleEnableDepth(msg *sidecar.Message) *sidecar.Message {
	if e.inferEngine == nil {
		// No inference engine: allocate a bare stub buffer for IPC lifecycle testing.
		depthSize := C.size_t(depthModelW * depthModelH * 2)
		var depthPtr unsafe.Pointer
		rc := C.cudaMalloc(&depthPtr, depthSize)
		if rc != C.cudaSuccess {
			return &sidecar.Message{Type: sidecar.MsgError, Error: fmt.Sprintf("cudaMalloc depth: error %d", rc)}
		}
		C.cudaMemset(depthPtr, 0, depthSize)

		e.mu.Lock()
		if e.depthBufPtr != nil {
			C.cudaFree(e.depthBufPtr)
		}
		e.depthBufPtr = depthPtr
		e.depthW = depthModelW
		e.depthH = depthModelH
		e.depthEnabled = true
		e.depthHandleExported = false
		e.mu.Unlock()

		e.sendOcclusionHandles()
		slog.Warn("depth enabled WITHOUT inference engine (stub buffer)")
		return &sidecar.Message{Type: sidecar.MsgOK}
	}

	// Real path: enable depth via the inference engine (loads TRT model).
	// Extract optional plan path from config.
	planPath := ""
	if cfg, ok := msg.Config.(map[string]any); ok {
		planPath, _ = cfg["plan_path"].(string)
	}
	if err := e.inferEngine.EnableDepth(planPath); err != nil {
		return &sidecar.Message{Type: sidecar.MsgError, Error: fmt.Sprintf("enable depth: %v", err)}
	}

	// Get the depth output buffer pointer directly from the engine.
	// Available immediately after EnableDepth — the model allocates its
	// output buffer during construction, no ProcessFrame needed.
	depthPtr := e.inferEngine.DepthOutputPtr()
	dw, dh := e.inferEngine.DepthDims()

	e.mu.Lock()
	// Engine-managed buffer: just update our pointer reference.
	// The inference engine owns this memory and frees it on Close/Disable.
	e.depthBufPtr = unsafe.Pointer(depthPtr)
	e.depthW = dw
	e.depthH = dh
	e.depthEnabled = true
	e.depthHandleExported = false
	e.mu.Unlock()

	// Export IPC handle immediately since the buffer is available now.
	e.sendOcclusionHandles()

	slog.Info("depth enabled (TRT)", "size", [2]int{depthModelW, depthModelH})
	return &sidecar.Message{Type: sidecar.MsgOK}
}

func (e *engine) handleDisableDepth(_ *sidecar.Message) *sidecar.Message {
	if e.inferEngine != nil {
		e.inferEngine.DisableDepth()
	}

	e.mu.Lock()
	e.depthEnabled = false
	// Only free the buffer if it's a stub allocation (not engine-managed).
	if e.depthBufPtr != nil && e.inferEngine == nil {
		C.cudaFree(e.depthBufPtr)
	}
	e.depthBufPtr = nil
	e.depthHandleExported = false
	e.mu.Unlock()

	slog.Info("depth disabled")
	return &sidecar.Message{Type: sidecar.MsgOK}
}

// --- SAM ---

func (e *engine) handleEnableSAM(msg *sidecar.Message) *sidecar.Message {
	// Use pool dimensions for SAM mask resolution.
	w, h := 1920, 1080
	if e.pool != nil {
		cfg := e.pool.Config()
		if cfg.Width > 0 && cfg.Height > 0 {
			w, h = cfg.Width, cfg.Height
		}
	}

	if e.inferEngine == nil {
		// No inference engine: allocate a bare stub buffer for IPC lifecycle testing.
		maskSize := C.size_t(w * h)
		var maskPtr unsafe.Pointer
		rc := C.cudaMalloc(&maskPtr, maskSize)
		if rc != C.cudaSuccess {
			return &sidecar.Message{Type: sidecar.MsgError, Error: fmt.Sprintf("cudaMalloc sam mask: error %d", rc)}
		}
		C.cudaMemset(maskPtr, 0, maskSize)

		e.mu.Lock()
		if e.samMaskPtr != nil {
			C.cudaFree(e.samMaskPtr)
		}
		e.samMaskPtr = maskPtr
		e.samMaskW = w
		e.samMaskH = h
		e.samEnabled = true
		e.samHandleExported = false
		e.mu.Unlock()

		e.sendOcclusionHandles()
		slog.Warn("sam enabled WITHOUT inference engine (stub buffer)")
		return &sidecar.Message{Type: sidecar.MsgOK}
	}

	// Real path: enable SAM via the inference engine.
	encoderPlan, decoderPlan := "", ""
	if cfg, ok := msg.Config.(map[string]any); ok {
		encoderPlan, _ = cfg["encoder_plan"].(string)
		decoderPlan, _ = cfg["decoder_plan"].(string)
	}
	if err := e.inferEngine.EnableSAM(encoderPlan, decoderPlan); err != nil {
		return &sidecar.Message{Type: sidecar.MsgError, Error: fmt.Sprintf("enable SAM: %v", err)}
	}

	// Pre-allocate mask buffer at frame resolution so we can export
	// the IPC handle immediately, without waiting for first Propagate.
	maskPtr := e.inferEngine.PreallocSAMMask(w, h)

	e.mu.Lock()
	// Engine-managed buffer: just update our pointer reference.
	// The inference engine owns this memory and frees it on Close/Disable.
	e.samMaskPtr = unsafe.Pointer(maskPtr)
	e.samMaskW = w
	e.samMaskH = h
	e.samEnabled = true
	e.samHandleExported = false
	e.mu.Unlock()

	// Export IPC handle immediately since the buffer is pre-allocated.
	e.sendOcclusionHandles()

	slog.Info("sam enabled (TRT)", "mask_size", [2]int{w, h}, "mask_ptr", maskPtr)
	return &sidecar.Message{Type: sidecar.MsgOK}
}

func (e *engine) handleDisableSAM(_ *sidecar.Message) *sidecar.Message {
	if e.inferEngine != nil {
		e.inferEngine.DisableSAM()
	}

	e.mu.Lock()
	e.samEnabled = false
	// Only free the buffer if it's a stub allocation (not engine-managed).
	if e.samMaskPtr != nil && e.inferEngine == nil {
		C.cudaFree(e.samMaskPtr)
	}
	e.samMaskPtr = nil
	e.samHandleExported = false
	e.mu.Unlock()

	slog.Info("sam disabled")
	return &sidecar.Message{Type: sidecar.MsgOK}
}

// sendOcclusionHandles exports IPC handles for whichever depth/mask buffers
// exist and sends an occlusion_ready message to the host.
func (e *engine) sendOcclusionHandles() {
	if e.worker == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	data := map[string]any{}

	if e.depthBufPtr != nil && !e.depthHandleExported {
		handle, err := sidecar.ExportIPCHandle(uintptr(e.depthBufPtr))
		if err != nil {
			slog.Error("export depth IPC handle", "error", err)
			return
		}
		data["depth_handle"] = encodeBase64Handle(handle)
		data["depth_w"] = e.depthW
		data["depth_h"] = e.depthH
		e.depthHandleExported = true
	}

	if e.samMaskPtr != nil && !e.samHandleExported {
		handle, err := sidecar.ExportIPCHandle(uintptr(e.samMaskPtr))
		if err != nil {
			slog.Error("export sam mask IPC handle", "error", err)
			return
		}
		data["mask_handle"] = encodeBase64Handle(handle)
		data["mask_w"] = e.samMaskW
		data["mask_h"] = e.samMaskH
		e.samHandleExported = true
	}

	if len(data) == 0 {
		return
	}

	if err := e.worker.Send(&sidecar.Message{
		Type: sidecar.MsgOcclusionReady,
		Data: data,
	}); err != nil {
		slog.Error("send occlusion_ready", "error", err)
	}
}

// --- Prompts ---

func (e *engine) handleAddPrompt(msg *sidecar.Message) *sidecar.Message {
	if e.inferEngine == nil {
		slog.Info("add_prompt (no engine)", "config", msg.Config)
		return &sidecar.Message{Type: sidecar.MsgOK}
	}

	// Parse SAMPrompt from config.
	prompt, err := extractSAMPrompt(msg)
	if err != nil {
		return &sidecar.Message{Type: sidecar.MsgError, Error: fmt.Sprintf("add_prompt: %v", err)}
	}
	if err := e.inferEngine.AddPrompt(prompt); err != nil {
		return &sidecar.Message{Type: sidecar.MsgError, Error: fmt.Sprintf("add_prompt: %v", err)}
	}
	slog.Info("prompt added", "id", prompt.ID, "type", prompt.Type)
	return &sidecar.Message{Type: sidecar.MsgOK}
}

func (e *engine) handleRemovePrompt(msg *sidecar.Message) *sidecar.Message {
	if e.inferEngine == nil {
		slog.Info("remove_prompt (no engine)", "config", msg.Config)
		return &sidecar.Message{Type: sidecar.MsgOK}
	}

	cfg, ok := msg.Config.(map[string]any)
	if !ok {
		return &sidecar.Message{Type: sidecar.MsgError, Error: "missing config"}
	}
	id, _ := cfg["id"].(string)
	if id == "" {
		return &sidecar.Message{Type: sidecar.MsgError, Error: "missing prompt id"}
	}
	if err := e.inferEngine.RemovePrompt(id); err != nil {
		return &sidecar.Message{Type: sidecar.MsgError, Error: fmt.Sprintf("remove_prompt: %v", err)}
	}
	slog.Info("prompt removed", "id", id)
	return &sidecar.Message{Type: sidecar.MsgOK}
}

// --- Visual Tracking ---

func (e *engine) handleEnableTracking(msg *sidecar.Message) *sidecar.Message {
	sourceID, err := extractSourceID(msg)
	if err != nil {
		return &sidecar.Message{Type: sidecar.MsgError, Error: err.Error()}
	}

	// Extract optional model paths and intrinsics from config.
	cfg, _ := msg.Config.(map[string]any)
	spPlan, _ := cfg["superpoint_plan"].(string)
	lgPlan, _ := cfg["lightglue_plan"].(string)
	mapDir, _ := cfg["map_dir"].(string)

	// Fall back to engine-level config.
	if spPlan == "" && e.modelsDir != "" {
		spPlan = e.modelsDir + "/superpoint.plan"
	}
	if lgPlan == "" && e.modelsDir != "" {
		lgPlan = e.modelsDir + "/lightglue.plan"
	}
	if mapDir == "" {
		mapDir = e.trackingMapDir
	}

	e.mu.Lock()
	e.trackingSources[sourceID] = true
	e.mu.Unlock()

	// Try to create a real VisualLocalizer. This will fail if models or
	// studio map aren't available — that's expected and non-fatal.
	if mapDir != "" && spPlan != "" && lgPlan != "" {
		intrinsics := tracking.CameraIntrinsics{} // default
		localizer, locErr := tracking.NewVisualLocalizer(mapDir, spPlan, lgPlan, intrinsics)
		if locErr != nil {
			slog.Warn("tracking: VisualLocalizer creation failed (models likely missing)", "source", sourceID, "error", locErr)
		} else {
			// Set frame format from pool config.
			if e.pool != nil {
				poolCfg := e.pool.Config()
				localizer.SetFrameFormat(gpu.GPUFrameFormat(poolCfg.Format))
			}
			if startErr := localizer.Start(); startErr != nil {
				slog.Warn("tracking: VisualLocalizer start failed", "source", sourceID, "error", startErr)
				localizer.Close()
			} else {
				e.mu.Lock()
				e.trackingLocals[sourceID] = localizer
				e.mu.Unlock()
				slog.Info("tracking enabled (real)", "source", sourceID)
				return &sidecar.Message{Type: sidecar.MsgOK}
			}
		}
	}

	slog.Info("tracking enabled (no localizer)", "source", sourceID)
	return &sidecar.Message{Type: sidecar.MsgOK}
}

func (e *engine) handleDisableTracking(msg *sidecar.Message) *sidecar.Message {
	sourceID, err := extractSourceID(msg)
	if err != nil {
		return &sidecar.Message{Type: sidecar.MsgError, Error: err.Error()}
	}

	e.mu.Lock()
	delete(e.trackingSources, sourceID)
	if loc, ok := e.trackingLocals[sourceID]; ok {
		if loc != nil {
			loc.Close()
		}
		delete(e.trackingLocals, sourceID)
	}
	e.mu.Unlock()

	slog.Info("tracking disabled", "source", sourceID)
	return &sidecar.Message{Type: sidecar.MsgOK}
}

func (e *engine) handleTrackFrame(msg *sidecar.Message) *sidecar.Message {
	cfg, ok := msg.Config.(map[string]any)
	if !ok {
		return nil
	}
	slotF, _ := cfg["slot"].(float64)
	sourceID, _ := cfg["source_id"].(string)
	slot := int(slotF)

	var ctrl *sidecar.GPUPoolControl
	if e.pool != nil {
		ctrl = e.pool.Control()
	}
	if ctrl != nil {
		if !ctrl.MarkProcessing(slot) {
			return nil
		}
	}

	// Wait for host GPU work to complete before reading the frame.
	if e.pool != nil {
		if err := e.pool.WaitReadyEvent(slot); err != nil {
			slog.Warn("track_frame: wait ready event", "slot", slot, "error", err)
		}
	}

	// Run visual localization if a localizer exists for this source.
	e.mu.Lock()
	loc := e.trackingLocals[sourceID]
	e.mu.Unlock()

	if loc != nil && e.pool != nil {
		framePtr := e.pool.FrameDevPtr(slot)
		poolCfg := e.pool.Config()
		loc.ProcessFrame(framePtr, poolCfg.Width, poolCfg.Height, poolCfg.Pitch)

		// Send pose result if available.
		if pose := loc.LatestPose(); pose != nil && e.worker != nil {
			_ = e.worker.Send(&sidecar.Message{
				Type: sidecar.MsgPoseResult,
				Data: map[string]any{
					"source_id": sourceID,
					"position":  pose.Position,
					"rotation":  pose.Orientation,
					"timestamp": pose.Timestamp,
				},
			})
		}
	} else {
		slog.Debug("track_frame (no localizer)", "source", sourceID, "slot", slot)
	}

	// Record done event so host knows our GPU work is complete.
	if e.pool != nil {
		if err := e.pool.RecordDoneEvent(slot); err != nil {
			slog.Warn("track_frame: record done event", "slot", slot, "error", err)
		}
	}

	// Mark slot done and notify host.
	if ctrl != nil {
		ctrl.MarkDone(slot)
	}
	if e.worker != nil {
		_ = e.worker.Send(&sidecar.Message{
			Type: sidecar.MsgTrackSlotDone,
			Data: map[string]any{"slot": slot},
		})
	}

	return nil
}

// --- Frame processing (via control message) ---

func (e *engine) handleProcessFrame(msg *sidecar.Message) *sidecar.Message {
	slog.Debug("process_frame via control message (stub)")
	return &sidecar.Message{Type: sidecar.MsgOK}
}

// --- Frame loop (GPU pool polling) ---

// frameLoop polls the GPU pool control region for slots marked Ready,
// claims them, runs inference, and marks them Done. After processing,
// sends a slot_done message to the host so it can release in-flight frames.
func (e *engine) frameLoop(ctx context.Context) {
	defer e.wg.Done()
	ctrl := e.pool.Control()
	slotCount := e.pool.SlotCount()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		found := false
		for i := 0; i < slotCount; i++ {
			if ctrl.LoadState(i) != sidecar.SlotReady {
				continue
			}

			if !ctrl.MarkProcessing(i) {
				continue
			}

			found = true

			// Wait for host GPU work to complete before reading the frame.
			// The host records the ready event after writing frame data to VRAM;
			// this ensures our subsequent GPU reads see the completed writes.
			if err := e.pool.WaitReadyEvent(i); err != nil {
				slog.Warn("wait ready event", "slot", i, "error", err)
			}

			// Read source key from shared memory (written by host before MarkReady).
			// Falls back to the slotSource map for legacy "infer" command path.
			sourceKey := ctrl.LoadSourceKey(i)
			if sourceKey == "" {
				e.mu.Lock()
				sourceKey = e.slotSource[i]
				delete(e.slotSource, i)
				e.mu.Unlock()
			}

			framePtr := e.pool.FrameDevPtr(i)
			pts := ctrl.LoadPTS(i)

			// Dispatch based on source key prefix.
			e.processFrame(sourceKey, framePtr, pts, i)

			// Record done event so host knows our GPU work is complete.
			if err := e.pool.RecordDoneEvent(i); err != nil {
				slog.Warn("record done event", "slot", i, "error", err)
			}

			ctrl.MarkDone(i)

			// Send typed slot_done so the correct host proxy handles it.
			if e.worker != nil {
				msgType := sidecar.MsgSlotDone
				if sourceKey == sidecar.SourceKeyAR {
					msgType = sidecar.MsgARSlotDone
				} else if strings.HasPrefix(sourceKey, sidecar.SourceKeyTrackPrefix) {
					msgType = sidecar.MsgTrackSlotDone
				}
				_ = e.worker.Send(&sidecar.Message{
					Type: msgType,
					Data: map[string]any{"slot": i},
				})
			}
		}

		if !found {
			time.Sleep(200 * time.Microsecond)
		}
	}
}

// runARInference runs depth+SAM inference on the given frame pointer and
// updates occlusion IPC handles if the output pointers changed. Shared
// by both handleInferAR (control message path) and processFrame (GPU pool
// polling path).
func (e *engine) runARInference(framePtr uintptr) {
	e.mu.Lock()
	hasDepth := e.depthEnabled
	hasSAM := e.samEnabled
	e.mu.Unlock()

	if e.inferEngine == nil || (!hasDepth && !hasSAM) {
		return
	}

	poolCfg := e.pool.Config()
	e.inferEngine.ProcessFrame(framePtr, poolCfg.Width, poolCfg.Height, poolCfg.Pitch)

	// Check if new occlusion pointers are available and export IPC handles.
	occ := e.inferEngine.LastOcclusion()
	if occ == nil {
		return
	}

	e.mu.Lock()
	if hasDepth && occ.DepthPtr != 0 && (e.depthBufPtr == nil || uintptr(e.depthBufPtr) != occ.DepthPtr) {
		e.depthBufPtr = unsafe.Pointer(occ.DepthPtr)
		e.depthW = occ.DepthW
		e.depthH = occ.DepthH
		e.depthHandleExported = false
	}
	if hasSAM && occ.MaskPtr != 0 && (e.samMaskPtr == nil || uintptr(e.samMaskPtr) != occ.MaskPtr) {
		e.samMaskPtr = unsafe.Pointer(occ.MaskPtr)
		e.samMaskW = occ.Width
		e.samMaskH = occ.Height
		e.samHandleExported = false
	}
	e.mu.Unlock()
	e.sendOcclusionHandles()
}

// processFrame runs inference on a single GPU frame slot.
// For segmentation sources, constructs a GPUFrame from the IPC-shared device
// pointer and runs the full RVM TRT pipeline. AR and tracking are still stubs.
func (e *engine) processFrame(sourceKey string, framePtr uintptr, pts int64, slot int) {
	if sourceKey == "" {
		slog.Debug("process_frame: no source key for slot", "slot", slot)
		return
	}

	// AR inference (depth + SAM) via the inference engine.
	if sourceKey == sidecar.SourceKeyAR {
		e.runARInference(framePtr)
		return
	}

	// Visual tracking: extract source ID from key prefix and run localizer.
	if strings.HasPrefix(sourceKey, sidecar.SourceKeyTrackPrefix) {
		trackSourceID := strings.TrimPrefix(sourceKey, sidecar.SourceKeyTrackPrefix)
		e.mu.Lock()
		loc := e.trackingLocals[trackSourceID]
		e.mu.Unlock()

		if loc != nil {
			cfg := e.pool.Config()
			loc.ProcessFrame(framePtr, cfg.Width, cfg.Height, cfg.Pitch)

			// Send pose result if available.
			if pose := loc.LatestPose(); pose != nil && e.worker != nil {
				_ = e.worker.Send(&sidecar.Message{
					Type: sidecar.MsgPoseResult,
					Data: map[string]any{
						"source_id": trackSourceID,
						"position":  pose.Position,
						"rotation":  pose.Orientation,
						"timestamp": pose.Timestamp,
					},
				})
			}
		}
		return
	}

	if e.segEngine == nil || !e.segEngine.IsEnabled(sourceKey) {
		return
	}

	// Construct a GPUFrame from the pool slot's device pointer.
	// The frame data is in VRAM shared via CUDA IPC — zero-copy.
	cfg := e.pool.Config()
	frame := gpu.NewGPUFrameFromDevPtr(framePtr, cfg.Width, cfg.Height, cfg.Pitch, pts, gpu.GPUFrameFormat(cfg.Format))
	defer frame.Release()

	// Run the full RVM pipeline: NV12→RGB→TRT inference→mask upscale→sync.
	// The mask is written in-place to the engine's internal buffer, which
	// is exported to the host via IPC handle (sent at enable time).
	e.segEngine.Segment(sourceKey, frame)
}

// --- Helpers ---

// extractSourceID pulls the source_id string from a message's Config field.
func extractSourceID(msg *sidecar.Message) (string, error) {
	if msg.Config == nil {
		return "", fmt.Errorf("missing config")
	}

	switch cfg := msg.Config.(type) {
	case map[string]any:
		id, ok := cfg["source_id"].(string)
		if !ok || id == "" {
			return "", fmt.Errorf("missing or empty source_id in config")
		}
		return id, nil
	default:
		raw, err := json.Marshal(cfg)
		if err != nil {
			return "", fmt.Errorf("marshal config: %w", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return "", fmt.Errorf("unmarshal config: %w", err)
		}
		id, ok := m["source_id"].(string)
		if !ok || id == "" {
			return "", fmt.Errorf("missing or empty source_id in config")
		}
		return id, nil
	}
}

// extractDimensions extracts width and height from a message's Config field.
func extractDimensions(msg *sidecar.Message) (int, int) {
	cfg, ok := msg.Config.(map[string]any)
	if !ok {
		return 0, 0
	}
	wf, _ := cfg["width"].(float64)
	hf, _ := cfg["height"].(float64)
	return int(wf), int(hf)
}

// encodeBase64Handle encodes a CUDA IPC handle as a base64 string.
func encodeBase64Handle(h sidecar.CUDAIPCHandle) string {
	return base64.StdEncoding.EncodeToString(h[:])
}

// extractSAMPrompt parses a SAMPrompt from a message's Config field.
func extractSAMPrompt(msg *sidecar.Message) (arinfer.SAMPrompt, error) {
	cfg, ok := msg.Config.(map[string]any)
	if !ok {
		return arinfer.SAMPrompt{}, fmt.Errorf("missing config")
	}

	id, _ := cfg["id"].(string)
	pType, _ := cfg["type"].(string)
	label, _ := cfg["label"].(string)

	prompt := arinfer.SAMPrompt{
		ID:    id,
		Type:  pType,
		Label: label,
	}

	// Parse points: [[x1,y1], [x2,y2], ...]
	if pts, ok := cfg["points"].([]any); ok {
		for _, pt := range pts {
			if pair, ok := pt.([]any); ok && len(pair) == 2 {
				x, _ := pair[0].(float64)
				y, _ := pair[1].(float64)
				prompt.Points = append(prompt.Points, [2]int{int(x), int(y)})
			}
		}
	}

	// Parse box: [x1, y1, x2, y2]
	if box, ok := cfg["box"].([]any); ok && len(box) == 4 {
		for i := 0; i < 4; i++ {
			v, _ := box[i].(float64)
			prompt.Box[i] = int(v)
		}
	}

	return prompt, nil
}
