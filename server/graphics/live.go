package graphics

import (
	"encoding/binary"
	"errors"
	"image"
	"sync/atomic"

	"github.com/zsiec/switchframe/server/internal/video"
)

// Errors returned for live fill+key layers.
var (
	ErrLiveSources = errors.New("graphics: live layer needs two different sources for fill and key")
	ErrLiveLayer   = errors.New("graphics: live layer is full frame; position and fly animations do not apply")
	// ErrLiveUnsupported is the error a live guard wraps (SetLiveGuard).
	ErrLiveUnsupported = errors.New("graphics: live layer not supported by this pipeline")
)

// LiveOptions describes how a layer composites its live fill and key.
type LiveOptions struct {
	// Premultiplied marks a shaped fill, one the renderer has already
	// multiplied by its key: it is added over the keyed-out background
	// rather than mixed in a second time, which would darken its edges.
	Premultiplied bool
}

// LiveSource names the sources a live layer reads this tick.
type LiveSource struct {
	ID        int
	Fill, Key string
}

// LivePicture is one source picture in the program's format: YUV420p, or
// YUV422P10LE when TenBit.
type LivePicture struct {
	Data          []byte
	Width, Height int
	TenBit        bool
}

// liveFeed makes a layer show a live fill+key source pair instead of its
// RGBA overlay. The key's luma is the fill's alpha, as with a hardware
// fill+key pair.
type liveFeed struct {
	fill, key     string
	premultiplied bool

	// fillPic and keyPic are set for one tick by the caller that owns the
	// frames (SetLiveFrames) and cleared before it releases them.
	fillPic, keyPic LivePicture

	// mismatchLogged keeps a format mismatch to one log line per SetLive;
	// it is set under the read lock.
	mismatchLogged atomic.Bool
}

// liveScratch holds the mask buffers of the live blend.
type liveScratch struct {
	mask, chroma []byte
}

func (s *liveScratch) buffers(luma, chroma int) ([]byte, []byte) {
	if cap(s.mask) < luma {
		s.mask = make([]byte, luma)
	}
	if cap(s.chroma) < chroma {
		s.chroma = make([]byte, chroma)
	}
	return s.mask[:luma], s.chroma[:chroma]
}

// SetLiveGuard sets a check SetLive runs first, so a pipeline that cannot
// draw a live layer refuses it rather than dropping it on air.
func (c *Compositor) SetLiveGuard(fn func(LiveOptions) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.liveGuard = fn
}

// SetLive makes a layer show the fill source keyed by the key source's
// luma, full frame, in place of its overlay. The layer keeps its on/off,
// fade and z-order.
func (c *Compositor) SetLive(id int, fill, key string, opts LiveOptions) error {
	if fill == "" || key == "" || fill == key {
		return ErrLiveSources
	}
	// An unknown layer is reported as such, not as a pipeline refusal.
	c.mu.RLock()
	_, ok := c.layers[id]
	guard := c.liveGuard
	c.mu.RUnlock()
	if !ok {
		return ErrLayerNotFound
	}
	if guard != nil {
		if err := guard(opts); err != nil {
			return err
		}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrCompositorClosed
	}
	layer, ok := c.layers[id]
	if !ok {
		c.mu.Unlock()
		return ErrLayerNotFound
	}
	// A fly or slide moves the layer; a live layer has no position to move.
	if layer.animConfig != nil && layer.animConfig.ToRect != nil {
		c.mu.Unlock()
		return ErrLiveLayer
	}
	layer.live = &liveFeed{fill: fill, key: key, premultiplied: opts.Premultiplied}
	// A live layer covers the frame: the fill and key are program-sized.
	layer.rect = image.Rectangle{}
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()
	if cb != nil {
		cb(state)
	}
	return nil
}

// ClearLive returns a layer to its overlay. A layer without one has
// nothing left to show and goes off.
func (c *Compositor) ClearLive(id int) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrCompositorClosed
	}
	layer, ok := c.layers[id]
	if !ok {
		c.mu.Unlock()
		return ErrLayerNotFound
	}
	if layer.live == nil {
		c.mu.Unlock()
		return nil
	}
	layer.live = nil
	if layer.overlay == nil {
		c.cancelLayerFadeLocked(layer)
		c.cancelLayerAnimationLocked(layer)
		layer.active = false
		layer.fadePosition = 0
	}
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()
	if cb != nil {
		cb(state)
	}
	return nil
}

