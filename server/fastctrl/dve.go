package fastctrl

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/zsiec/switchframe/server/dve"
)

const (
	MsgDVESlotTransform byte = 0x04
	MsgDVESingleParam   byte = 0x05
)

// DVE single-parameter IDs for knob/slider control.
const (
	DVEParamPositionX    uint16 = 0x0000
	DVEParamPositionY    uint16 = 0x0001
	DVEParamWidth        uint16 = 0x0002
	DVEParamHeight       uint16 = 0x0003
	DVEParamRotationDeg  uint16 = 0x0004
	DVEParamPerspectiveX uint16 = 0x0005
	DVEParamPerspectiveY uint16 = 0x0006
	// 0x0007 reserved
	DVEParamCropTop       uint16 = 0x0008
	DVEParamCropBottom    uint16 = 0x0009
	DVEParamCropLeft      uint16 = 0x000A
	DVEParamCropRight     uint16 = 0x000B
	DVEParamOpacity       uint16 = 0x000C
	DVEParamDefocusRadius uint16 = 0x000D
	DVEParamBorderWidth   uint16 = 0x000E
	DVEParamCornerRadius  uint16 = 0x000F
)

// DVESlotTransform holds parsed DVE transform datagram fields.
type DVESlotTransform struct {
	SlotID    int
	PositionX float32
	PositionY float32
	Width     float32
	Height    float32
	Rotation  float32
	Opacity   float32
}

// ParseDVESlotTransform parses a DVE slot transform datagram.
// Wire format (25 bytes after message type byte stripped by dispatcher):
//
//	[0]       = slotId (uint8, 0-7)
//	[1-4]     = positionX (float32 BE)
//	[5-8]     = positionY (float32 BE)
//	[9-12]    = width (float32 BE)
//	[13-16]   = height (float32 BE)
//	[17-20]   = rotation degrees (float32 BE)
//	[21-24]   = opacity (float32 BE)
func ParseDVESlotTransform(data []byte) (DVESlotTransform, error) {
	if len(data) < 25 {
		return DVESlotTransform{}, fmt.Errorf("DVE slot transform: need 25 bytes, got %d", len(data))
	}

	slotID := int(data[0])
	if slotID >= dve.MaxSlots {
		return DVESlotTransform{}, fmt.Errorf("slot ID %d out of range (max %d)", slotID, dve.MaxSlots-1)
	}

	posX := math.Float32frombits(binary.BigEndian.Uint32(data[1:5]))
	posY := math.Float32frombits(binary.BigEndian.Uint32(data[5:9]))
	w := math.Float32frombits(binary.BigEndian.Uint32(data[9:13]))
	h := math.Float32frombits(binary.BigEndian.Uint32(data[13:17]))
	rot := math.Float32frombits(binary.BigEndian.Uint32(data[17:21]))
	opacity := math.Float32frombits(binary.BigEndian.Uint32(data[21:25]))

	if math.IsNaN(float64(posX)) || math.IsNaN(float64(posY)) ||
		math.IsNaN(float64(w)) || math.IsNaN(float64(h)) ||
		math.IsNaN(float64(rot)) || math.IsNaN(float64(opacity)) ||
		math.IsInf(float64(posX), 0) || math.IsInf(float64(posY), 0) ||
		math.IsInf(float64(w), 0) || math.IsInf(float64(h), 0) ||
		math.IsInf(float64(rot), 0) || math.IsInf(float64(opacity), 0) {
		return DVESlotTransform{}, fmt.Errorf("DVE slot transform: NaN or Inf value in datagram")
	}

	if w <= 0 {
		return DVESlotTransform{}, fmt.Errorf("DVE slot transform: width must be > 0, got %v", w)
	}
	if h <= 0 {
		return DVESlotTransform{}, fmt.Errorf("DVE slot transform: height must be > 0, got %v", h)
	}
	if opacity < 0 || opacity > 1 {
		return DVESlotTransform{}, fmt.Errorf("DVE slot transform: opacity must be in [0, 1], got %v", opacity)
	}

	return DVESlotTransform{
		SlotID:    slotID,
		PositionX: posX,
		PositionY: posY,
		Width:     w,
		Height:    h,
		Rotation:  rot,
		Opacity:   opacity,
	}, nil
}

// DVESingleParam holds a parsed single-parameter update.
type DVESingleParam struct {
	SlotID  int
	ParamID uint16
	Value   float32
}

// ParseDVESingleParam parses a DVE single parameter datagram.
// Wire format (7 bytes after message type byte stripped by dispatcher):
//
//	[0]     = slotId (uint8, 0-7)
//	[1-2]   = paramId (uint16 BE)
//	[3-6]   = value (float32 BE)
func ParseDVESingleParam(data []byte) (DVESingleParam, error) {
	if len(data) < 7 {
		return DVESingleParam{}, fmt.Errorf("DVE single param: need 7 bytes, got %d", len(data))
	}

	slotID := int(data[0])
	if slotID >= dve.MaxSlots {
		return DVESingleParam{}, fmt.Errorf("slot ID %d out of range (max %d)", slotID, dve.MaxSlots-1)
	}

	paramID := binary.BigEndian.Uint16(data[1:3])
	value := math.Float32frombits(binary.BigEndian.Uint32(data[3:7]))

	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		return DVESingleParam{}, fmt.Errorf("DVE single param: NaN or Inf value")
	}

	return DVESingleParam{
		SlotID:  slotID,
		ParamID: paramID,
		Value:   value,
	}, nil
}
