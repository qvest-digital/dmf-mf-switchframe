package htmlgfx

// bgraToRGBA converts BGRA pixels with premultiplied alpha to RGBA pixels
// with straight (non-premultiplied) alpha. dst and src may alias for
// in-place conversion. len(src) must be a multiple of 4 and len(dst)
// must be >= len(src).
func bgraToRGBA(dst, src []byte) {
	n := len(src)
	for i := 0; i < n; i += 4 {
		b := src[i]
		g := src[i+1]
		r := src[i+2]
		a := src[i+3]

		switch a {
		case 0:
			// Fully transparent: zero everything.
			dst[i] = 0
			dst[i+1] = 0
			dst[i+2] = 0
			dst[i+3] = 0
		case 255:
			// Fully opaque: premul == straight, just swap B↔R.
			dst[i] = r
			dst[i+1] = g
			dst[i+2] = b
			dst[i+3] = 255
		default:
			// Un-premultiply: channel = min((premul * 255 + a/2) / a, 255).
			a16 := uint16(a)
			rr := uint16(r)*255 + a16/2
			gg := uint16(g)*255 + a16/2
			bb := uint16(b)*255 + a16/2

			rr /= a16
			gg /= a16
			bb /= a16

			if rr > 255 {
				rr = 255
			}
			if gg > 255 {
				gg = 255
			}
			if bb > 255 {
				bb = 255
			}

			dst[i] = byte(rr)
			dst[i+1] = byte(gg)
			dst[i+2] = byte(bb)
			dst[i+3] = a
		}
	}
}
