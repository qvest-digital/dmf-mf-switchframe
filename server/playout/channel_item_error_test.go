package playout

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// switchableProvider fails every Open while failing is set and serves
// frames otherwise. failed counts the Opens that failed.
type switchableProvider struct {
	failing atomic.Bool
	failed  atomic.Int32
	frames  *FakeAssetProvider
}

func (p *switchableProvider) Open(uri string) (FrameIterator, error) {
	if p.failing.Load() {
		p.failed.Add(1)
		return nil, errors.New("playout: asset not yet cached")
	}
	return p.frames.Open(uri)
}

// A channel whose asset cannot be opened stays on air, moving past the
// item and back to it on a loop, with nothing playing. Its status has to
// say so: the peer engine plays the same item, and the redundancy check
// otherwise reads the pair as in sync.
func TestChannel_StatusReportsItemThatCannotBeOpened(t *testing.T) {
	p := &switchableProvider{frames: fakeProvider(50)}
	p.failing.Store(true)
	ch := NewChannel(1, ChannelConfig{AssetProvider: p, AllowedLocalPrefixes: []string{"/"}})
	item := clipItem("a")
	item.Loop = true
	if err := ch.LoadPlaylist(testPlaylist(item)); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ch.Stop()

	waitFor(t, 2*time.Second, func() bool { return ch.Status().ItemError != "" })
	st := ch.Status()
	if st.State != "on_air" || !strings.Contains(st.ItemError, "asset not yet cached") {
		t.Fatalf("status = %+v, want on_air with the open error", st)
	}

	// The channel does not try the item again by itself; a snapshot push
	// from the peer seeks it, and once the asset opens the error is gone.
	p.failing.Store(false)
	if err := ch.SeekToItem(0, 0); err != nil {
		t.Fatalf("SeekToItem: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return ch.Status().ItemError == "" })

	// A reload starts from a clean slate.
	p.failing.Store(true)
	if err := ch.SeekToItem(0, 0); err != nil {
		t.Fatalf("SeekToItem: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return ch.Status().ItemError != "" })
	ch.Stop()
	if err := ch.LoadPlaylist(testPlaylist(item)); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if e := ch.Status().ItemError; e != "" {
		t.Fatalf("item error after reload = %q", e)
	}
}

// The error describes the item on air. Once the scheduler has moved past
// the item that failed to open, an engine still reporting it would read
// as a playout mismatch against a peer that plays the same item, and
// every resync push would leave it standing.
func TestChannel_ItemErrorClearsWhenSchedulerMovesOn(t *testing.T) {
	p := &switchableProvider{frames: fakeProvider(50)}
	p.failing.Store(true)
	ch := NewChannel(1, ChannelConfig{AssetProvider: p, AllowedLocalPrefixes: []string{"/"}})
	if err := ch.LoadPlaylist(testPlaylist(clipItem("a"), liveItem("live1", "cam1"))); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ch.Stop()

	waitFor(t, 2*time.Second, func() bool { return ch.Status().CurrentItemID == "live1" })
	if p.failed.Load() == 0 {
		t.Fatal("clip a was never opened")
	}
	// The scheduler cursor moves on the failed open; the item change that
	// clears the error is handled on the next tick.
	waitFor(t, time.Second, func() bool {
		st := ch.Status()
		return st.CurrentItemID == "live1" && st.ItemError == ""
	})
}

// A snapshot push seeks the channel to the item the peer plays. A seek to
// a live or slate item opens no asset, so it has to drop the error of the
// item it moved away from itself.
func TestChannel_SeekToNonClipItemClearsItemError(t *testing.T) {
	for _, target := range []PlaylistItem{liveItem("live1", "cam1"), slateItem("slate1", 60_000)} {
		t.Run(string(target.Type), func(t *testing.T) {
			p := &switchableProvider{frames: fakeProvider(50)}
			p.failing.Store(true)
			ch := NewChannel(1, ChannelConfig{AssetProvider: p, AllowedLocalPrefixes: []string{"/"}})
			// A looping item does not auto-advance, so the channel stays on
			// the clip that cannot be opened.
			a := clipItem("a")
			a.Loop = true
			if err := ch.LoadPlaylist(testPlaylist(a, target)); err != nil {
				t.Fatalf("LoadPlaylist: %v", err)
			}
			if err := ch.Start(); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer ch.Stop()

			waitFor(t, 2*time.Second, func() bool { return ch.Status().ItemError != "" })
			if err := ch.SeekToItem(1, 0); err != nil {
				t.Fatalf("SeekToItem: %v", err)
			}
			if st := ch.Status(); st.CurrentItemID != target.ID || st.ItemError != "" {
				t.Fatalf("after seek to %s: item %q, error %q", target.ID, st.CurrentItemID, st.ItemError)
			}
		})
	}
}

// With no item left on air there is no item whose open failed.
func TestChannel_ItemErrorClearsWhenPlaylistCompletes(t *testing.T) {
	p := &switchableProvider{frames: fakeProvider(50)}
	p.failing.Store(true)
	ch := NewChannel(1, ChannelConfig{AssetProvider: p, AllowedLocalPrefixes: []string{"/"}})
	if err := ch.LoadPlaylist(testPlaylist(clipItem("a"))); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ch.Stop()

	waitFor(t, 2*time.Second, func() bool { return ch.Status().State != "on_air" })
	if p.failed.Load() == 0 {
		t.Fatal("clip a was never opened")
	}
	if st := ch.Status(); st.ItemError != "" {
		t.Fatalf("completed channel: state %q, error %q", st.State, st.ItemError)
	}
}
