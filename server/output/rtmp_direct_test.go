//go:build cgo && !noffmpeg

package output

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestRTMPDirect_NewRequiresURL(t *testing.T) {
	t.Parallel()
	_, err := NewRTMPDirect(RTMPCallerConfig{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "URL is required")
}

func TestRTMPDirect_ID(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	require.Equal(t, "rtmp-direct", r.ID())
}

func TestRTMPDirect_StatusBeforeStart(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	status := r.Status()
	require.Equal(t, StateStopped, status.State)
}

func TestRTMPDirect_WriteVideoBeforeStart(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	frame := &media.VideoFrame{PTS: 90000, IsKeyframe: true, WireData: []byte{0x00, 0x00, 0x00, 0x01}}
	writeErr := r.WriteVideo(frame)
	require.Error(t, writeErr)
}

func TestRTMPDirect_StartWithMockConnect(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	r.connectFn = func(url string) error {
		return nil
	}

	err = r.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	status := r.Status()
	require.Equal(t, StateActive, status.State)
}

func TestRTMPDirect_DropsNonKeyframeBeforeInit(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	r.connectFn = func(url string) error { return nil }

	err = r.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	// Send a non-keyframe — should be silently dropped.
	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: false,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00},
	}
	writeErr := r.WriteVideo(frame)
	require.NoError(t, writeErr)

	// Give the write loop time to process.
	time.Sleep(50 * time.Millisecond)

	require.False(t, r.initialized.Load(), "should not initialize on non-keyframe")
}

func TestRTMPDirect_InitOnKeyframe(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	r.connectFn = func(url string) error { return nil }

	err = r.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	sps := []byte{0x67, 0x42, 0xc0, 0x1e}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00},
	}
	writeErr := r.WriteVideo(frame)
	require.NoError(t, writeErr)

	require.Eventually(t, func() bool {
		return r.initialized.Load()
	}, 2*time.Second, 10*time.Millisecond, "should initialize on keyframe with SPS/PPS")
}

func TestRTMPDirect_ADTSStripping(t *testing.T) {
	t.Parallel()
	// Build ADTS header (7 bytes) + raw AAC payload.
	adtsHeader := []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}
	rawAAC := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	data := append(adtsHeader, rawAAC...)

	stripped := stripADTS(data)
	require.Equal(t, rawAAC, stripped)
}

func TestRTMPDirect_ADTSStripping_NoHeader(t *testing.T) {
	t.Parallel()
	rawData := []byte{0x01, 0x02, 0x03, 0x04}
	stripped := stripADTS(rawData)
	require.Equal(t, rawData, stripped, "non-ADTS data should pass through unchanged")
}

func TestRTMPDirect_WriteDropsDuringReconnect(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	// Manually set state to reconnecting without actually starting.
	r.state.Store(ptrTo(StateReconnecting))
	r.videoCh = make(chan *media.VideoFrame, rtmpVideoChSize)
	r.audioCh = make(chan *media.AudioFrame, rtmpAudioChSize)
	r.stopCh = make(chan struct{})

	frame := &media.VideoFrame{PTS: 90000, IsKeyframe: true, WireData: []byte{0x00}}
	writeErr := r.WriteVideo(frame)
	require.NoError(t, writeErr, "write during reconnect should return nil (silently drop)")
}

func TestRTMPDirect_RTMPStatusSnapshot(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/secret-key"})
	require.NoError(t, err)

	snap := r.RTMPStatusSnapshot()
	require.False(t, snap.Active)
	require.Equal(t, "rtmp://example.com/live/****", snap.URL)

	r.state.Store(ptrTo(StateActive))
	snap = r.RTMPStatusSnapshot()
	require.True(t, snap.Active)
	require.Equal(t, "active", snap.State)

	r.state.Store(ptrTo(StateReconnecting))
	snap = r.RTMPStatusSnapshot()
	require.True(t, snap.Active)
	require.Equal(t, "reconnecting", snap.State)
}

func TestRTMPDirect_ImplementsFrameAdapter(t *testing.T) {
	t.Parallel()
	var _ FrameAdapter = (*RTMPDirect)(nil)
}

func TestRTMPDirect_CloseWithoutStart(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	closeErr := r.Close()
	require.NoError(t, closeErr)
}

