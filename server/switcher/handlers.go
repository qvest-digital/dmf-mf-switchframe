package switcher

import (
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/graphics"
)

// graphicsHandlers groups graphics hot-path handlers published together as
// one atomic pointer. Grouping reduces Switcher field count and co-locates
// mutations that the per-frame handler reads together (keyBridge + DVE).
// Published via atomic.Pointer[graphicsHandlers] using copy-on-write.
type graphicsHandlers struct {
	keyBridge     *graphics.KeyProcessorBridge
	dveCompositor *dve.Compositor
}

// audioHandlers groups the per-frame audio callbacks. audioTransition is
// read during transitions to sync video dissolve with audio crossfade;
// audioHandler routes raw audio frames to the mixer.
type audioHandlers struct {
	audioHandler    func(sourceKey string, frame *media.AudioFrame)
	audioTransition audioTransitionHandler
}

// frameSyncBundle groups the reference-clock frame synchronizer and its
// active flag plus the global delay buffer. The per-frame handler reads
// `active` first and bails early before touching `sync`; grouping keeps
// the two fields in a single atomic load.
type frameSyncBundle struct {
	sync        *FrameSynchronizer
	delayBuffer *DelayBuffer
	active      bool
}
