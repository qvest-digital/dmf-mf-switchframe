package output

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/createonly"
	"github.com/zsiec/switchframe/server/internal/mpegts"
)

// RecorderConfig configures a FileRecorder.
type RecorderConfig struct {
	Dir            string        // Output directory for recording files.
	RotateAfter    time.Duration // Rotate after this duration; default 1h, 0 = disabled.
	MaxFileSize    int64         // Rotate after this many bytes; default 0 (unlimited).
	FilenamePrefix string        // Optional prefix for recording files; default "program".
	// ArchiveDir, when set, receives every finished file, copied there
	// front to back as a new file once it is closed, and the local copy is
	// then removed. It suits a create-only directory such as an
	// object-store mount, which the recorder could not write to directly:
	// the periodic fsync would end the upload. Files a previous run left in
	// Dir are archived when recording starts.
	ArchiveDir string
}

// ErrNothingRecorded is the error a stopped recording reports when no
// program video reached it: the muxer starts a file at the first keyframe,
// so a program source that delivers no frames leaves the file empty. The
// empty file is removed rather than left behind as a recording.
var ErrNothingRecorded = errors.New("no program video reached the recorder, so nothing was recorded")

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
	stopped        time.Time // when Close ended the recording
	fileStarted    time.Time // current file start
	fileBytes      int64     // bytes written to current file
	fileIndex      int       // 1-based file index
	baseTimestamp  string    // shared timestamp across rotated files
	bytes          atomic.Int64
	errMsg         string
	pendingRotate  bool      // true when rotation threshold reached but awaiting keyframe
	cachedPATMPT   []byte    // cached PAT+PMT packets from stream for rotated files
	bytesSinceSync int64     // bytes written since last fsync
	lastSyncTime   time.Time // time of last periodic fsync

	// Copies into ArchiveDir in flight, and the files they copy.
	archiving sync.WaitGroup
	inArchive map[string]bool

	// onWrite is told the bytes each write put in the file. Set before
	// Start and never changed after.
	onWrite func(n int)
}

// addBytesLocked accounts n bytes written to the current file.
func (r *FileRecorder) addBytesLocked(n int) {
	r.fileBytes += int64(n)
	r.bytes.Add(int64(n))
	if r.onWrite != nil {
		r.onWrite(n)
	}
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

	r.archiveLeftoversLocked()
	if err := r.openFileLocked(now); err != nil {
		return err
	}

	r.state = StateActive
	return nil
}

// archiveLeftoversLocked archives the recordings a previous run left in
// Dir, such as one cut short by a restart. Must be called with r.mu held,
// before the new file is opened.
func (r *FileRecorder) archiveLeftoversLocked() {
	if r.config.ArchiveDir == "" {
		return
	}
	entries, err := os.ReadDir(r.config.Dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".ts" {
			continue
		}
		r.archiveLocked(filepath.Join(r.config.Dir, e.Name()))
	}
}

// archiveLocked copies a closed recording into ArchiveDir in the
// background, then removes it from Dir. On failure the file stays in Dir
// and the next Start retries it. Must be called with r.mu held.
func (r *FileRecorder) archiveLocked(path string) {
	if r.config.ArchiveDir == "" || r.inArchive[path] {
		return
	}
	if r.inArchive == nil {
		r.inArchive = map[string]bool{}
	}
	r.inArchive[path] = true
	dst := filepath.Join(r.config.ArchiveDir, filepath.Base(path))
	r.archiving.Add(1)
	go func() {
		defer r.archiving.Done()
		err := archiveRecording(dst, path)
		r.mu.Lock()
		delete(r.inArchive, path)
		r.mu.Unlock()
		if err != nil {
			slog.Error("recording not archived", "file", path, "archive", dst, "err", err)
			return
		}
		slog.Info("recording archived", "file", path, "archive", dst)
	}()
}

// archiveRecording copies src to the new file dst and removes src. A dst
// that already exists is an earlier archive of the same file: complete when
// it has src's size, and the local removal is what did not happen; cut
// short by a restart otherwise, since a copy that dies still leaves a file
// in an object store, and then replaced. Only this recorder's engine
// writes into its archive directory, so nothing else owns that file.
func archiveRecording(dst, src string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	err := createonly.Copy(dst, src)
	if errors.Is(err, fs.ErrExist) {
		si, serr := os.Stat(src)
		di, derr := os.Stat(dst)
		switch {
		case serr != nil:
			err = serr
		case derr == nil && si.Size() == di.Size():
			err = nil
		default:
			if err = os.Remove(dst); err == nil {
				err = createonly.Copy(dst, src)
			}
		}
	}
	if err != nil {
		return err
	}
	return os.Remove(src)
}

// WaitArchived blocks until every archive copy started so far is done.
func (r *FileRecorder) WaitArchived() {
	r.archiving.Wait()
}

