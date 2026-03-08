package transition

// BlendUniformBytes applies uniform alpha blend across byte slices:
//
//	dst[i] = (a[i]*(256-pos) + b[i]*pos) >> 8
//
// pos must be 0-256. SIMD-accelerated on amd64 and arm64.
// dst, a, and b must all have the same length. Returns immediately if len(dst) == 0.
func BlendUniformBytes(dst, a, b []byte, pos int) {
	if len(dst) == 0 {
		return
	}
	blendUniform(&dst[0], &a[0], &b[0], len(dst), pos, 256-pos)
}
