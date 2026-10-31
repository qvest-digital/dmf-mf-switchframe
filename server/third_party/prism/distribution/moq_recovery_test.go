package distribution

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
	webtransport "github.com/quic-go/webtransport-go"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/certs"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/prism/moq"
)

// stopSending is the application error code a viewer cancels a stream
// with; any code makes the server's next write on it fail.
const stopSending webtransport.StreamErrorCode = 0x10

func moqTestSession(t *testing.T) (*MoQSession, *webtransport.Session) {
	t.Helper()
	server, client := webTransportSession(t)
	m := NewMoQSession(MoQSessionConfig{
		ID:        "recovery",
		Session:   server,
		Control:   &mockControlStream{Reader: &bytes.Buffer{}, Writer: &bytes.Buffer{}},
		StreamKey: "live",
		Relay:     NewRelay(),
	})
	return m, client
}

// cancelFirstUniStream accepts the first stream the server opens and
// cancels reading it, as a viewer that drops a stream does.
func cancelFirstUniStream(t *testing.T, client *webtransport.Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := client.AcceptUniStream(ctx)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	s.CancelRead(stopSending)
}

// acceptGroup accepts the next stream the server opens and returns the
// group ID in its subgroup header.
func acceptGroup(t *testing.T, client *webtransport.Session) uint64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := client.AcceptUniStream(ctx)
	if err != nil {
		t.Fatalf("no further stream from the server: %v", err)
	}
	r := quicvarint.NewReader(s)
	var hdr [3]uint64 // stream type, track alias, group ID
	for i := range hdr {
		if hdr[i], err = quicvarint.Read(r); err != nil {
			t.Fatalf("read stream header: %v", err)
		}
	}
	return hdr[2]
}

func subscribeTrack(m *MoQSession, track string) *moqTrackSub {
	m.handleSubscribe(context.Background(), moq.Subscribe{
		RequestID: 1, Namespace: []string{"prism", "live"},
		TrackName: track, FilterType: moq.FilterNextGroupStart,
	})
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.subscriptions[track]
}

// testWriter wraps a track's writer and counts the writes that failed. With
// err set, every write fails with it instead of writing, once gate, if
// set, is closed.
type testWriter struct {
	StreamFrameWriter
	err     error
	gate    chan struct{}
	entered atomic.Int32
	failed  atomic.Int32
}

func (w *testWriter) write(f func() (int64, error)) (int64, error) {
	w.entered.Add(1)
	var n int64
	err := w.err
	if err != nil {
		if w.gate != nil {
			<-w.gate
		}
	} else {
		n, err = f()
	}
	if err != nil {
		w.failed.Add(1)
	}
	return n, err
}

func (w *testWriter) WriteStreamHeader(s io.Writer, trackID byte, groupID, tsMS uint32) error {
	_, err := w.write(func() (int64, error) {
		return 0, w.StreamFrameWriter.WriteStreamHeader(s, trackID, groupID, tsMS)
	})
	return err
}

func (w *testWriter) WriteVideoFrame(s io.Writer, f *media.VideoFrame) (int64, error) {
	return w.write(func() (int64, error) { return w.StreamFrameWriter.WriteVideoFrame(s, f) })
}

func (w *testWriter) WriteAudioFrame(s io.Writer, data []byte, tsMS uint32) (int64, error) {
	return w.write(func() (int64, error) { return w.StreamFrameWriter.WriteAudioFrame(s, data, tsMS) })
}

func (w *testWriter) WriteDataObject(s io.Writer, data []byte, tsMS uint32) (int64, error) {
	return w.write(func() (int64, error) { return w.StreamFrameWriter.WriteDataObject(s, data, tsMS) })
}

// wrapWriter puts a testWriter in front of sub's writer. The write loop
// reads the writer only after it receives a frame, which the test sends
// after this.
func wrapWriter(sub *moqTrackSub, err error) *testWriter {
	w := &testWriter{StreamFrameWriter: sub.writer, err: err}
	sub.writer = w
	return w
}

// until polls cond for up to 5 s.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func keyframe(group uint32) *media.VideoFrame {
	return &media.VideoFrame{IsKeyframe: true, GroupID: group, WireData: []byte{0, 0, 0, 1, 0x65}}
}