// LiveSources returns the sources of the live layers that are visible,
// for the caller that feeds them frames each tick.
func (c *Compositor) LiveSources() []LiveSource {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []LiveSource
	for _, id := range c.sortedIDs {
		layer := c.layers[id]
		if layer.live == nil || !layer.active || layer.fadePosition < 1.0/255.0 {
			continue
		}
		out = append(out, LiveSource{ID: id, Fill: layer.live.fill, Key: layer.live.key})
	}
	return out
}

// LiveOnAir returns the fill and key sources of the live layers that are
// on, for tally.
func (c *Compositor) LiveOnAir() (fills, keys []string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, layer := range c.layers {
		if layer.live == nil || !layer.active {
			continue
		}
		fills = append(fills, layer.live.fill)
		keys = append(keys, layer.live.key)
	}
	return fills, keys
}

// SetLiveFrames hands a live layer this tick's fill and key pictures. The
// compositor reads them, without copying, until ClearLiveFrames: the
// caller keeps the frames alive until then.
func (c *Compositor) SetLiveFrames(id int, fill, key LivePicture) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if layer, ok := c.layers[id]; ok && layer.live != nil {
		layer.live.fillPic, layer.live.keyPic = fill, key
	}
}

// ClearLiveFrames drops every live layer's pictures, before the caller
// releases the frames behind them.
func (c *Compositor) ClearLiveFrames() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, layer := range c.layers {
		if layer.live != nil {
			layer.live.fillPic, layer.live.keyPic = LivePicture{}, LivePicture{}
		}
	}
}

// livePictures returns the layer's pictures when both are there and match
// the program frame. Caller holds c.mu.
func (c *Compositor) livePictures(layer *graphicsLayer, width, height int, tenBit bool) (fill, key []byte, ok bool) {
	lf := layer.live
	if lf.fillPic.Data == nil || lf.keyPic.Data == nil {
		return nil, nil, false
	}
	size := video.YUV420FrameSize(width, height)
	if tenBit {
		size = video.YUV422_10bitFrameSize(width, height)
	}
	for _, p := range []LivePicture{lf.fillPic, lf.keyPic} {
		if p.Width != width || p.Height != height || p.TenBit != tenBit || len(p.Data) < size {
			if !lf.mismatchLogged.Swap(true) {
				c.log.Warn("live layer skipped: fill and key must match the program size and format",
					"layer", layer.id, "fill", lf.fill, "key", lf.key,
					"width", p.Width, "height", p.Height, "program_width", width, "program_height", height)
			}
			return nil, nil, false
		}
	}
	return lf.fillPic.Data, lf.keyPic.Data, true
}

// blendLiveLocked composites a live layer onto a program frame. Caller
// holds c.mu (read).
func (c *Compositor) blendLiveLocked(yuv []byte, width, height int, tenBit bool, layer *graphicsLayer) {
	fill, key, ok := c.livePictures(layer, width, height, tenBit)
	if !ok {
		return
	}
	// The read lock admits other readers; the scratch buffers need their own.
	c.liveMu.Lock()
	defer c.liveMu.Unlock()
	if tenBit {
		blendLive10(yuv, fill, key, width, height, layer.fadePosition, layer.live.premultiplied, &c.liveScratch)
	} else {
		blendLive8(yuv, fill, key, width, height, layer.fadePosition, layer.live.premultiplied, &c.liveScratch)
	}
}

// liveKeyLUT8 maps a legal-range 8-bit key luma to the fill's weight,
// 16 -> 0 and 235 -> 255, with the fade folded in so the blend needs no
// second pass.
func liveKeyLUT8(lut *[256]byte, fade float64) {
	for y := range lut {
		lut[y] = liveWeight(float64(y-16)/219, fade)
	}
}

// liveKeyLUT10 is liveKeyLUT8 for 10-bit luma, 64 -> 0 and 940 -> 255,
// indexed by the full 10-bit value so no precision is lost before the
// 8-bit weight.
func liveKeyLUT10(lut *[1024]byte, fade float64) {
	for y := range lut {
		lut[y] = liveWeight(float64(y-64)/876, fade)
	}
}