// TestRTMPDirect_CloseJoinsReconnectLoop reproduces the leak where Close
// joins only the writeLoop (doneCh) but not the reconnectLoop goroutine.
// If Close runs while the reconnect loop is past its backoff timer, the
// loop proceeds with no context re-check: it reopens the RTMP connection
// (leaking the FFmpeg context), stores StateActive after Close stored
// StateStopped, and fires onReconnect into a Manager that already removed
// the adapter.
func TestRTMPDirect_CloseJoinsReconnectLoop(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	r.connectFn = func(string) error { return nil } // test mode

	require.NoError(t, r.Start(context.Background()))
	r.bo.Initial = 30 * time.Millisecond

	var reconnects atomic.Int32
	r.onReconnect = func() { reconnects.Add(1) }

	// Spawn the reconnect loop, then pin it: grab r.mu while it sleeps in
	// its backoff so that after the timer fires it blocks at the top of
	// its work section, past the backoff select's goCtx.Done() case.
	r.enterReconnect("simulated endpoint failure")
	r.mu.Lock()
	time.Sleep(150 * time.Millisecond) // backoff fired; loop now blocked on r.mu

	closeReturned := make(chan struct{})
	go func() {
		_ = r.Close() // cancels goCtx, joins writeLoop, must also join reconnectLoop
		close(closeReturned)
	}()
	time.Sleep(20 * time.Millisecond) // let Close cancel the context
	r.mu.Unlock()

	select {
	case <-closeReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return")
	}

	// Wait for the reconnect goroutine to fully exit, then verify it did
	// not complete a reconnect after Close.
	require.Eventually(t, func() bool {
		return !r.reconnecting.Load()
	}, 2*time.Second, time.Millisecond)
	require.Equal(t, int32(0), reconnects.Load(),
		"reconnect completed after Close — reconnectLoop was not joined")
	require.Equal(t, StateStopped, *r.state.Load(),
		"final state must be StateStopped, not a resurrected StateActive")
}

// TestRTMPDirect_EnterReconnectDuringWindowRespawnsLoop reproduces the CAS
// window where the adapter gets permanently stuck in StateReconnecting:
// reconnectLoop stores StateActive and calls onReconnect BEFORE its deferred
// reconnecting.Store(false) runs. A write failure in that window calls
// enterReconnect, which stores StateReconnecting but loses the
// CompareAndSwap against the still-set flag, so no new loop is spawned; the
// old loop then exits and clears the flag. handleVideo/handleAudio drop all
// frames while state != StateActive, so the output is dead until Close/Start.
func TestRTMPDirect_EnterReconnectDuringWindowRespawnsLoop(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	r.connectFn = func(string) error { return nil } // test mode

	require.NoError(t, r.Start(context.Background()))
	defer func() { _ = r.Close() }()
	r.bo.Initial = time.Millisecond

	var reconnects atomic.Int32
	r.onReconnect = func() {
		if reconnects.Add(1) == 1 {
			// Inside the window: state == StateActive but reconnecting is
			// still true. This is what handleVideo does when the first
			// write after a reconnect fails on a flapping server.
			r.enterReconnect("write failed during reconnect window")
		}
	}

	r.enterReconnect("initial failure")

	// The adapter must recover to StateActive via a re-spawned reconnect
	// loop; without the fix it is stuck in StateReconnecting forever.
	require.Eventually(t, func() bool {
		return *r.state.Load() == StateActive
	}, 5*time.Second, 10*time.Millisecond,
		"adapter stuck in StateReconnecting with no reconnect goroutine")
}

func TestRTMPDirect_BackoffProgression(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	levels := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for _, expected := range levels {
		got := r.bo.Next()
		lo := time.Duration(float64(expected) * 0.79)
		hi := time.Duration(float64(expected) * 1.21)
		require.True(t, got >= lo && got <= hi,
			"expected backoff near %v (+/-20%%), got %v", expected, got)
	}
}

