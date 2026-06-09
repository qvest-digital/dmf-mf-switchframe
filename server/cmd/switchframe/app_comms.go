package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"

	"github.com/zsiec/switchframe/server/comms"
	"github.com/zsiec/switchframe/server/operator"
)

const (
	commsMsgAudio   = 0x01
	commsMsgControl = 0x02
)

// commsMuteCmd is the JSON payload for a control mute command.
type commsMuteCmd struct {
	Action string `json:"action"`
	Muted  bool   `json:"muted"`
}

// handleCommsBidiStream is the OnBidirectionalStream callback for Prism.
// It reads a handshake message containing the operator ID, then delegates
// to handleCommsStream for the audio read/write loop.
func (a *App) handleCommsBidiStream(_ string, stream io.ReadWriteCloser) {
	defer func() { _ = stream.Close() }()

	if a.commsMgr == nil {
		return
	}

	// The first message on the stream is a control message containing the
	// operator ID as a JSON handshake: {"action":"hello","operatorId":"..."}
	header := make([]byte, 3)
	if _, err := io.ReadFull(stream, header); err != nil {
		return
	}
	if header[0] != commsMsgControl {
		return
	}
	payloadLen := binary.BigEndian.Uint16(header[1:3])
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(stream, payload); err != nil {
		return
	}

	var hello struct {
		Action     string `json:"action"`
		OperatorID string `json:"operatorId"`
		Token      string `json:"token"`
	}
	if err := json.Unmarshal(payload, &hello); err != nil || hello.Action != "hello" || hello.OperatorID == "" {
		return
	}

	// Authenticate the handshake. Prism viewer sessions carry no operator
	// auth, so without this check any client that can reach the server could
	// bind a stream to an arbitrary operator: receive their N-1 mix
	// (eavesdrop), inject audio as them, toggle their mute, and kick them
	// from comms on disconnect via the deferred Leave below.
	if !a.authorizeCommsStream(hello.OperatorID, hello.Token) {
		slog.Warn("comms stream rejected: unauthorized hello", "operator", hello.OperatorID)
		return
	}

	// Register this stream as the operator's active comms stream, superseding
	// (closing) any lingering previous stream from before a reconnect.
	a.registerCommsStream(hello.OperatorID, stream)

	// Auto-leave when the stream closes (browser disconnect, refresh, etc.)
	// so the participant doesn't remain as a ghost in the comms session —
	// but only if this stream is still the operator's CURRENT stream. A
	// stale pre-reconnect stream that errors out late (e.g. QUIC idle
	// timeout) must not tear down the fresh session the reconnected
	// operator is actively using.
	defer func() {
		if a.unregisterCommsStream(hello.OperatorID, stream) {
			a.commsMgr.Leave(hello.OperatorID)
		}
	}()

	a.handleCommsStream(hello.OperatorID, stream, stream)
}

// authorizeCommsStream validates that the comms stream handshake token is
// allowed to act as operatorID. It mirrors the REST path's
// authorizeCommsTarget (control/api_comms.go): the token must resolve to a
// registered operator who is either the target operator or a director. When
// no operator store is wired, the claimed ID is trusted for backward
// compatibility, mirroring the rest of the API.
func (a *App) authorizeCommsStream(operatorID, token string) bool {
	if a.operatorStore == nil {
		return true
	}
	caller, err := a.operatorStore.GetByToken(token)
	if err != nil {
		return false
	}
	return caller.ID == operatorID || caller.Role == operator.RoleDirector
}

// registerCommsStream records stream as operatorID's active comms stream,
// closing any previous stream for the same operator. Closing the stale
// stream unblocks its read loop so its goroutines unwind promptly; otherwise
// both streams' write goroutines would compete for the participant's single
// send channel, splitting the encoded mix between a dead and a live stream.
func (a *App) registerCommsStream(operatorID string, stream io.ReadWriteCloser) {
	a.commsStreamsMu.Lock()
	if a.commsStreams == nil {
		a.commsStreams = make(map[string]io.ReadWriteCloser)
	}
	old := a.commsStreams[operatorID]
	a.commsStreams[operatorID] = stream
	a.commsStreamsMu.Unlock()

	if old != nil && old != stream {
		slog.Info("comms stream superseded by reconnect, closing stale stream", "operator", operatorID)
		_ = old.Close()
	}
}

