package output

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// RecorderConfig configures a FileRecorder.
type RecorderConfig struct {
	Dir         string        // Output directory for recording files.
	RotateAfter time.Duration // Rotate after this duration; default 1h, 0 = disabled.
	MaxFileSize int64         // Rotate after this many bytes; default 0 (unlimited).
}

// syncBytesThreshold is the number of bytes written between periodic fsync
// calls. 4MB provides crash resilience without excessive I/O overhead.
const syncBytesThreshold int64 = 4 * 1024 * 1024

// FileRecorder is an Adapter that writes muxed MPEG-TS data to a file.
// Files are named program_{date}_{time}_{index}.ts in the configured directory.
// Rotation occurs based on time elapsed and/or file size thresholds.
type FileRecorder struct {
	config RecorderConfig

	mu             sync.Mutex
	file           *os.File
	filename       string
	state          AdapterState
	started        time.Time // overall recording start
	fileStarted    time.Time // current file start
	fileBytes      int64     // bytes written to current file
	fileIndex      int       // 1-based file index
	baseTimestamp  string    // shared timestamp across rotated files
	bytes          atomic.Int64
	errMsg         string
	pendingRotate  bool   // true when rotation threshold reached but awaiting keyframe
	cachedPATMPT   []byte // cached PAT+PMT packets from stream for rotated files
	bytesSinceSync int64  // bytes written since last fsync
	lastSyncTime   time.Time // time of last periodic fsync
}

// NewFileRecorder creates a FileRecorder with the given configuration.
// Both RotateAfter and MaxFileSize default to zero (disabled). To get the
// recommended 1-hour rotation, set RotateAfter explicitly or use the API
// handler which applies defaults.
func NewFileRecorder(config RecorderConfig) *FileRecorder {
	return &FileRecorder{
		config: config,
		state:  StateStopped,
	}
}

// ID returns the adapter identifier.
func (r *FileRecorder) ID() string {
	return "recorder"
}

// Start creates a new MPEG-TS file and begins accepting writes.
//
// The context parameter is accepted for API compatibility with the
// Adapter interface but is not currently checked; file creation
// is a fast local operation.
func (r *FileRecorder) Start(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state == StateActive {
		return ErrRecorderActive
	}

	now := time.Now()
	r.baseTimestamp = now.Format("20060102_150405")
	r.fileIndex = 1
	r.started = now
	r.bytes.Store(0)
	r.errMsg = ""

	if err := r.openFileLocked(now); err != nil {
		return err
	}

	r.state = StateActive
	return nil
}

// openFileLocked creates a new file for recording. Must be called with r.mu held.
func (r *FileRecorder) openFileLocked(now time.Time) error {
	r.filename = fmt.Sprintf("program_%s_%03d.ts", r.baseTimestamp, r.fileIndex)
	path := filepath.Join(r.config.Dir, r.filename)

	if err := os.MkdirAll(r.config.Dir, 0o755); err != nil {
		r.state = StateError
		r.errMsg = err.Error()
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		r.state = StateError
		r.errMsg = err.Error()
		return err
	}

	r.file = f
	r.fileStarted = now
	r.fileBytes = 0
	r.bytesSinceSync = 0
	return nil
}

// shouldRotate checks whether the current file should be rotated based on
// the configured time and size thresholds.
func (r *FileRecorder) shouldRotate() bool {
	// Check time-based rotation.
	if r.config.RotateAfter > 0 && time.Since(r.fileStarted) >= r.config.RotateAfter {
		return true
	}

	// Check size-based rotation.
	if r.config.MaxFileSize > 0 && r.fileBytes >= r.config.MaxFileSize {
		return true
	}

	return false
}

