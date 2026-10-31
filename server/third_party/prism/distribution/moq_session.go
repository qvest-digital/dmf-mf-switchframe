package distribution

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	webtransport "github.com/quic-go/webtransport-go"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/prism/moq"
)

// moqTrackSub holds state for a single track subscription within a MoQ session.
type moqTrackSub struct {
	requestID       uint64
	trackAlias      uint64
	trackName       string
	writer          StreamFrameWriter
	videoCh         chan *media.VideoFrame
	audioCh         chan *media.AudioFrame
	captionCh       chan *ccx.CaptionFrame
	audioTrackIndex int
	cancel          context.CancelFunc
}

// Compile-time interface checks.
var _ Viewer = (*MoQSession)(nil)

// StatsProviderFunc resolves the StatsProvider for a stream key lazily,
// since the pipeline may not exist when the MoQ session is created.
type StatsProviderFunc func(streamKey string) StatsProvider

// MoQSession manages a single MoQ viewer connection. It implements the Viewer
// interface so the Relay can fan out frames to it. Internally, it dispatches
// frames to per-track subscriptions, each with its own write loop and moqWriter.
type MoQSession struct {
	id                 string
	log                *slog.Logger
	streamKey          string
	session            *webtransport.Session
	control            io.ReadWriter
	controlReader      *bufio.Reader // persistent buffered reader for control stream
	relay              *Relay
	statsProvider      StatsProviderFunc
	controlBroadcaster *ControlBroadcaster
	onDatagram            func(streamKey string, data []byte)
	onBidirectionalStream func(streamKey string, stream io.ReadWriteCloser)
	controlMu             sync.Mutex

	mu             sync.RWMutex
	subscriptions  map[string]*moqTrackSub // key: trackName
	nextTrackAlias uint64

	damagedGroup atomic.Uint32
	closed       atomic.Bool
	failOnce     sync.Once

	videoSent      atomic.Int64
	audioSent      atomic.Int64
	captionSent    atomic.Int64
	videoDropped   atomic.Int64
	audioDropped   atomic.Int64
	captionDropped atomic.Int64
	bytesSent      atomic.Int64
	lastVideoTsMS  atomic.Int64
	lastAudioTsMS  atomic.Int64
}

// MoQSessionConfig holds the parameters for creating a new MoQ session.
type MoQSessionConfig struct {
	ID                 string
	Session            *webtransport.Session
	Control            io.ReadWriter
	StreamKey          string
	Relay              *Relay
	StatsProvider      StatsProviderFunc
	ControlBroadcaster *ControlBroadcaster

	// OnDatagram is called when a WebTransport datagram arrives from a viewer.
	// The callback receives the viewer's stream key and the raw datagram bytes.
	// Called from the session's datagram read goroutine — must not block.
	OnDatagram func(streamKey string, data []byte)

	// OnBidirectionalStream is called when a viewer opens a new bidirectional
	// WebTransport stream. The callback receives the stream key and the stream.
	// It runs in its own goroutine per stream and should return when done.
	OnBidirectionalStream func(streamKey string, stream io.ReadWriteCloser)
}

// NewMoQSession creates a new MoQ session for the given stream key.
func NewMoQSession(cfg MoQSessionConfig) *MoQSession {
	return &MoQSession{
		id:                 cfg.ID,
		log:                slog.With("session", cfg.ID, "stream", cfg.StreamKey),
		streamKey:          cfg.StreamKey,
		session:            cfg.Session,
		control:            cfg.Control,
		controlReader:      bufio.NewReader(cfg.Control),
		relay:              cfg.Relay,
		statsProvider:      cfg.StatsProvider,
		controlBroadcaster: cfg.ControlBroadcaster,
		onDatagram:            cfg.OnDatagram,
		onBidirectionalStream: cfg.OnBidirectionalStream,
		subscriptions:         make(map[string]*moqTrackSub),
	}
}

// ID returns the unique identifier for this MoQ session.
func (m *MoQSession) ID() string { return m.id }

