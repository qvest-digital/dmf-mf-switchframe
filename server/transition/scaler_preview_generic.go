//go:build !amd64 && !arm64

package transition

// ScaleYUV420Preview scales a YUV420 frame optimized for preview encoding.
// Generic fallback uses direct bilinear.
func ScaleYUV420Preview(src []byte, srcW, srcH int, dst []byte, dstW, dstH int, boxBuf *[]byte) {
	ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)
}
