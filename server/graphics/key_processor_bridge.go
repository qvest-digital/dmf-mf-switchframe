package graphics

import (
	"log/slog"
	"sync"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// KeyProcessorBridge wraps a KeyProcessor with H.264 decode/encode capability
// so it can operate in the broadcastVideo pipeline on compressed frames.
//
// Fill sources (keyed inputs) are decoded and cached via IngestFillFrame.
// When ProcessFrame is called with the program frame, it decodes the program,
// applies all enabled keys via KeyProcessor.Process, and re-encodes.
//
// When no keys are enabled, ProcessFrame returns the frame unchanged (zero overhead).
// The bridge follows the same codec lifecycle pattern as Compositor.ProcessFrame.
type KeyProcessorBridge struct {
	mu sync.Mutex

	kp             *KeyProcessor
	decoderFactory transition.DecoderFactory
	encoderFactory func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error)

	// Per-source decoders and cached YUV for fill frames
	fillDecoders map[string]transition.VideoDecoder
	fillYUV      map[string][]byte

	// Program frame codec pipeline
	decoder   transition.VideoDecoder
	encoder   transition.VideoEncoder
	encWidth  int
	encHeight int
	yuvBuf    []byte // reusable buffer for decoded program YUV
	groupID   uint32 // monotonic group ID for encoded frames

	// Callback for VideoInfo changes (new SPS/PPS from encoder)
	onVideoInfoChange func(sps, pps []byte, width, height int)
}

// NewKeyProcessorBridge creates a bridge wrapping the given KeyProcessor.
func NewKeyProcessorBridge(kp *KeyProcessor) *KeyProcessorBridge {
	return &KeyProcessorBridge{
		kp:           kp,
		fillDecoders: make(map[string]transition.VideoDecoder),
		fillYUV:      make(map[string][]byte),
	}
}

// SetCodecFactories configures decoder and encoder factories.
// Must be called before ProcessFrame or IngestFillFrame.
func (b *KeyProcessorBridge) SetCodecFactories(
	dec transition.DecoderFactory,
	enc func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error),
) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.decoderFactory = dec
	b.encoderFactory = enc
}

// OnVideoInfoChange sets a callback for when the encoder produces a keyframe
// with new SPS/PPS. Used to update the program relay's VideoInfo.
func (b *KeyProcessorBridge) OnVideoInfoChange(fn func(sps, pps []byte, width, height int)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onVideoInfoChange = fn
}

// IngestFillFrame decodes a source frame and caches its YUV data for use
// during ProcessFrame. Only frames from sources with enabled keys are
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

// ProcessFrame is the video processor hook called by the switcher on every
// program frame. When upstream keys are enabled and fill frames are cached,
// it decodes the program frame, applies keying, and re-encodes.
//
// When no keys are enabled, returns the frame unchanged (zero overhead).
// Follows the same codec lifecycle pattern as Compositor.ProcessFrame.
func (b *KeyProcessorBridge) ProcessFrame(frame *media.VideoFrame) *media.VideoFrame {
	// Fast path: no enabled keys -> passthrough
	if !b.kp.HasEnabledKeys() {
		return frame
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Check if we have any cached fills
	if len(b.fillYUV) == 0 {
		return frame
	}

	if b.decoderFactory == nil || b.encoderFactory == nil {
		return frame
	}

	// Lazy-init program decoder
	if b.decoder == nil {
		if !frame.IsKeyframe {
			return frame // need keyframe to start
		}
		dec, err := b.decoderFactory()
		if err != nil {
			slog.Error("keyer: decoder init failed", "err", err)
			return frame
		}
		b.decoder = dec
	}

	// Decode program frame: AVC1 -> Annex B -> YUV420
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

	yuv, w, h, err := b.decoder.Decode(annexB)
	if err != nil {
		slog.Debug("keyer: decode failed", "err", err)
		if b.encoder != nil {
			return nil // codec mismatch, drop frame
		}
		return frame
	}

	// Lazy-init encoder (need dimensions from first decode)
	if b.encoder == nil {
		enc, err := b.encoderFactory(w, h, transition.DefaultBitrate, transition.DefaultFPS)
		if err != nil {
			slog.Error("keyer: encoder init failed", "err", err, "w", w, "h", h)
			return frame
		}
		b.encoder = enc
		b.encWidth = w
		b.encHeight = h
		slog.Info("keyer: codec pipeline initialized", "w", w, "h", h)
	}

	// Deep copy decoded YUV (decoder reuses internal buffer)
	yuvSize := w * h * 3 / 2
	if len(b.yuvBuf) != yuvSize {
		b.yuvBuf = make([]byte, yuvSize)
	}
	copy(b.yuvBuf, yuv[:yuvSize])

	// Apply upstream keys
	b.kp.Process(b.yuvBuf, b.fillYUV, w, h)

	// Encode: force IDR when source was keyframe to maintain GOP structure
	forceIDR := frame.IsKeyframe
	encoded, isKeyframe, err := b.encoder.Encode(b.yuvBuf, forceIDR)
	if err != nil {
		slog.Warn("keyer: encode failed", "err", err)
		return nil
	}

	// Maintain monotonic group ID
	if frame.GroupID > b.groupID {
		b.groupID = frame.GroupID
	}
	if isKeyframe {
		b.groupID++
	}

	// Build output VideoFrame (Annex B -> AVC1)
	avc1 := codec.AnnexBToAVC1(encoded)
	result := &media.VideoFrame{
		PTS:        frame.PTS,
		DTS:        frame.DTS,
		IsKeyframe: isKeyframe,
		WireData:   avc1,
		Codec:      frame.Codec,
		GroupID:    b.groupID,
	}

	if isKeyframe {
		for _, nalu := range codec.ExtractNALUs(avc1) {
			if len(nalu) == 0 {
				continue
			}
			switch nalu[0] & 0x1F {
			case 7:
				result.SPS = nalu
			case 8:
				result.PPS = nalu
			}
		}
		if result.SPS != nil && result.PPS != nil && b.onVideoInfoChange != nil {
			b.onVideoInfoChange(result.SPS, result.PPS, b.encWidth, b.encHeight)
		}
	}

	return result
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

// Close releases all codec resources.
func (b *KeyProcessorBridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, dec := range b.fillDecoders {
		dec.Close()
	}
	b.fillDecoders = make(map[string]transition.VideoDecoder)
	b.fillYUV = make(map[string][]byte)
	if b.decoder != nil {
		b.decoder.Close()
		b.decoder = nil
	}
	if b.encoder != nil {
		b.encoder.Close()
		b.encoder = nil
	}
}