// handleSetup performs the CLIENT_SETUP / SERVER_SETUP exchange.
// Returns the stream key from the PATH parameter if present.
func (m *MoQSession) handleSetup() (string, error) {
	msgType, payload, err := moq.ReadControlMsg(m.controlReader)
	if err != nil {
		return "", fmt.Errorf("read CLIENT_SETUP: %w", err)
	}
	if msgType != moq.MsgClientSetup {
		return "", fmt.Errorf("expected CLIENT_SETUP (0x%x), got 0x%x", moq.MsgClientSetup, msgType)
	}

	cs, err := moq.ParseClientSetup(payload)
	if err != nil {
		return "", fmt.Errorf("parse CLIENT_SETUP: %w", err)
	}

	// Check version compatibility
	versionOK := false
	for _, v := range cs.Versions {
		if v == moq.Version {
			versionOK = true
			break
		}
	}
	if !versionOK {
		return "", fmt.Errorf("%w (client offered %v)", moq.ErrVersionMismatch, cs.Versions)
	}

	// Send SERVER_SETUP
	ss := moq.ServerSetup{
		SelectedVersion: moq.Version,
		MaxRequestID:    100,
	}

	m.controlMu.Lock()
	err = moq.WriteControlMsg(m.control, moq.MsgServerSetup, moq.SerializeServerSetup(ss))
	m.controlMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("write SERVER_SETUP: %w", err)
	}

	// Send MAX_REQUEST_ID
	m.controlMu.Lock()
	err = moq.WriteControlMsg(m.control, moq.MsgMaxRequestID, moq.SerializeMaxRequestID(100))
	m.controlMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("write MAX_REQUEST_ID: %w", err)
	}

	pathKey := ""
	if cs.HasPath {
		pathKey = cs.Path
	}
	return pathKey, nil
}

// Run starts the MoQ session control loop. It blocks until the session ends.
func (m *MoQSession) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go m.readControlLoop(ctx)
	go m.readDatagramLoop(ctx)
	go m.acceptBidirectionalStreams(ctx)

	<-ctx.Done()

	m.closed.Store(true)

	// Send GOAWAY
	m.controlMu.Lock()
	_ = moq.WriteControlMsg(m.control, moq.MsgGoAway, moq.SerializeGoAway(moq.GoAway{}))
	m.controlMu.Unlock()

	// Cancel all subscriptions
	m.mu.Lock()
	for _, sub := range m.subscriptions {
		if sub.cancel != nil {
			sub.cancel()
		}
	}
	m.subscriptions = make(map[string]*moqTrackSub)
	m.mu.Unlock()

	return ctx.Err()
}

// readControlLoop reads and dispatches control messages from the client.
func (m *MoQSession) readControlLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		msgType, payload, err := moq.ReadControlMsg(m.controlReader)
		if err != nil {
			if ctx.Err() == nil {
				m.log.Debug("control read error", "error", err)
			}
			return
		}

		switch msgType {
		case moq.MsgSubscribe:
			sub, err := moq.ParseSubscribe(payload)
			if err != nil {
				m.log.Warn("bad SUBSCRIBE", "error", err)
				continue
			}
			m.handleSubscribe(ctx, sub)

		case moq.MsgUnsubscribe:
			unsub, err := moq.ParseUnsubscribe(payload)
			if err != nil {
				m.log.Warn("bad UNSUBSCRIBE", "error", err)
				continue
			}
			m.handleUnsubscribe(unsub)

		case moq.MsgMaxRequestID:
			// Acknowledge but don't enforce client quotas
			m.log.Debug("MAX_REQUEST_ID from client")

		default:
			m.log.Debug("unknown message", "type", msgType)
		}
	}
}

// readDatagramLoop reads WebTransport datagrams and dispatches them to the
// onDatagram callback. It exits when the context is cancelled, the callback
// is nil, or the session is nil.
func (m *MoQSession) readDatagramLoop(ctx context.Context) {
	if m.onDatagram == nil || m.session == nil {
		return
	}
	for {
		data, err := m.session.ReceiveDatagram(ctx)
		if err != nil {
			if ctx.Err() == nil {
				m.log.Debug("datagram read error", "error", err)
			}
			return
		}
		m.onDatagram(m.streamKey, data)
	}
}

// acceptBidirectionalStreams accepts additional bidirectional streams from the
// WebTransport session (beyond the initial MoQ control stream) and dispatches
// each to the onBidirectionalStream callback in its own goroutine.
func (m *MoQSession) acceptBidirectionalStreams(ctx context.Context) {
	if m.onBidirectionalStream == nil || m.session == nil {
		return
	}
	for {
		stream, err := m.session.AcceptStream(ctx)
		if err != nil {
			if ctx.Err() == nil {
				m.log.Debug("bidi stream accept error", "error", err)
			}
			return
		}
		go m.onBidirectionalStream(m.streamKey, stream)
	}
}

