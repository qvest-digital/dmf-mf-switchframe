package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/nmos"
	"github.com/zsiec/switchframe/server/st2110"
	"github.com/zsiec/switchframe/server/switcher"
)

// The program video Flow and its Sender's caps state the pipeline format, and
// the format can change at runtime. A change republishes both: the Flow under
// the same id at the new geometry and rate, the caps under a new version, so
// a controller re-reading on the version sees what the program now is.
func TestST2110ProgramFlowAndCapsFollowThePipelineFormat(t *testing.T) {
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)
	a := &App{sw: sw}
	a.wireFormatChange(false)

	store := nmos.NewResourceStore()
	flow := programVideoFlow("flow-1", "source-1", "device-1", 1920, 1080, 50, 1)
	store.AddFlow(flow)
	store.AddSender(&nmos.Sender{ID: "sender-1", FlowID: flow.ID,
		Caps: nmos.ST2110SenderCaps(flow, 0, st2110PacketTimeMs, nmos.NewVersion())})
	a.st2110State = &st2110State{store: store, programVideoSenderID: "sender-1"}
	before := store.GetSender("sender-1")
	time.Sleep(time.Millisecond)

	if err := sw.SetPipelineFormat(switcher.PipelineFormat{Width: 1280, Height: 720,
		FPSNum: 30000, FPSDen: 1001, Name: "720p29.97"}); err != nil {
		t.Fatal(err)
	}

	after := store.GetSender("sender-1")
	got := store.GetFlow(after.FlowID)
	if after.FlowID != "flow-1" {
		t.Errorf("the sender moved to flow %s; a resolution change keeps the Flow id", after.FlowID)
	}
	if got.FrameWidth != 1280 || got.FrameHeight != 720 ||
		got.GrainRate.Numerator != 30000 || got.GrainRate.Denominator != 1001 {
		t.Errorf("flow states %dx%d at %+v, want 1280x720 at 30000/1001",
			got.FrameWidth, got.FrameHeight, *got.GrainRate)
	}
	set := after.Caps["constraint_sets"].([]map[string]any)[0]
	enum := func(k string) any { return set[k].(map[string]any)["enum"].([]any)[0] }
	if enum(nmos.CapFrameWidth) != 1280 || enum(nmos.CapFrameHeight) != 720 ||
		enum(nmos.CapGrainRate) != (nmos.Rational{Numerator: 30000, Denominator: 1001}) {
		t.Errorf("caps state %v", set)
	}
	was, now := string(before.Caps["version"].(nmos.Version)), string(after.Caps["version"].(nmos.Version))
	if nmos.CompareVersions(now, was) <= 0 {
		t.Errorf("caps version %s did not move from %s", now, was)
	}
	if nmos.CompareVersions(string(after.Version), string(before.Version)) <= 0 {
		t.Errorf("sender version %s did not move from %s", after.Version, before.Version)
	}
	if nmos.CompareVersions(now, string(after.Version)) > 0 {
		t.Errorf("caps version %s is later than the sender's %s", now, after.Version)
	}
}

// Without ST 2110 configured, a format change touches nothing of it.
func TestAFormatChangeWithoutST2110(t *testing.T) {
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)
	a := &App{sw: sw}
	a.wireFormatChange(false)
	if err := sw.SetPipelineFormat(switcher.PipelineFormat{Width: 1280, Height: 720,
		FPSNum: 50, FPSDen: 1}); err != nil {
		t.Fatal(err)
	}
}

// fakeVideoTx is a video transmitter that records its frames and closing.
type fakeVideoTx struct {
	frames atomic.Int64
	closed atomic.Bool
}

func (f *fakeVideoTx) PutFrame([]byte) error { f.frames.Add(1); return nil }
func (f *fakeVideoTx) Close() error          { f.closed.Store(true); return nil }

// fakeAudioTx is an audio transmitter that records closing.
type fakeAudioTx struct{ closed atomic.Bool }

