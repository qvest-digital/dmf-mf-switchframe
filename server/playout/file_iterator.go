package playout

import (
	"fmt"
	"math"
	"strings"

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

// DefaultDecoderFactory creates a single-threaded FFmpeg decoder via the
// codec package. Returns an error if FFmpeg is not available (non-cgo builds).
func DefaultDecoderFactory() (VideoDecoder, error) {
	return codec.NewVideoDecoderSingleThread()
}

// FileFrameIterator demuxes and decodes a media file to raw YUV+audio frames.
// It loads ALL frames into memory upfront (suitable for playout clips, not
// multi-hour streams). Audio is interleaved lazily in Next() using PTS matching.
type FileFrameIterator struct {
	frames      []DecodedFrame
	audioFrames []audioFrameInfo
	index       int
	audioIndex  int
	fps         float64
}

// NewFileFrameIterator demuxes and decodes a media file, returning an iterator
// over raw YUV420 video frames with interleaved audio.
// Uses DefaultDecoderFactory for the video decoder.
func NewFileFrameIterator(path string) (*FileFrameIterator, error) {
	return NewFileFrameIteratorWithDecoder(path, DefaultDecoderFactory)
}

// NewFileFrameIteratorWithDecoder demuxes and decodes a media file using the
// provided decoder factory. This allows callers to inject mock decoders for
// testing or alternative decoder implementations.
func NewFileFrameIteratorWithDecoder(path string, factory DecoderFactory) (*FileFrameIterator, error) {
	// Step 1: Demux the file to get H.264 video frames + AAC audio frames.
	videoFrames, audioFrames, err := clip.DemuxFile(path)
	if err != nil {
		return nil, fmt.Errorf("demux %s: %w", path, err)
	}
	if len(videoFrames) == 0 {
		return nil, fmt.Errorf("no video frames in %s", path)
	}

	// Step 2: Create decoder.
	if factory == nil {
		return nil, fmt.Errorf("no decoder factory provided")
	}
	decoder, err := factory()
	if err != nil {
		return nil, fmt.Errorf("create decoder: %w", err)
	}
	defer decoder.Close()

	// Step 3: Decode each video frame to YUV420.
	var decoded []DecodedFrame
	var annexBBuf []byte
	var prependBuf []byte

	for _, vf := range videoFrames {
		// Convert AVC1 wire data to Annex B for the decoder.
		annexBBuf = codec.AVC1ToAnnexBInto(vf.WireData(), annexBBuf[:0])
		if len(annexBBuf) == 0 {
			continue
		}

		// Prepend SPS/PPS for keyframes so the decoder can initialize.
		decoderInput := annexBBuf
		if vf.IsKeyframe() && len(vf.SPS()) > 0 {
			prependBuf = codec.PrependSPSPPSInto(vf.SPS(), vf.PPS(), annexBBuf, prependBuf[:0])
			decoderInput = prependBuf
		}

		yuv, w, h, decErr := decoder.Decode(decoderInput)
		if decErr != nil {
			errMsg := decErr.Error()
			// "buffering" is expected for B-frame reordering — the decoder
			// needs reference frames before it can output.
			if strings.Contains(errMsg, "buffering") {
				continue
			}
			// Skip frames with decode errors (corrupted data, etc.).
			continue
		}

		// Deep-copy: decoder may reuse its output buffer.
		yuvCopy := make([]byte, len(yuv))
		copy(yuvCopy, yuv)

		decoded = append(decoded, DecodedFrame{
			YUV:        yuvCopy,
			PTS:        vf.PTS(),
			IsKeyframe: vf.IsKeyframe(),
			Width:      w,
			Height:     h,
		})
	}

	// Step 4: Drain remaining buffered frames (B-frame reordering).
	if drainable, ok := decoder.(DrainableDecoder); ok {
		_ = drainable.SendEOS()
		for {
			yuv, w, h, drainErr := drainable.ReceiveFrame()
			if drainErr != nil {
				break
			}
			yuvCopy := make([]byte, len(yuv))
			copy(yuvCopy, yuv)

			// Drained frames don't have a PTS from the source — estimate
			// from the last decoded frame's PTS.
			var pts int64
			if len(decoded) > 0 {
				last := decoded[len(decoded)-1]
				// Estimate PTS spacing from existing frames.
				if len(decoded) >= 2 {
					pts = last.PTS + (last.PTS - decoded[len(decoded)-2].PTS)
				} else {
					pts = last.PTS + 3000 // ~30fps default
				}
			}

			decoded = append(decoded, DecodedFrame{
				YUV:    yuvCopy,
				PTS:    pts,
				Width:  w,
				Height: h,
			})
		}
	}

	if len(decoded) == 0 {
		return nil, fmt.Errorf("no frames decoded from %s", path)
	}

	// Step 5: Extract audio frame info for PTS-matched interleaving in Next().
	var audio []audioFrameInfo
	for _, af := range audioFrames {
		audio = append(audio, audioFrameInfo{
			data:       af.Data(),
			pts:        af.PTS(),
			sampleRate: af.SampleRate(),
			channels:   af.Channels(),
		})
	}

	// Step 6: Estimate FPS from decoded frame PTS values.
	fps := estimateFPSFromDecoded(decoded)

	return &FileFrameIterator{
		frames:      decoded,
		audioFrames: audio,
		index:       0,
		fps:         fps,
	}, nil
}

// Next returns the next decoded frame, or (nil, nil) at end of stream.
// Audio frames are matched by PTS: each video frame gets the first available
// audio frame whose PTS falls within the video frame's time window
// [currentPTS, nextVideoPTS). The last video frame's window extends to MaxInt64.
func (f *FileFrameIterator) Next() (*DecodedFrame, error) {
	if f.index >= len(f.frames) {
		return nil, nil
	}
	frame := &f.frames[f.index]

	// Determine the PTS upper bound for this video frame's audio window.
	var ptsBound int64
	if f.index+1 < len(f.frames) {
		ptsBound = f.frames[f.index+1].PTS
	} else {
		ptsBound = math.MaxInt64
	}

	// Determine the lower bound: for the first video frame, accept all audio
	// from the beginning (lower bound = MinInt64). For subsequent frames, skip
	// audio that falls before this video frame's PTS.
	if f.index > 0 {
		for f.audioIndex < len(f.audioFrames) && f.audioFrames[f.audioIndex].pts < frame.PTS {
			f.audioIndex++
		}
	}

	// Attach the first audio frame in [lowerBound, nextVideoPTS).
	if f.audioIndex < len(f.audioFrames) {
		af := f.audioFrames[f.audioIndex]
		if af.pts < ptsBound {
			frame.AudioData = af.data
			frame.AudioPTS = af.pts
			frame.AudioSampleRate = af.sampleRate
			frame.AudioChannels = af.channels
			f.audioIndex++
		}
	}

	f.index++
	return frame, nil
}

// EstimateFPS returns the estimated frames per second.
func (f *FileFrameIterator) EstimateFPS() float64 {
	return f.fps
}

// TotalFrames returns the total number of decoded frames.
func (f *FileFrameIterator) TotalFrames() int {
	return len(f.frames)
}

// Close is a no-op — all resources are released during construction.
func (f *FileFrameIterator) Close() {}

// estimateFPSFromDecoded estimates FPS from decoded frame PTS values.
func estimateFPSFromDecoded(frames []DecodedFrame) float64 {
	if len(frames) < 2 {
		return 30.0
	}
	minPTS, maxPTS := frames[0].PTS, frames[0].PTS
	for _, f := range frames[1:] {
		if f.PTS < minPTS {
			minPTS = f.PTS
		}
		if f.PTS > maxPTS {
			maxPTS = f.PTS
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
