package fastctrl

import (
	"encoding/binary"
	"fmt"
	"image"

	"github.com/zsiec/switchframe/server/layout"
)

const (
	MsgLayoutSlotPosition byte = 0x01
	MsgTransitionPosition byte = 0x02
)

func ParseLayoutSlotPosition(data []byte) (slotID int, rect image.Rectangle, err error) {
	if len(data) < 9 {
		return 0, image.Rectangle{}, fmt.Errorf("layout slot position: need 9 bytes, got %d", len(data))
	}
	slotID = int(data[0])
	if slotID >= layout.MaxSlots {
		return 0, image.Rectangle{}, fmt.Errorf("slot ID %d out of range (max %d)", slotID, layout.MaxSlots-1)
	}
	x := int(binary.BigEndian.Uint16(data[1:3]))
	y := int(binary.BigEndian.Uint16(data[3:5]))
	w := int(binary.BigEndian.Uint16(data[5:7]))
	h := int(binary.BigEndian.Uint16(data[7:9]))
	if x%2 != 0 || y%2 != 0 || w%2 != 0 || h%2 != 0 {
		return 0, image.Rectangle{}, fmt.Errorf("values must be even-aligned: x=%d y=%d w=%d h=%d", x, y, w, h)
	}
	if w == 0 || h == 0 {
		return 0, image.Rectangle{}, fmt.Errorf("zero dimension: w=%d h=%d", w, h)
	}
	rect = image.Rect(x, y, x+w, y+h)
	return slotID, rect, nil
}