func (f *fakeAudioTx) PutFrame([]float32) error { return nil }
func (f *fakeAudioTx) Close() error             { f.closed.Store(true); return nil }

// txRig is an App whose program output runs on fake TX sessions: a video
// and an audio Sender active at 1080p50, created through the same factory a
// restart uses.
type txRig struct {
	a       *App
	created []st2110.TxVideoConfig
	videos  []*fakeVideoTx
	audios  []*fakeAudioTx
	failVid bool
	conn    *httptest.Server
}

func newTxRig(t *testing.T) *txRig {
	t.Helper()
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)
	if err := sw.SetPipelineFormat(switcher.PipelineFormat{Width: 1920, Height: 1080,
		FPSNum: 50, FPSDen: 1}); err != nil {
		t.Fatal(err)
	}
	r := &txRig{a: &App{sw: sw, mixer: audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000, Channels: 2, ClockDriven: true})}}
	r.a.wireFormatChange(false)

	store := nmos.NewResourceStore()
	flow := programVideoFlow("flow-1", "source-1", "device-1", 1920, 1080, 50, 1)
	store.AddFlow(flow)
	store.AddSender(&nmos.Sender{ID: "video-1", FlowID: flow.ID,
		Caps: nmos.ST2110SenderCaps(flow, 0, st2110PacketTimeMs, nmos.NewVersion())})
	cs := nmos.NewConnectionServer(nmos.ConnectionServerOpts{})
	cs.AddSender("video-1", "")
	cs.AddSender("audio-1", "")
	r.conn = httptest.NewServer(cs.Handler())
	t.Cleanup(r.conn.Close)

	state := &st2110State{store: store, connServer: cs, programVideoSenderID: "video-1",
		videoSenderID: "video-1", audioSenderID: "audio-1",
		videoTxCfg: &st2110.TxVideoConfig{MulticastIP: "239.1.1.1", Port: 5004,
			Format: st2110.VideoFormat{Width: 1920, Height: 1080, FPSNum: 50, FPSDen: 1}},
		audioTxCfg: &st2110.TxAudioConfig{MulticastIP: "239.1.1.2", Port: 5006},
	}
	state.newVideoTx = func(cfg st2110.TxVideoConfig) (st2110.VideoTransmitter, error) {
		if r.failVid {
			return nil, errors.New("no queue left")
		}
		r.created = append(r.created, cfg)
		v := &fakeVideoTx{}
		r.videos = append(r.videos, v)
		return v, nil
	}
	state.newAudioTx = func(st2110.TxAudioConfig) (st2110.AudioTransmitter, error) {
		au := &fakeAudioTx{}
		r.audios = append(r.audios, au)
		return au, nil
	}
	r.a.st2110State = state
	state.txMu.Lock()
	err := r.a.startOrRestartST2110Output()
	state.txMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if state.output != nil {
			state.output.Stop()
		}
	})
	return r
}