// handleSubscribe processes a SUBSCRIBE message.
func (m *MoQSession) handleSubscribe(ctx context.Context, sub moq.Subscribe) {
	// Validate namespace: must be ["prism", streamKey]
	if len(sub.Namespace) != 2 || sub.Namespace[0] != "prism" || sub.Namespace[1] != m.streamKey {
		m.sendSubscribeError(sub.RequestID, 404, moq.ErrUnknownNamespace.Error())
		return
	}

	// Only support live filter types
	if sub.FilterType != moq.FilterNextGroupStart && sub.FilterType != moq.FilterLatestObject {
		m.sendSubscribeError(sub.RequestID, 400, moq.ErrUnsupportedFilter.Error())
		return
	}

	trackName := sub.TrackName

	// Allocate track alias
	m.mu.Lock()
	alias := m.nextTrackAlias
	m.nextTrackAlias++
	m.mu.Unlock()

	switch trackName {
	case "catalog":
		m.handleCatalogSubscribe(ctx, sub, alias)

	case "video":
		m.handleMediaSubscribe(ctx, sub, alias, trackName, "video", 0)

	case "captions":
		m.handleMediaSubscribe(ctx, sub, alias, trackName, "captions", 0)

	case "stats":
		m.handleStatsSubscribe(ctx, sub, alias)

	case "control":
		m.handleControlSubscribe(ctx, sub, alias)

	default:
		// Check for audio tracks: "audio0", "audio1", etc.
		if suffix, ok := strings.CutPrefix(trackName, "audio"); ok {
			if idx, err := strconv.Atoi(suffix); err == nil && idx >= 0 {
				m.handleMediaSubscribe(ctx, sub, alias, trackName, "audio", idx)
				return
			}
		}
		m.sendSubscribeError(sub.RequestID, 404, moq.ErrUnknownTrack.Error())
	}
}

// handleCatalogSubscribe builds and delivers the catalog, then sends SUBSCRIBE_OK.
func (m *MoQSession) handleCatalogSubscribe(ctx context.Context, sub moq.Subscribe, alias uint64) {
	// Taken before the catalog is built, so a change in between is not missed.
	changed := m.relay.CatalogChanged()
	catalogJSON, err := buildMoQCatalog(m.streamKey, m.relay, m.controlBroadcaster != nil)
	if err != nil {
		m.sendSubscribeError(sub.RequestID, 500, "catalog build failed")
		return
	}

	if err := writeCatalogObject(ctx, m.session, alias, catalogJSON); err != nil {
		m.log.Warn("catalog delivery failed", "error", err)
		m.sendSubscribeError(sub.RequestID, 500, "catalog delivery failed")
		return
	}

	m.sendSubscribeOK(sub.RequestID, alias, moq.GroupOrderAscending, true, 0, 0)
	go m.closeOnCatalogChange(ctx, changed)
}

// closeOnCatalogChange closes the session once the info its catalog was
// built from changes, as it does when an input wired to nothing gets its
// first video or audio. A viewer reads the catalog once per session and
// reconnects on a close, which brings it the new catalog and its tracks.
func (m *MoQSession) closeOnCatalogChange(ctx context.Context, changed <-chan struct{}) {
	select {
	case <-ctx.Done():
	case <-changed:
		m.log.Info("catalog changed, closing session")
		if m.session != nil {
			_ = m.session.CloseWithError(wtErrCatalogChanged, "catalog changed")
		}
	}
}

// cancelSubscription ends the subscription of a track, if any, so that a
// repeated SUBSCRIBE replaces it instead of leaving its write loop behind.
func (m *MoQSession) cancelSubscription(trackName string) {
	m.mu.Lock()
	old := m.subscriptions[trackName]
	delete(m.subscriptions, trackName)
	m.mu.Unlock()
	if old != nil && old.cancel != nil {
		old.cancel()
	}
}

// fail closes the session after an error that is not confined to one
// stream, so the viewer sees the close and reconnects. An error caused by
// the subscription's own context ending, or by the session having closed,
// is no failure. Every write loop of a lost connection fails; only the
// first one is logged.
func (m *MoQSession) fail(ctx context.Context, op string, err error) {
	if ctx.Err() != nil || (m.session != nil && m.session.Context().Err() != nil) {
		return
	}
	m.failOnce.Do(func() {
		m.log.Warn("moq session failed", "op", op, "error", err)
		if m.session != nil {
			_ = m.session.CloseWithError(wtErrInternal, op+" failed")
		}
	})
}

