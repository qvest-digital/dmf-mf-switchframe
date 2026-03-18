package transition

// ScaleYUV420Preview scales a YUV420 frame optimized for preview encoding.
// On arm64, the NEON bilinear kernel is already fast enough that box pre-shrink
// adds overhead from the extra memory pass. Use direct bilinear.
func ScaleYUV420Preview(src []byte, srcW, srcH int, dst []byte, dstW, dstH int, boxBuf *[]byte) {
	ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)
}
