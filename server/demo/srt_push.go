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

// accessUnit represents a group of TS packets that share the same PTS.
// A real encoder sends all packets for one frame as a burst at the
// frame's presentation time.
type accessUnit struct {
	pts    int64 // PES PTS in 90kHz ticks (-1 if no PTS found)
	start  int   // byte offset in file data
	end    int   // byte offset end (exclusive)
}

// streamFileLoop reads the TS file into memory and streams it in a loop
// using PTS-based pacing that matches real encoder behavior.
//
// Pacing strategy: the file is pre-parsed into access units (groups of
// TS packets sharing the same PES PTS). Each access unit is sent as a
// burst at the wall-clock time derived from its PTS. This produces the
// same timing as a real encoder like OBS or FFmpeg -re.
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

		for i, unit := range units {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			// Compute the time window for this access unit: from its PTS
			// to the next unit's PTS. Spread TS packet sends evenly across
			// this window to simulate real encoder CBR delivery. This prevents
			// SRT's TSBPD from seeing burst arrivals (which it delivers as
			// bursts after the latency delay, causing ~165ms gaps).
			var unitStartTime, unitEndTime time.Time
			if unit.pts >= 0 {
				sendPTS := unit.pts + ptsOffset
				unitStartTime = pacingTarget(anchorWall, anchorPTS, sendPTS)

				// Next unit's PTS (or end of file = start + frame duration)
				nextPTS := sendPTS + 3750 // default: 1 frame at 24fps
				if i+1 < len(units) && units[i+1].pts >= 0 {
					nextPTS = units[i+1].pts + ptsOffset
				}
				unitEndTime = pacingTarget(anchorWall, anchorPTS, nextPTS)
			}

			// Count TS packets in this unit for even distribution.
			unitBytes := unit.end - unit.start
			numChunks := (unitBytes + srtChunkSize - 1) / srtChunkSize
			if numChunks < 1 {
				numChunks = 1
			}

			// Send TS packets spread evenly across the time window.
			chunkIdx := 0
			for off := unit.start; off < unit.end; {
				if ctx.Err() != nil {
					return ctx.Err()
				}

				// Compute per-packet send time (linear interpolation).
				if !unitStartTime.IsZero() && !unitEndTime.IsZero() {
					frac := float64(chunkIdx) / float64(numChunks)
					target := unitStartTime.Add(time.Duration(frac * float64(unitEndTime.Sub(unitStartTime))))
					now := time.Now()
					if target.After(now) {
						sleepCtx(ctx, target.Sub(now))
						if ctx.Err() != nil {
							return ctx.Err()
						}
					}
				}

				end := off + srtChunkSize
				if end > unit.end {
					end = unit.end
				}
				_, err := conn.Write(data[off:end])
				if err != nil {
					return err
				}
				off = end
				chunkIdx++
			}
		}
	}
}

// parseAccessUnits groups TS packets by their PES PTS into access units.
// Packets without a PES header are grouped with the preceding access unit.
// This produces the same burst pattern as a real encoder.
func parseAccessUnits(data []byte) []accessUnit {
	var units []accessUnit
	var current *accessUnit

	for offset := 0; offset+tsPacketLen <= len(data); offset += tsPacketLen {
		pkt := data[offset : offset+tsPacketLen]
		if pkt[0] != 0x47 {
			continue // skip non-sync packets
		}

		// Check for PES start: payload_unit_start_indicator
		pusi := (pkt[1] & 0x40) != 0
		pid := int(pkt[1]&0x1F)<<8 | int(pkt[2])

		// Skip PAT/PMT/null packets — they don't carry media.
		if pid == 0 || pid == 0x1FFF {
			if current != nil {
				current.end = offset + tsPacketLen
			}
			continue
		}

		if pusi {
			// Extract PTS from PES header if present.
			pts := extractPESPTS(pkt)

			if current != nil {
				// Close previous access unit.
				current.end = offset
			}

			// Start new access unit.
			units = append(units, accessUnit{
				pts:   pts,
				start: offset,
				end:   offset + tsPacketLen,
			})
			current = &units[len(units)-1]
		} else if current != nil {
			// Continuation packet — extend current access unit.
			current.end = offset + tsPacketLen
		}
	}

	// Merge audio-only units into the preceding video unit for burst delivery.
	// A real encoder sends audio and video for the same time period together.
	return mergeSmallUnits(units)
}

// extractPESPTS extracts the PTS from a TS packet's PES header.
// Returns -1 if no PTS is found.
func extractPESPTS(pkt []byte) int64 {
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

	// Check PTS flag in PES header.
	if len(payload) < 14 {
		return -1
	}
	ptsFlag := (payload[7] >> 6) & 0x03
	if ptsFlag < 2 {
		return -1 // no PTS
	}

	return decodePTS(payload[9:14])
}

// mergeSmallUnits merges access units smaller than 2 TS packets into
// the preceding unit. Audio PES packets are typically 1-2 TS packets
// and should be sent with the preceding video frame, not independently.
func mergeSmallUnits(units []accessUnit) []accessUnit {
	if len(units) <= 1 {
		return units
	}

	var merged []accessUnit
	for i, u := range units {
		size := u.end - u.start
		if i > 0 && size <= 2*tsPacketLen && len(merged) > 0 {
			// Merge into previous unit.
			merged[len(merged)-1].end = u.end
		} else {
			merged = append(merged, u)
		}
	}
	return merged
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
