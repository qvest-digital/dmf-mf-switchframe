package output

import (
	"fmt"
	"time"

	"github.com/zsiec/switchframe/server/internal/mpegts"
)

// ptrTo returns a pointer to v. Used for atomic.Pointer[T].Store().
func ptrTo[T any](v T) *T { return &v }

const (
	defaultSRTLatency = 120 // ms

	// SRT passphrase length limits (libsrt requires 10-79 characters).
	srtPassphraseMinLen = 10
	srtPassphraseMaxLen = 79
)

// srtKeyLength maps a DestinationConfig.Encryption value to the srtgo AES
// key length in bytes. An empty string returns 0 (srtgo defaults to AES-128
// when a passphrase is set). Unknown values return an error.
func srtKeyLength(encryption string) (int, error) {
	switch encryption {
	case "":
		return 0, nil
	case "aes-128":
		return 16, nil
	case "aes-192":
		return 24, nil
	case "aes-256":
		return 32, nil
	default:
		return 0, fmt.Errorf("unsupported encryption %q (use aes-128, aes-192, or aes-256)", encryption)
	}
}

// validateEncryptionConfig checks the Encryption/Passphrase fields of a
// destination config. SRT encryption requires a 10-79 character passphrase;
// RTMP destinations do not support these fields at all. Rejecting bad
// configs here prevents silently streaming an "encrypted" destination in
// cleartext.
func validateEncryptionConfig(config DestinationConfig) error {
	if config.Type == "rtmp" {
		if config.Encryption != "" || config.Passphrase != "" {
			return fmt.Errorf("encryption/passphrase are not supported for rtmp destinations")
		}
		return nil
	}
	if _, err := srtKeyLength(config.Encryption); err != nil {
		return err
	}
	if config.Encryption != "" && config.Passphrase == "" {
		return fmt.Errorf("passphrase is required when encryption is set")
	}
	if config.Passphrase != "" {
		if n := len(config.Passphrase); n < srtPassphraseMinLen || n > srtPassphraseMaxLen {
			return fmt.Errorf("passphrase must be %d-%d characters (got %d)",
				srtPassphraseMinLen, srtPassphraseMaxLen, n)
		}
	}
	return nil
}

// srtConn abstracts SRT connection operations for testing without real network I/O.
// Both SRTCaller and SRTListener use this interface.
type srtConn interface {
	Write(data []byte) (int, error)
	Close()
}

// SRTStatter is implemented by connections that can report SRT-level statistics.
// The srtConn interface intentionally does not include this — callers use type
// assertion to check for stats support, matching the pattern in srt/source.go.
type SRTStatter interface {
	SRTStats(clear bool) SRTConnStats
}

// SRTConnStats holds raw SRT connection statistics for send-side polling.
// This is a local type to avoid importing srtgo into non-wire files.
type SRTConnStats struct {
	RTT          time.Duration
	RTTVar       time.Duration
	MbpsSendRate float64
	SendLossRate float64
	SendPackets  uint64
	SendLoss     uint64
	SendDropped  uint64
	Retransmits  uint64
	MsSndBuf     time.Duration
	SendBufSize  int
	FlightSize   int
}

// chunkedConn wraps an srtConn and splits writes into chunks of at most
// mpegts.LiveMaxPayload bytes. SRT live mode rejects writes larger than 1316
// bytes, but the MPEG-TS muxer flushes full frames which can be much larger.
type chunkedConn struct {
	inner srtConn
}

func (c *chunkedConn) Write(data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		chunk := data
		if len(chunk) > mpegts.LiveMaxPayload {
			chunk = data[:mpegts.LiveMaxPayload]
		}
		n, err := c.inner.Write(chunk)
		total += n
		if err != nil {
			return total, err
		}
		data = data[len(chunk):]
	}
	return total, nil
}

func (c *chunkedConn) Close() {
	c.inner.Close()
}

// SRTStats delegates to the inner connection if it implements SRTStatter.
func (c *chunkedConn) SRTStats(clear bool) SRTConnStats {
	if s, ok := c.inner.(SRTStatter); ok {
		return s.SRTStats(clear)
	}
	return SRTConnStats{}
}
