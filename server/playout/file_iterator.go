package playout

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/codec"
)

// audioFrameInfo holds extracted audio frame data for PTS-matched interleaving.
// This decouples the iterator from the clip.BufferedAudioFrame type so audio
// matching can happen lazily in Next().
type audioFrameInfo struct {
	data       []byte
	pts        int64
	sampleRate int
	channels   int
}

// encodedFrame holds a pre-processed H.264 frame ready for decoding.
// Wire data is Annex B format with SPS/PPS prepended on keyframes.
// Encoded frames are small (~2-5KB each) so keeping them all in memory is fine.
type encodedFrame struct {
	wireData   []byte // Annex B H.264 with SPS/PPS prepended on keyframes
	pts        int64
	isKeyframe bool
}

// VideoDecoder decodes H.264 Annex B data to raw YUV420 planar.
// Matches transition.VideoDecoder via structural typing.
type VideoDecoder interface {
	Decode(h264 []byte) (yuv []byte, width, height int, err error)
	Close()
}

// DrainableDecoder extends VideoDecoder with EOS drain support.
type DrainableDecoder interface {
	VideoDecoder
	SendEOS() error
	ReceiveFrame() (yuv []byte, width, height int, err error)
}

// DecoderFactory is a function that creates a video decoder.
// This allows dependency injection for different build configurations
// (cgo vs non-cgo) and for testing.
type DecoderFactory func() (VideoDecoder, error)

// DefaultDecoderFactory creates a frame-threaded FFmpeg decoder via the
// codec package. Returns an error if FFmpeg is not available (non-cgo builds).
// One thread decodes a high-bitrate 1080p H.264 clip below real time on a
// server core, and the channel decodes on its tick; frames a threaded
// decoder holds back come out of later Next calls and the drain.
func DefaultDecoderFactory() (VideoDecoder, error) {
	return codec.NewVideoDecoder()
}

// FileFrameIterator demuxes a media file upfront and decodes frames on-demand.
// Encoded H.264 frames (~50MB for a 5-min clip) are kept in memory, but YUV
// decoding happens one frame at a time in Next(), keeping memory usage at ~3MB
// per decoded frame instead of ~81GB for all frames decoded upfront.
//
// Audio is interleaved lazily in Next() using PTS matching.
type FileFrameIterator struct {
	// mu guards the decode state and the closed flag so that Close() (which may
	// run on a different goroutine, e.g. snapshot-sync SeekToItem) is mutually
	// exclusive with an in-flight Next()/Seek(). Without this, Close() could free
	// the cgo/FFmpeg decoder while Next() is inside decoder.Decode() — a
	// use-after-free — and the plain bool `closed` would be a data race.
	mu sync.Mutex

	// Encoded frames (small -- kept in memory).
	encodedFrames []encodedFrame
	audioFrames   []audioFrameInfo

	// Decode state.
	decoder        VideoDecoder   // kept alive across Next() calls
	decoderFactory DecoderFactory // for creating fresh decoders on Seek
	draining       bool           // true after all encoded frames have been fed
	frameIndex     int            // next encoded frame to feed
	audioIndex     int            // next audio frame to consider
	held           int            // frames fed this pass that the decoder has not output

	// Looping: the frames of the next pass fed to get this pass's last
	// pictures out of the decoder, and how many of them it holds.
	loop     bool
	nextFed  int
	nextHeld int
	passDone bool // every picture of the pass is out; the decoder holds only the next pass

	// Output tracking for PTS estimation during drain.
	outputCount int   // number of decoded frames returned so far
	lastPTS     int64 // PTS of the last returned frame
	prevPTS     int64 // PTS of the second-to-last returned frame

	// Metadata.
	fps         float64
	totalFrames int
	closed      bool
}

// NewFileFrameIterator demuxes and decodes a media file, returning an iterator
// over raw YUV420 video frames with interleaved audio.
// Uses DefaultDecoderFactory for the video decoder.
func NewFileFrameIterator(path string) (*FileFrameIterator, error) {
	return NewFileFrameIteratorWithDecoder(path, DefaultDecoderFactory)
}

