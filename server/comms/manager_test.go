package comms

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerJoinLeave(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	m := NewManager(func() {})
	defer m.Close()

	// Join two participants.
	if err := m.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join op1: %v", err)
	}
	if err := m.Join("op2", "Bob"); err != nil {
		t.Fatalf("Join op2: %v", err)
	}

	// Verify state shows 2 participants.
	st := m.State()
	if st == nil {
		t.Fatal("State() should not be nil with participants")
	}
	if !st.Active {
		t.Error("State should be active")
	}
	if len(st.Participants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(st.Participants))
	}

	// Leave one.
	m.Leave("op1")
	st = m.State()
	if st == nil {
		t.Fatal("State() should not be nil with 1 participant")
	}
	if len(st.Participants) != 1 {
		t.Fatalf("expected 1 participant after leave, got %d", len(st.Participants))
	}
	if st.Participants[0].OperatorID != "op2" {
		t.Errorf("remaining participant should be op2, got %s", st.Participants[0].OperatorID)
	}

	// Leave last.
	m.Leave("op2")
	st = m.State()
	if st != nil {
		t.Error("State() should be nil when no participants")
	}
}

func TestManagerMaxParticipants(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	m := NewManager(func() {})
	defer m.Close()

	// Fill to capacity.
	for i := 0; i < MaxParticipants; i++ {
		id := string(rune('A' + i))
		if err := m.Join(id, "Name"+id); err != nil {
			t.Fatalf("Join %s: %v", id, err)
		}
	}

	// Next join should fail.
	err := m.Join("overflow", "Overflow")
	if err != ErrCommsFull {
		t.Fatalf("expected ErrCommsFull, got %v", err)
	}
}

func TestManagerDuplicateJoin(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	m := NewManager(func() {})
	defer m.Close()

	if err := m.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join: %v", err)
	}

	// Duplicate join should be idempotent.
	if err := m.Join("op1", "Alice Again"); err != nil {
		t.Fatalf("duplicate Join: %v", err)
	}

	st := m.State()
	if st == nil {
		t.Fatal("State() should not be nil")
	}
	if len(st.Participants) != 1 {
		t.Fatalf("expected 1 participant after duplicate join, got %d", len(st.Participants))
	}
}

func TestManagerSetMuted(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	m := NewManager(func() {})
	defer m.Close()

	if err := m.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join: %v", err)
	}

	if err := m.SetMuted("op1", true); err != nil {
		t.Fatalf("SetMuted: %v", err)
	}

	st := m.State()
	if st == nil {
		t.Fatal("State() should not be nil")
	}
	if !st.Participants[0].Muted {
		t.Error("participant should be muted")
	}

	// SetMuted on non-existent should return error.
	if err := m.SetMuted("ghost", true); err != ErrNotInComms {
		t.Fatalf("expected ErrNotInComms, got %v", err)
	}
}

func TestManagerBroadcastOnLeave(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	var called atomic.Int32
	m := NewManager(func() {
		called.Add(1)
	})
	defer m.Close()

	if err := m.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join: %v", err)
	}

	m.Leave("op1")

	// Leave triggers onBroadcast (needed for stream-close auto-leave path).
	if called.Load() < 1 {
		t.Error("onBroadcast should have been called on leave")
	}
}

func TestManagerStateNilWhenEmpty(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	m := NewManager(func() {})
	defer m.Close()

	st := m.State()
	if st != nil {
		t.Error("State() should be nil when no participants")
	}
}

// trackingEncoder counts Encode calls and flags any call that observes the
// participant already closed. Used by TestCommsManager_MixTick_NoEncoderCallAfterClose
// to verify that the mixTick → encodeAndSend path never calls the Opus
// encoder on a participant whose close() has set closedAtomic.
type trackingEncoder struct {
	owner           atomic.Pointer[participant]
	totalCalls      atomic.Int64
	afterCloseCalls atomic.Int64
}

