package graphics

import (
	"errors"
	"image"
	"log/slog"
	"sync"
)

var (
	ErrSequenceNotFound = errors.New("graphics: no sequence loaded on this layer")
)

// SequenceEngine manages animated PNG sequence lifecycles across graphics
// layers. It follows the same pattern as TickerEngine: it holds its own mutex
// for the sequences map and acquires compositor.mu directly for layer mutations.
type SequenceEngine struct {
	compositor *Compositor
	log        *slog.Logger

	mu        sync.Mutex
	sequences map[int]*sequenceState // layerID -> loaded sequence data
	closeOnce sync.Once
}

// NewSequenceEngine creates a sequence engine bound to a compositor.
func NewSequenceEngine(c *Compositor) *SequenceEngine {
	return &SequenceEngine{
		compositor: c,
		log:        slog.With("component", "sequence-engine"),
		sequences:  make(map[int]*sequenceState),
	}
}

// upload decodes a ZIP synchronously and installs the sequence on the layer.
// Used by tests. Production code should use UploadAsync.
func (se *SequenceEngine) upload(layerID int, zipData []byte) error {
	seq, err := LoadSequenceZIP(zipData)
	if err != nil {
		return err
	}

	se.mu.Lock()
	se.sequences[layerID] = seq
	se.mu.Unlock()

	se.compositor.mu.Lock()
	layer, ok := se.compositor.layers[layerID]
	if !ok {
		se.compositor.mu.Unlock()
		se.mu.Lock()
		delete(se.sequences, layerID)
		se.mu.Unlock()
		return ErrLayerNotFound
	}

	layer.seq = seq
	layer.rect = image.Rect(seq.cropX, seq.cropY, seq.cropX+seq.cropW, seq.cropY+seq.cropH)

	// Set initial overlay so On() works. Prefer first loop frame, fall back to last in-frame.
	initFrame := seq.holdFrame()
	if initFrame != nil {
		layer.overlay = initFrame
		layer.overlayWidth = seq.cropW
		layer.overlayHeight = seq.cropH
		layer.overlayGen++
	}

	state := se.compositor.buildStateLocked()
	cb := se.compositor.onStateChange
	se.compositor.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

// UploadAsync starts background processing of a ZIP archive of PNG frames.
// sourceFPS is the frame rate of the source material. If it differs from the
// pipeline fps, frames are resampled (nearest-neighbor) to match the pipeline
// rate so playback is always 1:1 and butter smooth.
func (se *SequenceEngine) UploadAsync(layerID int, zipData []byte, sourceFPS float64) error {
	// Validate layer exists before spawning goroutine.
	se.compositor.mu.Lock()
	layer, ok := se.compositor.layers[layerID]
	if !ok {
		se.compositor.mu.Unlock()
		return ErrLayerNotFound
	}
	layer.seqProcessing = true
	state := se.compositor.buildStateLocked()
	cb := se.compositor.onStateChange
	se.compositor.mu.Unlock()

	if cb != nil {
		cb(state)
	}

	go se.processUpload(layerID, zipData, sourceFPS)
	return nil
}

// processUpload decodes the ZIP and installs the sequence on the layer.
// Called in a background goroutine by UploadAsync.
func (se *SequenceEngine) processUpload(layerID int, zipData []byte, sourceFPS float64) {
	// Progress callback: update layer state and broadcast on each frame.
	// Throttle to every 2% to avoid flooding the state broadcast.
	lastBroadcast := 0.0
	onProgress := func(progress float64) {
		if progress-lastBroadcast < 0.02 && progress < 1.0 {
			return
		}
		lastBroadcast = progress
		se.compositor.mu.Lock()
		layer, ok := se.compositor.layers[layerID]
		if ok {
			layer.seqProcessingProgress = progress
		}
		state := se.compositor.buildStateLocked()
		cb := se.compositor.onStateChange
		se.compositor.mu.Unlock()
		if cb != nil {
			cb(state)
		}
	}

	seq, err := LoadSequenceZIPWithProgress(zipData, onProgress)

	if err == nil {
		// Resample to pipeline fps if source fps differs.
		se.compositor.mu.RLock()
		pipelineFPSNum := se.compositor.pipelineFPSNum
		pipelineFPSDen := se.compositor.pipelineFPSDen
		se.compositor.mu.RUnlock()

		if pipelineFPSNum > 0 && pipelineFPSDen > 0 && sourceFPS > 0 {
			pipelineFPS := float64(pipelineFPSNum) / float64(pipelineFPSDen)
			if pipelineFPS != sourceFPS {
				seq.inFrames = resampleFrames(seq.inFrames, sourceFPS, pipelineFPS)
				seq.loopFrames = resampleFrames(seq.loopFrames, sourceFPS, pipelineFPS)
				seq.outFrames = resampleFrames(seq.outFrames, sourceFPS, pipelineFPS)
				se.log.Info("sequence resampled",
					"layer", layerID,
					"sourceFPS", sourceFPS,
					"pipelineFPS", pipelineFPS,
					"inFrames", len(seq.inFrames),
					"outFrames", len(seq.outFrames))
			}
		}
	}

	se.compositor.mu.Lock()
	layer, ok := se.compositor.layers[layerID]
	if !ok {
		se.compositor.mu.Unlock()
		se.log.Error("sequence upload: layer gone", "layer", layerID)
		return
	}
	layer.seqProcessing = false

	if err != nil {
		se.log.Error("sequence upload failed", "layer", layerID, "error", err)
		state := se.compositor.buildStateLocked()
		cb := se.compositor.onStateChange
		se.compositor.mu.Unlock()
		if cb != nil {
			cb(state)
		}
		return
	}

	// Install the decoded sequence.
	se.mu.Lock()
	se.sequences[layerID] = seq
	se.mu.Unlock()

	layer.seq = seq
	layer.rect = image.Rect(seq.cropX, seq.cropY, seq.cropX+seq.cropW, seq.cropY+seq.cropH)

	initFrame := seq.holdFrame()
	if initFrame != nil {
		layer.overlay = initFrame
		layer.overlayWidth = seq.cropW
		layer.overlayHeight = seq.cropH
		layer.overlayGen++
	}

	state := se.compositor.buildStateLocked()
	cb := se.compositor.onStateChange
	se.compositor.mu.Unlock()

	if cb != nil {
		cb(state)
	}

	se.log.Info("sequence uploaded", "layer", layerID,
		"in", len(seq.inFrames), "loop", len(seq.loopFrames), "out", len(seq.outFrames),
		"crop", image.Rect(seq.cropX, seq.cropY, seq.cropX+seq.cropW, seq.cropY+seq.cropH))
}

// HasSequence returns true if a sequence is loaded on the given layer.
func (se *SequenceEngine) HasSequence(layerID int) bool {
	se.mu.Lock()
	defer se.mu.Unlock()
	_, ok := se.sequences[layerID]
	return ok
}

// AutoOn starts the sequence with the animate-in segment, then transitions
// to the loop. If there are no in frames, it goes directly to loop. The layer
// is activated and the overlay is set to the first frame of the starting phase.
func (se *SequenceEngine) AutoOn(layerID int) error {
	se.mu.Lock()
	seq, ok := se.sequences[layerID]
	if !ok {
		se.mu.Unlock()
		return ErrSequenceNotFound
	}
	se.mu.Unlock()

	se.compositor.mu.Lock()
	layer, ok := se.compositor.layers[layerID]
	if !ok {
		se.compositor.mu.Unlock()
		return ErrLayerNotFound
	}
	// Guard against concurrent Upload replacing layer.seq between lock acquisitions.
	if layer.seq != seq {
		se.compositor.mu.Unlock()
		return ErrSequenceNotFound
	}

	seq.startIn()

	// Set overlay to the first frame of the starting phase.
	frame := seq.currentFrame()
	if frame != nil {
		layer.overlay = frame
		layer.overlayWidth = seq.cropW
		layer.overlayHeight = seq.cropH
		layer.overlayGen++
	}

	layer.active = true
	layer.fadePosition = 1.0

	state := se.compositor.buildStateLocked()
	cb := se.compositor.onStateChange
	se.compositor.mu.Unlock()

	if cb != nil {
		cb(state)
	}

	se.log.Debug("sequence AutoOn", "layer", layerID, "phase", seq.phase)
	return nil
}

// CutOn starts the sequence directly at the loop segment, skipping any
// animate-in. The layer is activated immediately.
func (se *SequenceEngine) CutOn(layerID int) error {
	se.mu.Lock()
	seq, ok := se.sequences[layerID]
	if !ok {
		se.mu.Unlock()
		return ErrSequenceNotFound
	}
	se.mu.Unlock()

	se.compositor.mu.Lock()
	layer, ok := se.compositor.layers[layerID]
	if !ok {
		se.compositor.mu.Unlock()
		return ErrLayerNotFound
	}
	if layer.seq != seq {
		se.compositor.mu.Unlock()
		return ErrSequenceNotFound
	}

	seq.startLoop()

	// Set overlay to first loop frame.
	frame := seq.currentFrame()
	if frame != nil {
		layer.overlay = frame
		layer.overlayWidth = seq.cropW
		layer.overlayHeight = seq.cropH
		layer.overlayGen++
	}

	layer.active = true
	layer.fadePosition = 1.0

	state := se.compositor.buildStateLocked()
	cb := se.compositor.onStateChange
	se.compositor.mu.Unlock()

	if cb != nil {
		cb(state)
	}

	se.log.Debug("sequence CutOn", "layer", layerID)
	return nil
}

// AutoOff starts the animate-out segment. ProcessYUV will automatically
// deactivate the layer when the out segment completes. If there are no out
// frames, the layer is deactivated immediately.
func (se *SequenceEngine) AutoOff(layerID int) error {
	se.mu.Lock()
	seq, ok := se.sequences[layerID]
	if !ok {
		se.mu.Unlock()
		return ErrSequenceNotFound
	}
	se.mu.Unlock()

	se.compositor.mu.Lock()
	layer, ok := se.compositor.layers[layerID]
	if !ok {
		se.compositor.mu.Unlock()
		return ErrLayerNotFound
	}
	if layer.seq != seq {
		se.compositor.mu.Unlock()
		return ErrSequenceNotFound
	}

	seq.startOut()

	if seq.phase == seqPhaseDone {
		// No out frames — deactivate immediately.
		seq.phase = seqPhaseIdle
		seq.frameIdx = 0
		layer.active = false
		layer.fadePosition = 0.0
	} else {
		// Set overlay to first out frame.
		frame := seq.currentFrame()
		if frame != nil {
			layer.overlay = frame
			layer.overlayWidth = seq.cropW
			layer.overlayHeight = seq.cropH
			layer.overlayGen++
		}
	}

	state := se.compositor.buildStateLocked()
	cb := se.compositor.onStateChange
	se.compositor.mu.Unlock()

	if cb != nil {
		cb(state)
	}

	se.log.Debug("sequence AutoOff", "layer", layerID, "phase", seq.phase)
	return nil
}

// CutOff immediately stops the sequence and deactivates the layer.
func (se *SequenceEngine) CutOff(layerID int) error {
	se.mu.Lock()
	seq, ok := se.sequences[layerID]
	if !ok {
		se.mu.Unlock()
		return ErrSequenceNotFound
	}
	se.mu.Unlock()

	se.compositor.mu.Lock()
	layer, ok := se.compositor.layers[layerID]
	if !ok {
		se.compositor.mu.Unlock()
		return ErrLayerNotFound
	}
	if layer.seq != seq {
		se.compositor.mu.Unlock()
		return ErrSequenceNotFound
	}

	seq.phase = seqPhaseIdle
	seq.frameIdx = 0
	layer.active = false
	layer.fadePosition = 0.0

	state := se.compositor.buildStateLocked()
	cb := se.compositor.onStateChange
	se.compositor.mu.Unlock()

	if cb != nil {
		cb(state)
	}

	se.log.Debug("sequence CutOff", "layer", layerID)
	return nil
}

// SetSpeed changes the playback speed of a loaded sequence.
// Speed 1.0 = normal, 0.5 = half, 2.0 = double. Clamped to [0.1, 4.0].
func (se *SequenceEngine) SetSpeed(layerID int, speed float64) error {
	if speed < 0.1 {
		speed = 0.1
	}
	if speed > 4.0 {
		speed = 4.0
	}

	se.compositor.mu.Lock()
	layer, ok := se.compositor.layers[layerID]
	if !ok {
		se.compositor.mu.Unlock()
		return ErrLayerNotFound
	}
	if layer.seq == nil {
		se.compositor.mu.Unlock()
		return ErrSequenceNotFound
	}
	layer.seq.speed = speed
	state := se.compositor.buildStateLocked()
	cb := se.compositor.onStateChange
	se.compositor.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

// resampleFrames converts a frame sequence from sourceFPS to targetFPS using
// nearest-neighbor frame selection. Returns the original slice unchanged if
// no resampling is needed (same fps or empty input).
func resampleFrames(frames [][]byte, sourceFPS, targetFPS float64) [][]byte {
	if len(frames) == 0 || sourceFPS <= 0 || targetFPS <= 0 {
		return frames
	}
	// Duration of the original sequence in seconds.
	duration := float64(len(frames)) / sourceFPS
	// Number of frames needed at the target rate.
	targetCount := int(duration*targetFPS + 0.5)
	if targetCount <= 0 {
		targetCount = 1
	}
	if targetCount == len(frames) {
		return frames
	}

	resampled := make([][]byte, targetCount)
	for i := 0; i < targetCount; i++ {
		// Map target frame index to source frame index (nearest-neighbor).
		srcIdx := int(float64(i)*sourceFPS/targetFPS + 0.5)
		if srcIdx >= len(frames) {
			srcIdx = len(frames) - 1
		}
		resampled[i] = frames[srcIdx]
	}
	return resampled
}

// Delete removes the sequence from the layer and deactivates it.
func (se *SequenceEngine) Delete(layerID int) error {
	se.mu.Lock()
	_, ok := se.sequences[layerID]
	if !ok {
		se.mu.Unlock()
		return ErrSequenceNotFound
	}
	delete(se.sequences, layerID)
	se.mu.Unlock()

	se.compositor.mu.Lock()
	layer, ok := se.compositor.layers[layerID]
	if ok {
		layer.seq = nil
		layer.active = false
		layer.fadePosition = 0.0
		layer.overlay = nil
		layer.overlayWidth = 0
		layer.overlayHeight = 0
		layer.overlayGen++
	}

	state := se.compositor.buildStateLocked()
	cb := se.compositor.onStateChange
	se.compositor.mu.Unlock()

	if cb != nil {
		cb(state)
	}

	se.log.Debug("sequence deleted", "layer", layerID)
	return nil
}

// Close cleans up all sequences. It is safe to call multiple times.
func (se *SequenceEngine) Close() {
	se.closeOnce.Do(func() {
		se.mu.Lock()
		layerIDs := make([]int, 0, len(se.sequences))
		for id := range se.sequences {
			layerIDs = append(layerIDs, id)
		}
		se.sequences = make(map[int]*sequenceState)
		se.mu.Unlock()

		// Clear sequence state from each layer and deactivate.
		for _, id := range layerIDs {
			se.compositor.mu.Lock()
			layer, ok := se.compositor.layers[id]
			if ok {
				layer.seq = nil
				layer.active = false
				layer.fadePosition = 0.0
			}
			se.compositor.mu.Unlock()
		}
	})
}