// handleMediaSubscribe creates a track subscription and starts the write loop.
func (m *MoQSession) handleMediaSubscribe(ctx context.Context, sub moq.Subscribe, alias uint64, trackName string, mediaType string, audioIdx int) {
	m.cancelSubscription(trackName)
	subCtx, subCancel := context.WithCancel(ctx)

	trackSub := &moqTrackSub{
		requestID:       sub.RequestID,
		trackAlias:      alias,
		trackName:       trackName,
		audioTrackIndex: audioIdx,
		cancel:          subCancel,
	}

	switch mediaType {
	case "video":
		trackSub.writer = NewMoQWriter(alias, priorityVideo)
		trackSub.videoCh = make(chan *media.VideoFrame, media.VideoBufferSize)
		// Replay the full cached GOP into the channel before starting the write
		// loop. The client-side renderer skips to the latest decoded frame, so
		// this provides immediate decodable content at the live edge.
		if n := m.relay.ReplayFullGOPToChannel(trackSub.videoCh); n > 0 {
			m.log.Debug("replayed GOP into video channel", "frames", n)
		}
		go m.writeVideoLoop(subCtx, trackSub)

	case "audio":
		trackSub.writer = NewMoQWriter(alias, priorityAudio)
		trackSub.audioCh = make(chan *media.AudioFrame, media.AudioBufferSize)
		// Replay recent audio frames into the channel before starting the write
		// loop, pre-filling the client's audio buffer for immediate playback.
		if n := m.relay.ReplayAudioToChannel(audioIdx, trackSub.audioCh); n > 0 {
			m.log.Debug("replayed audio into channel", "track", trackName, "frames", n)
		}
		go m.writeAudioLoop(subCtx, trackSub)

	case "captions":
		trackSub.writer = NewMoQWriter(alias, priorityCaptions)
		trackSub.captionCh = make(chan *ccx.CaptionFrame, viewerCaptionBuffer)
		go m.writeCaptionLoop(subCtx, trackSub)
	}

	m.mu.Lock()
	m.subscriptions[trackName] = trackSub
	m.mu.Unlock()

	m.sendSubscribeOK(sub.RequestID, alias, moq.GroupOrderAscending, false, 0, 0)

	m.log.Debug("track subscribed",
		"track", trackName,
		"alias", alias,
		"requestID", sub.RequestID)
}

// handleUnsubscribe cancels a track subscription.
func (m *MoQSession) handleUnsubscribe(unsub moq.Unsubscribe) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, sub := range m.subscriptions {
		if sub.requestID == unsub.RequestID {
			if sub.cancel != nil {
				sub.cancel()
			}
			delete(m.subscriptions, name)
			m.log.Debug("track unsubscribed",
				"track", name,
				"requestID", unsub.RequestID)
			return
		}
	}
}

// sendSubscribeOK sends a SUBSCRIBE_OK on the control stream.
func (m *MoQSession) sendSubscribeOK(requestID, trackAlias uint64, groupOrder byte, contentExists bool, largestGroup, largestObj uint64) {
	sok := moq.SubscribeOK{
		RequestID:     requestID,
		TrackAlias:    trackAlias,
		Expires:       0,
		GroupOrder:    groupOrder,
		ContentExists: contentExists,
		LargestGroup:  largestGroup,
		LargestObj:    largestObj,
	}
	m.controlMu.Lock()
	defer m.controlMu.Unlock()
	if err := moq.WriteControlMsg(m.control, moq.MsgSubscribeOK, moq.SerializeSubscribeOK(sok)); err != nil {
		m.log.Warn("write SUBSCRIBE_OK failed", "error", err)
	}
}

// sendSubscribeError sends a SUBSCRIBE_ERROR on the control stream.
func (m *MoQSession) sendSubscribeError(requestID, errorCode uint64, reason string) {
	se := moq.SubscribeError{
		RequestID:    requestID,
		ErrorCode:    errorCode,
		ReasonPhrase: reason,
	}
	m.controlMu.Lock()
	defer m.controlMu.Unlock()
	if err := moq.WriteControlMsg(m.control, moq.MsgSubscribeError, moq.SerializeSubscribeError(se)); err != nil {
		m.log.Warn("write SUBSCRIBE_ERROR failed", "error", err)
	}
}

