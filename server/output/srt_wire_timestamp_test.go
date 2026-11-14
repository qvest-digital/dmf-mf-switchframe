package output

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	srt "github.com/zsiec/srtgo"
)

// A libsrt receiver corrects its play-out clock from the timestamps of the
// sender's ACKACK packets and compares them with the data packets'. An
// ACKACK stamped on a clock other than the connection's data epoch reads as
// clock drift: the receiver's latency collapses, and when that clock passes
// 2^32 us (about 71.6 minutes) every receiver connected to the listener
// stops delivering until it reconnects. The listener's clock starts when it
// binds, long before a client connects, so the two epochs must not differ
// by how long the listener has been up.
func TestSRTListenerACKACKSharesDataEpoch(t *testing.T) {
	const listenerAge = 1500 * time.Millisecond

	l, addr := startSRTListenerOutput(t)

	time.Sleep(listenerAge)

	px := newSRTTimestampProxy(t, addr)
	defer px.close()

	conn, err := srt.Dial(px.addr(), srt.Config{Latency: 120 * time.Millisecond, ConnTimeout: 3 * time.Second})
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()

	require.Eventually(t, func() bool { return l.ConnectionCount() == 1 }, 3*time.Second, 10*time.Millisecond)

	payload := make([]byte, 1316)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(px.pairs()) < 20 {
		_, err := l.Write(payload)
		require.NoError(t, err)
		time.Sleep(5 * time.Millisecond)
	}

	pairs := px.pairs()
	require.GreaterOrEqual(t, len(pairs), 20, "too few ACKACKs following a data packet")
	for _, p := range pairs {
		gap := time.Duration(int32(p.ackack-p.data)) * time.Microsecond
		require.Less(t, gap.Abs(), 250*time.Millisecond,
			"ACKACK timestamp %d us vs preceding data timestamp %d us", p.ackack, p.data)
	}
}

// srtTimestampProxy relays UDP between one SRT caller and a listener and
// records the header timestamps of the packets the listener sends.
type srtTimestampProxy struct {
	pc       *net.UDPConn
	upstream *net.UDPAddr

	mu       sync.Mutex
	client   *net.UDPAddr
	lastData uint32
	haveData bool
	recorded []tsPair
}

type tsPair struct{ data, ackack uint32 }

func newSRTTimestampProxy(t *testing.T, upstream *net.UDPAddr) *srtTimestampProxy {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	p := &srtTimestampProxy{pc: pc, upstream: upstream}
	go p.run()
	return p
}

func (p *srtTimestampProxy) addr() string { return p.pc.LocalAddr().String() }

func (p *srtTimestampProxy) close() { _ = p.pc.Close() }

func (p *srtTimestampProxy) run() {
	buf := make([]byte, 2048)
	for {
		n, from, err := p.pc.ReadFromUDP(buf)
		if err != nil {
			return
		}
		pkt := buf[:n]
		if from.Port == p.upstream.Port && from.IP.Equal(p.upstream.IP) {
			p.record(pkt)
			p.mu.Lock()
			client := p.client
			p.mu.Unlock()
			if client != nil {
				_, _ = p.pc.WriteToUDP(pkt, client)
			}
			continue
		}
		p.mu.Lock()
		p.client = from
		p.mu.Unlock()
		_, _ = p.pc.WriteToUDP(pkt, p.upstream)
	}
}

// record notes the timestamp of a data packet, and pairs each ACKACK with
// the data packet sent just before it.
func (p *srtTimestampProxy) record(pkt []byte) {
	if len(pkt) < 16 {
		return
	}
	ts := binary.BigEndian.Uint32(pkt[8:12])
	p.mu.Lock()
	defer p.mu.Unlock()
	if pkt[0]&0x80 == 0 {
		p.lastData, p.haveData = ts, true
		return
	}
	const ctrlACKACK = 0x0006
	if binary.BigEndian.Uint16(pkt[0:2])&0x7fff != ctrlACKACK {
		return
	}
	if p.haveData {
		p.recorded = append(p.recorded, tsPair{data: p.lastData, ackack: ts})
	}
}

func (p *srtTimestampProxy) pairs() []tsPair {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]tsPair(nil), p.recorded...)
}