func TestRTMPDirect_ChannelOverflowCountsDrops(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	// Set up channels and state manually (no writeLoop draining them).
	r.videoCh = make(chan *media.VideoFrame, rtmpVideoChSize)
	r.audioCh = make(chan *media.AudioFrame, rtmpAudioChSize)
	r.stopCh = make(chan struct{})
	r.state.Store(ptrTo(StateActive))

	// Fill the video channel completely.
	for i := 0; i < rtmpVideoChSize; i++ {
		frame := &media.VideoFrame{
			PTS:      int64(i * 3000),
			WireData: []byte{0x00},
		}
		_ = r.WriteVideo(frame)
	}

	// The next write should overflow and count the drop.
	overflowFrame := &media.VideoFrame{
		PTS:      int64(rtmpVideoChSize * 3000),
		WireData: []byte{0x00},
	}
	_ = r.WriteVideo(overflowFrame)

	require.Equal(t, int64(1), r.videoDropped.Load(),
		"expected 1 video drop, got %d", r.videoDropped.Load())

	// Fill the audio channel completely.
	for i := 0; i < rtmpAudioChSize; i++ {
		frame := &media.AudioFrame{
			PTS:  int64(i * 3000),
			Data: []byte{0x00},
		}
		_ = r.WriteAudio(frame)
	}

	// The next write should overflow.
	overflowAudio := &media.AudioFrame{
		PTS:  int64(rtmpAudioChSize * 3000),
		Data: []byte{0x00},
	}
	_ = r.WriteAudio(overflowAudio)

	require.Equal(t, int64(1), r.audioDropped.Load(),
		"expected 1 audio drop, got %d", r.audioDropped.Load())
}

func TestParseSPSDimensions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		sps          []byte
		wantW, wantH int
	}{
		{
			// x264 Baseline 1920x1080 (no cropping needed, 120 MBs × 68 map units × 2)
			// But 68*16=1088, not 1080 → needs frame_crop_bottom_offset=4
			// Real x264 SPS for 1920x1080 Baseline:
			name: "x264 Baseline 1920x1080",
			sps: []byte{
				0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02,
				0x27, 0xe5, 0xc0, 0x44, 0x00, 0x00, 0x03, 0x00,
				0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60,
				0xc6, 0x58,
			},
			wantW: 1920,
			wantH: 1080,
		},
		{
			// x264 High profile 1280x720 (no cropping, 80×45 MBs)
			name: "x264 High 1280x720",
			sps: []byte{
				0x67, 0x64, 0x00, 0x1f, 0xac, 0xd9, 0x40, 0x50,
				0x05, 0xbb, 0x01, 0x10, 0x00, 0x00, 0x03, 0x00,
				0x10, 0x00, 0x00, 0x03, 0x03, 0xc0, 0xf1, 0x62,
				0xe4, 0x80,
			},
			wantW: 1280,
			wantH: 720,
		},
		{
			// Minimal Baseline SPS (too short for full parse → fallback)
			name:  "minimal fallback",
			sps:   []byte{0x67, 0x42, 0xc0, 0x1e},
			wantW: 1920,
			wantH: 1080,
		},
		{
			name:  "nil SPS",
			sps:   nil,
			wantW: 1920,
			wantH: 1080,
		},
		{
			name:  "empty SPS",
			sps:   []byte{},
			wantW: 1920,
			wantH: 1080,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w, h := parseSPSDimensions(tt.sps)
			require.Equal(t, tt.wantW, w, "width")
			require.Equal(t, tt.wantH, h, "height")
		})
	}
}

// silentTCPServer accepts connections and never answers, as an ingest does
// when it hangs mid-handshake or a stateful firewall drops its replies.
func silentTCPServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var conns []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return ln.Addr().String()
}

func rtmpTestKeyframe(pts int64) *media.VideoFrame {
	return &media.VideoFrame{
		PTS: pts, DTS: pts, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xc0, 0x1e},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00},
	}
}

// Stopping a destination must not wait on an ingest that stopped answering:
// the connect runs on the write loop, and Close joins it.
func TestRTMPDirect_CloseAbortsHungConnect(t *testing.T) {
	addr := silentTCPServer(t)
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://" + addr + "/live/key"})
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background()))
	require.NoError(t, r.WriteVideo(rtmpTestKeyframe(90000)))
	time.Sleep(200 * time.Millisecond) // the write loop is now in the handshake

	closed := make(chan struct{})
	go func() {
		_ = r.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked behind a connect the ingest never answers")
	}
}

