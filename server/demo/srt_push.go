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
	}
}

// accessUnit represents a video-frame-aligned group of TS packets.
// Contains one video frame plus all audio/data packets until the next
// video frame. A real encoder sends all packets for one frame as a
// burst when the frame is produced.
type accessUnit struct {
	pts   int64 // video PES PTS in 90kHz ticks (-1 if no PTS found)
	start int   // byte offset in file data
	end   int   // byte offset end (exclusive)
}

// streamFileLoop reads the TS file into memory and streams it in a loop
// using PTS-based burst pacing that matches real encoder behavior.
//
// Pacing strategy: the file is pre-parsed into video-frame-aligned
// access units. Each unit contains one video frame + associated audio.
// At each frame's PTS time, all TS packets are burst-sent at once.
// This matches how real encoders (OBS, FFmpeg) operate: produce a
// frame, write all packets, wait for next frame time.
//
// Previous approach spread packets evenly within each frame period
// using per-packet sleeps (~0.75ms each). macOS timer resolution is
// ~1-4ms, so accumulated overshoot was 100-170ms per frame.
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

	// Pre-parse into access units for PTS-based pacing.
	units := parseAccessUnits(data)
	if len(units) == 0 {
		log.Warn("srt-push: no access units found")
		return nil
	}

	// Pre-scan timestamp locations for patching at loop boundaries.
	tsEntries, firstPTS, lastPTS := scanTimestamps(data)
	var loopPTSDelta int64
	if firstPTS >= 0 && lastPTS > firstPTS {
		loopPTSDelta = lastPTS - firstPTS + 3750
	}

	// Find the first valid PTS for anchoring.
	var anchorPTS int64 = -1
	for _, u := range units {
		if u.pts >= 0 {
			anchorPTS = u.pts
			break
		}
	}
	if anchorPTS < 0 {
		anchorPTS = 0
	}

	fileDuration := estimateFileDuration(data)

	log.Info("srt-push: streaming",
		"duration", fileDuration.Round(time.Millisecond),
		"bytes", len(data),
		"accessUnits", len(units),
		"tsLocations", len(tsEntries),
		"loopDelta", time.Duration(loopPTSDelta)*time.Second/90000,
	)

	anchorWall := time.Now()
	var ptsOffset int64

	for loop := 1; ; loop++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if loop > 1 {
			if loopPTSDelta > 0 && len(tsEntries) > 0 {
				addTimestampOffset(data, tsEntries, loopPTSDelta)
			}
			ptsOffset += loopPTSDelta
		}

		for _, unit := range units {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			// Wait until this access unit's PTS time, then send all its
			// packets as a burst. This matches real encoder behavior: the
			// encoder produces a frame, outputs all TS packets immediately,
			// then waits until the next frame time.
			//
			// Previous approach spread packets within each frame using
			// per-packet sleeps (~0.75ms each). macOS timer resolution is
			// ~1-4ms, so accumulated overshoot was 14-179ms per frame,
			// directly causing the 100-170ms broadcast gaps.
			if unit.pts >= 0 {
				sendPTS := unit.pts + ptsOffset
				target := pacingTarget(anchorWall, anchorPTS, sendPTS)
				now := time.Now()
				if target.After(now) {
					sleepCtx(ctx, target.Sub(now))
					if ctx.Err() != nil {
						return ctx.Err()
					}
				}
			}

			// Burst-send all TS packets in this access unit.
			for off := unit.start; off < unit.end; {
				end := off + srtChunkSize
				if end > unit.end {
					end = unit.end
				}
				_, err := conn.Write(data[off:end])
				if err != nil {
					return err
				}
				off = end
			}
		}
	}
}