func (e *trackingEncoder) Encode(pcm []int16, frameSize int, buf []byte) (int, error) {
	e.totalCalls.Add(1)
	if p := e.owner.Load(); p != nil && p.closedAtomic.Load() {
		e.afterCloseCalls.Add(1)
	}
	// Return a short valid-shaped packet so the caller's copy + send path
	// exercises normally.
	n := 10
	if n > len(buf) {
		n = len(buf)
	}
	for i := 0; i < n; i++ {
		buf[i] = byte(i)
	}
	return n, nil
}

func TestCommsManager_MixTick_NoEncoderCallAfterClose(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	// Every participant gets a fresh tracking encoder. Factory is only
	// called from Join (under m.mu) from this single test goroutine, so the
	// slice append needs no synchronization.
	var encoders []*trackingEncoder
	factory := func() (opusEncoderAPI, error) {
		enc := &trackingEncoder{}
		encoders = append(encoders, enc)
		return enc, nil
	}

	m := newManagerWithEncoderFactory(nil, factory)
	defer m.Close()

	// attachOwner walks the participants map and binds each tracking encoder
	// to its owning participant. Called after Join so each encoder's
	// closedAtomic-observer has a live pointer to check.
	attachOwner := func() {
		m.mu.Lock()
		for _, p := range m.participants {
			// p.encoder is the opusEncoderAPI we handed back from factory.
			// In-package access gives us the concrete type assertion.
			if te, ok := p.encoder.(*trackingEncoder); ok {
				te.owner.Store(p)
			}
		}
		m.mu.Unlock()
	}

	// Steady participant feeds PCM so mixTick actually enters the encode loop.
	if err := m.Join("steady", "Steady"); err != nil {
		t.Fatalf("Join steady: %v", err)
	}
	attachOwner()

	steadyP, ok := m.GetParticipant("steady")
	if !ok {
		t.Fatal("steady participant lookup failed")
	}

	stopFeed := make(chan struct{})
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		pcm := make([]int16, FrameSize)
		for i := range pcm {
			pcm[i] = 1000
		}
		for {
			select {
			case <-stopFeed:
				return
			default:
				select {
				case steadyP.pcmQ <- pcm:
				default:
				}
				time.Sleep(3 * time.Millisecond)
			}
		}
	}()

	// Race loop: Join + Leave a tmp participant while mixTick ticks.
	// With the 20ms mixLoop cadence, a 25ms lifetime per tmp is guaranteed
	// to overlap at least one tick. 30 iterations × 25ms ≈ 750ms wall
	// time, enough to hit the Leave-during-mixTick window repeatedly.
	for iter := 0; iter < 30; iter++ {
		opID := fmt.Sprintf("tmp%d", iter)
		if err := m.Join(opID, opID); err != nil {
			t.Fatalf("Join %s: %v", opID, err)
		}
		attachOwner()
		time.Sleep(time.Duration(20+iter%10) * time.Millisecond)
		m.Leave(opID)
	}

	// Drain: allow any late mixTick to complete so any racing encoder call
	// is observed by the afterCloseCalls counter.
	time.Sleep(60 * time.Millisecond)
	close(stopFeed)
	<-feedDone

	// Invariant: encodeAndSend must gate on p.closed. Any Encode call that
	// observed p.closedAtomic == true is a miss.
	var badCalls int64
	var totalCalls int64
	for _, te := range encoders {
		badCalls += te.afterCloseCalls.Load()
		totalCalls += te.totalCalls.Load()
	}
	if badCalls != 0 {
		t.Fatalf("encoder called %d times after participant close (out of %d total calls)", badCalls, totalCalls)
	}
	if totalCalls == 0 {
		t.Fatal("mixTick never reached the encode path — test setup is ineffective")
	}
}