// NewFileFrameIteratorWithDecoder demuxes a media file and prepares an iterator
// that decodes frames on-demand. The decoder is created here but kept alive --
// actual decoding happens in Next(). This keeps memory usage proportional to
// encoded data size (~50MB for 5 min) rather than decoded YUV (~81GB).
func NewFileFrameIteratorWithDecoder(path string, factory DecoderFactory) (*FileFrameIterator, error) {
	// Step 1: Demux the file to get H.264 video frames + AAC audio frames.
	videoFrames, audioFrames, err := clip.DemuxFile(path)
	if err != nil {
		return nil, fmt.Errorf("demux %s: %w", path, err)
	}
	if len(videoFrames) == 0 {
		return nil, fmt.Errorf("no video frames in %s", path)
	}

	// Step 2: Create decoder (kept alive for streaming decode in Next()).
	if factory == nil {
		return nil, fmt.Errorf("no decoder factory provided")
	}
	decoder, err := factory()
	if err != nil {
		return nil, fmt.Errorf("create decoder: %w", err)
	}

	// Step 3: Pre-process encoded frames (AVC1 -> Annex B, prepend SPS/PPS).
	// These are small (~2-5KB each), safe to keep in memory.
	encoded := make([]encodedFrame, 0, len(videoFrames))
	var annexBBuf []byte
	var prependBuf []byte

	for _, vf := range videoFrames {
		// Convert AVC1 wire data to Annex B for the decoder.
		annexBBuf = codec.AVC1ToAnnexBInto(vf.WireData(), annexBBuf[:0])
		if len(annexBBuf) == 0 {
			continue
		}

		// Prepend SPS/PPS for keyframes so the decoder can initialize.
		var wireData []byte
		if vf.IsKeyframe() && len(vf.SPS()) > 0 {
			prependBuf = codec.PrependSPSPPSInto(vf.SPS(), vf.PPS(), annexBBuf, prependBuf[:0])
			wireData = make([]byte, len(prependBuf))
			copy(wireData, prependBuf)
		} else {
			wireData = make([]byte, len(annexBBuf))
			copy(wireData, annexBBuf)
		}

		encoded = append(encoded, encodedFrame{
			wireData:   wireData,
			pts:        vf.PTS(),
			isKeyframe: vf.IsKeyframe(),
		})
	}

	if len(encoded) == 0 {
		decoder.Close()
		return nil, fmt.Errorf("no video frames in %s", path)
	}

	// Step 4: Extract audio frame info for PTS-matched interleaving in Next().
	var audio []audioFrameInfo
	for _, af := range audioFrames {
		audio = append(audio, audioFrameInfo{
			data:       af.Data(),
			pts:        af.PTS(),
			sampleRate: af.SampleRate(),
			channels:   af.Channels(),
		})
	}

	return newFileFrameIterator(encoded, audio, decoder, factory), nil
}

// newFileFrameIterator builds an iterator over demuxed frames. A stream cut
// mid-GOP starts with frames that reference pictures it does not carry, and
// a stream cut at an open-GOP keyframe follows it with leading pictures,
// shown before it, that reference the GOP before. A fresh decoder drops
// both, but one that keeps running into the next pass of a loop holds the
// previous pass's references and puts out wrong pictures for them, so
// every pass would differ from the first. They are dropped here, with the
// audio that ends before the first keyframe, so every pass starts at the
// keyframe with its audio and frame indices count shown pictures. The
// audio is dropped even when the stream starts at the keyframe: a clip
// whose video was cut and audio was not would otherwise play the cut-off
// audio over its first pictures.
func newFileFrameIterator(encoded []encodedFrame, audio []audioFrameInfo, decoder VideoDecoder, factory DecoderFactory) *FileFrameIterator {
	for i, ef := range encoded {
		if !ef.isKeyframe {
			continue
		}
		audio = audio[sort.Search(len(audio), func(j int) bool {
			return audio[j].pts >= ef.pts || audio[j].pts+audioFrameTicks(audio[j]) > ef.pts
		}):]
		// Leading pictures come before the next keyframe in decode order
		// and below the keyframe in presentation order.
		kept := []encodedFrame{ef}
		j := i + 1
		for ; j < len(encoded) && !encoded[j].isKeyframe; j++ {
			if encoded[j].pts >= ef.pts {
				kept = append(kept, encoded[j])
			}
		}
		encoded = append(kept, encoded[j:]...)
		break
	}

	// totalFrames is the encoded count: a decoder that drops nothing puts
	// out one picture per frame, held-back ones in later Next calls and the
	// drain.
	return &FileFrameIterator{
		encodedFrames:  encoded,
		audioFrames:    audio,
		decoder:        decoder,
		decoderFactory: factory,
		fps:            estimateFPSFromEncodedFrames(encoded),
		totalFrames:    len(encoded),
	}
}

