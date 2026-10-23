package main

import (
	"context"

	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/fastctrl"
)

// initFastControl sets up the fast-control datagram dispatcher with handlers
// for transition position updates, graphics position updates, and DVE transforms.
func (a *App) initFastControl() {
	a.fastCtrl = fastctrl.NewDispatcher()

	a.fastCtrl.Register(fastctrl.MsgTransitionPosition, a.handleFastTransitionUpdate)
	a.fastCtrl.Register(fastctrl.MsgGraphicsLayerPosition, a.handleFastGraphicsUpdate)
	a.fastCtrl.Register(fastctrl.MsgDVESlotTransform, a.handleFastDVETransform)
	a.fastCtrl.Register(fastctrl.MsgDVESingleParam, a.handleFastDVESingleParam)
	a.fastCtrl.Register(fastctrl.MsgDVESlotSource, a.handleFastDVESlotSource)
	a.fastCtrl.Register(fastctrl.MsgDVESlotEnable, a.handleFastDVESlotEnable)
	a.fastCtrl.Register(fastctrl.MsgDVEScaleMode, a.handleFastDVEScaleMode)
	a.fastCtrl.Register(fastctrl.MsgDVEBorder, a.handleFastDVEBorder)
	a.fastCtrl.Register(fastctrl.MsgPing, a.handleFastPing)
}

func (a *App) handleFastTransitionUpdate(data []byte) error {
	pos, err := fastctrl.ParseTransitionPosition(data)
	if err != nil {
		return err
	}
	return a.sw.SetTransitionPosition(context.Background(), pos)
}

func (a *App) handleFastGraphicsUpdate(data []byte) error {
	layerID, rect, err := fastctrl.ParseGraphicsLayerPosition(data)
	if err != nil {
		return err
	}
	return a.compositor.UpdateLayerRect(layerID, rect)
}

func (a *App) handleFastDVETransform(data []byte) error {
	t, err := fastctrl.ParseDVESlotTransform(data)
	if err != nil {
		return err
	}
	dl := a.dveCompositor.GetLayout()
	if dl == nil || t.SlotID >= len(dl.Slots) {
		return nil // ignore if no DVE layout or slot out of range
	}
	a.dveCompositor.UpdateSlotTransform(t.SlotID, func(xf *dve.Transform) {
		xf.PositionX = float64(t.PositionX)
		xf.PositionY = float64(t.PositionY)
		xf.Width = float64(t.Width)
		xf.Height = float64(t.Height)
		xf.RotationDeg = float64(t.Rotation)
		if t.Opacity > 0 {
			xf.Opacity = float64(t.Opacity)
		}
	})
	return nil
}

func (a *App) handleFastDVESingleParam(data []byte) error {
	p, err := fastctrl.ParseDVESingleParam(data)
	if err != nil {
		return err
	}
	dl := a.dveCompositor.GetLayout()
	if dl == nil || p.SlotID >= len(dl.Slots) {
		return nil
	}
	a.dveCompositor.UpdateSlotTransform(p.SlotID, func(xf *dve.Transform) {
		v := float64(p.Value)
		switch p.ParamID {
		case fastctrl.DVEParamPositionX:
			xf.PositionX = v
		case fastctrl.DVEParamPositionY:
			xf.PositionY = v
		case fastctrl.DVEParamWidth:
			xf.Width = v
		case fastctrl.DVEParamHeight:
			xf.Height = v
		case fastctrl.DVEParamRotationDeg:
			xf.RotationDeg = v
		case fastctrl.DVEParamPerspectiveX:
			xf.PerspectiveX = v
		case fastctrl.DVEParamPerspectiveY:
			xf.PerspectiveY = v
		case fastctrl.DVEParamCropTop:
			xf.CropTop = v
		case fastctrl.DVEParamCropBottom:
			xf.CropBottom = v
		case fastctrl.DVEParamCropLeft:
			xf.CropLeft = v
		case fastctrl.DVEParamCropRight:
			xf.CropRight = v
		case fastctrl.DVEParamOpacity:
			xf.Opacity = v
		case fastctrl.DVEParamDefocusRadius:
			xf.DefocusRadius = v
		case fastctrl.DVEParamBorderWidth:
			xf.Border.Width = v
		case fastctrl.DVEParamCornerRadius:
			xf.Border.CornerRadius = v
		}
	})
	return nil
}

func (a *App) handleFastDVESlotSource(data []byte) error {
	src, err := fastctrl.ParseDVESlotSource(data)
	if err != nil {
		return err
	}
	dl := a.dveCompositor.GetLayout()
	if dl == nil || src.SlotID >= len(dl.Slots) {
		return nil
	}
	a.dveCompositor.UpdateSlot(src.SlotID, func(s *dve.Slot) {
		s.SourceKey = src.SourceKey
	})
	return nil
}

func (a *App) handleFastDVESlotEnable(data []byte) error {
	msg, err := fastctrl.ParseDVESlotEnable(data)
	if err != nil {
		return err
	}
	if msg.Enabled {
		a.dveCompositor.SlotOn(msg.SlotID)
	} else {
		a.dveCompositor.SlotOff(msg.SlotID)
	}
	return nil
}

func (a *App) handleFastDVEScaleMode(data []byte) error {
	msg, err := fastctrl.ParseDVEScaleMode(data)
	if err != nil {
		return err
	}
	dl := a.dveCompositor.GetLayout()
	if dl == nil || msg.SlotID >= len(dl.Slots) {
		return nil
	}
	a.dveCompositor.UpdateSlotTransform(msg.SlotID, func(xf *dve.Transform) {
		xf.ScaleMode = msg.Mode
		xf.CropAnchor = [2]float64{float64(msg.AnchorX), float64(msg.AnchorY)}
	})
	return nil
}

func (a *App) handleFastDVEBorder(data []byte) error {
	msg, err := fastctrl.ParseDVEBorder(data)
	if err != nil {
		return err
	}
	dl := a.dveCompositor.GetLayout()
	if dl == nil || msg.SlotID >= len(dl.Slots) {
		return nil
	}
	a.dveCompositor.UpdateSlotTransform(msg.SlotID, func(xf *dve.Transform) {
		xf.Border.Width = float64(msg.Width)
		xf.Border.CornerRadius = float64(msg.Radius)
		xf.Border.ColorY = msg.ColorY
		xf.Border.ColorCb = msg.ColorCb
		xf.Border.ColorCr = msg.ColorCr
	})
	return nil
}

// handleFastPing answers on the control track: a datagram reaches the
// server without a session to reply on. Every viewer of the control track
// receives the pong, and a browser matches it to its own ping by seq.
func (a *App) handleFastPing(data []byte) error {
	seq, _ := fastctrl.ParsePing(data)
	a.controlPub.PublishRaw(fastctrl.BuildPong(clock.Now(), seq))
	a.fastCtrl.RecordPongSent()
	return nil
}
