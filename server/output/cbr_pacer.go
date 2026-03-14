package output

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/metrics"
)

// ComputeMuxrate calculates the TS muxrate from video and audio bitrates.
// Adds 12% overhead for PAT/PMT repetition, PCR adaptation fields, PES
// headers, and SCTE-35 sections.
func ComputeMuxrate(videoBps, audioBps int64) int64 {
	return int64(float64(videoBps+audioBps) * 1.12)
}

// nullTSPacket returns a single 188-byte null TS packet (PID 0x1FFF).
// ISO 13818-1 defines null packets as stuffing that decoders discard.
func nullTSPacket() [tsPacketSize]byte {
	var pkt [tsPacketSize]byte
	pkt[0] = 0x47 // sync byte
	pkt[1] = 0x1F // PID high bits (no PUSI, no TEI, no priority)
	pkt[2] = 0xFF // PID low bits → PID = 0x1FFF
	pkt[3] = 0x10 // AFC=01 (payload only), CC=0
	for i := 4; i < tsPacketSize; i++ {
		pkt[i] = 0xFF // null payload
	}
	return pkt
}

// CBRPacer paces MPEG-TS output to a constant bitrate by inserting null
// packets (PID 0x1FFF) between real TS data. It replaces AsyncAdapter for
// SRT destinations when CBR mode is enabled.
//
// Design: A tick goroutine fires every tickInterval (default 10ms). On each
// tick, it computes how many bytes should have been sent since start (byte-debt
// tracking against a monotonic clock), swaps the accumulation buffer, and
// emits real data + null padding to fill the target rate. If real data exceeds
// the budget, it's sent in full (burst, never drop). SRT's 120ms receive
// buffer absorbs short-term jitter.
type CBRPacer struct {
	muxrateBps   int64
	bytesPerSec  float64
	tickInterval time.Duration

	mu      sync.Mutex
	buf     []byte // accumulation buffer (producer side)
	swapBuf []byte // swap buffer (avoids allocation)

	startTime time.Time
	bytesSent int64

	adapters atomic.Pointer[[]Adapter]

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once

	// Pre-allocated null slab (one tick's worth of null packets).
	nullSlab []byte

	// Reusable output buffer for tick() — avoids allocation per tick.
	outBuf []byte

	// Prometheus metrics (optional).
	prom *metrics.Metrics

	// Metrics (atomic counters for lock-free reads via CBRStatus).
	nullPktsTotal  atomic.Int64
	realBytesTotal atomic.Int64
	padBytesTotal  atomic.Int64
	burstTicks     atomic.Int64
}

