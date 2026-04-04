package main

import (
	"context"

	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/fastctrl"
)

// initFastControl sets up the fast-control datagram dispatcher with handlers
// for layout slot position updates and transition position updates.
func (a *App) initFastControl() {
	a.fastCtrl = fastctrl.NewDispatcher()

	a.fastCtrl.Register(fastctrl.MsgLayoutSlotPosition, a.handleFastLayoutUpdate)
	a.fastCtrl.Register(fastctrl.MsgTransitionPosition, a.handleFastTransitionUpdate)
	a.fastCtrl.Register(fastctrl.MsgGraphicsLayerPosition, a.handleFastGraphicsUpdate)
	a.fastCtrl.Register(fastctrl.MsgDVESlotTransform, a.handleFastDVETransform)
	a.fastCtrl.Register(fastctrl.MsgDVESingleParam, a.handleFastDVESingleParam)
}

func (a *App) handleFastLayoutUpdate(data []byte) error {
	slotID, rect, err := fastctrl.ParseLayoutSlotPosition(data)
	if err != nil {
		return err
	}
	return a.layoutCompositor.UpdateSlotRect(slotID, rect)
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
	dl := a.layoutCompositor.GetDVELayout()
	if dl == nil || t.SlotID >= len(dl.Slots) {
		return nil // ignore if no DVE layout or slot out of range
	}
	a.layoutCompositor.UpdateDVESlotTransform(t.SlotID, func(xf *dve.Transform) {
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
	dl := a.layoutCompositor.GetDVELayout()
	if dl == nil || p.SlotID >= len(dl.Slots) {
		return nil
	}
	a.layoutCompositor.UpdateDVESlotTransform(p.SlotID, func(xf *dve.Transform) {
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
