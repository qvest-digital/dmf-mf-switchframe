package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/comms"
	"github.com/zsiec/switchframe/server/operator"
)

// fakeBidiStream is an in-memory io.ReadWriteCloser that serves a fixed
// byte sequence on Read. When the data is exhausted it either blocks until
// Close (blockAfterData=true, simulating a live stream) or returns io.EOF.
type fakeBidiStream struct {
	mu             sync.Mutex
	data           *bytes.Reader
	blockAfterData bool

	closed    chan struct{}
	closeOnce sync.Once

	drained   chan struct{} // closed once all buffered data has been consumed
	drainOnce sync.Once
}

func newFakeBidiStream(data []byte, blockAfterData bool) *fakeBidiStream {
	return &fakeBidiStream{
		data:           bytes.NewReader(data),
		blockAfterData: blockAfterData,
		closed:         make(chan struct{}),
		drained:        make(chan struct{}),
	}
}

func (s *fakeBidiStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	n, _ := s.data.Read(p)
	s.mu.Unlock()
	if n > 0 {
		return n, nil
	}
	s.drainOnce.Do(func() { close(s.drained) })
	if !s.blockAfterData {
		return 0, io.EOF
	}
	<-s.closed
	return 0, io.EOF
}

func (s *fakeBidiStream) Write(p []byte) (int, error) {
	select {
	case <-s.closed:
		return 0, io.ErrClosedPipe
	default:
		return len(p), nil
	}
}

func (s *fakeBidiStream) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

// waitCh fails the test if ch does not close within the timeout.
func waitCh(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(msg)
	}
}

func helloMsg(operatorID string) []byte {
	return makeWireMsg(commsMsgControl,
		[]byte(fmt.Sprintf(`{"action":"hello","operatorId":%q}`, operatorID)))
}

func helloMsgWithToken(operatorID, token string) []byte {
	return makeWireMsg(commsMsgControl,
		[]byte(fmt.Sprintf(`{"action":"hello","operatorId":%q,"token":%q}`, operatorID, token)))
}

func muteMsg(muted bool) []byte {
	return makeWireMsg(commsMsgControl, []byte(fmt.Sprintf(`{"action":"mute","muted":%t}`, muted)))
}

// participantMuted reports the Muted flag for operatorID, and whether the
// participant is present at all.
func participantMuted(mgr *comms.Manager, operatorID string) (muted, present bool) {
	state := mgr.State()
	if state == nil {
		return false, false
	}
	for _, p := range state.Participants {
		if p.OperatorID == operatorID {
			return p.Muted, true
		}
	}
	return false, false
}

// TestCommsBidiStream_StaleStreamDoesNotTearDownReconnectedSession reproduces
// the reconnect sequence from finding #14: an operator's old stream S1
// lingers (e.g. QUIC idle timeout) while the browser reconnects with a fresh
// stream S2 bound to the same participant. When S1 finally errors, its
// deferred Leave must NOT tear down the participant that S2 is actively
// using — otherwise the operator silently vanishes from comms while their
// client believes it is connected.
func TestCommsBidiStream_StaleStreamDoesNotTearDownReconnectedSession(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}

	app := &App{commsMgr: mgr}

	// S1: the original stream. Stays open (blocking) after the hello.
	s1 := newFakeBidiStream(helloMsg("op1"), true)
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		app.handleCommsBidiStream("", s1)
	}()
	waitCh(t, s1.drained, "s1 hello was not consumed")

	// S2: the reconnect. Attaches to the same participant (REST re-join is
	// idempotent, so no new participant is created).
	s2 := newFakeBidiStream(helloMsg("op1"), true)
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		app.handleCommsBidiStream("", s2)
	}()
	waitCh(t, s2.drained, "s2 hello was not consumed")

	// The stale stream finally errors out (idle timeout). Its deferred Leave
	// must be skipped because S2 superseded it.
	_ = s1.Close()
	waitCh(t, done1, "stale stream handler did not return")

	if _, ok := mgr.GetParticipant("op1"); !ok {
		t.Fatal("stale stream's deferred Leave tore down the reconnected operator's fresh comms session")
	}

	// The CURRENT stream closing must still leave the session (no ghost).
	_ = s2.Close()
	waitCh(t, done2, "current stream handler did not return")

	if _, ok := mgr.GetParticipant("op1"); ok {
		t.Fatal("current stream close did not remove the participant from comms")
	}
}

// TestCommsBidiStream_SupersedeClosesStaleStream verifies that attaching a
// fresh stream for an operator closes the lingering previous stream, so the
// two streams do not compete for the participant's single send channel
// (which would split the encoded N-1 mix arbitrarily between a dead and a
// live stream, garbling audio).
func TestCommsBidiStream_SupersedeClosesStaleStream(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}

	app := &App{commsMgr: mgr}

	s1 := newFakeBidiStream(helloMsg("op1"), true)
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		app.handleCommsBidiStream("", s1)
	}()
	waitCh(t, s1.drained, "s1 hello was not consumed")

	s2 := newFakeBidiStream(helloMsg("op1"), true)
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		app.handleCommsBidiStream("", s2)
	}()
	waitCh(t, s2.drained, "s2 hello was not consumed")

	// Registering S2 must close the stale S1, unwinding its handler.
	waitCh(t, s1.closed, "stale stream was not closed when superseded by the reconnect stream")
	waitCh(t, done1, "stale stream handler did not return after supersession")

	if _, ok := mgr.GetParticipant("op1"); !ok {
		t.Fatal("participant removed during supersession")
	}

	_ = s2.Close()
	waitCh(t, done2, "current stream handler did not return")
}