// --- Viewer interface implementation ---

// SendVideo dispatches a video frame to the video subscription if active.
func (m *MoQSession) SendVideo(frame *media.VideoFrame) {
	m.mu.RLock()
	sub := m.subscriptions["video"]
	m.mu.RUnlock()

	if sub == nil || sub.videoCh == nil {
		return
	}

	trySendVideo(frame, sub.videoCh, &m.damagedGroup, &m.videoSent, &m.videoDropped)
}

// SendAudio dispatches an audio frame to the matching audio subscription.
func (m *MoQSession) SendAudio(frame *media.AudioFrame) {
	trackName := fmt.Sprintf("audio%d", frame.TrackIndex)

	m.mu.RLock()
	sub := m.subscriptions[trackName]
	m.mu.RUnlock()

	if sub == nil || sub.audioCh == nil {
		return
	}

	select {
	case sub.audioCh <- frame:
		m.audioSent.Add(1)
	default:
		m.audioDropped.Add(1)
	}
}

// SendCaptions dispatches a caption frame to the caption subscription.
func (m *MoQSession) SendCaptions(frame *ccx.CaptionFrame) {
	m.mu.RLock()
	sub := m.subscriptions["captions"]
	m.mu.RUnlock()

	if sub == nil || sub.captionCh == nil {
		return
	}

	select {
	case sub.captionCh <- frame:
		m.captionSent.Add(1)
	default:
		m.captionDropped.Add(1)
	}
}

// Stats returns delivery metrics for this MoQ session.
func (m *MoQSession) Stats() ViewerStats {
	return ViewerStats{
		ID:             m.id,
		VideoSent:      m.videoSent.Load(),
		AudioSent:      m.audioSent.Load(),
		CaptionSent:    m.captionSent.Load(),
		VideoDropped:   m.videoDropped.Load(),
		AudioDropped:   m.audioDropped.Load(),
		CaptionDropped: m.captionDropped.Load(),
		BytesSent:      m.bytesSent.Load(),
		LastVideoTsMS:  m.lastVideoTsMS.Load(),
		LastAudioTsMS:  m.lastAudioTsMS.Load(),
	}
}

// --- Write loops ---

// writeVideoLoop writes each group on a stream of its own. An error on
// that stream ends the group, and the next keyframe opens a new one; any
// other error ends the session.
func (m *MoQSession) writeVideoLoop(ctx context.Context, sub *moqTrackSub) {
	var currentStream *webtransport.SendStream
	var currentGroupID uint32
	var wireGroupID uint32

	closeStream := func() {
		if currentStream != nil {
			currentStream.Close()
			currentStream = nil
		}
	}
	defer closeStream()

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-sub.videoCh:
			if !ok {
				return
			}

			if frame.IsKeyframe {
				closeStream()
				wireGroupID = nextWireGroup(wireGroupID, currentGroupID, frame.GroupID)
				currentGroupID = frame.GroupID

				t0 := time.Now()
				stream, err := openUniStream(ctx, m.session)
				if openDur := time.Since(t0); openDur > 50*time.Millisecond {
					m.log.Warn("video stream open slow",
						"duration_ms", openDur.Milliseconds(),
						"group", currentGroupID)
				}
				if err != nil {
					m.fail(ctx, "video stream open", err)
					return
				}

				tsMS := uint32(frame.PTS / 1000)
				if err := sub.writer.WriteStreamHeader(stream, TrackIDVideo, wireGroupID, tsMS); err != nil {
					stream.Close()
					if !streamOnly(err) {
						m.fail(ctx, "video header write", err)
						return
					}
					m.log.Debug("video group dropped", "group", currentGroupID, "error", err)
					continue
				}
				currentStream = stream
			}

			if currentStream == nil || !fitsVideoStream(frame, currentGroupID) {
				continue
			}

			t0 := time.Now()
			n, err := sub.writer.WriteVideoFrame(currentStream, frame)
			if writeDur := time.Since(t0); writeDur > 50*time.Millisecond {
				m.log.Warn("video frame write slow",
					"duration_ms", writeDur.Milliseconds(),
					"bytes", n,
					"keyframe", frame.IsKeyframe,
					"group", currentGroupID)
			}
			if err != nil {
				closeStream()
				if !streamOnly(err) {
					m.fail(ctx, "video frame write", err)
					return
				}
				m.log.Debug("video group dropped", "group", currentGroupID, "error", err)
				continue
			}
			m.bytesSent.Add(n)
			m.lastVideoTsMS.Store(frame.PTS / 1000)
		}
	}
}