// A connect that never completes must fail on its own, so the adapter
// reports the error and retries with backoff instead of hanging silently.
func TestRTMPDirect_HungConnectTimesOutIntoReconnect(t *testing.T) {
	addr := silentTCPServer(t)
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://" + addr + "/live/key"})
	require.NoError(t, err)
	r.ioTimeout = 300 * time.Millisecond
	r.bo.Initial = time.Hour // keep the reconnect loop asleep
	require.NoError(t, r.Start(context.Background()))
	defer func() { _ = r.Close() }()
	require.NoError(t, r.WriteVideo(rtmpTestKeyframe(90000)))

	require.Eventually(t, func() bool {
		return *r.state.Load() == StateReconnecting
	}, 3*time.Second, 10*time.Millisecond, "connect never gave up")
}

// An ingest that refuses every connection must see the retries back off.
// Without a cached sequence header the reconnect loop hands the retry back
// to the next keyframe, and resetting the backoff there turned every retry
// into a fresh first attempt.
func TestRTMPDirect_FailedFirstConnectBacksOff(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	var attempts atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			attempts.Add(1)
			_ = c.Close()
		}
	}()

	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://" + ln.Addr().String() + "/live/key"})
	require.NoError(t, err)
	r.bo.Initial = 20 * time.Millisecond
	r.bo.JitterPct = 0
	require.NoError(t, r.Start(context.Background()))
	defer func() { _ = r.Close() }()

	// Keyframes every 10 ms for 1.3 s. Backing off from 20 ms doubles
	// through 40, 80, 160, 320 and 640 ms: about seven attempts.
	for end := time.Now().Add(1300 * time.Millisecond); time.Now().Before(end); {
		require.NoError(t, r.WriteVideo(rtmpTestKeyframe(90000)))
		time.Sleep(10 * time.Millisecond)
	}
	require.LessOrEqual(t, attempts.Load(), int32(10), "retries did not back off")
	require.GreaterOrEqual(t, attempts.Load(), int32(3), "adapter stopped retrying")
}

// flvTag is one audio or video tag read back from an FLV file.
type flvTag struct {
	kind  byte // 8 audio, 9 video
	ms    uint32
	media bool // coded media rather than a sequence header or end of sequence
}

func readFLVTags(t *testing.T, path string) []flvTag {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(b), 13)
	require.Equal(t, "FLV", string(b[:3]))
	var tags []flvTag
	for i := 13; i+11 <= len(b); {
		kind := b[i]
		size := int(b[i+1])<<16 | int(b[i+2])<<8 | int(b[i+3])
		ms := uint32(b[i+7])<<24 | uint32(b[i+4])<<16 | uint32(b[i+5])<<8 | uint32(b[i+6])
		body := b[i+11 : i+11+size]
		if kind == 8 || kind == 9 {
			// AACPacketType and AVCPacketType both sit at body[1]; 1 is
			// coded media.
			tags = append(tags, flvTag{kind: kind, ms: ms, media: len(body) > 1 && body[1] == 1})
		}
		i += 11 + size + 4
	}
	return tags
}

func mediaTimestamps(tags []flvTag, kind byte) []uint32 {
	var out []uint32
	for _, tg := range tags {
		if tg.kind == kind && tg.media {
			out = append(out, tg.ms)
		}
	}
	return out
}

// FLV carries timestamps in milliseconds. Frames arrive with 90 kHz PTS, and
// writing those ticks unscaled makes an ingest see 90 seconds of media per
// second of wall time.
func TestRTMPDirect_FLVTimestampsInMilliseconds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.flv")
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "file:" + path})
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background()))

	const base = int64(900000)
	key := &media.VideoFrame{
		PTS: base, DTS: base, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xc0, 0x1e},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00},
	}
	require.NoError(t, r.WriteVideo(key))
	require.Eventually(t, r.initialized.Load, 2*time.Second, 5*time.Millisecond)

	// Audio from before the first video frame and frames for 1 s at 50 fps.
	require.NoError(t, r.WriteAudio(&media.AudioFrame{PTS: base - 1920, Data: []byte{0x21, 0x10, 0x04}}))
	for i := int64(1); i < 50; i++ {
		require.NoError(t, r.WriteVideo(&media.VideoFrame{
			PTS: base + i*1800, DTS: base + i*1800,
			WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x41, 0x9a, 0x02, 0x00},
		}))
		if i <= 40 {
			pts := base + (i-1)*1920
			require.NoError(t, r.WriteAudio(&media.AudioFrame{PTS: pts, Data: []byte{0x21, 0x10, 0x04}}))
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, r.Close())

	tags := readFLVTags(t, path)
	video := mediaTimestamps(tags, 9)
	audio := mediaTimestamps(tags, 8)
	require.Len(t, video, 50)
	for i, ms := range video {
		require.Equal(t, uint32(i*20), ms, "video tag %d", i)
	}
	require.Len(t, audio, 40, "audio from before the first video frame is dropped")
	for i, ms := range audio {
		require.InDelta(t, float64(i)*1920/90, float64(ms), 1, "audio tag %d", i)
	}
}

