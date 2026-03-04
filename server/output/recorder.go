package output

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// FileRecorder is an OutputAdapter that writes muxed MPEG-TS data to a file.
// Files are named program_{date}_{time}.ts in the configured directory.
type FileRecorder struct {
	dir      string
	mu       sync.Mutex
	file     *os.File
	filename string
	state    AdapterState
	started  time.Time
	bytes    atomic.Int64
	errMsg   string
}

// NewFileRecorder creates a FileRecorder that writes files to dir.
func NewFileRecorder(dir string) *FileRecorder {
	return &FileRecorder{
		dir:   dir,
		state: StateStopped,
	}
}

// ID returns the adapter identifier.
func (r *FileRecorder) ID() string {
	return "recorder"
}

// Start creates a new MPEG-TS file and begins accepting writes.
func (r *FileRecorder) Start(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state == StateActive {
		return errors.New("recorder already active")
	}

	now := time.Now()
	r.filename = fmt.Sprintf("program_%s.ts", now.Format("20060102_150405"))
	path := filepath.Join(r.dir, r.filename)

	f, err := os.Create(path)
	if err != nil {
		r.state = StateError
		r.errMsg = err.Error()
		return err
	}

	r.file = f
	r.state = StateActive
	r.started = now
	r.bytes.Store(0)
	r.errMsg = ""
	return nil
}

// Write sends muxed MPEG-TS data to the file.
func (r *FileRecorder) Write(tsData []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateActive || r.file == nil {
		return 0, errors.New("recorder not active")
	}

	n, err := r.file.Write(tsData)
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

	err := r.file.Close()
	r.file = nil
	r.state = StateStopped
	return err
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