func delta(group uint32) *media.VideoFrame {
	return &media.VideoFrame{GroupID: group, WireData: []byte{0, 0, 0, 1, 0x41}}
}

// A viewer that resets one group's stream ends that group only. The write
// loop used to return on the failed write and leave the subscription
// registered: the relay kept queueing frames nobody read, every keyframe
// displaced them again, and the viewer froze on a session that never
// closed, so nothing on either side reconnected.
func TestMoQVideoStreamResetResumesAtNextKeyframe(t *testing.T) {
	m, client := moqTestSession(t)
	sub := subscribeTrack(m, "video")
	w := wrapWriter(sub, nil)

	m.SendVideo(keyframe(1))
	cancelFirstUniStream(t, client)
	until(t, "a failed video write", func() bool {
		m.SendVideo(delta(1))
		return w.failed.Load() > 0
	})

	m.SendVideo(keyframe(2))
	m.SendVideo(delta(2))
	if g := acceptGroup(t, client); g != 2 {
		t.Fatalf("next stream carries group %d, want 2", g)
	}
	if m.session.Context().Err() != nil {
		t.Fatal("a reset of one stream closed the session")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.subscriptions["video"] != sub {
		t.Fatal("subscription replaced or removed")
	}
}

func TestMoQAudioStreamResetOpensNewStream(t *testing.T) {
	m, client := moqTestSession(t)
	w := wrapWriter(subscribeTrack(m, "audio0"), nil)

	m.SendAudio(&media.AudioFrame{Data: []byte{0xff, 0xf1}})
	cancelFirstUniStream(t, client)
	until(t, "a failed audio write", func() bool {
		m.SendAudio(&media.AudioFrame{PTS: 21_333, Data: []byte{0xff, 0xf1}})
		return w.failed.Load() > 0
	})

	m.SendAudio(&media.AudioFrame{PTS: 42_666, Data: []byte{0xff, 0xf1}})
	acceptGroup(t, client)
	if m.session.Context().Err() != nil {
		t.Fatal("a reset of the audio stream closed the session")
	}
}

// A viewer that resets every audio stream it is sent would have the
// session open one for each frame for as long as it stays connected.
func TestMoQAudioStreamFailuresInARowCloseSession(t *testing.T) {
	m, _ := moqTestSession(t)
	w := wrapWriter(subscribeTrack(m, "audio0"), &webtransport.StreamError{ErrorCode: stopSending})

	until(t, "the session to close", func() bool {
		m.SendAudio(&media.AudioFrame{Data: []byte{0xff, 0xf1}})
		return m.session.Context().Err() != nil
	})
	if n := w.failed.Load(); n != maxAudioStreamFailures {
		t.Fatalf("session closed after %d failed audio streams, want %d", n, maxAudioStreamFailures)
	}
}

// logRecorder records the messages of every log record at Warn or above.
type logRecorder struct {
	mu   sync.Mutex
	msgs []string
}

func (r *logRecorder) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }
func (r *logRecorder) WithAttrs([]slog.Attr) slog.Handler           { return r }
func (r *logRecorder) WithGroup(string) slog.Handler                { return r }
func (r *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, rec.Message)
	return nil
}

func (r *logRecorder) count(msg string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.msgs {
		if m == msg {
			n++
		}
	}
	return n
}

// An error that is not confined to one stream closes the session, so the
// viewer reconnects; left open, the viewer would freeze on a session that
// carries nothing. Every track fails once the connection is gone, and the
// loss is logged once, not once per track.
func TestMoQSessionErrorClosesSessionOnce(t *testing.T) {
	m, client := moqTestSession(t)
	logs := &logRecorder{}
	m.log = slog.New(logs)
	broken := errors.New("connection lost")
	gate := make(chan struct{})
	var writers []*testWriter
	for _, track := range []string{"video", "audio0", "captions"} {
		w := wrapWriter(subscribeTrack(m, track), broken)
		w.gate = gate
		writers = append(writers, w)
	}

	m.SendVideo(keyframe(1))
	m.SendAudio(&media.AudioFrame{Data: []byte{0xff, 0xf1}})
	m.SendCaptions(&ccx.CaptionFrame{})
	// Every track is mid-write when the connection goes.
	until(t, "a write on every track", func() bool {
		for _, w := range writers {
			if w.entered.Load() == 0 {
				return false
			}
		}
		return true
	})
	close(gate)
	select {
	case <-client.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session left open after a session error")
	}
	// The write loops return after their failure; give the last one time
	// to log before counting.
	time.Sleep(50 * time.Millisecond)
	if n := logs.count("moq session failed"); n != 1 {
		t.Fatalf("session failure logged %d times, want 1", n)
	}
}