// A lip-sync offset that falls by 400 ms mid-stream must not drop an RTMP
// output: FFmpeg's FLV muxer refuses a video timestamp that goes backwards,
// and the failed write sent the adapter into a reconnect.
func TestRTMPDirect_LipSyncFallKeepsConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.flv")
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "file:" + path})
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background()))
	m := NewManager()
	defer func() { _ = m.Close() }()
	raw := int64(36000)
	m.SetLipSyncSource(func() int64 { return raw })
	m.frameAdapters.Store(&[]FrameAdapter{r})

	const base, step = int64(900000), int64(1800)
	for i := int64(0); i < 60; i++ {
		if i == 20 {
			raw = 0
		}
		f := &media.VideoFrame{PTS: base + i*step, DTS: base + i*step,
			WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x41, 0x9a, 0x02, 0x00}}
		if i == 0 {
			f = &media.VideoFrame{PTS: base, DTS: base, IsKeyframe: true,
				SPS:      []byte{0x67, 0x42, 0xc0, 0x1e},
				PPS:      []byte{0x68, 0xce, 0x38, 0x80},
				WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00}}
		}
		m.DirectWriteVideo(f)
		if i == 0 {
			require.Eventually(t, r.initialized.Load, 2*time.Second, 5*time.Millisecond)
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, StateActive, *r.state.Load(), "error %q", *r.lastError.Load())
	require.NoError(t, r.Close())

	video := mediaTimestamps(readFLVTags(t, path), 9)
	require.Len(t, video, 60)
	for i := 1; i < len(video); i++ {
		require.Greater(t, video[i], video[i-1], "video tag %d", i)
	}
}

// A video timestamp that steps back, from any source, is written just after
// the last one rather than failing the FLV write.
func TestRTMPDirect_VideoTimestampStepBackKeepsConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.flv")
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "file:" + path})
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background()))

	const base = int64(900000)
	require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: base, DTS: base, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xc0, 0x1e},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00}}))
	require.Eventually(t, r.initialized.Load, 2*time.Second, 5*time.Millisecond)
	for _, pts := range []int64{base + 1800, base + 3600, base + 900, base + 5400} {
		require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: pts, DTS: pts,
			WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x41, 0x9a, 0x02, 0x00}}))
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, StateActive, *r.state.Load(), "error %q", *r.lastError.Load())
	require.NoError(t, r.Close())
	require.Equal(t, []uint32{0, 20, 40, 41, 60}, mediaTimestamps(readFLVTags(t, path), 9))
}

// After a reconnect, audio must wait for the keyframe that rebases the new
// connection's timeline instead of being stamped against the old one.
func TestRTMPDirect_AudioWaitsForRebaseAfterReconnect(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	r.connectFn = func(string) error { return nil }
	r.state.Store(ptrTo(StateActive))
	r.initialized.Store(true)
	r.ptsBase = 90000

	r.pendingIDR.Store(true)
	r.handleAudio(&media.AudioFrame{PTS: 180000, Data: []byte{0x21, 0x10}})
	require.Zero(t, r.bytesWritten.Load(), "audio before the rebasing keyframe")

	r.handleVideo(&media.VideoFrame{PTS: 270000, IsKeyframe: true, WireData: []byte{0x01}})
	require.False(t, r.pendingIDR.Load())
	r.handleAudio(&media.AudioFrame{PTS: 180000, Data: []byte{0x21, 0x10}})
	require.Zero(t, r.bytesWritten.Load()-1, "audio from before the new base")
	r.handleAudio(&media.AudioFrame{PTS: 271920, Data: []byte{0x21, 0x10}})
	require.Equal(t, int64(3), r.bytesWritten.Load())
}

// Close aborts a flush in flight, which fails that write. A destination
// stopped on purpose must not keep reporting the write error.
func TestRTMPDirect_CloseClearsWriteError(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background()))
	r.lastError.Store(ptrTo("RTMP video write failed"))
	require.NoError(t, r.Close())

	st := r.RTMPStatusSnapshot()
	require.Equal(t, string(StateStopped), st.State)
	require.Empty(t, r.Status().Error)
}

