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

// FileRecorder is an OutputAdapter that writes muxed MPEG-TS data to a file.
// Files are named program_{date}_{time}_{index}.ts in the configured directory.
// Rotation occurs based on time elapsed and/or file size thresholds.
type FileRecorder struct {
	config RecorderConfig

	mu            sync.Mutex
	file          *os.File
	filename      string
	state         AdapterState
	started       time.Time // overall recording start
	fileStarted   time.Time // current file start
	fileBytes     int64     // bytes written to current file
	fileIndex     int       // 1-based file index
	baseTimestamp string    // shared timestamp across rotated files
	bytes         atomic.Int64
	errMsg        string
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
// OutputAdapter interface but is not currently checked; file creation
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

// Write sends muxed MPEG-TS data to the file. If a rotation threshold
// has been reached, the current file is closed and a new one is opened
// before writing.
func (r *FileRecorder) Write(tsData []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateActive || r.file == nil {
		return 0, ErrRecorderNotActive
	}

	// Check if rotation is needed before writing.
	if r.shouldRotate() {
		if err := r.rotateLocked(); err != nil {
			return 0, fmt.Errorf("rotate file: %w", err)
		}
	}

	n, err := r.file.Write(tsData)
	r.fileBytes += int64(n)
	r.bytes.Add(int64(n))
	if err != nil {
		r.errMsg = err.Error()
		return n, err
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

// Compile-time check that FileRecorder satisfies the OutputAdapter interface.
var _ OutputAdapter = (*FileRecorder)(nil)
