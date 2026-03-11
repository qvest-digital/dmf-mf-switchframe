package output

import (
	"sync"
	"sync/atomic"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCBRPacer_NullPacketFormat(t *testing.T) {
	// Verify null TS packets have correct format: sync byte 0x47,
	// PID 0x1FFF, AFC=01 (payload only), payload all 0xFF.
	pkt := nullTSPacket()
	require.Len(t, pkt, tsPacketSize)

	assert.Equal(t, byte(0x47), pkt[0], "sync byte")
	pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
	assert.Equal(t, uint16(0x1FFF), pid, "null PID")
	assert.Equal(t, byte(0x10), pkt[3]&0x30, "AFC=payload only")

	// Payload bytes (4..187) should all be 0xFF.
	for i := 4; i < tsPacketSize; i++ {
		assert.Equal(t, byte(0xFF), pkt[i], "null payload byte %d", i)
	}
}

func TestCBRPacer_PadsToMuxrate(t *testing.T) {
	// Enqueue a small amount of TS data, verify output fills to target muxrate.
	const muxrateBps = 1_000_000 // 1 Mbps

	var mu sync.Mutex
	var totalBytes int64

	sink := &mockSink{
		writeFn: func(data []byte) (int, error) {
			mu.Lock()
			totalBytes += int64(len(data))
			mu.Unlock()
			return len(data), nil
		},
	}

	p := NewCBRPacer(muxrateBps, 10*time.Millisecond)
	adapters := []Adapter{sink}
	p.SetAdapters(&adapters)
	p.Start()

	// Enqueue one TS packet worth of real data.
	realData := make([]byte, tsPacketSize)
	realData[0] = 0x47
	p.Enqueue(realData)

	// Let it run for ~100ms.
	time.Sleep(120 * time.Millisecond)
	p.Stop()

	mu.Lock()
	got := totalBytes
	mu.Unlock()

	// In 100ms at 1 Mbps = 12,500 bytes expected.
	// Allow 50% tolerance for timer jitter in CI.
	expectedBytes := int64(float64(muxrateBps) / 8 * 0.1) // 100ms
	assert.InDelta(t, expectedBytes, got, float64(expectedBytes)*0.5,
		"output should be roughly %d bytes for 100ms at %d bps", expectedBytes, muxrateBps)

	// All output should be a multiple of 188.
	assert.Zero(t, got%tsPacketSize, "output must be TS-packet-aligned")
}

func TestCBRPacer_BurstWhenExceeded(t *testing.T) {
	// When real data exceeds the budget, send it all (don't drop).
	const muxrateBps = 100_000 // very low: ~12.5 KB/s

	var mu sync.Mutex
	var totalBytes int64

	sink := &mockSink{
		writeFn: func(data []byte) (int, error) {
			mu.Lock()
			totalBytes += int64(len(data))
			mu.Unlock()
			return len(data), nil
		},
	}

	p := NewCBRPacer(muxrateBps, 10*time.Millisecond)
	adapters := []Adapter{sink}
	p.SetAdapters(&adapters)
	p.Start()

	// Enqueue much more than the budget allows.
	bigData := make([]byte, tsPacketSize*100) // 18,800 bytes
	for i := 0; i < len(bigData); i += tsPacketSize {
		bigData[i] = 0x47
	}
	p.Enqueue(bigData)

	time.Sleep(50 * time.Millisecond)
	p.Stop()

	mu.Lock()
	got := totalBytes
	mu.Unlock()

	// All real data must be sent, plus any null padding.
	assert.GreaterOrEqual(t, got, int64(len(bigData)),
		"all real data must be sent even when exceeding budget")
	assert.Greater(t, p.BurstTicks(), int64(0), "should record burst ticks")
}

func TestCBRPacer_EmptyTickSendsNullOnly(t *testing.T) {
	// No data enqueued — output should be all null packets.
	const muxrateBps = 1_000_000

	var mu sync.Mutex
	var received [][]byte

	sink := &mockSink{
		writeFn: func(data []byte) (int, error) {
			cp := make([]byte, len(data))
			copy(cp, data)
			mu.Lock()
			received = append(received, cp)
			mu.Unlock()
			return len(data), nil
		},
	}

	p := NewCBRPacer(muxrateBps, 10*time.Millisecond)
	adapters := []Adapter{sink}
	p.SetAdapters(&adapters)
	p.Start()

	// Don't enqueue anything — let it run a few ticks.
	time.Sleep(50 * time.Millisecond)
	p.Stop()

	mu.Lock()
	allData := received
	mu.Unlock()

	require.NotEmpty(t, allData, "should have produced output from null ticks")

	// Verify all output is null packets (PID 0x1FFF).
	for _, chunk := range allData {
		require.Zero(t, len(chunk)%tsPacketSize, "output must be TS-packet-aligned")
		for i := 0; i+tsPacketSize <= len(chunk); i += tsPacketSize {
			require.Equal(t, byte(0x47), chunk[i], "sync byte")
			pid := uint16(chunk[i+1]&0x1F)<<8 | uint16(chunk[i+2])
			assert.Equal(t, uint16(0x1FFF), pid, "all packets should be null PID")
		}
	}
}

func TestCBRPacer_OutputMultipleOf188(t *testing.T) {
	// Every write to adapters must be a multiple of 188 bytes.
	const muxrateBps = 5_000_000

	var badWrites atomic.Int64

	sink := &mockSink{
		writeFn: func(data []byte) (int, error) {
			if len(data)%tsPacketSize != 0 {
				badWrites.Add(1)
			}
			return len(data), nil
		},
	}

	p := NewCBRPacer(muxrateBps, 10*time.Millisecond)
	adapters := []Adapter{sink}
	p.SetAdapters(&adapters)
	p.Start()

	// Enqueue a variety of TS-aligned sizes.
	for i := 0; i < 20; i++ {
		data := make([]byte, tsPacketSize*(i+1))
		data[0] = 0x47
		p.Enqueue(data)
		time.Sleep(5 * time.Millisecond)
	}

	time.Sleep(30 * time.Millisecond)
	p.Stop()

	assert.Zero(t, badWrites.Load(), "all writes must be a multiple of 188 bytes")
}

func TestCBRPacer_StopDrainsBuffer(t *testing.T) {
	// Remaining data should be flushed on stop.
	const muxrateBps = 10_000_000

	var mu sync.Mutex
	var totalBytes int64

	sink := &mockSink{
		writeFn: func(data []byte) (int, error) {
			mu.Lock()
			totalBytes += int64(len(data))
			mu.Unlock()
			return len(data), nil
		},
	}

	p := NewCBRPacer(muxrateBps, 10*time.Millisecond)
	adapters := []Adapter{sink}
	p.SetAdapters(&adapters)
	p.Start()

	// Enqueue data.
	data := make([]byte, tsPacketSize*50)
	for i := 0; i < len(data); i += tsPacketSize {
		data[i] = 0x47
	}
	p.Enqueue(data)

	// Stop immediately — should drain the buffer.
	p.Stop()

	mu.Lock()
	got := totalBytes
	mu.Unlock()

	assert.GreaterOrEqual(t, got, int64(len(data)),
		"all enqueued data should be flushed on stop")
}

func TestCBRPacer_ComputeMuxrate(t *testing.T) {
	// Verify muxrate formula: (video + audio) * 1.12
	tests := []struct {
		videoBps int64
		audioBps int64
		want     int64
	}{
		{10_000_000, 128_000, 11_343_360},   // (10M + 128K) * 1.12
		{6_000_000, 128_000, 6_863_360},     // (6M + 128K) * 1.12
		{20_000_000, 256_000, 22_686_720},   // (20M + 256K) * 1.12
	}

	for _, tt := range tests {
		got := ComputeMuxrate(tt.videoBps, tt.audioBps)
		assert.Equal(t, tt.want, got,
			"ComputeMuxrate(%d, %d)", tt.videoBps, tt.audioBps)
	}
}

// mockSink implements Adapter for testing.
type mockSink struct {
	writeFn func([]byte) (int, error)
}

func (m *mockSink) ID() string                              { return "mock-sink" }
func (m *mockSink) Start(_ context.Context) error           { return nil }
func (m *mockSink) Write(data []byte) (int, error)          { return m.writeFn(data) }
func (m *mockSink) Close() error                            { return nil }
func (m *mockSink) Status() AdapterStatus                   { return AdapterStatus{} }