// parseAccessUnits groups TS packets into video-frame-aligned access units.
// Each access unit starts at a video PES start and includes ALL packets
// (video, audio, PAT, PMT) until the next video PES start. This matches
// how a real encoder outputs data: one complete video frame + associated
// audio as a burst.
//
// If the file starts with non-video packets (audio, PAT/PMT), they are
// included in the first video access unit.
func parseAccessUnits(data []byte) []accessUnit {
	// First pass: find the video PID by looking for video PES stream IDs.
	videoPID := findVideoPID(data)
	if videoPID < 0 {
		// No video found — fall back to PES-start-based grouping.
		return parseAccessUnitsByPES(data)
	}

	// Second pass: group packets by video PES boundaries.
	var units []accessUnit
	var current *accessUnit

	for offset := 0; offset+tsPacketLen <= len(data); offset += tsPacketLen {
		pkt := data[offset : offset+tsPacketLen]
		if pkt[0] != 0x47 {
			continue
		}

		pid := int(pkt[1]&0x1F)<<8 | int(pkt[2])
		pusi := (pkt[1] & 0x40) != 0

		// New video PES start → new access unit.
		if pid == videoPID && pusi {
			pts := extractPESDTS(pkt)

			if current != nil {
				current.end = offset
			}
			units = append(units, accessUnit{
				pts:   pts,
				start: offset,
				end:   offset + tsPacketLen,
			})
			current = &units[len(units)-1]
		} else if current != nil {
			// Any other packet (audio, PAT, PMT, continuation) → extend.
			current.end = offset + tsPacketLen
		} else {
			// Packets before first video PES — create a preamble unit.
			// These will be merged into the first video unit below.
			units = append(units, accessUnit{
				pts:   -1,
				start: offset,
				end:   offset + tsPacketLen,
			})
			current = &units[len(units)-1]
		}
	}

	// Merge any preamble (pts=-1) into the first video unit.
	if len(units) >= 2 && units[0].pts < 0 && units[1].pts >= 0 {
		units[1].start = units[0].start
		units = units[1:]
	}

	return units
}

// findVideoPID returns the PID carrying the video elementary stream,
// or -1 if no video stream is found.
func findVideoPID(data []byte) int {
	for off := 0; off+tsPacketLen <= len(data); off += tsPacketLen {
		pkt := data[off : off+tsPacketLen]
		if pkt[0] != 0x47 {
			continue
		}
		pusi := (pkt[1] & 0x40) != 0
		pid := int(pkt[1]&0x1F)<<8 | int(pkt[2])
		if !pusi || pid == 0 || pid == 0x1FFF {
			continue
		}
		// Check for PES with video stream ID (0xE0-0xEF).
		hasPayload := pkt[3]&0x10 != 0
		hasAdapt := pkt[3]&0x20 != 0
		if !hasPayload {
			continue
		}
		payloadOff := 4
		if hasAdapt {
			if payloadOff >= tsPacketLen {
				continue
			}
			afLen := int(pkt[payloadOff])
			payloadOff += 1 + afLen
		}
		if payloadOff+4 > tsPacketLen {
			continue
		}
		payload := pkt[payloadOff:]
		if payload[0] == 0 && payload[1] == 0 && payload[2] == 1 {
			streamID := payload[3]
			if streamID >= 0xE0 && streamID <= 0xEF {
				return pid
			}
		}
	}
	return -1
}

// parseAccessUnitsByPES is the fallback parser when no video PID is found.
// Groups packets by any PES start, then merges small units.
func parseAccessUnitsByPES(data []byte) []accessUnit {
	var units []accessUnit
	var current *accessUnit

	for offset := 0; offset+tsPacketLen <= len(data); offset += tsPacketLen {
		pkt := data[offset : offset+tsPacketLen]
		if pkt[0] != 0x47 {
			continue
		}
		pusi := (pkt[1] & 0x40) != 0
		pid := int(pkt[1]&0x1F)<<8 | int(pkt[2])
		if pid == 0 || pid == 0x1FFF {
			if current != nil {
				current.end = offset + tsPacketLen
			}
			continue
		}
		if pusi {
			pts := extractPESDTS(pkt)
			if current != nil {
				current.end = offset
			}
			units = append(units, accessUnit{
				pts:   pts,
				start: offset,
				end:   offset + tsPacketLen,
			})
			current = &units[len(units)-1]
		} else if current != nil {
			current.end = offset + tsPacketLen
		}
	}
	return units
}