// A write that fails because its subscription ended, by UNSUBSCRIBE or a
// repeated SUBSCRIBE, is no session error.
func TestMoQFailIgnoresEndedSubscription(t *testing.T) {
	m, _ := moqTestSession(t)
	logs := &logRecorder{}
	m.log = slog.New(logs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	m.fail(ctx, "video frame write", errors.New("context canceled"))
	if m.session.Context().Err() != nil {
		t.Fatal("an ended subscription closed the session")
	}
	if n := logs.count("moq session failed"); n != 0 {
		t.Fatalf("an ended subscription logged %d session failures, want 0", n)
	}
}

// Producers number their groups themselves and a playout channel, clip
// player or replay that stops and starts counts from 1 again. Viewers
// drop a group below the newest they have seen, so the IDs a session
// sends must keep increasing whatever the producer does.
func TestMoQWireGroupIDsIncreaseAcrossProducerRestart(t *testing.T) {
	m, client := moqTestSession(t)
	subscribeTrack(m, "video")

	var got []uint64
	for _, g := range []uint32{5, 7, 1, 2} {
		m.SendVideo(keyframe(g))
		got = append(got, acceptGroup(t, client))
	}
	want := []uint64{5, 7, 8, 9} // a producer gap (5 -> 7) still shows
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wire groups %v for producer groups 5 7 1 2, want %v", got, want)
		}
	}
}

// A second SUBSCRIBE for a track replaces the first; the first one's write
// loop has to end with it rather than block for the session's lifetime.
func TestMoQRepeatedSubscribeEndsPreviousSubscription(t *testing.T) {
	m, _ := moqTestSession(t)
	m.controlBroadcaster = NewControlBroadcaster()
	for _, track := range []string{"video", "audio0", "captions", "stats", "control"} {
		first := subscribeTrack(m, track)
		cancelled := false
		cancel := first.cancel
		first.cancel = func() { cancelled = true; cancel() }

		second := subscribeTrack(m, track)
		if second == first {
			t.Fatalf("%s: second SUBSCRIBE did not create a subscription", track)
		}
		if !cancelled {
			t.Fatalf("%s: second SUBSCRIBE left the first subscription running", track)
		}
	}
}

