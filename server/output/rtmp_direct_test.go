//go:build cgo && !noffmpeg

package output

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestRTMPDirect_NewRequiresURL(t *testing.T) {
	t.Parallel()
	_, err := NewRTMPDirect(RTMPCallerConfig{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "URL is required")
}

func TestRTMPDirect_ID(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	require.Equal(t, "rtmp-direct", r.ID())
}

func TestRTMPDirect_StatusBeforeStart(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	status := r.Status()
	require.Equal(t, StateStopped, status.State)
}

func TestRTMPDirect_WriteVideoBeforeStart(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	frame := &media.VideoFrame{PTS: 90000, IsKeyframe: true, WireData: []byte{0x00, 0x00, 0x00, 0x01}}
	writeErr := r.WriteVideo(frame)
	require.Error(t, writeErr)
}

func TestRTMPDirect_StartWithMockConnect(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	r.connectFn = func(url string) error {
		return nil
	}

	err = r.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	status := r.Status()
	require.Equal(t, StateActive, status.State)
}

func TestRTMPDirect_DropsNonKeyframeBeforeInit(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	r.connectFn = func(url string) error { return nil }

	err = r.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	// Send a non-keyframe — should be silently dropped.
	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: false,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00},
	}
	writeErr := r.WriteVideo(frame)
	require.NoError(t, writeErr)

	// Give the write loop time to process.
	time.Sleep(50 * time.Millisecond)

	require.False(t, r.initialized.Load(), "should not initialize on non-keyframe")
}

func TestRTMPDirect_InitOnKeyframe(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	r.connectFn = func(url string) error { return nil }

	err = r.Start(context.Background())
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	sps := []byte{0x67, 0x42, 0xc0, 0x1e}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		SPS:        sps,
		PPS:        pps,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00},
	}
	writeErr := r.WriteVideo(frame)
	require.NoError(t, writeErr)

	require.Eventually(t, func() bool {
		return r.initialized.Load()
	}, 2*time.Second, 10*time.Millisecond, "should initialize on keyframe with SPS/PPS")
}

func TestRTMPDirect_ADTSStripping(t *testing.T) {
	t.Parallel()
	// Build ADTS header (7 bytes) + raw AAC payload.
	adtsHeader := []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}
	rawAAC := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	data := append(adtsHeader, rawAAC...)

	stripped := stripADTS(data)
	require.Equal(t, rawAAC, stripped)
}

func TestRTMPDirect_ADTSStripping_NoHeader(t *testing.T) {
	t.Parallel()
	rawData := []byte{0x01, 0x02, 0x03, 0x04}
	stripped := stripADTS(rawData)
	require.Equal(t, rawData, stripped, "non-ADTS data should pass through unchanged")
}

func TestRTMPDirect_WriteDropsDuringReconnect(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	// Manually set state to reconnecting without actually starting.
	r.state.Store(ptrTo(StateReconnecting))
	r.videoCh = make(chan *media.VideoFrame, rtmpVideoChSize)
	r.audioCh = make(chan *media.AudioFrame, rtmpAudioChSize)
	r.stopCh = make(chan struct{})

	frame := &media.VideoFrame{PTS: 90000, IsKeyframe: true, WireData: []byte{0x00}}
	writeErr := r.WriteVideo(frame)
	require.NoError(t, writeErr, "write during reconnect should return nil (silently drop)")
}

func TestRTMPDirect_RTMPStatusSnapshot(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/secret-key"})
	require.NoError(t, err)

	snap := r.RTMPStatusSnapshot()
	require.False(t, snap.Active)
	require.Equal(t, "rtmp://example.com/live/****", snap.URL)

	r.state.Store(ptrTo(StateActive))
	snap = r.RTMPStatusSnapshot()
	require.True(t, snap.Active)
	require.Equal(t, "active", snap.State)

	r.state.Store(ptrTo(StateReconnecting))
	snap = r.RTMPStatusSnapshot()
	require.True(t, snap.Active)
	require.Equal(t, "reconnecting", snap.State)
}

func TestRTMPDirect_ImplementsFrameAdapter(t *testing.T) {
	t.Parallel()
	var _ FrameAdapter = (*RTMPDirect)(nil)
}

