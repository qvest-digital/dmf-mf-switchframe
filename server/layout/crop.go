package layout

// ComputeCropRect computes the largest source sub-region that matches the
// slot's aspect ratio. The region is positioned using anchorX/anchorY (0.0-1.0).
// All output values are even-aligned for YUV420 correctness.
func ComputeCropRect(srcW, srcH, slotW, slotH int, anchorX, anchorY float64) (cropX, cropY, cropW, cropH int) {
	if srcW <= 0 || srcH <= 0 || slotW <= 0 || slotH <= 0 {
		return 0, 0, srcW, srcH
	}

	// Compare aspect ratios using cross-multiplication to avoid float division.
	// srcW/srcH vs slotW/slotH → srcW*slotH vs slotW*srcH
	srcAspect := srcW * slotH
	slotAspect := slotW * srcH

	if srcAspect == slotAspect {
		// Aspect ratios match — no crop needed.
		return 0, 0, EvenAlign(srcW), EvenAlign(srcH)
	}

	if srcAspect > slotAspect {
		// Source is wider than slot — crop horizontally.
		cropH = EvenAlign(srcH)
		cropW = EvenAlign(slotW * srcH / slotH)
		if cropW > srcW {
			cropW = EvenAlign(srcW)
		}
	} else {
		// Source is taller than slot — crop vertically.
		cropW = EvenAlign(srcW)
		cropH = EvenAlign(slotH * srcW / slotW)
		if cropH > srcH {
			cropH = EvenAlign(srcH)
		}
	}

	// Position using anchor.
	maxX := srcW - cropW
	maxY := srcH - cropH
	cropX = EvenAlign(int(anchorX * float64(maxX)))
	cropY = EvenAlign(int(anchorY * float64(maxY)))

	return cropX, cropY, cropW, cropH
}

// CropYUV420Region extracts a YUV420 sub-region from src into dst.
// dst must be at least cropW*cropH*3/2 bytes.
// All crop coordinates must be even-aligned.
func CropYUV420Region(dst, src []byte, srcW, srcH, cropX, cropY, cropW, cropH int) {
	if cropW <= 0 || cropH <= 0 {
		return
	}

	srcYSize := srcW * srcH
	dstYSize := cropW * cropH
	chromaSrcW := srcW / 2
	chromaCropW := cropW / 2
	chromaCropH := cropH / 2
	chromaCropX := cropX / 2
	chromaCropY := cropY / 2
	chromaSrcH := srcH / 2

	// Y plane: row-by-row copy from sub-region.
	for y := 0; y < cropH; y++ {
		srcOff := (cropY+y)*srcW + cropX
		dstOff := y * cropW
		copy(dst[dstOff:dstOff+cropW], src[srcOff:srcOff+cropW])
	}

	// Cb and Cr planes at half resolution.
	for plane := 0; plane < 2; plane++ {
		srcBase := srcYSize + plane*(chromaSrcW*chromaSrcH)
		dstBase := dstYSize + plane*(chromaCropW*chromaCropH)
		for y := 0; y < chromaCropH; y++ {
			srcOff := srcBase + (chromaCropY+y)*chromaSrcW + chromaCropX
			dstOff := dstBase + y*chromaCropW
			copy(dst[dstOff:dstOff+chromaCropW], src[srcOff:srcOff+chromaCropW])
		}
	}
}
