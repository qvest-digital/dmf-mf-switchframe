//go:build amd64

package graphics

// alphaBlendRGBAChromaRow processes one row of Cb/Cr chroma planes.
// Scalar amd64 assembly: eliminates bounds checks for stride-8 RGBA access.
//
//go:noescape
func alphaBlendRGBAChromaRow(cbRow *byte, crRow *byte, rgba *byte, chromaWidth int, alphaScale256 int)
