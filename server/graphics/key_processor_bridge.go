package graphics

import (
	"log/slog"
	"sync"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// KeyProcessorBridge wraps a KeyProcessor with fill-source decode capability.
//
// Fill sources (keyed inputs) are decoded and cached via IngestFillFrame.
// The pipeline coordinator calls ProcessYUV on the raw YUV program frame
// to apply all enabled keys via KeyProcessor.Process.
//
// When no keys are enabled, ProcessYUV returns the buffer unchanged (zero overhead).
type KeyProcessorBridge struct {
	mu sync.Mutex

	kp             *KeyProcessor
	decoderFactory transition.DecoderFactory

	// Per-source decoders and cached YUV for fill frames
	fillDecoders map[string]transition.VideoDecoder
	fillYUV      map[string][]byte
}

// NewKeyProcessorBridge creates a bridge wrapping the given KeyProcessor.
func NewKeyProcessorBridge(kp *KeyProcessor) *KeyProcessorBridge {
	return &KeyProcessorBridge{
		kp:           kp,
		fillDecoders: make(map[string]transition.VideoDecoder),
		fillYUV:      make(map[string][]byte),
	}
}

// SetDecoderFactory configures the decoder factory used to decode fill source
// frames in IngestFillFrame. Must be called before IngestFillFrame.
func (b *KeyProcessorBridge) SetDecoderFactory(dec transition.DecoderFactory) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.decoderFactory = dec
}

// IngestFillFrame decodes a source frame and caches its YUV data for use
// during ProcessYUV. Only frames from sources with enabled keys are
// decoded; others are ignored.
//
// Called by the switcher on every source frame in handleVideoFrame.
func (b *KeyProcessorBridge) IngestFillFrame(sourceKey string, frame *media.VideoFrame) {
	// Fast path: skip if this source has no enabled key
	if !b.kp.HasEnabledKeys() {
		return
	}
	cfg, ok := b.kp.GetKey(sourceKey)
	if !ok || !cfg.Enabled {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.decoderFactory == nil {
		return
	}

	// Lazy-init decoder for this source
	dec, exists := b.fillDecoders[sourceKey]
	if !exists {
		if !frame.IsKeyframe {
			return // need keyframe to init decoder
		}
		var err error
		dec, err = b.decoderFactory()
		if err != nil {
			slog.Debug("keyer: fill decoder init failed", "source", sourceKey, "err", err)
			return
		}
		b.fillDecoders[sourceKey] = dec
	}

	// Decode fill frame
	annexB := codec.AVC1ToAnnexB(frame.WireData)
	if frame.IsKeyframe && len(frame.SPS) > 0 {
		var buf []byte
		buf = append(buf, 0x00, 0x00, 0x00, 0x01)
		buf = append(buf, frame.SPS...)
		buf = append(buf, 0x00, 0x00, 0x00, 0x01)
		buf = append(buf, frame.PPS...)
		buf = append(buf, annexB...)
		annexB = buf
	}

	yuv, w, h, err := dec.Decode(annexB)
	if err != nil {
		return
	}

	// Deep copy (decoder reuses internal buffer)
	yuvSize := w * h * 3 / 2
	cached := b.fillYUV[sourceKey]
	if len(cached) != yuvSize {
		cached = make([]byte, yuvSize)
	}
	copy(cached, yuv[:yuvSize])
	b.fillYUV[sourceKey] = cached
}

// RemoveFillSource removes the cached fill data and decoder for a source.
// Called when a source's key is removed or disabled.
func (b *KeyProcessorBridge) RemoveFillSource(source string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if dec, ok := b.fillDecoders[source]; ok {
		dec.Close()
		delete(b.fillDecoders, source)
	}
	delete(b.fillYUV, source)
}

// HasEnabledKeysWithFills returns true if there are enabled keys AND cached
// fill frames. Used by the pipeline coordinator to decide whether to enter
// the slow decode/process/encode path.
func (b *KeyProcessorBridge) HasEnabledKeysWithFills() bool {
	if !b.kp.HasEnabledKeys() {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.fillYUV) > 0
}

// ProcessYUV applies upstream keys to a raw YUV420 buffer in-place.
// This is the codec-free processor used by the pipeline coordinator.
// When no keys are enabled or no fills are cached, returns yuv unchanged.
func (b *KeyProcessorBridge) ProcessYUV(yuv []byte, width, height int) []byte {
	if !b.kp.HasEnabledKeys() {
		return yuv
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.fillYUV) == 0 {
		return yuv
	}

	b.kp.Process(yuv, b.fillYUV, width, height)
	return yuv
}

// Close releases all fill decoder resources.
func (b *KeyProcessorBridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, dec := range b.fillDecoders {
		dec.Close()
	}
	b.fillDecoders = make(map[string]transition.VideoDecoder)
	b.fillYUV = make(map[string][]byte)
}