func TestRTMPDirect_CloseWithoutStart(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)
	closeErr := r.Close()
	require.NoError(t, closeErr)
}

func TestRTMPDirect_BackoffProgression(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	levels := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for _, expected := range levels {
		got := r.nextBackoff()
		lo := time.Duration(float64(expected) * 0.79)
		hi := time.Duration(float64(expected) * 1.21)
		require.True(t, got >= lo && got <= hi,
			"expected backoff near %v (+/-20%%), got %v", expected, got)
	}
}

func TestRTMPDirect_ChannelOverflowCountsDrops(t *testing.T) {
	t.Parallel()
	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "rtmp://example.com/live/key"})
	require.NoError(t, err)

	// Set up channels and state manually (no writeLoop draining them).
	r.videoCh = make(chan *media.VideoFrame, rtmpVideoChSize)
	r.audioCh = make(chan *media.AudioFrame, rtmpAudioChSize)
	r.stopCh = make(chan struct{})
	r.state.Store(ptrTo(StateActive))

	// Fill the video channel completely.
	for i := 0; i < rtmpVideoChSize; i++ {
		frame := &media.VideoFrame{
			PTS:      int64(i * 3000),
			WireData: []byte{0x00},
		}
		_ = r.WriteVideo(frame)
	}

	// The next write should overflow and count the drop.
	overflowFrame := &media.VideoFrame{
		PTS:      int64(rtmpVideoChSize * 3000),
		WireData: []byte{0x00},
	}
	_ = r.WriteVideo(overflowFrame)

	require.Equal(t, int64(1), r.videoDropped.Load(),
		"expected 1 video drop, got %d", r.videoDropped.Load())

	// Fill the audio channel completely.
	for i := 0; i < rtmpAudioChSize; i++ {
		frame := &media.AudioFrame{
			PTS:  int64(i * 3000),
			Data: []byte{0x00},
		}
		_ = r.WriteAudio(frame)
	}

	// The next write should overflow.
	overflowAudio := &media.AudioFrame{
		PTS:  int64(rtmpAudioChSize * 3000),
		Data: []byte{0x00},
	}
	_ = r.WriteAudio(overflowAudio)

	require.Equal(t, int64(1), r.audioDropped.Load(),
		"expected 1 audio drop, got %d", r.audioDropped.Load())
}

func TestParseSPSDimensions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		sps           []byte
		wantW, wantH  int
	}{
		{
			// x264 Baseline 1920x1080 (no cropping needed, 120 MBs × 68 map units × 2)
			// But 68*16=1088, not 1080 → needs frame_crop_bottom_offset=4
			// Real x264 SPS for 1920x1080 Baseline:
			name: "x264 Baseline 1920x1080",
			sps: []byte{
				0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02,
				0x27, 0xe5, 0xc0, 0x44, 0x00, 0x00, 0x03, 0x00,
				0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60,
				0xc6, 0x58,
			},
			wantW: 1920,
			wantH: 1080,
		},
		{
			// x264 High profile 1280x720 (no cropping, 80×45 MBs)
			name: "x264 High 1280x720",
			sps: []byte{
				0x67, 0x64, 0x00, 0x1f, 0xac, 0xd9, 0x40, 0x50,
				0x05, 0xbb, 0x01, 0x10, 0x00, 0x00, 0x03, 0x00,
				0x10, 0x00, 0x00, 0x03, 0x03, 0xc0, 0xf1, 0x62,
				0xe4, 0x80,
			},
			wantW: 1280,
			wantH: 720,
		},
		{
			// Minimal Baseline SPS (too short for full parse → fallback)
			name: "minimal fallback",
			sps:  []byte{0x67, 0x42, 0xc0, 0x1e},
			wantW: 1920,
			wantH: 1080,
		},
		{
			name:  "nil SPS",
			sps:   nil,
			wantW: 1920,
			wantH: 1080,
		},
		{
			name:  "empty SPS",
			sps:   []byte{},
			wantW: 1920,
			wantH: 1080,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w, h := parseSPSDimensions(tt.sps)
			require.Equal(t, tt.wantW, w, "width")
			require.Equal(t, tt.wantH, h, "height")
		})
	}
}
