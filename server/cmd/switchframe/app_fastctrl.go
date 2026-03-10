package main

import (
	"context"

	"github.com/zsiec/switchframe/server/fastctrl"
)

// initFastControl sets up the fast-control datagram dispatcher with handlers
// for layout slot position updates and transition position updates.
func (a *App) initFastControl() {
	a.fastCtrl = fastctrl.New()

	a.fastCtrl.Register(fastctrl.MsgLayoutSlotPosition, a.handleFastLayoutUpdate)
	a.fastCtrl.Register(fastctrl.MsgTransitionPosition, a.handleFastTransitionUpdate)
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
