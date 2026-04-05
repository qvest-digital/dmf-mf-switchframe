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
	MsgDVESlotSource    byte = 0x06
	MsgDVESlotEnable    byte = 0x07
	MsgDVEScaleMode     byte = 0x08
	MsgDVEBorder        byte = 0x09
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

// DVESlotSource holds a parsed slot source assignment.
type DVESlotSource struct {
	SlotID    int
	SourceKey string
}

// ParseDVESlotSource parses a DVE slot source datagram.
// Wire format (variable length, min 2 bytes after message type byte stripped by dispatcher):
//
//	[0]              = slotId (uint8, 0-7)
//	[1]              = keyLen (uint8)
//	[2..2+keyLen-1]  = key (bytes, UTF-8 source key)
func ParseDVESlotSource(data []byte) (DVESlotSource, error) {
	if len(data) < 2 {
		return DVESlotSource{}, fmt.Errorf("DVE slot source: need at least 2 bytes, got %d", len(data))
	}

	slotID := int(data[0])
	if slotID >= dve.MaxSlots {
		return DVESlotSource{}, fmt.Errorf("slot ID %d out of range (max %d)", slotID, dve.MaxSlots-1)
	}

	keyLen := int(data[1])
	if len(data) < 2+keyLen {
		return DVESlotSource{}, fmt.Errorf("DVE slot source: need %d bytes for key, got %d", 2+keyLen, len(data))
	}

	return DVESlotSource{
		SlotID:    slotID,
		SourceKey: string(data[2 : 2+keyLen]),
	}, nil
}

// DVESlotEnable holds a parsed slot enable/disable update.
type DVESlotEnable struct {
	SlotID  int
	Enabled bool
}

// ParseDVESlotEnable parses a DVE slot enable datagram.
// Wire format (2 bytes after message type byte stripped by dispatcher):
//
//	[0] = slotId (uint8, 0-7)
//	[1] = enabled (uint8, 0 or 1)
func ParseDVESlotEnable(data []byte) (DVESlotEnable, error) {
	if len(data) < 2 {
		return DVESlotEnable{}, fmt.Errorf("DVE slot enable: need 2 bytes, got %d", len(data))
	}

	slotID := int(data[0])
	if slotID >= dve.MaxSlots {
		return DVESlotEnable{}, fmt.Errorf("slot ID %d out of range (max %d)", slotID, dve.MaxSlots-1)
	}

	if data[1] > 1 {
		return DVESlotEnable{}, fmt.Errorf("DVE slot enable: enabled must be 0 or 1, got %d", data[1])
	}

	return DVESlotEnable{
		SlotID:  slotID,
		Enabled: data[1] == 1,
	}, nil
}

// DVEScaleMode holds a parsed scale mode update.
type DVEScaleMode struct {
	SlotID  int
	Mode    string
	AnchorX float32
	AnchorY float32
}

// scaleModeLookup maps wire mode bytes to string constants.
var scaleModeLookup = [3]string{dve.ScaleModeStretch, dve.ScaleModeFill, dve.ScaleModeFit}

// ParseDVEScaleMode parses a DVE scale mode datagram.
// Wire format (10 bytes after message type byte stripped by dispatcher):
//
//	[0]   = slotId (uint8, 0-7)
//	[1]   = mode (uint8): 0=stretch, 1=fill, 2=fit
//	[2-5] = anchorX (float32 BE)
//	[6-9] = anchorY (float32 BE)
func ParseDVEScaleMode(data []byte) (DVEScaleMode, error) {
	if len(data) < 10 {
		return DVEScaleMode{}, fmt.Errorf("DVE scale mode: need 10 bytes, got %d", len(data))
	}

	slotID := int(data[0])
	if slotID >= dve.MaxSlots {
		return DVEScaleMode{}, fmt.Errorf("slot ID %d out of range (max %d)", slotID, dve.MaxSlots-1)
	}

	mode := data[1]
	if mode > 2 {
		return DVEScaleMode{}, fmt.Errorf("DVE scale mode: mode must be 0-2, got %d", mode)
	}

	anchorX := math.Float32frombits(binary.BigEndian.Uint32(data[2:6]))
	anchorY := math.Float32frombits(binary.BigEndian.Uint32(data[6:10]))

	if math.IsNaN(float64(anchorX)) || math.IsNaN(float64(anchorY)) ||
		math.IsInf(float64(anchorX), 0) || math.IsInf(float64(anchorY), 0) {
		return DVEScaleMode{}, fmt.Errorf("DVE scale mode: NaN or Inf value in datagram")
	}

	if anchorX < 0 || anchorX > 1 {
		return DVEScaleMode{}, fmt.Errorf("DVE scale mode: anchorX must be in [0, 1], got %v", anchorX)
	}
	if anchorY < 0 || anchorY > 1 {
		return DVEScaleMode{}, fmt.Errorf("DVE scale mode: anchorY must be in [0, 1], got %v", anchorY)
	}

	return DVEScaleMode{
		SlotID:  slotID,
		Mode:    scaleModeLookup[mode],
		AnchorX: anchorX,
		AnchorY: anchorY,
	}, nil
}

// DVEBorder holds a parsed border update.
type DVEBorder struct {
	SlotID  int
	Width   float32
	Radius  float32
	ColorY  byte
	ColorCb byte
	ColorCr byte
}

// ParseDVEBorder parses a DVE border datagram.
// Wire format (12 bytes after message type byte stripped by dispatcher):
//
//	[0]    = slotId (uint8, 0-7)
//	[1-4]  = width (float32 BE)
//	[5-8]  = radius (float32 BE)
//	[9]    = Y (uint8)
//	[10]   = Cb (uint8)
//	[11]   = Cr (uint8)
func ParseDVEBorder(data []byte) (DVEBorder, error) {
	if len(data) < 12 {
		return DVEBorder{}, fmt.Errorf("DVE border: need 12 bytes, got %d", len(data))
	}

	slotID := int(data[0])
	if slotID >= dve.MaxSlots {
		return DVEBorder{}, fmt.Errorf("slot ID %d out of range (max %d)", slotID, dve.MaxSlots-1)
	}

	width := math.Float32frombits(binary.BigEndian.Uint32(data[1:5]))
	radius := math.Float32frombits(binary.BigEndian.Uint32(data[5:9]))

	if math.IsNaN(float64(width)) || math.IsNaN(float64(radius)) ||
		math.IsInf(float64(width), 0) || math.IsInf(float64(radius), 0) {
		return DVEBorder{}, fmt.Errorf("DVE border: NaN or Inf value in datagram")
	}

	if width < 0 {
		return DVEBorder{}, fmt.Errorf("DVE border: width must be >= 0, got %v", width)
	}
	if radius < 0 {
		return DVEBorder{}, fmt.Errorf("DVE border: radius must be >= 0, got %v", radius)
	}

	return DVEBorder{
		SlotID:  slotID,
		Width:   width,
		Radius:  radius,
		ColorY:  data[9],
		ColorCb: data[10],
		ColorCr: data[11],
	}, nil
}
