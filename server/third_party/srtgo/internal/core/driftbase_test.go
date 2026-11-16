package core_test

import (
	"testing"

	"github.com/zsiec/srtgo/internal/clock"
	"github.com/zsiec/srtgo/internal/core"
	"github.com/zsiec/srtgo/internal/packet"
)

// controlSamplers feed a connection one control packet that carries the
// sender's clock, ts, received late after now, on each path that samples
// TSBPD drift and wrap state from one.
var controlSamplers = []struct {
	name   string
	sample func(c *core.Conn, now clock.Timestamp, ts uint32, late clock.Microseconds)
}{
	{"keepalive", func(c *core.Conn, now clock.Timestamp, ts uint32, late clock.Microseconds) {
		c.HandlePacket(now.Add(late), packet.NewControl(nil, packet.CtrlTypeKeepalive, 1, ts))
		drainOutputs(c)
	}},
	// The receiver sends a full ACK every SYN, data or not, and the
	// sender's ACKACK carries the sender's clock.
	{"ackack", func(c *core.Conn, now clock.Timestamp, ts uint32, late clock.Microseconds) {
		c.HandleTimer(now, core.TimerACK)
		for _, ackNo := range sentACKs(c) {
			ackack := packet.NewControl(nil, packet.CtrlTypeACKACK, 1, ts)
			ackack.Header.TypeSpecific = ackNo
			c.HandlePacket(now.Add(late), ackack)
		}
		drainOutputs(c)
	}},
}

// A connection whose clock is old when it is created, as every connection a
// listener accepts is (they share the listener's clock), must measure no
// drift from the control packets that arrive before its first data packet.
// Until that packet sets the TSBPD time base, the base is 0, so such a sample
// reads the clock's whole age as drift; averaged into the first epoch it
// holds every packet back by a share of that age, here 10 samples of 2000 s
// over a 1000-sample epoch, 20 s. The samples after the base is set still
// count: the sender's control packets arrive 8 ms later against their
// timestamps than its data did, which the tracer corrects for as 5 ms on
// the time base and 3 ms of drift.
func TestDriftNotSampledBeforeTimeBase(t *testing.T) {
	const skew = 8 * clock.Millisecond
	for _, tc := range controlSamplers {
		t.Run(tc.name, func(t *testing.T) {
			base := clock.Timestamp(2000 * clock.Second) // the listener's age at accept
			const rcvISN = 5000
			c := core.NewEstablished(core.Config{
				PeerSocketID: 1, PayloadSize: 1200, SendISN: 100, RecvISN: rcvISN,
				MaxBW: 1 << 34, Live: true, TsbpdDelay: 120_000, BufferCapacity: 4096,
			}, base)
			drainOutputs(c)

			// The sender's clock starts at the connection; its encoder takes
			// 100 ms to send the first data packet.
			ts := uint32(0)
			for ; ts < 100_000; ts += 10_000 {
				tc.sample(c, base.Add(clock.Microseconds(ts)), ts, 0)
			}
			// Then data, with a full epoch of samples.
			for i := 0; i < 1100; i++ {
				now := base.Add(clock.Microseconds(ts))
				c.HandlePacket(now, packet.NewData(nil, uint32(rcvISN+i), ts, 1, make([]byte, 100)))
				tc.sample(c, now, ts, skew)
				ts += 10_000
			}

			if d := c.Stats().DriftMicros; d != skew-5*clock.Millisecond {
				t.Fatalf("DriftMicros = %d us, want %d: the epoch averaged samples taken before the time base, which read the clock's age as drift, or none taken after it", d, skew-5*clock.Millisecond)
			}
		})
	}
}

// A wrap of the sender's 32-bit timestamp that control packets show before
// the first data packet must not count. The time base comes from that
// packet's raw timestamp, which carries no wrap, so a wrap counted before
// it adds 2^32 us, about 71.6 min, to every packet's playout time. The case
// needs a sender warmup longer than about 71.6 min. Here the sender's clock
// is 20 s short of the wrap at its first control packet and past it at its
// second, and its first data packet follows 21 s later.
func TestNoWrapBeforeTimeBase(t *testing.T) {
	const maxTimestamp = 0xFFFFFFFF
	for _, tc := range controlSamplers {
		t.Run(tc.name, func(t *testing.T) {
			base := clock.Timestamp(2000 * clock.Second)
			const (
				rcvISN = 5000
				delay  = 120 * clock.Millisecond
			)
			c := core.NewEstablished(core.Config{
				PeerSocketID: 1, PayloadSize: 1200, SendISN: 100, RecvISN: rcvISN,
				MaxBW: 1 << 34, Live: true, TsbpdDelay: delay, BufferCapacity: 4096,
			}, base)
			drainOutputs(c)

			tc.sample(c, base, maxTimestamp-uint32(20*clock.Second), 0)
			tc.sample(c, base.Add(60*clock.Second), uint32(40*clock.Second), 0)
			now := base.Add(81 * clock.Second)
			c.HandlePacket(now, packet.NewData(nil, rcvISN, uint32(61*clock.Second), 1, make([]byte, 100)))
			drainOutputs(c)

			now = now.Add(delay + clock.Millisecond)
			c.HandleTimer(now, core.TimerTSBPD)
			drainOutputs(c)
			for {
				e, ok := c.PollEvent()
				if !ok {
					t.Fatalf("data packet not delivered %d us after it arrived, past the %d us latency: its playout time counts a wrap seen before the time base", delay+clock.Millisecond, delay)
				}
				if _, ok := e.(core.DataReceived); ok {
					return
				}
			}
		})
	}
}

// sentACKs returns the ACK numbers of the full ACKs among c's pending
// outputs, and discards the rest.
func sentACKs(c *core.Conn) []uint32 {
	var acks []uint32
	for {
		o, ok := c.PollOutput()
		if !ok {
			return acks
		}
		if sp, ok := o.(core.SendPacket); ok {
			if sp.Packet.Header.IsControl && sp.Packet.Header.ControlType == packet.CtrlTypeACK {
				acks = append(acks, sp.Packet.Header.TypeSpecific)
			}
			if sp.Owned {
				sp.Packet.Release()
			}
		}
	}
}