// newTestOperatorStore creates an operator store with a victim, an attacker,
// and a director registered, returning the store and the three operators.
func newTestOperatorStore(t *testing.T) (*operator.Store, operator.Operator, operator.Operator, operator.Operator) {
	t.Helper()
	store, err := operator.NewStore(filepath.Join(t.TempDir(), "operators.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	victim, err := store.Register("Victim", operator.RoleAudio)
	if err != nil {
		t.Fatalf("Register victim: %v", err)
	}
	attacker, err := store.Register("Attacker", operator.RoleAudio)
	if err != nil {
		t.Fatalf("Register attacker: %v", err)
	}
	director, err := store.Register("Director", operator.RoleDirector)
	if err != nil {
		t.Fatalf("Register director: %v", err)
	}
	return store, victim, attacker, director
}

// joinOrSkip joins operatorID to the comms session, skipping the test when
// the opus codec is unavailable.
func joinOrSkip(t *testing.T, mgr *comms.Manager, operatorID, name string) {
	t.Helper()
	if err := mgr.Join(operatorID, name); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}
}

// TestCommsBidiStream_RejectsHelloWithoutToken covers finding #68: when an
// operator store is wired, a hello with no bearer token must not be bound to
// the claimed operator. The stream must not be able to mute the operator nor
// remove them from comms on disconnect.
func TestCommsBidiStream_RejectsHelloWithoutToken(t *testing.T) {
	store, victim, _, _ := newTestOperatorStore(t)

	mgr := comms.NewManager(nil)
	defer mgr.Close()
	joinOrSkip(t, mgr, victim.ID, "Victim")

	app := &App{commsMgr: mgr, operatorStore: store}

	// Attacker sends a hello claiming the victim's identity (no token),
	// followed by a mute command, then EOF.
	data := append(helloMsg(victim.ID), muteMsg(true)...)
	app.handleCommsBidiStream("", newFakeBidiStream(data, false))

	muted, present := participantMuted(mgr, victim.ID)
	if !present {
		t.Fatal("unauthenticated stream removed the victim from comms on disconnect")
	}
	if muted {
		t.Fatal("unauthenticated stream muted the victim")
	}
}

// TestCommsBidiStream_RejectsHelloWithWrongOperatorToken verifies that a
// valid token belonging to a DIFFERENT (non-director) operator cannot be
// used to bind a stream to the victim's identity.
func TestCommsBidiStream_RejectsHelloWithWrongOperatorToken(t *testing.T) {
	store, victim, attacker, _ := newTestOperatorStore(t)

	mgr := comms.NewManager(nil)
	defer mgr.Close()
	joinOrSkip(t, mgr, victim.ID, "Victim")

	app := &App{commsMgr: mgr, operatorStore: store}

	data := append(helloMsgWithToken(victim.ID, attacker.Token), muteMsg(true)...)
	app.handleCommsBidiStream("", newFakeBidiStream(data, false))

	muted, present := participantMuted(mgr, victim.ID)
	if !present {
		t.Fatal("cross-operator stream removed the victim from comms on disconnect")
	}
	if muted {
		t.Fatal("cross-operator stream muted the victim")
	}
}

// TestCommsBidiStream_AcceptsOwnToken verifies the positive path: a hello
// carrying the operator's own token binds the stream, control messages are
// applied, and disconnect leaves the session.
func TestCommsBidiStream_AcceptsOwnToken(t *testing.T) {
	store, victim, _, _ := newTestOperatorStore(t)

	mgr := comms.NewManager(nil)
	defer mgr.Close()
	joinOrSkip(t, mgr, victim.ID, "Victim")

	app := &App{commsMgr: mgr, operatorStore: store}

	data := append(helloMsgWithToken(victim.ID, victim.Token), muteMsg(true)...)
	s := newFakeBidiStream(data, true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.handleCommsBidiStream("", s)
	}()
	waitCh(t, s.drained, "authorized hello was not consumed")

	// The mute control message must be applied.
	deadline := time.Now().Add(5 * time.Second)
	for {
		muted, present := participantMuted(mgr, victim.ID)
		if present && muted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("authorized stream did not apply mute (present=%v muted=%v)", present, muted)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Disconnect must remove the participant (auto-leave).
	_ = s.Close()
	waitCh(t, done, "authorized stream handler did not return")
	if _, ok := mgr.GetParticipant(victim.ID); ok {
		t.Fatal("authorized stream close did not remove the participant from comms")
	}
}

// TestCommsBidiStream_AcceptsDirectorToken verifies that a director's token
// may act on another operator's comms stream, mirroring the REST
// authorizeCommsTarget semantics.
func TestCommsBidiStream_AcceptsDirectorToken(t *testing.T) {
	store, victim, _, director := newTestOperatorStore(t)

	mgr := comms.NewManager(nil)
	defer mgr.Close()
	joinOrSkip(t, mgr, victim.ID, "Victim")

	app := &App{commsMgr: mgr, operatorStore: store}

	data := append(helloMsgWithToken(victim.ID, director.Token), muteMsg(true)...)
	app.handleCommsBidiStream("", newFakeBidiStream(data, false))

	// Director-bound stream acted on the victim: mute applied, and EOF
	// triggered the auto-leave.
	if _, ok := mgr.GetParticipant(victim.ID); ok {
		t.Fatal("director stream close did not remove the participant from comms")
	}
}
