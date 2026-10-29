package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/nmos"
)

// mxlNMOSStateFile, under the state directory, holds the latest activation
// of each MXL receiver, so a restart reconnects the inputs as a controller
// left them rather than as they were booked.
const mxlNMOSStateFile = "mxl-nmos-receivers.json"

// mxlRestoreTimeout bounds how long a restart waits for a saved connection's
// flow to open. It is longer than connectTimeout: a connection that cannot
// be restored is replaced by the booked flow on both engines of a pair, so a
// flow that is only slow to be mirrored must not count as gone.
var mxlRestoreTimeout = 20 * time.Second

// mxlBooking is the flows a named input was booked with.
type mxlBooking struct {
	Video string `json:"video,omitempty"`
	Audio string `json:"audio,omitempty"`
	Data  string `json:"data,omitempty"`
}

func (b mxlBooking) flow(e mxlEssence) string {
	switch e {
	case essenceVideo:
		return b.Video
	case essenceAudio:
		return b.Audio
	}
	return b.Data
}

// savedActivation is a receiver's activation and the booking of its input
// when it was applied. A booking that has changed since was made after the
// activation and replaces it.
type savedActivation struct {
	Booking mxlBooking       `json:"booking"`
	Active  nmos.MXLEndpoint `json:"active"`
}

// mxlNMOSState is the saved activations and the file they are kept in. An
// empty path keeps nothing.
type mxlNMOSState struct {
	path  string
	mu    sync.Mutex
	saved map[string]savedActivation
}

// load reads the saved activations of the receivers in known; others belong
// to inputs no longer booked and are dropped.
func (s *mxlNMOSState) load(known map[string]mxlReceiverRef) {
	s.saved = map[string]savedActivation{}
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err == nil {
		err = json.Unmarshal(data, &s.saved)
	}
	if err != nil {
		slog.Warn("mxl nmos: saved connections not readable, inputs read their booked flows", "file", s.path, "error", err)
		s.saved = map[string]savedActivation{}
		return
	}
	pruned := false
	for id := range s.saved {
		if _, ok := known[id]; !ok {
			delete(s.saved, id)
			pruned = true
		}
	}
	if pruned {
		s.write()
	}
}

func (s *mxlNMOSState) get(id string) (savedActivation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.saved[id]
	return v, ok
}

// save records an applied activation of receiver id.
func (s *mxlNMOSState) save(id string, booking mxlBooking, active nmos.MXLEndpoint) {
	if s.path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved[id] = savedActivation{Booking: booking, Active: receiverEndpoint(active)}
	s.write()
}

// drop forgets receiver id's activation, which leaves it to its booking.
func (s *mxlNMOSState) drop(id string) {
	if s.path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.saved[id]; !ok {
		return
	}
	delete(s.saved, id)
	s.write()
}

// write replaces the file, so a restart reads either the former or the new
// activations, never part of them. A failure leaves the activation applied:
// only a later restart would lose it.
func (s *mxlNMOSState) write() {
	data, err := json.Marshal(s.saved)
	if err == nil {
		err = writeFileAtomic(s.path, data)
	}
	if err != nil {
		slog.Warn("mxl nmos: connections not saved, a restart reads the booked flows", "file", s.path, "error", err)
	}
}

// receiverEndpoint is active in the receiver form, sender_id included, which
// is the form a saved activation is read back in.
func receiverEndpoint(active nmos.MXLEndpoint) nmos.MXLEndpoint {
	var dom, flow *string
	if len(active.TransportParams) > 0 {
		dom, flow = active.TransportParams[0].DomainID, active.TransportParams[0].FlowID
	}
	e := nmos.NewMXLReceiverEndpoint(active.SenderID, active.MasterEnable, dom, flow)
	e.Activation = active.Activation
	return e
}

// saveMXLActivation records an applied activation with the booking of its
// input.
func (n *mxlNMOS) saveMXLActivation(id string, active nmos.MXLEndpoint) {
	n.state.save(id, n.bookings[n.receivers[id].input], active)
}

// restoreMXLNMOS reconnects each receiver to its saved activation, before
// the node is served, registered or compared with the peer engine, so all of
// them see what the inputs read. A receiver whose input was booked anew, or
// whose saved flow does not open, stays on the booked flow; that is a new
// activation, passed to the peer engine, so both engines of a pair read the
// booked flow rather than one of them the saved one.
func (a *App) restoreMXLNMOS(ctx context.Context) {
	n := a.mxlNMOS
	var wg sync.WaitGroup
	for _, id := range n.order {
		saved, ok := n.state.get(id)
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.restoreMXLReceiver(ctx, id, saved)
		}()
	}
	wg.Wait()
}

func (a *App) restoreMXLReceiver(ctx context.Context, id string, saved savedActivation) {
	n := a.mxlNMOS
	ref := n.receivers[id]
	booking := n.bookings[ref.input]
	log := slog.With("input", ref.input, "essence", ref.essence, "saved_flow", saved.Active.FlowID(),
		"booked_flow", booking.flow(ref.essence))
	if saved.Booking != booking {
		log.Info("mxl nmos: input booked anew since its last connection, reading the booked flow")
		a.activateMXLBooking(id)
		return
	}
	if err := a.applyMXLReceiver(ctx, ref, saved.Active, mxlRestoreTimeout); err != nil {
		log.Warn("mxl nmos: saved connection not restored, reading the booked flow", "error", err)
		// The attempt took the reader off the booked flow's retarget.
		flow := booking.flow(ref.essence)
		v := map[mxlEssence]*string{ref.essence: &flow}
		if err := a.retargetMXLInput(ref.input, v[essenceVideo], v[essenceAudio], v[essenceData]); err != nil {
			log.Warn("mxl nmos: booked flow not reconnected", "error", err)
		}
		a.activateMXLBooking(id)
		return
	}
	n.conn.SetReceiverActive(id, saved.Active)
	n.store.UpdateReceiverSubscription(id, saved.Active.SenderID, saved.Active.MasterEnable)
	log.Info("mxl nmos: saved connection restored", "sender", strOrEmpty(saved.Active.SenderID))
}

// activateMXLBooking makes receiver id's booked state, which it already
// reads, an activation of now: the peer engine adopts it over the saved
// connection it may still read, and a controller sees when it was made.
func (a *App) activateMXLBooking(id string) {
	n := a.mxlNMOS
	n.state.drop(id)
	active, _ := n.conn.ReceiverActive(id)
	mode, now := "activate_immediate", string(nmos.NewVersion())
	active.Activation = nmos.Activation{Mode: &mode, ActivationTime: &now}
	n.conn.SetReceiverActive(id, active)
	n.store.UpdateReceiverSubscription(id, active.SenderID, active.MasterEnable)
	if n.peerBase != "" {
		n.queueForward(id, active)
	}
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
