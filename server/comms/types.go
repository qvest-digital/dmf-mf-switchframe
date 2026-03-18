package comms

import "github.com/zsiec/switchframe/server/internal"

const (
	SampleRate      = 48000
	Channels        = 1
	FrameSize       = 960 // 20ms at 48kHz
	MaxParticipants = 6

	// Wire protocol message types
	MsgAudio   byte = 0x01
	MsgControl byte = 0x02
)

type State = internal.CommsState
type ParticipantInfo = internal.CommsParticipant