// maxAudioStreamFailures is how many audio streams in a row may fail
// before the session is given up. A viewer that resets every stream would
// otherwise have one opened for each frame, about fifty a second.
const maxAudioStreamFailures = 8

// writeAudioLoop writes the track on one stream. An error on that stream
// ends it, and the next frame opens a new one; any other error, or
// maxAudioStreamFailures stream errors in a row, ends the session.
func (m *MoQSession) writeAudioLoop(ctx context.Context, sub *moqTrackSub) {
	var stream *webtransport.SendStream
	var groupID uint32
	failures := 0 // stream errors since the last frame written
	tooManyFailures := func() bool {
		failures++
		return failures >= maxAudioStreamFailures
	}
	defer func() {
		if stream != nil {
			stream.Close()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-sub.audioCh:
			if !ok {
				return
			}

			if stream == nil {
				var err error
				stream, err = openUniStream(ctx, m.session)
				if err != nil {
					m.fail(ctx, "audio stream open", err)
					return
				}

				trackID := AudioTrackID(sub.audioTrackIndex)
				tsMS := uint32(frame.PTS / 1000)
				err = sub.writer.WriteStreamHeader(stream, trackID, groupID, tsMS)
				groupID++
				if err != nil {
					stream.Close()
					stream = nil
					if !streamOnly(err) || tooManyFailures() {
						m.fail(ctx, "audio header write", err)
						return
					}
					continue
				}
			}

			tsMS := uint32(frame.PTS / 1000)
			n, err := sub.writer.WriteAudioFrame(stream, frame.Data, tsMS)
			if err != nil {
				stream.Close()
				stream = nil
				if !streamOnly(err) || tooManyFailures() {
					m.fail(ctx, "audio frame write", err)
					return
				}
				m.log.Debug("audio stream dropped", "track", sub.trackName, "error", err)
				continue
			}
			failures = 0
			m.bytesSent.Add(n)
			m.lastAudioTsMS.Store(int64(tsMS))
		}
	}
}

func (m *MoQSession) writeCaptionLoop(ctx context.Context, sub *moqTrackSub) {
	var groupID uint32

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-sub.captionCh:
			if !ok {
				return
			}

			stream, err := openUniStream(ctx, m.session)
			if err != nil {
				m.fail(ctx, "caption stream open", err)
				return
			}

			tsMS := uint32(frame.PTS / 1000)
			if err := sub.writer.WriteStreamHeader(stream, TrackIDCaptions, groupID, tsMS); err != nil {
				stream.Close()
				if !streamOnly(err) {
					m.fail(ctx, "caption write", err)
					return
				}
				continue
			}

			data := frame.Serialize()
			n, err := sub.writer.WriteDataObject(stream, data, tsMS)
			if err != nil {
				stream.Close()
				if !streamOnly(err) {
					m.fail(ctx, "caption write", err)
					return
				}
				continue
			}

			m.bytesSent.Add(n + sub.writer.StreamHeaderSize())
			groupID++
			stream.Close()
		}
	}
}

// handleStatsSubscribe sets up the stats track subscription and starts the write loop.
func (m *MoQSession) handleStatsSubscribe(ctx context.Context, sub moq.Subscribe, alias uint64) {
	m.cancelSubscription("stats")
	subCtx, subCancel := context.WithCancel(ctx)

	trackSub := &moqTrackSub{
		requestID:  sub.RequestID,
		trackAlias: alias,
		trackName:  "stats",
		writer:     NewMoQWriter(alias, priorityStats),
		cancel:     subCancel,
	}

	m.mu.Lock()
	m.subscriptions["stats"] = trackSub
	m.mu.Unlock()

	m.sendSubscribeOK(sub.RequestID, alias, moq.GroupOrderAscending, false, 0, 0)

	go m.writeStatsLoop(subCtx, trackSub)

	m.log.Debug("stats track subscribed",
		"alias", alias,
		"requestID", sub.RequestID)
}