// trackingDecoder is the decode-path twin of trackingEncoder: it counts
// Decode calls and flags any that observe an already-closed participant.
// Used by TestCommsManager_IngestAudio_NoDecoderCallAfterClose to verify
// IngestAudio → decodeAudio never dispatches to the cgo decoder after
// participant.close() has set closedAtomic.
type trackingDecoder struct {
	owner           atomic.Pointer[participant]
	totalCalls      atomic.Int64
	afterCloseCalls atomic.Int64
}

func (d *trackingDecoder) Decode(data []byte, pcm []int16, frameSize int) (int, error) {
	d.totalCalls.Add(1)
	if p := d.owner.Load(); p != nil && p.closedAtomic.Load() {
		d.afterCloseCalls.Add(1)
	}
	// Return a valid-shaped frame so the caller's copy + enqueue path runs
	// normally. Zero-filled PCM keeps speaking detection quiet.
	n := frameSize
	if n > len(pcm) {
		n = len(pcm)
	}
	for i := 0; i < n; i++ {
		pcm[i] = 0
	}
	return n, nil
}

func TestCommsManager_IngestAudio_NoDecoderCallAfterClose(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	var (
		decodersMu sync.Mutex
		decoders   []*trackingDecoder
	)
	decFactory := func() (opusDecoderAPI, error) {
		dec := &trackingDecoder{}
		decodersMu.Lock()
		decoders = append(decoders, dec)
		decodersMu.Unlock()
		return dec, nil
	}

	m := newManagerWithFactories(nil, defaultEncoderFactory, decFactory)
	defer m.Close()

	// attachOwner binds each tracking decoder to its participant so the
	// post-close observer can load a live *participant pointer at Decode time.
	attachOwner := func() {
		m.mu.Lock()
		for _, p := range m.participants {
			if td, ok := p.decoder.(*trackingDecoder); ok {
				td.owner.Store(p)
			}
		}
		m.mu.Unlock()
	}

	// Dummy Opus payload — the tracking decoder ignores contents.
	opusData := []byte{0xFC}

	// Race loop: for each tmp participant, spawn N concurrent IngestAudio
	// spinners so that when Leave fires, at least one spinner is typically
	// queued at p.mu behind another's in-flight decodeAudio. When the first
	// in-flight call releases p.mu, Go's non-FIFO mutex may wake close()
	// (which sets p.closed=true) before the next queued spinner — that
	// spinner then enters decodeAudio with p.closed=true and, pre-fix, calls
	// the cgo decoder. The tracking decoder flips afterCloseCalls on that
	// observation. Single-spinner doesn't reliably exercise this because the
	// unlock→lock hand-off window is nanoseconds; multi-spinner makes close()
	// and another spinner contend for the same freshly-released p.mu.
	const (
		iterations     = 100
		spinnersPerOp  = 6
		warmupDuration = 200 * time.Microsecond
	)

	for iter := 0; iter < iterations; iter++ {
		opID := fmt.Sprintf("tmp%d", iter)
		if err := m.Join(opID, opID); err != nil {
			t.Fatalf("Join %s: %v", opID, err)
		}
		attachOwner()

		stop := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < spinnersPerOp; i++ {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						_ = m.IngestAudio(id, opusData)
					}
				}
			}(opID)
		}

		time.Sleep(warmupDuration)
		m.Leave(opID)
		close(stop)
		wg.Wait()
	}

	// Brief settle so any racing Decode call finishes its closedAtomic check.
	time.Sleep(20 * time.Millisecond)

	var badCalls, totalCalls int64
	decodersMu.Lock()
	for _, td := range decoders {
		badCalls += td.afterCloseCalls.Load()
		totalCalls += td.totalCalls.Load()
	}
	decodersMu.Unlock()

	if badCalls != 0 {
		t.Fatalf("decoder called %d times after participant close (out of %d total calls)", badCalls, totalCalls)
	}
	if totalCalls == 0 {
		t.Fatal("IngestAudio never reached the decode path — test setup is ineffective")
	}
}
