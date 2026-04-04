package wipemap

// GenHorizontal creates a left-to-right (or right-to-left if reverse) gradient.
func GenHorizontal(reverse bool) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		denom := max(w-1, 1)
		for x := 0; x < w; x++ {
			v := byte(x * 255 / denom)
			if reverse {
				v = 255 - v
			}
			for y := 0; y < h; y++ {
				buf[y*w+x] = v
			}
		}
		return buf
	}
}

// GenVertical creates a top-to-bottom (or bottom-to-top if reverse) gradient.
func GenVertical(reverse bool) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		denom := max(h-1, 1)
		for y := 0; y < h; y++ {
			v := byte(y * 255 / denom)
			if reverse {
				v = 255 - v
			}
			row := buf[y*w : y*w+w]
			for i := range row {
				row[i] = v
			}
		}
		return buf
	}
}