// openFileLocked creates a new file for recording. Must be called with r.mu held.
func (r *FileRecorder) openFileLocked(now time.Time) error {
	prefix := r.config.FilenamePrefix
	if prefix == "" {
		prefix = "program"
	}
	r.filename = fmt.Sprintf("%s_%s_%03d.ts", prefix, r.baseTimestamp, r.fileIndex)
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

// rotationEnabled reports whether any rotation threshold is configured. When
// rotation is disabled the cached PAT/PMT is never consumed, so the scan that
// populates it can be skipped entirely.
func (r *FileRecorder) rotationEnabled() bool {
	return r.config.RotateAfter > 0 || r.config.MaxFileSize > 0
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
		} else {
			r.archiveLocked(filepath.Join(r.config.Dir, r.filename))
		}
		// Clear the handle now that the old file is closed. If
		// openFileLocked below fails, r.file must not point at the
		// already-closed descriptor (avoids a stale handle / double close).
		r.file = nil
	}

	r.fileIndex++
	now := time.Now()
	if err := r.openFileLocked(now); err != nil {
		return err
	}

	slog.Info("recording file rotated", "file", r.filename, "index", r.fileIndex)
	return nil
}

// cachePATandPMT scans TS data for PAT (PID 0) and PMT (PMTPID) packets
// and caches them for writing at the start of rotated files.
func (r *FileRecorder) cachePATandPMT(tsData []byte) {
	var patPkt, pmtPkt []byte
	for i := 0; i+mpegts.PacketSize <= len(tsData); i += mpegts.PacketSize {
		if tsData[i] != mpegts.SyncByte {
			continue
		}
		pid := mpegts.ExtractPID(tsData[i:])
		if pid == mpegts.PATPID && patPkt == nil {
			patPkt = make([]byte, mpegts.PacketSize)
			copy(patPkt, tsData[i:i+mpegts.PacketSize])
		}
		if pid == mpegts.PMTPID && pmtPkt == nil {
			pmtPkt = make([]byte, mpegts.PacketSize)
			copy(pmtPkt, tsData[i:i+mpegts.PacketSize])
		}
		if patPkt != nil && pmtPkt != nil {
			break
		}
	}
	if patPkt != nil && pmtPkt != nil {
		r.cachedPATMPT = append(patPkt, pmtPkt...)
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

	// Check if rotation is needed -- defer until we see a keyframe.
	if !r.pendingRotate && r.shouldRotate() {
		r.pendingRotate = true
	}

	// Cache PAT/PMT for prepending after rotation. The full per-batch scan is
	// wasted CPU on the 30fps write path: go-astits retransmits PAT/PMT only
	// every ~40 PES packets, so most batches contain neither. Only scan when
	// rotation can actually happen (a cached copy is needed) and either we have
	// no copy yet or a rotation is pending -- the latter refreshes the cache one
	// last time just before it is consumed at the next keyframe. Once a copy
	// exists and no rotation is pending, the steady-state common case skips the
	// scan entirely.
	if r.rotationEnabled() && (r.cachedPATMPT == nil || r.pendingRotate) {
		r.cachePATandPMT(tsData)
	}

	// Rotate only when we have a keyframe to start the new file.
	if r.pendingRotate && mpegts.ContainsKeyframe(tsData) {
		if err := r.rotateLocked(); err != nil {
			return 0, fmt.Errorf("rotate file: %w", err)
		}
		r.pendingRotate = false

		// Write cached PAT/PMT at the start of the new file.
		if len(r.cachedPATMPT) > 0 {
			mpegts.SetDiscontinuityIndicator(r.cachedPATMPT)
			patN, err := r.file.Write(r.cachedPATMPT)
			r.addBytesLocked(patN)
			if err != nil {
				r.state = StateError
				r.errMsg = err.Error()
				// Close the descriptor so it is not leaked: Close()
				// short-circuits on StateError and the manager never
				// auto-closes an errored recorder.
				_ = r.file.Close()
				r.file = nil
				return 0, err
			}
		}
	}

	n, err := r.file.Write(tsData)
	r.addBytesLocked(n)
	if err != nil {
		r.state = StateError
		r.errMsg = err.Error()
		// Close the descriptor so it is not leaked: Close()
		// short-circuits on StateError and the manager never
		// auto-closes an errored recorder.
		_ = r.file.Close()
		r.file = nil
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
	r.stopped = time.Now()
	path := filepath.Join(r.config.Dir, r.filename)
	switch {
	case closeErr != nil:
	case r.fileBytes == 0 && r.fileIndex == 1:
		// A rotated file always starts at a keyframe, so only the first
		// one can be empty.
		r.errMsg = ErrNothingRecorded.Error()
		if err := os.Remove(path); err != nil {
			slog.Error("empty recording not removed", "file", path, "err", err)
		}
	default:
		r.archiveLocked(path)
	}
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

// Archiving returns the basenames of the closed files still being copied
// into ArchiveDir.
func (r *FileRecorder) Archiving() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.inArchive))
	for p := range r.inArchive {
		names = append(names, filepath.Base(p))
	}
	return names
}

// FinalStatus returns what a stopped recorder wrote: its last file, the
// bytes and duration, and why nothing was recorded, if nothing was.
func (r *FileRecorder) FinalStatus() RecordingStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	var dur float64
	if !r.started.IsZero() && !r.stopped.IsZero() {
		dur = r.stopped.Sub(r.started).Seconds()
	}
	return RecordingStatus{
		Filename:     r.filename,
		BytesWritten: r.bytes.Load(),
		DurationSecs: dur,
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
