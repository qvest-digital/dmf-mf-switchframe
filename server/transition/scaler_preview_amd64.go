package transition

// ScaleYUV420Preview scales a YUV420 frame optimized for preview encoding.
// On amd64, uses box-shrink 2x + bilinear for the remainder when the source
// is ≥1.5x the destination. This avoids the expensive scalar bilinear gather
// on 75% of the source pixels.
func ScaleYUV420Preview(src []byte, srcW, srcH int, dst []byte, dstW, dstH int, boxBuf *[]byte) {
	if srcW == dstW && srcH == dstH {
		copy(dst[:srcW*srcH*3/2], src[:srcW*srcH*3/2])
		return
	}

	// Box pre-shrink when source is ≥1.5x destination in both dimensions.
	if srcW >= dstW*3/2 && srcH >= dstH*3/2 && srcW >= 4 && srcH >= 4 {
		halfW := srcW / 2
		halfH := srcH / 2
		halfSize := halfW * halfH * 3 / 2
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
