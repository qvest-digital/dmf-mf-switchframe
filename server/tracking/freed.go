package tracking

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// freeDPacketSize is the fixed size of a Free-D position/orientation packet.
	freeDPacketSize = 29

	// freeDTypePosition is the packet type byte for position/orientation data.
	freeDTypePosition = 0xD1

	// freeDRotScale converts raw 24-bit signed counts to degrees.
	// 32768 counts = 1 degree.
	freeDRotScale = 32768.0

	// freeDPosScale converts raw 24-bit signed counts to millimeters.
	// 64 counts = 1 mm.
	freeDPosScale = 64.0
)

// Sentinel errors for the Free-D receiver.
var (
	errFreeDTooShort        = errors.New("freed: packet too short")
	errFreeDWrongType       = errors.New("freed: unsupported packet type")
	errFreeDChecksumInvalid = errors.New("freed: invalid checksum")
	errFreeDAlreadyRunning  = errors.New("freed: receiver already running")
	errFreeDClosed          = errors.New("freed: receiver closed")
)

// FreeDReceiver listens for Free-D UDP packets and provides camera pose data.
// It implements the TrackingSource interface.
type FreeDReceiver struct {
	addr     string
	cameraID byte // filter for this camera ID (0 = accept all)

	mu      sync.Mutex // protects conn
	conn    net.PacketConn
	latest  atomic.Pointer[CameraPose]
	running atomic.Bool
	closed  atomic.Bool
	wg      sync.WaitGroup
}

// NewFreeDReceiver creates a new Free-D receiver bound to the given UDP address.
// Set cameraID to 0 to accept packets from any camera.
func NewFreeDReceiver(addr string, cameraID byte) *FreeDReceiver {
	return &FreeDReceiver{
		addr:     addr,
		cameraID: cameraID,
	}
}

// Name returns the tracking source name.
func (f *FreeDReceiver) Name() string { return "freed" }

// Type returns the tracking source type.
func (f *FreeDReceiver) Type() string { return "freed" }

// Start begins listening for Free-D UDP packets.
func (f *FreeDReceiver) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed.Load() {
		return errFreeDClosed
	}
	if !f.running.CompareAndSwap(false, true) {
		return errFreeDAlreadyRunning
	}

	conn, err := net.ListenPacket("udp", f.addr)
	if err != nil {
		f.running.Store(false)
		return fmt.Errorf("freed: listen %s: %w", f.addr, err)
	}
	f.conn = conn

	f.wg.Add(1)
	go f.readLoop()

	return nil
}

// Stop halts listening without releasing the receiver for reuse.
func (f *FreeDReceiver) Stop() error {
	if !f.running.Load() {
		return nil
	}

	f.mu.Lock()
	conn := f.conn
	f.conn = nil
	f.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	f.wg.Wait()
	f.running.Store(false)
	return nil
}

// Running reports whether the receiver is actively listening.
func (f *FreeDReceiver) Running() bool {
	return f.running.Load()
}

// LatestPose returns the most recently received camera pose, or nil if
// no valid pose has been received yet.
func (f *FreeDReceiver) LatestPose() *CameraPose {
	return f.latest.Load()
}

// Addr returns the local address the receiver is bound to, or nil if not listening.
func (f *FreeDReceiver) Addr() net.Addr {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn == nil {
		return nil
	}
	return f.conn.LocalAddr()
}

// Close stops the receiver and releases all resources.
func (f *FreeDReceiver) Close() error {
	if f.closed.Swap(true) {
		return nil // already closed
	}
	return f.Stop()
}

// readLoop reads UDP packets until the connection is closed.
func (f *FreeDReceiver) readLoop() {
	defer f.wg.Done()

	buf := make([]byte, 256) // Free-D packets are 29 bytes; generous buffer
	for {
		f.mu.Lock()
		conn := f.conn
		f.mu.Unlock()

		if conn == nil {
			return
		}

		// Set a read deadline so we can check for shutdown periodically.
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // deadline elapsed, loop around to check conn
			}
			return // connection closed or fatal error
		}

		pose, camID, parseErr := parseFreeDPacket(buf[:n])
		if parseErr != nil {
			continue // skip malformed packets
		}

		// Apply camera ID filter.
		if f.cameraID != 0 && camID != f.cameraID {
			continue
		}

		f.latest.Store(pose)
	}
}

// parseFreeDPacket decodes a 29-byte Free-D position/orientation packet.
// It returns the camera pose, the camera ID, and any parse error.
func parseFreeDPacket(data []byte) (*CameraPose, byte, error) {
	if len(data) < freeDPacketSize {
		return nil, 0, errFreeDTooShort
	}

	if data[0] != freeDTypePosition {
		return nil, 0, errFreeDWrongType
	}

	// Verify checksum: sum of bytes 0..27 (mod 256) must equal byte 28.
	// This is the convention used by Mo-Sys StarTracker, stYpe, and most modern
	// Free-D implementations. An alternate convention used by some older systems
	// requires the sum of all 29 bytes (mod 256) to equal 0x40; that variant is
	// not implemented here.
	var sum byte
	for i := 0; i < 28; i++ {
		sum += data[i]
	}
	if sum != data[28] {
		return nil, 0, errFreeDChecksumInvalid
	}

	cameraID := data[1]

	// Decode rotation fields (24-bit signed, 32768 counts/degree).
	pan := float64(decode24Signed(data[2:5])) / freeDRotScale
	tilt := float64(decode24Signed(data[5:8])) / freeDRotScale
	roll := float64(decode24Signed(data[8:11])) / freeDRotScale

	// Decode position fields (24-bit signed, 64 counts/mm → convert to meters).
	xMm := float64(decode24Signed(data[11:14])) / freeDPosScale
	yMm := float64(decode24Signed(data[14:17])) / freeDPosScale
	zMm := float64(decode24Signed(data[17:20])) / freeDPosScale

	// Decode lens fields (24-bit unsigned, raw encoder values).
	zoom := float64(decode24Unsigned(data[20:23]))
	focus := float64(decode24Unsigned(data[23:26]))

	pose := &CameraPose{
		Position:     [3]float64{xMm / 1000.0, yMm / 1000.0, zMm / 1000.0},
		Orientation:  [3]float64{pan, tilt, roll},
		FocalLength:  zoom,
		FocusDistance: focus,
		Confidence:   1.0, // hardware tracking is authoritative
		Source:        "freed",
	}

	return pose, cameraID, nil
}

// decode24Signed decodes a 3-byte big-endian value as a signed 24-bit integer.
// If bit 23 is set, the value is sign-extended by subtracting 2^24.
func decode24Signed(b []byte) int32 {
	u := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	if u&0x800000 != 0 {
		return int32(u) - 0x1000000 // sign extend: subtract 2^24
	}
	return int32(u)
}

// decode24Unsigned decodes a 3-byte big-endian value as an unsigned 24-bit integer.
func decode24Unsigned(b []byte) uint32 {
	return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
}