// Next returns the next decoded frame, or (nil, nil) at end of stream.
// Each call decodes one frame from the pre-demuxed H.264 data, keeping memory
// usage at ~3MB per frame instead of loading all YUV into memory.
//
// Audio frames are matched by PTS: each video frame gets the first available
// audio frame whose PTS falls within the video frame's time window
// [currentPTS, nextVideoPTS). The last video frame's window extends to MaxInt64.
func (f *FileFrameIterator) Next() (*DecodedFrame, error) {
	// Hold the lock for the whole decode so a concurrent Close() cannot free the
	// decoder out from under decoder.Decode()/ReceiveFrame() (use-after-free).
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed || f.passDone {
		return nil, nil
	}

	// Phase 1: Feed encoded frames to the decoder until we get output.
	for f.frameIndex < len(f.encodedFrames) {
		ef := f.encodedFrames[f.frameIndex]
		f.frameIndex++

		yuv, w, h, decErr := f.decoder.Decode(ef.wireData)
		if decErr != nil {
			// "buffering" is expected for B-frame reordering and frame
			// threading: the decoder keeps the frame and outputs it later.
			// Frames with other decode errors (corrupted data, etc.) are
			// skipped.
			if isBuffering(decErr) {
				f.held++
			}
			continue
		}

		if len(yuv) == 0 {
			continue
		}

		// Got a decoded frame. Deep-copy: decoder may reuse its output buffer.
		return f.makeDecodedFrame(yuv, w, h, ef.pts, ef.isKeyframe), nil
	}

	// Looping: the pass's last pictures are still in the decoder. Feed it
	// the next pass's first frames until they come out instead of draining
	// it: a drained decoder has to be replaced, and a fresh frame-threaded
	// decoder returns nothing until its pipeline has filled. Rewind starts
	// the next pass after the frames fed here.
	if f.loop && !f.draining && len(f.encodedFrames) > 2*f.held {
		for f.held > 0 && f.nextFed < len(f.encodedFrames) {
			ef := f.encodedFrames[f.nextFed]
			f.nextFed++
			yuv, w, h, decErr := f.decoder.Decode(ef.wireData)
			if decErr != nil {
				if isBuffering(decErr) {
					f.nextHeld++
				}
				continue
			}
			if len(yuv) == 0 {
				continue
			}
			f.held--
			f.nextHeld++
			return f.makeDecodedFrame(yuv, w, h, f.estimateDrainPTS(), false), nil
		}
		if f.held == 0 {
			f.passDone = true
			return nil, nil
		}
	}

	// Phase 2: Drain decoder (B-frame reorder buffer).
	if !f.draining {
		f.draining = true
		if drainable, ok := f.decoder.(DrainableDecoder); ok {
			_ = drainable.SendEOS()
		}
	}

	if drainable, ok := f.decoder.(DrainableDecoder); ok {
		yuv, w, h, drainErr := drainable.ReceiveFrame()
		if drainErr == nil && len(yuv) > 0 {
			// Estimate PTS for drained frames from the last returned frames.
			pts := f.estimateDrainPTS()
			return f.makeDecodedFrame(yuv, w, h, pts, false), nil
		}
	}

	return nil, nil // truly done
}