// rotateLocked closes the current file and opens a new one with an
// incremented index. Must be called with r.mu held.
func (r *FileRecorder) rotateLocked() error {
	// Flush and close current file.
	if r.file != nil {
		if err := r.file.Sync(); err != nil {
			slog.Error("error syncing rotated file", "file", r.filename, "err", err)
		}
		if err := r.file.Close(); err != nil {
			slog.Error("error closing rotated file", "file", r.filename, "err", err)
		}
	}

	r.fileIndex++
	now := time.Now()
	if err := r.openFileLocked(now); err != nil {
		return err
	}

	slog.Info("recording file rotated", "file", r.filename, "index", r.fileIndex)
	return nil
}

// cachePATandPMT scans TS data for PAT (PID 0) and PMT (pmtPID) packets
// and caches them for writing at the start of rotated files.
func (r *FileRecorder) cachePATandPMT(tsData []byte) {
	var patPkt, pmtPkt []byte
	for i := 0; i+tsPacketSize <= len(tsData); i += tsPacketSize {
		if tsData[i] != 0x47 {
			continue
		}
		pid := uint16(tsData[i+1]&0x1F)<<8 | uint16(tsData[i+2])
		if pid == 0 && patPkt == nil {
			patPkt = make([]byte, tsPacketSize)
			copy(patPkt, tsData[i:i+tsPacketSize])
		}
		if pid == pmtPID && pmtPkt == nil {
			pmtPkt = make([]byte, tsPacketSize)
			copy(pmtPkt, tsData[i:i+tsPacketSize])
		}
		if patPkt != nil && pmtPkt != nil {
			break
		}
	}
	if patPkt != nil && pmtPkt != nil {
		r.cachedPATMPT = append(patPkt, pmtPkt...)
	}
}

// setTSDiscontinuityIndicator sets the MPEG-TS discontinuity_indicator flag
// on each 188-byte TS packet in the buffer. This is the standard way to signal
// that continuity counter tracking resets at this point, used when writing
// cached PAT/PMT packets at the start of a rotated recording file.
//
// For packets with no adaptation field (payload only, AFC=01), an adaptation
// field is inserted: AFC is changed to 11 (adaptation + payload), with
// adaptation_field_length=1 and the discontinuity_indicator bit set.
//
// For packets that already have an adaptation field (AFC=10 or 11), the
// discontinuity_indicator bit (bit 7 of the flags byte) is set in the
// existing adaptation field.
func setTSDiscontinuityIndicator(tsData []byte) {
	for i := 0; i+tsPacketSize <= len(tsData); i += tsPacketSize {
		if tsData[i] != 0x47 {
			continue // not a valid sync byte
		}

		afc := (tsData[i+3] >> 4) & 0x03

		switch {
		case afc >= 2:
			// Adaptation field already exists (AFC=10 or AFC=11).
			// Verify there is a flags byte (adaptation_field_length >= 1).
			if tsData[i+4] >= 1 {
				// Set discontinuity_indicator (bit 7 of flags byte at byte 5).
				tsData[i+5] |= 0x80
			}

		case afc == 1:
			// Payload only (AFC=01). We need to insert a minimal adaptation field.
			// Shifting payload right by 2 bytes drops the last 2 bytes. Verify
			// they are 0xFF stuffing before proceeding; otherwise skip this
			// packet to avoid corrupting a PAT/PMT with a full descriptor loop.
			if tsData[i+tsPacketSize-1] != 0xFF || tsData[i+tsPacketSize-2] != 0xFF {
				slog.Warn("setTSDiscontinuityIndicator: skipping payload-only packet with non-stuffing trailing bytes",
					"offset", i,
					"byte[-1]", fmt.Sprintf("0x%02X", tsData[i+tsPacketSize-1]),
					"byte[-2]", fmt.Sprintf("0x%02X", tsData[i+tsPacketSize-2]))
				continue
			}

			// Change AFC from 01 to 11 (adaptation + payload), preserving CC.
			tsData[i+3] = (tsData[i+3] & 0x0F) | 0x30

			// Shift existing payload right by 2 bytes to make room for the
			// adaptation field header (length + flags). The last 2 bytes of
			// the original payload are 0xFF stuffing and are safely dropped.
			copy(tsData[i+6:i+tsPacketSize], tsData[i+4:i+tsPacketSize-2])

			// Set adaptation_field_length = 1 (just the flags byte).
			tsData[i+4] = 1

			// Set flags byte with discontinuity_indicator (bit 7).
			tsData[i+5] = 0x80
		}
	}
}

