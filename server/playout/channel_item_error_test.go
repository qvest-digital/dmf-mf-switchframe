package playout

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// switchableProvider fails every Open while failing is set and serves
// frames otherwise.
type switchableProvider struct {
	failing atomic.Bool
	frames  *FakeAssetProvider
}

func (p *switchableProvider) Open(uri string) (FrameIterator, error) {
	if p.failing.Load() {
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