// makeDecodedFrame creates a DecodedFrame from decoded YUV data, attaches
// PTS-matched audio, and updates output tracking state.
func (f *FileFrameIterator) makeDecodedFrame(yuv []byte, w, h int, pts int64, isKeyframe bool) *DecodedFrame {
	// Deep-copy: decoder reuses its output buffer.
	yuvCopy := make([]byte, len(yuv))
	copy(yuvCopy, yuv)

	frame := &DecodedFrame{
		YUV:        yuvCopy,
		PTS:        pts,
		IsKeyframe: isKeyframe,
		Width:      w,
		Height:     h,
	}

	// PTS-matched audio interleaving.
	f.attachAudio(frame)

	// Update output tracking for drain PTS estimation.
	f.prevPTS = f.lastPTS
	f.lastPTS = pts
	f.outputCount++

	return frame
}

// attachAudio distributes audio frames proportionally across decoded video
// frames. Uses the same formula as clip/player.go:emitAudioForVideoFrame.
// The proportional approach guarantees all audio frames are delivered exactly
// once regardless of B-frame reordering or tick rate differences.
func (f *FileFrameIterator) attachAudio(frame *DecodedFrame) {
	totalAudio := len(f.audioFrames)
	totalVideo := len(f.encodedFrames)
	if totalAudio == 0 || totalVideo == 0 {
		return
	}

	// 1-based index matching clip/player.go (outputIdx incremented before call).
	targetAudioIdx := (f.outputCount + 1) * totalAudio / totalVideo
	if targetAudioIdx > totalAudio {
		targetAudioIdx = totalAudio
	}

	// Collect all audio frames from current index up to target.
	var batch []AudioFrameData
	for f.audioIndex < targetAudioIdx {
		af := f.audioFrames[f.audioIndex]
		batch = append(batch, AudioFrameData{
			Data:       af.data,
			PTS:        af.pts,
			SampleRate: af.sampleRate,
			Channels:   af.channels,
		})
		f.audioIndex++
	}

	if len(batch) > 0 {
		frame.AudioFrames = batch
		// Also set single-frame fields for backward compatibility.
		frame.AudioData = batch[0].Data
		frame.AudioPTS = batch[0].PTS
		frame.AudioSampleRate = batch[0].SampleRate
		frame.AudioChannels = batch[0].Channels
	}
}

// estimateDrainPTS estimates PTS for frames drained from the decoder's
// reorder buffer, based on the spacing of previously output frames.
func (f *FileFrameIterator) estimateDrainPTS() int64 {
	if f.outputCount >= 2 {
		return f.lastPTS + (f.lastPTS - f.prevPTS)
	}
	if f.outputCount == 1 {
		return f.lastPTS + 3000 // ~30fps default
	}
	return 0
}

// EstimateFPS returns the estimated frames per second.
func (f *FileFrameIterator) EstimateFPS() float64 {
	return f.fps
}

// TotalFrames returns the total number of frames.
// This is an estimate based on encoded frame count; the actual number of
// decoded frames may differ slightly due to B-frame reordering drain.
func (f *FileFrameIterator) TotalFrames() int {
	return f.totalFrames
}

// FrameIndex returns the current position in the encoded frame list.
func (f *FileFrameIterator) FrameIndex() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frameIndex
}

// SetLoop says whether another pass follows the current one. While set, the
// end of a pass keeps the decoder running into the next pass, so Rewind can
// continue without a fresh decoder.
func (f *FileFrameIterator) SetLoop(loop bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loop = loop
}

// Rewind starts the next pass at the first frame, keeping the demuxed frames.
// When the pass ended with the decoder already fed the next pass's first
// frames, decoding continues after them on the same decoder. Otherwise (the
// decoder was drained, or the pass was cut short) it restarts like Seek(0).
func (f *FileFrameIterator) Rewind() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return fmt.Errorf("iterator closed")
	}
	if !f.passDone {
		return f.seekLocked(0)
	}
	f.frameIndex = f.nextFed
	f.held = f.nextHeld
	f.nextFed, f.nextHeld = 0, 0
	f.passDone = false
	f.outputCount = 0
	f.audioIndex = 0
	f.lastPTS, f.prevPTS = 0, 0
	return nil
}

