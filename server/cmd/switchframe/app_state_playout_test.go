package main

import (
	"testing"

	"github.com/zsiec/switchframe/server/playout"
)

// The redundancy check in the UI compares the engines' playout channels
// from the control room state; an item one engine cannot open has to
// reach it.
func TestPlayoutChannelInfo_CarriesItemError(t *testing.T) {
	ch := playoutChannelInfo(playout.ChannelStatus{
		ID:        1,
		State:     "on_air",
		ItemIndex: 2,
		ItemError: "asset open failed: playout: asset not yet cached",
	})
	if ch.ItemError != "asset open failed: playout: asset not yet cached" || ch.State != "on_air" || ch.ItemIndex != 2 {
		t.Fatalf("channel info = %+v", ch)
	}
}
