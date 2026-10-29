package transition

import "github.com/zsiec/switchframe/server/internal/video"

// ScaleYUV420Preview scales a YUV420 frame optimized for preview encoding.
// On amd64, uses box-shrink 2x + bilinear for the remainder when the source
// is at least twice the destination. Bilinear alone samples 2x2 source
// pixels per destination pixel and skips the rest, so detail finer than the
// destination aliases into moire; the box pass averages it out first. It is
// the slower of the two: about 3.8 ms against 2.0 ms per 1080p to 480p frame.
func ScaleYUV420Preview(src []byte, srcW, srcH int, dst []byte, dstW, dstH int, boxBuf *[]byte) {
	if srcW == dstW && srcH == dstH {
		copy(dst[:video.YUV420FrameSize(srcW, srcH)], src[:video.YUV420FrameSize(srcW, srcH)])
		return
	}

	// Box pre-shrink only when the half size still covers the destination.
	// Below twice the destination, as 1080p to 720p, it would go down to
	// 960x540 and scale back up, losing detail the destination can show.
	if srcW >= dstW*2 && srcH >= dstH*2 && srcW >= 4 && srcH >= 4 {
		halfW := srcW / 2
		halfH := srcH / 2
		halfSize := video.YUV420FrameSize(halfW, halfH)
		if boxBuf != nil {
			if cap(*boxBuf) < halfSize {
				*boxBuf = make([]byte, halfSize)
			}
			*boxBuf = (*boxBuf)[:halfSize]
			BoxShrink2xYUV420(src, srcW, srcH, *boxBuf, halfW, halfH)
			ScaleYUV420(*boxBuf, halfW, halfH, dst, dstW, dstH)
			return
		}
	}

	ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)
}