// Seek repositions the iterator to the nearest keyframe at or before targetFrame.
// The old decoder is closed and a fresh one is created so decoding restarts
// cleanly from the keyframe's SPS/PPS headers.
func (f *FileFrameIterator) Seek(targetFrame int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seekLocked(targetFrame)
}

// seekLocked is Seek with f.mu held.
func (f *FileFrameIterator) seekLocked(targetFrame int) error {
	if f.closed {
		return fmt.Errorf("iterator closed")
	}
	if targetFrame < 0 || targetFrame >= len(f.encodedFrames) {
		return fmt.Errorf("seek target %d out of range [0, %d)", targetFrame, len(f.encodedFrames))
	}
	if f.decoderFactory == nil {
		return fmt.Errorf("seek: no decoder factory available")
	}

	// Find nearest keyframe at or before targetFrame.
	seekIdx := targetFrame
	for seekIdx > 0 && !f.encodedFrames[seekIdx].isKeyframe {
		seekIdx--
	}

	// Close old decoder, create fresh one.
	if f.decoder != nil {
		f.decoder.Close()
	}
	dec, err := f.decoderFactory()
	if err != nil {
		return fmt.Errorf("seek: create decoder: %w", err)
	}
	f.decoder = dec

	// Reset state to keyframe position.
	f.frameIndex = seekIdx
	f.outputCount = seekIdx
	f.draining = false
	f.held = 0
	f.nextFed, f.nextHeld = 0, 0
	f.passDone = false
	f.lastPTS = 0
	f.prevPTS = 0

	// Reset audio index to match video position by PTS. The audio list
	// starts at the first picture, so a seek to it starts at the first
	// audio frame.
	if seekIdx == 0 {
		f.audioIndex = 0
	} else if seekIdx < len(f.encodedFrames) {
		targetPTS := f.encodedFrames[seekIdx].pts
		f.audioIndex = sort.Search(len(f.audioFrames), func(i int) bool {
			return f.audioFrames[i].pts >= targetPTS
		})
	}

	return nil
}

// Close releases the decoder resources. It is idempotent and safe to call
// concurrently with Next()/Seek(): the lock ensures Close() blocks until any
// in-flight decode completes before freeing the decoder.
func (f *FileFrameIterator) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return
	}
	f.closed = true
	if f.decoder != nil {
		f.decoder.Close()
	}
}

// audioFrameTicks is the duration of an AAC frame in 90 kHz ticks.
func audioFrameTicks(af audioFrameInfo) int64 {
	if af.sampleRate <= 0 {
		return 0
	}
	return 1024 * 90000 / int64(af.sampleRate)
}

// isBuffering reports whether a Decode error means the decoder kept the frame
// and has no picture to output yet.
func isBuffering(err error) bool {
	return strings.Contains(err.Error(), "buffering")
}

// estimateFPSFromEncodedFrames estimates FPS from encoded frame PTS values.
func estimateFPSFromEncodedFrames(frames []encodedFrame) float64 {
	if len(frames) < 2 {
		return 30.0
	}
	minPTS, maxPTS := frames[0].pts, frames[0].pts
	for _, f := range frames[1:] {
		if f.pts < minPTS {
			minPTS = f.pts
		}
		if f.pts > maxPTS {
			maxPTS = f.pts
		}
	}
	ptsSpan := maxPTS - minPTS
	if ptsSpan <= 0 {
		return 30.0
	}
	fps := float64(len(frames)-1) * 90000.0 / float64(ptsSpan)
	if fps < 10 {
		fps = 10
	}
	if fps > 120 {
		fps = 120
	}
	return fps
}

// estimateFPSFromDecoded is an alias for estimateFPS in channel.go.
// Kept for test compatibility.
func estimateFPSFromDecoded(frames []DecodedFrame) float64 {
	return estimateFPS(frames)
}
