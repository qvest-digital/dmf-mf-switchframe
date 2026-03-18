package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"

	"github.com/zsiec/switchframe/server/comms"
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
	}
	if err := json.Unmarshal(payload, &hello); err != nil || hello.Action != "hello" || hello.OperatorID == "" {
		return
	}

	// Auto-leave when the stream closes (browser disconnect, refresh, etc.)
	// so the participant doesn't remain as a ghost in the comms session.
	defer a.commsMgr.Leave(hello.OperatorID)

	a.handleCommsStream(hello.OperatorID, stream, stream)
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
	done := make(chan struct{})
	defer close(done)

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