// Video PTS wraps at 33 bits after about 26.5 hours; audio PTS counts on.
// FLV timestamps carry on across the wrap for both, instead of video
// dropping to 0 behind audio.
func TestRTMPDirect_TimestampsContinueAcrossPTSWrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.flv")
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "file:" + path})
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background()))

	keyData := []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00}
	pData := []byte{0x00, 0x00, 0x00, 0x04, 0x41, 0x9a, 0x02, 0x00}
	audioData := []byte{0x21, 0x10, 0x04}
	var want int64
	wrapFrames(t,
		func(pts int64, key bool) error {
			f := &media.VideoFrame{PTS: pts, DTS: pts, WireData: pData}
			if key {
				f = &media.VideoFrame{PTS: pts, DTS: pts, IsKeyframe: true,
					SPS: []byte{0x67, 0x42, 0xc0, 0x1e}, PPS: []byte{0x68, 0xce, 0x38, 0x80}, WireData: keyData}
			}
			want += int64(len(f.WireData))
			if err := r.WriteVideo(f); err != nil {
				return err
			}
			if key {
				require.Eventually(t, r.initialized.Load, 2*time.Second, 5*time.Millisecond)
			}
			time.Sleep(time.Millisecond)
			return nil
		},
		func(pts int64) error {
			want += int64(len(audioData))
			return r.WriteAudio(&media.AudioFrame{PTS: pts, Data: audioData})
		})
	require.Eventually(t, func() bool { return r.bytesWritten.Load() == want }, 2*time.Second, 5*time.Millisecond,
		"not every frame was written; state %s, error %q", *r.state.Load(), *r.lastError.Load())
	require.NoError(t, r.Close())

	tags := readFLVTags(t, path)
	video := mediaTimestamps(tags, 9)
	audio := mediaTimestamps(tags, 8)
	require.Len(t, video, 50)
	for i, ms := range video {
		require.Equal(t, uint32(i*20), ms, "video tag %d", i)
	}
	require.Len(t, audio, 40)
	for i, ms := range audio {
		require.InDelta(t, float64(i)*1920/90, float64(ms), 1, "audio tag %d", i)
	}
}

// rtmpForMediaTime is an RTMPDirect in test mode with a video queue of one
// frame: send offers a frame, write takes the queued one as writeLoop would.
func rtmpForMediaTime(t *testing.T) *RTMPDirect {
	t.Helper()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	r.connectFn = func(string) error { return nil }
	r.videoCh = make(chan *media.VideoFrame, 1)
	r.state.Store(ptrTo(StateActive))
	r.initialized.Store(true)
	return r
}

func (r *RTMPDirect) testWrite() {
	select {
	case f := <-r.videoCh:
		r.handleVideo(f)
	default:
	}
}

const frameTicks50 = 1800 // 90 kHz ticks at 50 fps

// The media time sent is one program frame interval per frame written:
// a destination taking every frame sends one second per second.
func TestRTMPDirect_CountsMediaTime(t *testing.T) {
	t.Parallel()
	r := rtmpForMediaTime(t)
	pts := int64(90000)
	// Three steps measure the interval; frames before that count nothing.
	for i := 0; i < 4; i++ {
		require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: pts, IsKeyframe: i == 0, WireData: []byte{0x01}}))
		r.testWrite()
		pts += frameTicks50
	}
	start := r.Status().MediaTicks
	for i := 0; i < 50; i++ {
		require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: pts, WireData: []byte{0x01}}))
		r.testWrite()
		pts += frameTicks50
	}
	require.Equal(t, int64(90000), r.Status().MediaTicks-start, "one second of 50 fps frames")
}

// Frames the destination does not take in time are not media sent: the
// written frames' PTS steps span the dropped ones, so counting steps would
// read a destination taking every other frame as keeping up.
func TestRTMPDirect_MediaTimeFallsWithDroppedFrames(t *testing.T) {
	t.Parallel()
	r := rtmpForMediaTime(t)
	pts := int64(90000)
	for i := 0; i < 4; i++ {
		require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: pts, IsKeyframe: i == 0, WireData: []byte{0x01}}))
		r.testWrite()
		pts += frameTicks50
	}
	start := r.Status().MediaTicks
	// Two seconds offered, every second frame finds the queue full.
	for i := 0; i < 100; i++ {
		require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: pts, WireData: []byte{0x01}}))
		if i%2 == 1 {
			r.testWrite()
		}
		pts += frameTicks50
	}
	st := r.Status()
	require.Equal(t, int64(90000), st.MediaTicks-start, "half of two seconds")
	require.Equal(t, int64(50), st.VideoDropped)
}

