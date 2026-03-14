package output

// ringBuffer is a fixed-size circular buffer used to hold MPEG-TS data during
// SRT reconnection. If writes exceed the capacity, the oldest data is silently
// overwritten and the overflowed flag is set. On reconnect the SRT caller
// checks Overflowed(): if false the buffered data can be flushed; if true the
// data is stale and the caller should wait for the next keyframe.
type ringBuffer struct {
	data       []byte
	capacity   int
	readPos    int
	writePos   int
	count      int  // number of readable bytes
	overflowed bool // set when a write overwrites unread data
}

// newRingBuffer creates a ring buffer with the given byte capacity.
func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{
		data:     make([]byte, capacity),
		capacity: capacity,
	}
}

// Write appends p to the buffer. If p is larger than the remaining space,
// the oldest unread data is overwritten and overflowed is set to true.
// Write always succeeds (never returns an error) and always reports len(p)
// bytes written, matching the io.Writer contract.
func (r *ringBuffer) Write(p []byte) (int, error) {
	total := len(p) // preserve original length for io.Writer contract

	// If the write is larger than total capacity, only keep the last
	// capacity bytes (everything older is lost).
	didOverflow := false
	if total > r.capacity {
		r.overflowed = true
		didOverflow = true
		p = p[total-r.capacity:]
		// Reset positions — we're filling the entire buffer.
		r.writePos = 0
		r.readPos = 0
		r.count = 0
	}

	n := len(p)
	// If writing more than available space, mark overflow and advance readPos.
	if r.count+n > r.capacity {
		r.overflowed = true
		didOverflow = true
		overflow := r.count + n - r.capacity
		r.readPos = (r.readPos + overflow) % r.capacity
		r.count -= overflow
	}

	// Write in up to 2 segments (handle wraparound).
	firstLen := r.capacity - r.writePos
	if firstLen >= n {
		copy(r.data[r.writePos:], p)
	} else {
		copy(r.data[r.writePos:], p[:firstLen])
		copy(r.data[0:], p[firstLen:])
	}
	r.writePos = (r.writePos + n) % r.capacity
	r.count += n

	// After overflow, scan forward from readPos to find the first TS sync
	// byte (0x47), then trim count to a TS packet multiple. This ensures
	// ReadAll never returns data starting mid-packet. The scan handles
	// both partial-overwrite overflow (readPos lands mid-packet in
	// previously written data) and large-write overflow (data itself may
	// begin mid-packet after slicing to capacity).
	if didOverflow {
		r.alignToTSPacket()
	}

	return total, nil
}

// ReadAll returns all readable data as a new byte slice and resets the buffer.
// Returns nil if the buffer is empty. The overflowed flag is cleared.
func (r *ringBuffer) ReadAll() []byte {
	if r.count == 0 {
		r.overflowed = false
		return nil
	}

	out := make([]byte, r.count)
	if r.readPos+r.count <= r.capacity {
		// Contiguous region.
		copy(out, r.data[r.readPos:r.readPos+r.count])
	} else {
		// Wraps around the end of the backing array.
		firstPart := r.capacity - r.readPos
		copy(out, r.data[r.readPos:r.capacity])
		copy(out[firstPart:], r.data[:r.count-firstPart])
	}

	r.readPos = 0
	r.writePos = 0
	r.count = 0
	r.overflowed = false

	return out
}

// alignToTSPacket scans forward from readPos to find the first TS sync byte
// (0x47) and adjusts readPos/count accordingly. Then trims count to a TS
// packet multiple so partial trailing packets are excluded.
func (r *ringBuffer) alignToTSPacket() {
	// Scan up to one full TS packet worth of bytes to find 0x47.
	// In MPEG-TS data, sync bytes occur every 188 bytes, so we'll find one
	// within at most 187 bytes of scanning.
	found := false
	for skip := 0; skip < tsPacketSize && skip < r.count; skip++ {
		pos := (r.readPos + skip) % r.capacity
		if r.data[pos] == 0x47 {
			if skip > 0 {
				r.readPos = pos
				r.count -= skip
			}
			found = true
			break
		}
	}
	if !found {
		// No sync byte found — data is not valid TS. Clear readable data.
		r.count = 0
		return
	}

	// Trim trailing partial packet.
	if tail := r.count % tsPacketSize; tail != 0 {
		r.count -= tail
	}
}

// Len returns the number of unread bytes in the buffer.
func (r *ringBuffer) Len() int {
	return r.count
}

// Overflowed reports whether any write has silently discarded data since the
// last ReadAll or Reset.
func (r *ringBuffer) Overflowed() bool {
	return r.overflowed
}

// Reset clears the buffer and the overflowed flag.
func (r *ringBuffer) Reset() {
	r.readPos = 0
	r.writePos = 0
	r.count = 0
	r.overflowed = false
}
