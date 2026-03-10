package fastctrl

import (
	"encoding/binary"
	"fmt"
	"image"
)

const MsgGraphicsLayerPosition byte = 0x03

// ParseGraphicsLayerPosition parses a graphics layer position datagram.
// Wire format (10 bytes):
//
//	[0-1]  = Layer ID (uint16 BE)
//	[2-3]  = X (uint16 BE)
//	[4-5]  = Y (uint16 BE)
//	[6-7]  = Width (uint16 BE)
//	[8-9]  = Height (uint16 BE)
func ParseGraphicsLayerPosition(data []byte) (layerID int, rect image.Rectangle, err error) {
	if len(data) < 10 {
		return 0, image.Rectangle{}, fmt.Errorf("graphics layer position: need 10 bytes, got %d", len(data))
	}
	layerID = int(binary.BigEndian.Uint16(data[0:2]))
	x := int(binary.BigEndian.Uint16(data[2:4]))
	y := int(binary.BigEndian.Uint16(data[4:6]))
	w := int(binary.BigEndian.Uint16(data[6:8]))
	h := int(binary.BigEndian.Uint16(data[8:10]))
	if x%2 != 0 || y%2 != 0 || w%2 != 0 || h%2 != 0 {
		return 0, image.Rectangle{}, fmt.Errorf("values must be even-aligned: x=%d y=%d w=%d h=%d", x, y, w, h)
	}
	if w == 0 || h == 0 {
		return 0, image.Rectangle{}, fmt.Errorf("zero dimension: w=%d h=%d", w, h)
	}
	rect = image.Rect(x, y, x+w, y+h)
	return layerID, rect, nil
}
