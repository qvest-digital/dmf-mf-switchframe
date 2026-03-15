package demo

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	srtgo "github.com/zsiec/srtgo"
)

const (
	// tsPacketLen is the fixed MPEG-TS packet size.
	tsPacketLen = 188

	// srtChunkSize is 7 TS packets (1316 bytes), the standard SRT payload size.
	srtChunkSize = tsPacketLen * 7

	// reconnectDelay is the pause before retrying a failed SRT connection.
	reconnectDelay = time.Second
)

// StartSRTSources pushes test clips over SRT to a local listener.
// Each clip gets its own SRT connection with a unique streamid.
// Blocks until ctx is cancelled.
func StartSRTSources(ctx context.Context, addr string, clips []string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}

	for i, clip := range clips {
		base := filepath.Base(clip)
		ext := filepath.Ext(base)
		name := base[:len(base)-len(ext)]
		streamID := "live/" + name

		log.Info("srt-push: starting", "clip", clip, "streamID", streamID, "index", i)
		go pushFile(ctx, addr, clip, streamID, log)
	}

	// Block until context is done.
	<-ctx.Done()
}

// pushFile connects to the SRT listener and streams the file in a loop.
// On connection failure it retries with a delay. On write failure it
// reconnects. Runs until ctx is cancelled.
//
// NOTE: Each pushFile goroutine gets its own copy of the file data via
// os.ReadFile (called inside streamFileLoop). The data buffer is mutated
// in-place by addTimestampOffset at loop boundaries. Do NOT share this
// buffer between goroutines.
func pushFile(ctx context.Context, addr, filePath, streamID string, log *slog.Logger) {
	log = log.With("streamID", streamID, "file", filepath.Base(filePath))

	for {
		if ctx.Err() != nil {
			return
		}

		cfg := srtgo.DefaultConfig()
		cfg.StreamID = streamID

		conn, err := srtgo.Dial(addr, cfg)
		if err != nil {
			log.Warn("srt-push: dial failed, retrying", "err", err)
			sleepCtx(ctx, reconnectDelay)
			continue
		}

		log.Info("srt-push: connected")

		err = streamFileLoop(ctx, conn, filePath, log)
		_ = conn.Close()

		if ctx.Err() != nil {
			return
		}
		log.Warn("srt-push: disconnected, reconnecting", "err", err)
		sleepCtx(ctx, reconnectDelay)
	}
}

// streamFileLoop reads the TS file into memory and streams it in a loop
// using CBR pacing derived from the file's PTS duration.
//
// Pacing strategy: the file's duration is estimated by scanning for the
// first and last video PTS. Each loop iteration anchors byte offset 0
// to the current wall clock time and linearly interpolates send times
// for each chunk. This is self-correcting: if one sleep overshoots,
// the next compensates. No drift over hours.
//
// At each loop boundary, PTS/DTS/PCR values in the byte stream are
// advanced by the file duration to keep timestamps monotonically
// increasing for the downstream demuxer.
func streamFileLoop(ctx context.Context, conn *srtgo.Conn, filePath string, log *slog.Logger) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	if len(data) < tsPacketLen {
		log.Warn("srt-push: file too small", "bytes", len(data))
		return nil
	}

	fileDuration := estimateFileDuration(data)

	// Pre-scan timestamp locations for patching at loop boundaries.
	tsEntries, firstPTS, lastPTS := scanTimestamps(data)
	var loopPTSDelta int64
	if firstPTS >= 0 && lastPTS > firstPTS {
		// Add one frame (~3750 ticks at 24fps) to avoid collision at boundary.
		loopPTSDelta = lastPTS - firstPTS + 3750
	}

	log.Info("srt-push: streaming",
		"duration", fileDuration.Round(time.Millisecond),
		"bytes", len(data),
		"tsLocations", len(tsEntries),
		"loopDelta", time.Duration(loopPTSDelta)*time.Second/90000,
	)

	// globalStart tracks the absolute start for continuous pacing across loops.
	globalStart := time.Now()
	var totalBytesSent int64

	for loop := 1; ; loop++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if loop > 1 {
			// Advance all PTS/DTS/PCR by one loop duration so timestamps
			// remain monotonically increasing across the seam.
			//
			// Trade-off: this patches ALL timestamp locations in the file
			// buffer at once, which can take up to a few milliseconds for
			// large files (e.g., ~1ms for 900 entries). An incremental
			// per-chunk approach would amortize this cost but adds
			// complexity (binary search per chunk, extra bookkeeping).
			// Since the stall is brief (< 5ms) and happens only once per
			// loop (~10-15s), it's acceptable for a demo pusher. SRT's
			// TSBPD buffer absorbs the jitter.
			if loopPTSDelta > 0 && len(tsEntries) > 0 {
				addTimestampOffset(data, tsEntries, loopPTSDelta)
			}
			log.Debug("srt-push: loop", "loop", loop)
		}

		for offset := 0; offset < len(data); {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			end := offset + srtChunkSize
			if end > len(data) {
				end = len(data)
			}
			chunk := data[offset:end]

			// Pace: compute how far ahead of schedule we are based on
			// total bytes sent vs. expected time at the file's bitrate.
			//
			// Limitation: byte-rate pacing assumes roughly constant
			// bitrate (CBR). For VBR content, I-frames will be paced
			// slightly too slowly and P-frames slightly too fast.
			// PTS-based pacing would be more accurate but requires
			// real-time PES header parsing. SRT's TSBPD buffer
			// smooths out the burstiness, making this acceptable
			// for demo use.
			targetByteTime := float64(totalBytesSent) / (float64(len(data)) / fileDuration.Seconds())
			elapsed := time.Since(globalStart).Seconds()
			if targetByteTime > elapsed {
				sleepCtx(ctx, time.Duration((targetByteTime-elapsed)*float64(time.Second)))
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}

			_, err := conn.Write(chunk)
			if err != nil {
				return err
			}

			totalBytesSent += int64(end - offset)
			offset = end
		}
	}
}

