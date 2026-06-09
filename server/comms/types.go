package comms

import "github.com/zsiec/switchframe/server/internal"

const (
	SampleRate = 48000
	Channels   = 1
	FrameSize  = 960 // 20ms at 48kHz

	// MaxFrameSize is the largest Opus frame we accept on decode: 120ms at
	// 48kHz mono (5760 samples), the maximum frame duration Opus permits.
	// The decode scratch buffer is sized to this so a client that negotiates
	// a 40/60/120ms frame still decodes (opus_decode returns
	// OPUS_BUFFER_TOO_SMALL when the output capacity is below the frame's
	// sample count). Decoded frames longer than FrameSize are split into
	// consecutive 20ms FrameSize blocks for the mix path; see
	// participant.decodeAudio.
	MaxFrameSize = 5760

	MaxParticipants = 6

	// Wire protocol message types
	MsgAudio   byte = 0x01
	MsgControl byte = 0x02
)

type State = internal.CommsState
type ParticipantInfo = internal.CommsParticipant