// extractPESDTS extracts the timing reference for pacing from a TS packet's
// PES header. Returns DTS if present (always monotonic, used for decode/send
// order), otherwise PTS. Returns -1 if neither is found.
//
// With B-frames, PTS can go backward (display reordering), but DTS is always
// monotonically increasing. For pacing, DTS is the correct timestamp.
func extractPESDTS(pkt []byte) int64 {
	if len(pkt) < tsPacketLen {
		return -1
	}

	// Find payload start, accounting for adaptation field.
	afc := (pkt[3] >> 4) & 0x03
	var payloadStart int
	switch afc {
	case 1: // payload only
		payloadStart = 4
	case 3: // adaptation + payload
		if len(pkt) < 5 {
			return -1
		}
		afLen := int(pkt[4])
		payloadStart = 5 + afLen
	default:
		return -1
	}

	if payloadStart+14 > len(pkt) {
		return -1
	}

	payload := pkt[payloadStart:]

	// Check PES start code: 00 00 01
	if payload[0] != 0x00 || payload[1] != 0x00 || payload[2] != 0x01 {
		return -1
	}

	if len(payload) < 9 {
		return -1
	}
	flags := payload[7]
	hasPTS := flags&0x80 != 0
	hasDTS := flags&0x40 != 0

	if !hasPTS {
		return -1
	}

	// DTS present (PTS+DTS): DTS is at offset 14 in PES header.
	// Use DTS for pacing since it's always monotonically increasing.
	if hasDTS && len(payload) >= 19 {
		return decodePTS(payload[14:19])
	}

	// PTS only (no B-frames): PTS == DTS, use it directly.
	return decodePTS(payload[9:14])
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

	// Fallback: assume ~2 Mbps average bitrate, minimum 1 second.
	const fallbackBitrate = 2_000_000
	bytes := len(data)
	seconds := float64(bytes*8) / fallbackBitrate
	if seconds < 1.0 {
		seconds = 1.0
	}
	return time.Duration(seconds * float64(time.Second))
}

// --- TS scanning and patching functions ---

func scanFirstLastPTS(data []byte) (firstPTS, lastPTS int64) {
	firstPTS = -1
	lastPTS = -1
	for off := 0; off+tsPacketLen <= len(data); off += tsPacketLen {
		pkt := data[off : off+tsPacketLen]
		if pkt[0] != 0x47 {
			continue
		}
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
		if len(payload) < 14 || payload[0] != 0 || payload[1] != 0 || payload[2] != 1 {
			continue
		}
		streamID := payload[3]
		if streamID < 0xE0 || streamID > 0xEF {
			continue
		}
		if len(payload) < 9 {
			continue
		}
		flags := payload[7]
		if flags&0x80 == 0 || len(payload) < 14 {
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

type ptsEntry struct {
	offset int
	isPCR  bool
}

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
		if hasAdapt && payloadOff < tsPacketLen {
			afLen := int(pkt[payloadOff])
			if afLen > 0 && payloadOff+1 < tsPacketLen {
				afFlags := pkt[payloadOff+1]
				if afFlags&0x10 != 0 && afLen >= 7 {
					entries = append(entries, ptsEntry{offset: off + payloadOff + 2, isPCR: true})
				}
			}
			payloadOff += 1 + afLen
		}
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

func decodePTS(b []byte) int64 {
	return int64(b[0]>>1&0x07)<<30 |
		int64(b[1])<<22 |
		int64(b[2]>>1&0x7F)<<15 |
		int64(b[3])<<7 |
		int64(b[4]>>1&0x7F)
}

func encodePTS(b []byte, pts int64) {
	prefix := b[0] & 0xF0
	b[0] = prefix | byte((pts>>29)&0x0E) | 0x01
	b[1] = byte(pts >> 22)
	b[2] = byte((pts>>14)&0xFE) | 0x01
	b[3] = byte(pts >> 7)
	b[4] = byte((pts<<1)&0xFE) | 0x01
}

func decodePCR(b []byte) int64 {
	return int64(b[0])<<25 |
		int64(b[1])<<17 |
		int64(b[2])<<9 |
		int64(b[3])<<1 |
		int64(b[4]>>7)
}

func encodePCR(b []byte, base int64) {
	ext := uint16(b[4]&0x01)<<8 | uint16(b[5])
	b[0] = byte(base >> 25)
	b[1] = byte(base >> 17)
	b[2] = byte(base >> 9)
	b[3] = byte(base >> 1)
	b[4] = byte((base&1)<<7) | 0x7E | byte(ext>>8)
	b[5] = byte(ext)
}
