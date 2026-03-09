//go:build arm64

package graphics

// alphaBlendRGBAChromaRow processes one row of Cb/Cr chroma planes using NEON.
// Reads RGBA at stride-8 (every other full-res pixel), computes BT.709 Cb/Cr,
// and alpha-blends into the existing chroma values.
//
// Parameters:
//   - cbRow: pointer to one row of Cb plane (chromaWidth bytes)
//   - crRow: pointer to one row of Cr plane (chromaWidth bytes)
//   - rgba: pointer to corresponding full-res RGBA row (chromaWidth*2 pixels = chromaWidth*8 bytes)
//   - chromaWidth: number of chroma pixels in this row (= width/2)
//   - alphaScale256: pre-scaled alpha (0-256)
//
//go:noescape
func alphaBlendRGBAChromaRow(cbRow *byte, crRow *byte, rgba *byte, chromaWidth int, alphaScale256 int)
