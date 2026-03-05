package output

const (
	defaultSRTLatency = 120 // ms

	// srtLiveMaxPayload is the maximum payload per SRT write in live mode.
	// 7 MPEG-TS packets × 188 bytes = 1316 bytes, which fits within the
	// standard 1500-byte MTU minus SRT/UDP/IP overhead.
	srtLiveMaxPayload = 7 * 188 // 1316 bytes
)

// srtConn abstracts SRT connection operations for testing without real network I/O.
// Both SRTCaller and SRTListener use this interface.
type srtConn interface {
	Write(data []byte) (int, error)
	Close()
}

// chunkedConn wraps an srtConn and splits writes into chunks of at most
// srtLiveMaxPayload bytes. SRT live mode rejects writes larger than 1316
// bytes, but the MPEG-TS muxer flushes full frames which can be much larger.
type chunkedConn struct {
	inner srtConn
}

func (c *chunkedConn) Write(data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		chunk := data
		if len(chunk) > srtLiveMaxPayload {
			chunk = data[:srtLiveMaxPayload]
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