// unregisterCommsStream removes stream from the active-stream map if it is
// still the operator's current stream, and reports whether it was. Only the
// current stream's teardown may Leave() the comms session.
func (a *App) unregisterCommsStream(operatorID string, stream io.ReadWriteCloser) bool {
	a.commsStreamsMu.Lock()
	defer a.commsStreamsMu.Unlock()
	if a.commsStreams[operatorID] != stream {
		return false
	}
	delete(a.commsStreams, operatorID)
	return true
}

// handleCommsStream processes a bidirectional WebTransport stream for operator
// comms audio. It reads wire-protocol framed messages from the readable side
// and writes mixed audio back on the writable side.
//
// Wire protocol (both directions):
//
//	[1 byte type][2 bytes BE length][payload]
func (a *App) handleCommsStream(operatorID string, readable io.Reader, writable io.Writer) {
	if a.commsMgr == nil {
		return
	}

	p, ok := a.commsMgr.GetParticipant(operatorID)
	if !ok {
		return
	}

	log := slog.Default().With("component", "comms-stream", "operator", operatorID)
	log.Info("comms stream opened")

	// done signals the write goroutine to stop when the read loop exits.
	//
	// close(done) alone does NOT interrupt a write that is already blocked
	// inside writable.Write — once the goroutine leaves the select to perform
	// a Write, transport flow-control backpressure (a half-open peer that
	// reads control but never drains audio) can pin it there until the stream
	// is torn down. So in addition to signalling done we close the writable
	// side on read-loop exit when it implements io.Closer: that forces the
	// in-flight Write to return an error and unwinds the goroutine promptly,
	// rather than waiting for the outer stream.Close() in
	// handleCommsBidiStream (which only runs after this function returns).
	done := make(chan struct{})
	defer close(done)
	if wc, ok := writable.(io.Closer); ok {
		defer func() { _ = wc.Close() }()
	}

	// Write goroutine: read from participant's send channel and write
	// wire-protocol framed audio back to the client.
	go func() {
		header := make([]byte, 3)
		for {
			select {
			case <-done:
				return
			case packet, ok := <-p.SendCh():
				if !ok {
					return
				}
				if len(packet) > 0xFFFF {
					continue
				}
				header[0] = commsMsgAudio
				binary.BigEndian.PutUint16(header[1:3], uint16(len(packet)))
				if _, err := writable.Write(header); err != nil {
					log.Debug("comms stream write header error", "err", err)
					return
				}
				if _, err := writable.Write(packet); err != nil {
					log.Debug("comms stream write payload error", "err", err)
					return
				}
			}
		}
	}()

	// Read loop: read wire-protocol framed messages from the client.
	header := make([]byte, 3)
	for {
		if _, err := io.ReadFull(readable, header); err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				log.Debug("comms stream read header error", "err", err)
			}
			break
		}

		msgType := header[0]
		payloadLen := binary.BigEndian.Uint16(header[1:3])

		// Guard against oversized payloads (Opus frames are typically <200 bytes).
		if payloadLen > 4096 {
			log.Debug("comms stream payload too large", "len", payloadLen)
			break
		}

		payload := make([]byte, payloadLen)
		if payloadLen > 0 {
			if _, err := io.ReadFull(readable, payload); err != nil {
				if err != io.EOF && err != io.ErrUnexpectedEOF {
					log.Debug("comms stream read payload error", "err", err)
				}
				break
			}
		}

		switch msgType {
		case commsMsgAudio:
			if err := a.commsMgr.IngestAudio(operatorID, payload); err != nil {
				log.Debug("comms audio ingest error", "err", err)
			}

		case commsMsgControl:
			var cmd commsMuteCmd
			if err := json.Unmarshal(payload, &cmd); err != nil {
				log.Debug("comms control parse error", "err", err)
				continue
			}
			if cmd.Action == "mute" {
				if err := a.commsMgr.SetMuted(operatorID, cmd.Muted); err != nil {
					log.Debug("comms mute error", "err", err)
				}
			}

		default:
			log.Debug("comms unknown message type", "type", msgType)
		}
	}

	log.Info("comms stream closed")
}

// handleCommsStreamForTest is a test helper that exposes handleCommsStream
// with an explicit comms.Manager, avoiding the need to construct a full App.
func handleCommsStreamForTest(mgr *comms.Manager, operatorID string, readable io.Reader, writable io.Writer) {
	app := &App{commsMgr: mgr}
	app.handleCommsStream(operatorID, readable, writable)
}