// pacingTarget computes the wall-clock time at which a frame with the
// given PTS should be sent, relative to an anchor (time, PTS) pair.
// The PTS values are in 90kHz MPEG-TS clock ticks.
func pacingTarget(anchor time.Time, anchorPTS, currentPTS int64) time.Time {
	deltaTicks := currentPTS - anchorPTS
	deltaTime := time.Duration(deltaTicks) * time.Second / 90000
	return anchor.Add(deltaTime)
}

// estimateFileDuration scans the TS data for the first and last video
// PTS and returns the delta as a time.Duration. Falls back to a
// bitrate-based estimate if PTS scanning yields nothing useful.
func estimateFileDuration(data []byte) time.Duration {
	first, last := scanFirstLastPTS(data)
	if first >= 0 && last > first {
		deltaTicks := last - first
		// Add one frame duration (~3750 ticks at 24fps) since last PTS
		// marks the start of the last frame, not the end.
		deltaTicks += 3750
		return time.Duration(deltaTicks) * time.Second / 90000
	}

	// Fallback: assume 5 Mbps and derive duration from file size.
	const assumedBitrate = 5_000_000 // bits per second
	if len(data) > 0 {
		return time.Duration(float64(len(data)) * 8 / assumedBitrate * float64(time.Second))
	}
	return 10 * time.Second // last resort
}

// scanFirstLastPTS walks the TS byte stream and returns the first and last
// video PTS values in 90kHz ticks. Returns (-1, -1) if no video PTS found.
func scanFirstLastPTS(data []byte) (firstPTS, lastPTS int64) {
	firstPTS = -1
	lastPTS = -1

	for off := 0; off+tsPacketLen <= len(data); off += tsPacketLen {
		pkt := data[off : off+tsPacketLen]
		if pkt[0] != 0x47 {
			continue
		}

		// Only interested in PUSI packets with payload.
		pusi := pkt[1]&0x40 != 0
		hasPayload := pkt[3]&0x10 != 0
		hasAdapt := pkt[3]&0x20 != 0
		if !pusi || !hasPayload {
			continue
		}

		payloadOff := 4
		if hasAdapt && payloadOff < tsPacketLen {
			afLen := int(pkt[payloadOff])
			payloadOff += 1 + afLen
		}

		if payloadOff+14 > tsPacketLen {
			continue
		}

		payload := pkt[payloadOff:]
		// PES start code prefix.
		if len(payload) < 14 || payload[0] != 0 || payload[1] != 0 || payload[2] != 1 {
			continue
		}

		streamID := payload[3]
		// Video stream IDs: 0xE0-0xEF.
		if streamID < 0xE0 || streamID > 0xEF {
			continue
		}

		// Check PTS present flag.
		if len(payload) < 9 {
			continue
		}
		flags := payload[7]
		hasPTS := flags&0x80 != 0
		if !hasPTS || len(payload) < 14 {
			continue
		}

		pts := decodePTS(payload[9:])
		if firstPTS < 0 || pts < firstPTS {
			firstPTS = pts
		}
		if pts > lastPTS {
			lastPTS = pts
		}
	}

	return firstPTS, lastPTS
}

// --- TS timestamp scanning and patching (adapted from Prism's srt-push/tspatch.go) ---

// ptsEntry records a byte offset in the TS data where a PTS, DTS, or PCR
// value lives. Used for patching timestamps at loop boundaries.
type ptsEntry struct {
	offset int
	isPCR  bool // true = 6-byte PCR; false = 5-byte PTS/DTS
}