// A source change can step the PTS forwards or backwards by any amount;
// the frame written across it is one frame interval of media, not the step.
func TestRTMPDirect_MediaTimeIgnoresPTSJumps(t *testing.T) {
	t.Parallel()
	r := rtmpForMediaTime(t)
	send := func(pts int64) {
		require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: pts, IsKeyframe: true, WireData: []byte{0x01}}))
		r.testWrite()
	}
	pts := int64(90000)
	for i := 0; i < 4; i++ {
		send(pts)
		pts += frameTicks50
	}
	start := r.Status().MediaTicks
	pts += 3600 * 90000 // an hour ahead
	for i := 0; i < 3; i++ {
		send(pts)
		pts += frameTicks50
	}
	pts = 1000 // and back
	for i := 0; i < 3; i++ {
		send(pts)
		pts += frameTicks50
	}
	// A jump under a second still takes no more than one interval: the
	// interval is the median of the last three steps.
	pts += 45000
	send(pts)
	require.Equal(t, int64(7*frameTicks50), r.Status().MediaTicks-start, "seven frames written")
}

// Program PTS steps can jitter around the frame interval, as with a fresh
// source frame's own PTS; with every frame written the rate still reads
// one, not the low side of the jitter.
func TestRTMPDirect_MediaTimeAveragesJitter(t *testing.T) {
	t.Parallel()
	r := rtmpForMediaTime(t)
	steps := []int64{1800, 1700, 1900, 1800, 1650, 1950} // +-8% around 50 fps
	pts := int64(90000)
	for i := 0; i < 4; i++ {
		require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: pts, IsKeyframe: i == 0, WireData: []byte{0x01}}))
		r.testWrite()
		pts += frameTicks50
	}
	start, startPTS := r.Status().MediaTicks, pts-frameTicks50
	for i := 0; i < 600; i++ { // 12 s
		pts += steps[i%len(steps)] - frameTicks50
		require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: pts, WireData: []byte{0x01}}))
		r.testWrite()
		pts += frameTicks50
	}
	sent := float64(r.Status().MediaTicks - start)
	offered := float64(pts - frameTicks50 - startPTS)
	require.InDelta(t, 1.0, sent/offered, 0.01, "sent %.0f of %.0f ticks", sent, offered)
}

// Steps over a second are gaps, not intervals, even several in a row.
func TestRTMPDirect_MediaTimeIgnoresRepeatedGaps(t *testing.T) {
	t.Parallel()
	r := rtmpForMediaTime(t)
	send := func(pts int64) {
		require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: pts, IsKeyframe: true, WireData: []byte{0x01}}))
		r.testWrite()
	}
	pts := int64(90000)
	for i := 0; i < 4; i++ {
		send(pts)
		pts += frameTicks50
	}
	start := r.Status().MediaTicks
	for i := 0; i < 3; i++ {
		pts += 2 * 90000
		send(pts)
	}
	require.Equal(t, int64(3*frameTicks50), r.Status().MediaTicks-start)
}

// The time a reconnect keeps the destination down is not media sent: the
// frames before the first keyframe on the new connection are not written.
func TestRTMPDirect_MediaTimeSkipsAReconnect(t *testing.T) {
	t.Parallel()
	r := rtmpForMediaTime(t)
	send := func(pts int64, key bool) {
		require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: pts, IsKeyframe: key, WireData: []byte{0x01}}))
		r.testWrite()
	}
	pts := int64(90000)
	for i := 0; i < 4; i++ {
		send(pts, i == 0)
		pts += frameTicks50
	}
	start := r.Status().MediaTicks
	r.pendingIDR.Store(true)
	for i := 0; i < 100; i++ { // two seconds of P-frames
		send(pts, false)
		pts += frameTicks50
	}
	send(pts, true)
	send(pts+frameTicks50, false)
	require.Equal(t, int64(2*frameTicks50), r.Status().MediaTicks-start)
}