// NewCBRPacer creates a CBR pacer targeting the given muxrate in bits/sec.
func NewCBRPacer(muxrateBps int64, tickInterval time.Duration) *CBRPacer {
	if tickInterval <= 0 {
		tickInterval = 10 * time.Millisecond
	}

	bytesPerSec := float64(muxrateBps) / 8.0

	// Pre-allocate null slab: one tick's worth of null packets.
	bytesPerTick := int(bytesPerSec * tickInterval.Seconds())
	nullPktsPerTick := (bytesPerTick + tsPacketSize - 1) / tsPacketSize
	if nullPktsPerTick < 1 {
		nullPktsPerTick = 1
	}

	nullPkt := nullTSPacket()
	nullSlab := make([]byte, nullPktsPerTick*tsPacketSize)
	for i := 0; i < nullPktsPerTick; i++ {
		copy(nullSlab[i*tsPacketSize:], nullPkt[:])
	}

	// Pre-allocate buffers with generous capacity.
	initialCap := bytesPerTick * 2
	if initialCap < 4096 {
		initialCap = 4096
	}

	return &CBRPacer{
		muxrateBps:   muxrateBps,
		bytesPerSec:  bytesPerSec,
		tickInterval: tickInterval,
		buf:          make([]byte, 0, initialCap),
		swapBuf:      make([]byte, 0, initialCap),
		outBuf:       make([]byte, 0, initialCap*2),
		nullSlab:     nullSlab,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// SetMetrics attaches Prometheus metrics to the pacer.
func (p *CBRPacer) SetMetrics(pm *metrics.Metrics) {
	p.prom = pm
}

// SetAdapters sets the SRT adapters that receive paced output.
func (p *CBRPacer) SetAdapters(adapters *[]Adapter) {
	p.adapters.Store(adapters)
}

// Start begins the tick goroutine.
func (p *CBRPacer) Start() {
	p.startTime = time.Now()
	go p.tickLoop()
}

// Stop signals the tick loop to exit, drains remaining data, and waits
// for the goroutine to finish. Safe to call multiple times.
func (p *CBRPacer) Stop() {
	p.stopOnce.Do(func() { close(p.stopCh) })
	<-p.doneCh
}

// Enqueue appends TS data to the accumulation buffer. Called from the
// muxer output callback. Lock hold time is minimal (~50ns for append).
// Input must be TS-packet-aligned (multiple of 188 bytes).
func (p *CBRPacer) Enqueue(tsData []byte) {
	if len(tsData)%tsPacketSize != 0 {
		slog.Warn("CBR pacer: enqueued non-TS-aligned data", "len", len(tsData))
	}
	p.mu.Lock()
	p.buf = append(p.buf, tsData...)
	p.mu.Unlock()
}

// BurstTicks returns the number of ticks where real data exceeded budget.
func (p *CBRPacer) BurstTicks() int64 {
	return p.burstTicks.Load()
}

// NullPacketsTotal returns the total number of null packets inserted.
func (p *CBRPacer) NullPacketsTotal() int64 {
	return p.nullPktsTotal.Load()
}

// RealBytesTotal returns the total real (non-null) bytes sent.
func (p *CBRPacer) RealBytesTotal() int64 {
	return p.realBytesTotal.Load()
}

// PadBytesTotal returns the total null padding bytes sent.
func (p *CBRPacer) PadBytesTotal() int64 {
	return p.padBytesTotal.Load()
}

// tickLoop runs the pacing goroutine. It fires every tickInterval,
// swaps the accumulation buffer, and emits data at the target rate.
func (p *CBRPacer) tickLoop() {
	defer close(p.doneCh)

	ticker := time.NewTicker(p.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			// Drain remaining data.
			p.drainRemaining()
			return
		case <-ticker.C:
			p.tick()
		}
	}
}

// tick performs one pacing cycle.
func (p *CBRPacer) tick() {
	// Swap buffers under lock (~50ns hold time).
	p.mu.Lock()
	p.buf, p.swapBuf = p.swapBuf, p.buf
	p.mu.Unlock()

	// After swap, swapBuf holds the producer's accumulated data.
	// Take ownership and reset for next swap cycle.
	realData := p.swapBuf
	p.swapBuf = p.swapBuf[:0]

	// Compute byte-debt: how many bytes should have been sent by now.
	elapsed := time.Since(p.startTime)
	targetBytes := int64(elapsed.Seconds() * p.bytesPerSec)
	// Align to TS packet boundary.
	targetBytes = (targetBytes / tsPacketSize) * tsPacketSize

	bytesToSend := targetBytes - p.bytesSent
	if bytesToSend < 0 {
		bytesToSend = 0
	}

	realLen := int64(len(realData))

	if realLen >= bytesToSend {
		// Real data exceeds budget — send all of it (burst, never drop).
		p.emit(realData)
		p.bytesSent += realLen
		p.realBytesTotal.Add(realLen)
		p.burstTicks.Add(1)
		if pm := p.prom; pm != nil {
			pm.CBRRealBytesTotal.Add(float64(realLen))
			pm.CBRBurstTicksTotal.Inc()
		}
		return
	}

	// Real data fits within budget — pad with null packets.
	nullBytes := bytesToSend - realLen
	// Align null bytes to TS packet boundary.
	nullBytes = (nullBytes / tsPacketSize) * tsPacketSize

	if nullBytes <= 0 && realLen == 0 {
		// No debt and no data — nothing to send. Byte-debt will
		// accumulate naturally and self-correct on the next tick.
		return
	}

	// Build output into reusable buffer: real data + null padding.
	p.outBuf = p.outBuf[:0]
	if realLen > 0 {
		p.outBuf = append(p.outBuf, realData...)
		p.realBytesTotal.Add(realLen)
	}

	// Append null packets from slab.
	nullRemaining := int(nullBytes)
	for nullRemaining > 0 {
		chunk := len(p.nullSlab)
		if chunk > nullRemaining {
			chunk = nullRemaining
		}
		p.outBuf = append(p.outBuf, p.nullSlab[:chunk]...)
		nullRemaining -= chunk
	}

	nullPkts := nullBytes / tsPacketSize
	p.nullPktsTotal.Add(nullPkts)
	p.padBytesTotal.Add(nullBytes)
	if pm := p.prom; pm != nil {
		if realLen > 0 {
			pm.CBRRealBytesTotal.Add(float64(realLen))
		}
		if nullPkts > 0 {
			pm.CBRNullPacketsTotal.Add(float64(nullPkts))
			pm.CBRPadBytesTotal.Add(float64(nullBytes))
		}
	}
	p.bytesSent += int64(len(p.outBuf))
	p.emit(p.outBuf)
}

// drainRemaining flushes any data left in the buffer on stop.
// Uses the swap pattern to get an independent copy, preventing
// corruption from a late Enqueue call racing with the drain.
func (p *CBRPacer) drainRemaining() {
	p.mu.Lock()
	p.buf, p.swapBuf = p.swapBuf, p.buf
	p.mu.Unlock()

	remaining := p.swapBuf
	p.swapBuf = p.swapBuf[:0]

	if len(remaining) > 0 {
		n := int64(len(remaining))
		p.realBytesTotal.Add(n)
		p.bytesSent += n
		if pm := p.prom; pm != nil {
			pm.CBRRealBytesTotal.Add(float64(n))
		}
		p.emit(remaining)
	}
}

// emit writes data to all configured adapters.
func (p *CBRPacer) emit(data []byte) {
	if len(data) == 0 {
		return
	}

	adapters := p.adapters.Load()
	if adapters == nil {
		return
	}

	for _, a := range *adapters {
		if _, err := a.Write(data); err != nil {
			slog.Error("CBR pacer write error", "adapter", a.ID(), "err", err)
		}
	}
}