// scanTimestamps walks the TS data and returns every PTS, DTS, and PCR
// byte location, plus the first and last video PTS values.
func scanTimestamps(data []byte) (entries []ptsEntry, firstPTS, lastPTS int64) {
	firstPTS = -1

	for off := 0; off+tsPacketLen <= len(data); off += tsPacketLen {
		pkt := data[off : off+tsPacketLen]
		if pkt[0] != 0x47 {
			continue
		}

		hasAdapt := pkt[3]&0x20 != 0
		hasPayload := pkt[3]&0x10 != 0

		payloadOff := 4

		// Adaptation field: check for PCR.
		if hasAdapt && payloadOff < tsPacketLen {
			afLen := int(pkt[payloadOff])
			if afLen > 0 && payloadOff+1 < tsPacketLen {
				afFlags := pkt[payloadOff+1]
				if afFlags&0x10 != 0 && afLen >= 7 { // PCR flag
					entries = append(entries, ptsEntry{offset: off + payloadOff + 2, isPCR: true})
				}
			}
			payloadOff += 1 + afLen
		}

		// PES header: PTS/DTS on PUSI packets.
		pusi := pkt[1]&0x40 != 0
		if !pusi || !hasPayload || payloadOff >= tsPacketLen {
			continue
		}

		payload := pkt[payloadOff:]
		if len(payload) < 14 || payload[0] != 0 || payload[1] != 0 || payload[2] != 1 {
			continue
		}

		streamID := payload[3]
		isMedia := (streamID >= 0xC0 && streamID <= 0xDF) || (streamID >= 0xE0 && streamID <= 0xEF)
		if !isMedia {
			continue
		}

		if len(payload) < 9 {
			continue
		}
		flags := payload[7]
		hasPTS := flags&0x80 != 0
		hasDTS := flags&0x40 != 0

		if hasPTS && len(payload) >= 14 {
			absOff := off + payloadOff + 9
			entries = append(entries, ptsEntry{offset: absOff, isPCR: false})

			isVideo := streamID >= 0xE0 && streamID <= 0xEF
			if isVideo {
				pts := decodePTS(data[absOff:])
				if firstPTS < 0 || pts < firstPTS {
					firstPTS = pts
				}
				if pts > lastPTS {
					lastPTS = pts
				}
			}
		}
		if hasDTS && len(payload) >= 19 {
			absOff := off + payloadOff + 14
			entries = append(entries, ptsEntry{offset: absOff, isPCR: false})
		}
	}

	return entries, firstPTS, lastPTS
}

// addTimestampOffset advances every recorded PTS/DTS/PCR location by delta
// (in 90kHz ticks). Called once per loop iteration.
func addTimestampOffset(data []byte, entries []ptsEntry, delta int64) {
	for _, e := range entries {
		if e.isPCR {
			pcr := decodePCR(data[e.offset:])
			encodePCR(data[e.offset:], pcr+delta)
		} else {
			pts := decodePTS(data[e.offset:])
			encodePTS(data[e.offset:], pts+delta)
		}
	}
}

// decodePTS extracts a 33-bit PTS/DTS from the 5-byte PES timestamp encoding.
func decodePTS(b []byte) int64 {
	return int64(b[0]>>1&0x07)<<30 |
		int64(b[1])<<22 |
		int64(b[2]>>1&0x7F)<<15 |
		int64(b[3])<<7 |
		int64(b[4]>>1&0x7F)
}

// encodePTS writes a 33-bit PTS/DTS into the 5-byte PES timestamp encoding,
// preserving marker bits and prefix nibble.
func encodePTS(b []byte, pts int64) {
	prefix := b[0] & 0xF0
	b[0] = prefix | byte((pts>>29)&0x0E) | 0x01
	b[1] = byte(pts >> 22)
	b[2] = byte((pts>>14)&0xFE) | 0x01
	b[3] = byte(pts >> 7)
	b[4] = byte((pts<<1)&0xFE) | 0x01
}

// decodePCR extracts a 33-bit PCR base (90kHz) from the 6-byte adaptation field.
func decodePCR(b []byte) int64 {
	return int64(b[0])<<25 |
		int64(b[1])<<17 |
		int64(b[2])<<9 |
		int64(b[3])<<1 |
		int64(b[4]>>7)
}

// encodePCR writes a 33-bit PCR base into the 6-byte encoding, preserving
// the 9-bit extension and reserved bits.
func encodePCR(b []byte, base int64) {
	ext := uint16(b[4]&0x01)<<8 | uint16(b[5])
	b[0] = byte(base >> 25)
	b[1] = byte(base >> 17)
	b[2] = byte(base >> 9)
	b[3] = byte(base >> 1)
	b[4] = byte((base&1)<<7) | 0x7E | byte(ext>>8)
	b[5] = byte(ext)
}
