// Package st2110 provides SMPTE ST 2110 professional media transport
// via Intel Media Transport Library (MTL). Builds without the st2110 tag
// get stub implementations that return ErrST2110NotAvailable.
package st2110

import "errors"

// ErrST2110NotAvailable is returned by stub constructors when the st2110
// build tag is not set.
var ErrST2110NotAvailable = errors.New("ST 2110 support not compiled (build with -tags st2110)")

// VideoFormat describes an ST 2110-20 video essence.
type VideoFormat struct {
	Width       int
	Height      int
	FPSNum      int
	FPSDen      int
	Sampling    string // e.g. "YCbCr-4:2:2"
	Colorimetry string // e.g. "BT709"
	BitDepth    int    // 8 or 10
	Compressed  bool   // ST 2110-22 (JPEG XS) when true
}

// AudioFormat describes an ST 2110-30 audio essence.
type AudioFormat struct {
	SampleRate   int // typically 48000
	Channels     int
	BitDepth     int // typically 24 (L24)
	PacketTimeUs int // packet time in microseconds (125 or 1000)
}

// AncillaryFormat describes an ST 2110-40 ancillary data essence.
type AncillaryFormat struct {
	FPSNum int
	FPSDen int
}

// RxVideoConfig configures an ST 2110-20 video receiver session.
type RxVideoConfig struct {
	MulticastIP  string
	SourceIP     string
	Port         int
	Format       VideoFormat
	FrameBuffers int
}

// TxVideoConfig configures an ST 2110-20 video transmitter session.
type TxVideoConfig struct {
	MulticastIP  string
	SourceIP     string
	Port         int
	Format       VideoFormat
	FrameBuffers int
}

// RxAudioConfig configures an ST 2110-30 audio receiver session.
type RxAudioConfig struct {
	MulticastIP  string
	SourceIP     string
	Port         int
	Format       AudioFormat
	FrameBuffers int
}

// TxAudioConfig configures an ST 2110-30 audio transmitter session.
type TxAudioConfig struct {
	MulticastIP  string
	SourceIP     string
	Port         int
	Format       AudioFormat
	FrameBuffers int
}

// RxAncillaryConfig configures an ST 2110-40 ancillary data receiver session.
type RxAncillaryConfig struct {
	MulticastIP string
	SourceIP    string
	Port        int
	Format      AncillaryFormat
}

// TxAncillaryConfig configures an ST 2110-40 ancillary data transmitter session.
type TxAncillaryConfig struct {
	MulticastIP string
	SourceIP    string
	Port        int
	Format      AncillaryFormat
}

// InstanceConfig configures the MTL library instance.
type InstanceConfig struct {
	Port     string // DPDK port identifier (e.g. "0000:31:00.0")
	SourceIP string // local IP address for TX sessions
	Netmask  string // subnet mask
	Gateway  string // default gateway
	TxQueues int    // number of TX queues
	RxQueues int    // number of RX queues
	PTPMode  string // PTP clock mode (e.g. "auto", "l2", "l4")
	LogLevel int    // MTL log level (0=error, 7=debug)
	LCores   string // DPDK lcore list (e.g. "2,3,4,5")
}

// PTPStatus reports the current PTP synchronization state.
type PTPStatus struct {
	Locked        bool
	GrandmasterID string
	OffsetNs      int64
	Domain        int
}

// SessionStats provides per-session receive statistics.
type SessionStats struct {
	FramesReceived uint64
	FramesDropped  uint64
	FramesCorrupt  uint64
	PacketsLost    uint64
}