func liveWeight(a, fade float64) byte {
	a = min(max(a, 0), 1)
	return byte(a*fade*255 + 0.5)
}

// blendLive8 keys a YUV420p fill onto bg by the key's luma.
func blendLive8(bg, fill, key []byte, width, height int, fade float64, premultiplied bool, s *liveScratch) {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)
	mask, chroma := s.buffers(ySize, uvSize)

	var lut [256]byte
	liveKeyLUT8(&lut, fade)
	lumaKeyMaskLUT(&mask[0], &key[0], &lut[0], ySize)
	downsampleMask2x2(chroma, mask, width, height)

	cb, cr := ySize, ySize+uvSize
	if !premultiplied {
		blendMaskY(&bg[0], &fill[0], &mask[0], ySize)
		blendMaskY(&bg[cb], &fill[cb], &chroma[0], uvSize)
		blendMaskY(&bg[cr], &fill[cr], &chroma[0], uvSize)
		return
	}
	fw := int(fade*256 + 0.5)
	blendShaped8(bg[:ySize], fill[:ySize], mask, 16, fw)
	blendShaped8(bg[cb:cr], fill[cb:cr], chroma, 128, fw)
	blendShaped8(bg[cr:cr+uvSize], fill[cr:cr+uvSize], chroma, 128, fw)
}

// blendLive10 keys a YUV422P10LE fill onto bg by the key's luma.
func blendLive10(bg, fill, key []byte, width, height int, fade float64, premultiplied bool, s *liveScratch) {
	ySize := width * height
	chromaW := width / 2
	cSize := chromaW * height
	mask, chroma := s.buffers(ySize, cSize)

	var lut [1024]byte
	liveKeyLUT10(&lut, fade)
	for i := range mask {
		mask[i] = lut[binary.LittleEndian.Uint16(key[2*i:])&1023]
	}
	// 4:2:2: one chroma sample per two luma samples of a row.
	for i := range chroma {
		chroma[i] = byte((int(mask[2*i]) + int(mask[2*i+1]) + 1) >> 1)
	}

	cb, cr := 2*ySize, 2*ySize+2*cSize
	if !premultiplied {
		keyMaskBlend10Row(&bg[0], &fill[0], &mask[0], ySize)
		keyMaskBlend10Row(&bg[cb], &fill[cb], &chroma[0], cSize)
		keyMaskBlend10Row(&bg[cr], &fill[cr], &chroma[0], cSize)
		return
	}
	fw := int(fade*256 + 0.5)
	blendShaped10(bg[:cb], fill[:cb], mask, 64, fw)
	blendShaped10(bg[cb:cr], fill[cb:cr], chroma, 512, fw)
	blendShaped10(bg[cr:cr+2*cSize], fill[cr:cr+2*cSize], chroma, 512, fw)
}

// blendShaped8 adds a shaped fill over bg keyed out by mask:
//
//	out = bg + ((black-bg)*w + (fill-black)*fw + 128) >> 8
//
// with w the weight blendMaskY derives from mask and fw the fade. Black
// is the zero of the fill's signal, 16 for luma and 128 for chroma, so a
// shaped fill lands where its unshaped original would.
func blendShaped8(bg, fill, mask []byte, black, fw int) {
	for i := range mask {
		w := int(mask[i])
		w += w >> 7
		b := int(bg[i])
		v := b + (((black-b)*w + (int(fill[i])-black)*fw + 128) >> 8)
		bg[i] = byte(min(max(v, 0), 255))
	}
}

// blendShaped10 is blendShaped8 for little-endian 10-bit samples, black
// 64 for luma and 512 for chroma.
func blendShaped10(bg, fill, mask []byte, black, fw int) {
	for i := range mask {
		w := int(mask[i])
		w += w >> 7
		b := int(binary.LittleEndian.Uint16(bg[2*i:]))
		f := int(binary.LittleEndian.Uint16(fill[2*i:]))
		v := b + (((black-b)*w + (f-black)*fw + 128) >> 8)
		binary.LittleEndian.PutUint16(bg[2*i:], uint16(min(max(v, 0), 1023)))
	}
}