// Write sends muxed MPEG-TS data to the file. If a rotation threshold
// has been reached, the current file is closed and a new one is opened
// before writing. Rotation is deferred until a keyframe arrives to
// ensure the new file starts with a decodable GOP.
func (r *FileRecorder) Write(tsData []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateActive || r.file == nil {
		return 0, ErrRecorderNotActive
	}

	// Cache PAT/PMT for use after rotation.
	r.cachePATandPMT(tsData)

	// Check if rotation is needed -- defer until we see a keyframe.
	if !r.pendingRotate && r.shouldRotate() {
		r.pendingRotate = true
	}

	// Rotate only when we have a keyframe to start the new file.
	if r.pendingRotate && containsKeyframe(tsData) {
		if err := r.rotateLocked(); err != nil {
			return 0, fmt.Errorf("rotate file: %w", err)
		}
		r.pendingRotate = false

		// Write cached PAT/PMT at the start of the new file.
		if len(r.cachedPATMPT) > 0 {
			setTSDiscontinuityIndicator(r.cachedPATMPT)
			patN, err := r.file.Write(r.cachedPATMPT)
			r.fileBytes += int64(patN)
			r.bytes.Add(int64(patN))
			if err != nil {
				r.state = StateError
				r.errMsg = err.Error()
				return 0, err
			}
		}
	}

	n, err := r.file.Write(tsData)
	r.fileBytes += int64(n)
	r.bytes.Add(int64(n))
	if err != nil {
		r.state = StateError
		r.errMsg = err.Error()
		return n, err
	}

	// Periodic fsync to limit data loss on crash.
	r.bytesSinceSync += int64(n)
	if r.bytesSinceSync >= syncBytesThreshold {
		if syncErr := r.file.Sync(); syncErr != nil {
			slog.Error("periodic fsync failed", "file", r.filename, "err", syncErr)
		} else {
			r.lastSyncTime = time.Now()
		}
		r.bytesSinceSync = 0
	}

	return n, nil
}

// Close stops recording and closes the file. Safe to call multiple times.
func (r *FileRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateActive || r.file == nil {
		return nil
	}

	var syncErr error
	if err := r.file.Sync(); err != nil {
		syncErr = fmt.Errorf("sync: %w", err)
	}
	closeErr := r.file.Close()
	r.file = nil
	r.state = StateStopped
	if syncErr != nil {
		if closeErr != nil {
			return fmt.Errorf("%w; close: %v", syncErr, closeErr)
		}
		return syncErr
	}
	return closeErr
}

// Status returns the current adapter status.
func (r *FileRecorder) Status() AdapterStatus {
	r.mu.Lock()
	defer r.mu.Unlock()

	return AdapterStatus{
		State:        r.state,
		BytesWritten: r.bytes.Load(),
		StartedAt:    r.started,
		Error:        r.errMsg,
	}
}

// Filename returns the current recording filename (basename only).
func (r *FileRecorder) Filename() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.filename
}

// RecordingStatusSnapshot returns a RecordingStatus suitable for inclusion
// in ControlRoomState JSON.
func (r *FileRecorder) RecordingStatusSnapshot() RecordingStatus {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateActive {
		return RecordingStatus{Active: false}
	}

	var dur float64
	if !r.started.IsZero() {
		dur = time.Since(r.started).Seconds()
	}

	return RecordingStatus{
		Active:       true,
		Filename:     r.filename,
		BytesWritten: r.bytes.Load(),
		DurationSecs: dur,
	}
}

// Compile-time check that FileRecorder satisfies the Adapter interface.
var _ Adapter = (*FileRecorder)(nil)