// writeStatsLoop sends a StreamSnapshot as JSON every second on a new uni-stream,
// following the same pattern as writeCaptionLoop (one stream per update, incrementing groupID).
func (m *MoQSession) writeStatsLoop(ctx context.Context, sub *moqTrackSub) {
	ticker := time.NewTicker(statsInterval)
	defer ticker.Stop()

	var groupID uint32

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.closed.Load() {
				return
			}

			if m.statsProvider == nil {
				continue
			}
			provider := m.statsProvider(m.streamKey)
			if provider == nil {
				continue
			}

			viewerStats := m.Stats()
			data, err := json.Marshal(statsMessage{
				Type:        "stats",
				Stats:       provider.StreamSnapshot(),
				ViewerStats: &viewerStats,
			})
			if err != nil {
				continue
			}

			stream, err := openUniStream(ctx, m.session)
			if err != nil {
				m.fail(ctx, "stats stream open", err)
				return
			}

			tsMS := uint32(time.Now().UnixMilli())
			if err := sub.writer.WriteStreamHeader(stream, 0, groupID, tsMS); err != nil {
				stream.Close()
				if !streamOnly(err) {
					m.fail(ctx, "stats write", err)
					return
				}
				continue
			}

			n, err := sub.writer.WriteDataObject(stream, data, tsMS)
			if err != nil {
				stream.Close()
				if !streamOnly(err) {
					m.fail(ctx, "stats write", err)
					return
				}
				continue
			}

			m.bytesSent.Add(n + sub.writer.StreamHeaderSize())
			groupID++
			stream.Close()
		}
	}
}

// handleControlSubscribe sets up the control track subscription and starts the write loop.
// The control track delivers application-level JSON state updates (e.g., switcher control
// room state) to connected browsers. It is only available when ControlCh is configured
// in ServerConfig. Each viewer gets its own channel from the ControlBroadcaster.
func (m *MoQSession) handleControlSubscribe(ctx context.Context, sub moq.Subscribe, alias uint64) {
	if m.controlBroadcaster == nil {
		m.sendSubscribeError(sub.RequestID, 404, "control track not available")
		return
	}

	// Before Subscribe: the old subscription's cancel unsubscribes m.id.
	m.cancelSubscription("control")
	subCtx, subCancel := context.WithCancel(ctx)

	// Subscribe to the broadcaster to get a per-session channel.
	ch := m.controlBroadcaster.Subscribe(m.id)

	trackSub := &moqTrackSub{
		requestID:  sub.RequestID,
		trackAlias: alias,
		trackName:  "control",
		writer:     NewMoQWriter(alias, priorityControl),
		cancel: func() {
			subCancel()
			m.controlBroadcaster.Unsubscribe(m.id)
		},
	}

	m.mu.Lock()
	m.subscriptions["control"] = trackSub
	m.mu.Unlock()

	m.sendSubscribeOK(sub.RequestID, alias, moq.GroupOrderAscending, false, 0, 0)

	go m.writeControlLoop(subCtx, trackSub, ch)

	m.log.Debug("control track subscribed",
		"alias", alias,
		"requestID", sub.RequestID)
}

// writeControlLoop reads JSON state snapshots from the control channel and sends
// each as a MoQ group on a new uni-stream, following the same pattern as writeStatsLoop.
func (m *MoQSession) writeControlLoop(ctx context.Context, sub *moqTrackSub, ch <-chan []byte) {
	var groupID uint32

	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}

			if m.closed.Load() {
				return
			}

			stream, err := openUniStream(ctx, m.session)
			if err != nil {
				m.fail(ctx, "control stream open", err)
				return
			}

			tsMS := uint32(time.Now().UnixMilli())
			if err := sub.writer.WriteStreamHeader(stream, 0, groupID, tsMS); err != nil {
				stream.Close()
				if !streamOnly(err) {
					m.fail(ctx, "control write", err)
					return
				}
				continue
			}

			n, err := sub.writer.WriteDataObject(stream, data, tsMS)
			if err != nil {
				stream.Close()
				if !streamOnly(err) {
					m.fail(ctx, "control write", err)
					return
				}
				continue
			}

			m.bytesSent.Add(n + sub.writer.StreamHeaderSize())
			groupID++
			stream.Close()
		}
	}
}

// openUniStream opens a unidirectional stream on session. OpenUniStreamSync
// derives a cancellable context from the one it is given for each stream
// and never cancels it, so every stream opened on a long-lived ctx would
// stay attached to it until ctx ends. The context handed over here is
// cancelled once the open returns, which drops that child; the stream does
// not use the context after it is open.
func openUniStream(ctx context.Context, session *webtransport.Session) (*webtransport.SendStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return session.OpenUniStreamSync(ctx)
}