func (r *txRig) masterEnable(t *testing.T, sender string) bool {
	t.Helper()
	resp, err := http.Get(r.conn.URL + "/x-nmos/connection/v1.1/single/senders/" + sender + "/active")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var active struct {
		MasterEnable bool `json:"master_enable"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&active); err != nil {
		t.Fatal(err)
	}
	return active.MasterEnable
}

// A video TX running when the format changes is created again at the new
// format, so what goes out on the wire is what the Flow and the caps now
// state: the old session is closed, the new one is opened at 1280x720 with
// the same destination, and program frames of the new size reach it.
func TestST2110RunningVideoTxRestartsAtTheNewFormat(t *testing.T) {
	r := newTxRig(t)
	if err := r.a.sw.SetPipelineFormat(switcher.PipelineFormat{Width: 1280, Height: 720,
		FPSNum: 30000, FPSDen: 1001}); err != nil {
		t.Fatal(err)
	}

	if len(r.created) != 2 {
		t.Fatalf("%d video TX sessions created, want the first and one at the new format", len(r.created))
	}
	if !r.videos[0].closed.Load() {
		t.Error("the TX running at the old format was not closed")
	}
	got := r.created[1]
	if got.Format.Width != 1280 || got.Format.Height != 720 ||
		got.Format.FPSNum != 30000 || got.Format.FPSDen != 1001 {
		t.Errorf("the new TX is opened at %+v, want 1280x720 at 30000/1001", got.Format)
	}
	if got.MulticastIP != "239.1.1.1" || got.Port != 5004 {
		t.Errorf("the new TX lost the destination: %+v", got)
	}
	sink := r.a.sw.GetRawVideoSink()
	if sink == nil {
		t.Fatal("no program video sink after the restart")
	}
	// A frame still at the old size, as one from before the pipeline swap,
	// is kept off the TX sized for the new one.
	for range 10 {
		sink(&switcher.ProcessingFrame{Data: make([]byte, 1920*1080*3/2), Width: 1920, Height: 1080})
		time.Sleep(10 * time.Millisecond)
	}
	if n := r.videos[1].frames.Load(); n != 0 {
		t.Errorf("%d frames of the old size reached the TX opened at the new one", n)
	}
	for range 20 {
		sink(&switcher.ProcessingFrame{Data: make([]byte, 1280*720*3/2), Width: 1280, Height: 720})
		if r.videos[1].frames.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.videos[1].frames.Load() == 0 {
		t.Error("program frames at the new format do not reach the new TX")
	}
	if !r.masterEnable(t, "video-1") {
		t.Error("the restarted video Sender reads inactive")
	}
}

// Where the video TX cannot be created again, the video Sender is taken off
// the air the way a failed activation leaves it, and reads inactive on the
// Connection API rather than active with nothing sent. The audio Sender,
// which the restart stopped too, is started again alone.
func TestST2110FailedVideoRestartKeepsAudio(t *testing.T) {
	r := newTxRig(t)
	r.failVid = true
	if err := r.a.sw.SetPipelineFormat(switcher.PipelineFormat{Width: 1280, Height: 720,
		FPSNum: 50, FPSDen: 1}); err != nil {
		t.Fatal(err)
	}

	state := r.a.st2110State
	if state.videoTxCfg != nil || state.videoSenderID != "" {
		t.Errorf("video state kept after the failed restart: %+v %q", state.videoTxCfg, state.videoSenderID)
	}
	if r.a.sw.GetRawVideoSink() != nil {
		t.Error("the program video sink still feeds a closed output")
	}
	if r.masterEnable(t, "video-1") {
		t.Error("the video Sender reads active with no TX")
	}
	if len(r.audios) != 2 || !r.audios[0].closed.Load() || r.audios[1].closed.Load() {
		t.Errorf("%d audio TX sessions, want the first closed by the restart and one open alone", len(r.audios))
	}
	if state.output == nil || state.audioTxCfg == nil || !r.masterEnable(t, "audio-1") {
		t.Error("the audio Sender is not sending")
	}
}

// A format change with no TX running starts none.
func TestST2110IdleProgramStaysIdleOnAFormatChange(t *testing.T) {
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)
	a := &App{sw: sw}
	a.wireFormatChange(false)
	store := nmos.NewResourceStore()
	flow := programVideoFlow("flow-1", "source-1", "device-1", 1920, 1080, 50, 1)
	store.AddFlow(flow)
	store.AddSender(&nmos.Sender{ID: "sender-1", FlowID: flow.ID})
	a.st2110State = &st2110State{store: store, programVideoSenderID: "sender-1"}

	if err := sw.SetPipelineFormat(switcher.PipelineFormat{Width: 1280, Height: 720,
		FPSNum: 50, FPSDen: 1}); err != nil {
		t.Fatal(err)
	}
	if a.st2110State.output != nil || a.st2110State.videoTxCfg != nil {
		t.Error("a format change started a TX nobody activated")
	}
}