// startTestServer serves s.handleMoQ on a loopback UDP port, with the
// HTTP/3 setup webTransportSession uses, and returns its address.
func startTestServer(t *testing.T, keys ...string) (*Server, string) {
	t.Helper()
	cert, err := certs.Generate(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(ServerConfig{Addr: udp.LocalAddr().String(), Cert: cert})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		srv.RegisterStream(k)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/moq", srv.handleMoQ)
	h3 := &http3.Server{
		Handler:   mux,
		TLSConfig: http3.ConfigureTLSConfig(&tls.Config{Certificates: []tls.Certificate{cert.TLSCert}}),
	}
	webtransport.ConfigureHTTP3Server(h3)
	srv.wtSrv = &webtransport.Server{H3: h3, CheckOrigin: func(*http.Request) bool { return true }}
	go func() { _ = srv.wtSrv.Serve(udp) }()
	t.Cleanup(func() { _ = srv.wtSrv.Close() })
	return srv, udp.LocalAddr().String()
}

// dialMoQ dials /moq for key and completes CLIENT_SETUP / SERVER_SETUP.
func dialMoQ(ctx context.Context, t *testing.T, addr, key string) (*webtransport.Session, *webtransport.Stream, *bufio.Reader) {
	t.Helper()
	var d webtransport.Dialer
	d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	t.Cleanup(func() { _ = d.Close() })
	var sess *webtransport.Session
	var err error
	for range 50 { // the server may not be listening yet
		_, sess, err = d.Dial(ctx, fmt.Sprintf("https://%s/moq?stream=%s", addr, key), http.Header{})
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ctrl, err := sess.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open control: %v", err)
	}
	if err := moq.WriteControlMsg(ctrl, moq.MsgClientSetup, buildClientSetupPayload([]uint64{moq.Version}, "", 100)); err != nil {
		t.Fatal(err)
	}
	// One reader for the stream's life: ReadControlMsg buffers ahead.
	r := bufio.NewReader(ctrl)
	for _, want := range []uint64{moq.MsgServerSetup, moq.MsgMaxRequestID} {
		typ, _, err := moq.ReadControlMsg(r)
		if err != nil || typ != want {
			t.Fatalf("setup: got %#x, %v; want %#x", typ, err, want)
		}
	}
	return sess, ctrl, r
}

func subscribePayload(reqID uint64, ns []string, track string) []byte {
	var b []byte
	b = quicvarint.Append(b, reqID)
	b = moq.AppendNamespaceTuple(b, ns)
	b = quicvarint.Append(b, uint64(len(track)))
	b = append(b, track...)
	b = append(b, 128, moq.GroupOrderAscending, 0)
	b = quicvarint.Append(b, moq.FilterLatestObject)
	return quicvarint.Append(b, 0)
}

// clientConnectTimeout is how long the browser gives a transport to
// deliver its catalog before it abandons the dial and dials again
// (CONNECT_TIMEOUT_MS in ui/src/lib/transport/media-pipeline.ts).
const clientConnectTimeout = 4 * time.Second

// subscribeCatalog dials key, subscribes its catalog and waits for the
// SUBSCRIBE_OK, failing once the browser would have abandoned the dial.
func subscribeCatalog(t *testing.T, addr, key string) *webtransport.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess, ctrl, r := dialMoQ(ctx, t, addr, key)
	t.Cleanup(func() { _ = sess.CloseWithError(0, "") })

	start := time.Now()
	if err := moq.WriteControlMsg(ctrl, moq.MsgSubscribe, subscribePayload(0, []string{"prism", key}, "catalog")); err != nil {
		t.Fatal(err)
	}
	got := make(chan uint64, 1)
	go func() {
		typ, _, err := moq.ReadControlMsg(r)
		if err == nil {
			got <- typ
		}
	}()
	select {
	case typ := <-got:
		if typ != moq.MsgSubscribeOK {
			t.Fatalf("catalog subscribe answered with %#x, want SUBSCRIBE_OK", typ)
		}
	case <-time.After(clientConnectTimeout):
		t.Fatalf("catalog subscribe unanswered after %v; the browser abandons the dial "+
			"at this point and redials", time.Since(start).Round(time.Millisecond))
	}
	return sess
}

// A stream with no video yet, such as an input wired to nothing, has to
// answer its catalog subscription as quickly as any other. The handler
// waited up to 30 s for video info before it read the control stream at
// all, so the browser gave up after its own 4 s, dialled again, and kept
// that up for as long as the tab was open: one session per dial, each
// held on the server for the full 30 s after the browser left.
func TestMoQCatalogAnsweredForStreamWithoutVideo(t *testing.T) {
	_, addr := startTestServer(t, "mxl:input")
	subscribeCatalog(t, addr, "mxl:input")
}

// A catalog answered from defaults goes stale once the stream gets its
// real info. Browsers read the catalog once per session, so the session
// closes and the reconnect brings the catalog with the real tracks.
func TestMoQSessionClosesWhenCatalogInfoArrives(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Relay)
		closes bool
	}{
		{"video info", func(r *Relay) { r.SetVideoInfo(VideoInfo{Codec: "avc1.64001f", Width: 854, Height: 480}) }, true},
		{"audio info", func(r *Relay) { r.SetAudioInfo(AudioInfo{Codec: "mp4a.40.2", SampleRate: 48000, Channels: 8}) }, true},
		{"audio track count", func(r *Relay) { r.SetAudioTrackCount(2) }, true},
		{"same audio track count", func(r *Relay) { r.SetAudioTrackCount(1) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, addr := startTestServer(t, "mxl:input")
			sess := subscribeCatalog(t, addr, "mxl:input")
			tc.change(srv.GetRelay("mxl:input"))

			select {
			case <-sess.Context().Done():
				if !tc.closes {
					t.Fatal("session closed although the catalog did not change")
				}
			case <-time.After(2 * time.Second):
				if tc.closes {
					t.Fatal("session still open 2 s after its catalog went stale")
				}
			}
		})
	}
}
