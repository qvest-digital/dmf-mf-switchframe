package output

import "context"

// scte35Filter wraps an OutputAdapter and strips MPEG-TS packets
// carrying the SCTE-35 PID before forwarding to the inner adapter.
// Used when a destination has SCTE35Enabled == false.
type scte35Filter struct {
	inner OutputAdapter
	pid   uint16
}

// newSCTE35Filter creates a filter that strips TS packets with the given PID.
func newSCTE35Filter(inner OutputAdapter, pid uint16) *scte35Filter {
	return &scte35Filter{inner: inner, pid: pid}
}

func (f *scte35Filter) ID() string { return f.inner.ID() }

func (f *scte35Filter) Start(ctx context.Context) error { return f.inner.Start(ctx) }

func (f *scte35Filter) Write(data []byte) (int, error) {
	const tsPacketSize = 188
	inputLen := len(data)

	if inputLen < tsPacketSize {
		// Not a full TS packet — pass through as-is.
		return f.inner.Write(data)
	}

	// Process packet-by-packet. Build filtered output.
	filtered := make([]byte, 0, inputLen)
	for i := 0; i+tsPacketSize <= inputLen; i += tsPacketSize {
		pkt := data[i : i+tsPacketSize]
		// Verify sync byte.
		if pkt[0] != 0x47 {
			// Not a valid TS packet — include it.
			filtered = append(filtered, pkt...)
			continue
		}
		// Extract PID from bytes 1-2.
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid == f.pid {
			continue // strip this packet
		}
		filtered = append(filtered, pkt...)
	}

	if len(filtered) == 0 {
		return inputLen, nil
	}
	n, err := f.inner.Write(filtered)
	if err != nil {
		return n, err
	}
	return inputLen, nil
}

func (f *scte35Filter) Close() error { return f.inner.Close() }

func (f *scte35Filter) Status() AdapterStatus { return f.inner.Status() }

// Compile-time check that scte35Filter satisfies OutputAdapter.
var _ OutputAdapter = (*scte35Filter)(nil)
