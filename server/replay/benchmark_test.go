package replay

import (
	"testing"
	"time"

	"github.com/zsiec/prism/media"
)

func BenchmarkReplayBuffer_RecordFrame(b *testing.B) {
	buf := newReplayBuffer(60, 0)
	frame := &media.VideoFrame{
		PTS:        0,
		IsKeyframe: true,
		WireData:   makeAVC1Data(10000),
		SPS:        []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:        []byte{0x68, 0xCE, 0x38, 0x80},
	}

	b.ResetTimer()
	// Reuse frame object — RecordFrame deep-copies all data,
	// so mutating PTS/IsKeyframe between iterations is safe.
	for i := 0; i < b.N; i++ {
		frame.PTS = int64(i) * 3003
		frame.IsKeyframe = i%30 == 0
		buf.RecordFrame(frame)
	}
}

func BenchmarkReplayBuffer_ExtractClip(b *testing.B) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()

	// Pre-fill buffer with 1800 frames (~60s at 30fps).
	for i := 0; i < 1800; i++ {
		frame := &media.VideoFrame{
			PTS:        int64(i) * 3003,
			IsKeyframe: i%30 == 0,
			WireData:   makeAVC1Data(5000),
		}
		if i%30 == 0 {
			frame.SPS = []byte{0x67, 0x42, 0xC0, 0x1E}
			frame.PPS = []byte{0x68, 0xCE, 0x38, 0x80}
		}
		buf.recordFrameAt(frame, now.Add(time.Duration(i)*33*time.Millisecond))
	}

	inTime := now.Add(20 * time.Second)
	outTime := now.Add(30 * time.Second)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = buf.ExtractClip(inTime, outTime)
	}
}

func BenchmarkReplayViewer_SendVideo(b *testing.B) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("bench", buf)
	frame := &media.VideoFrame{
		PTS:        0,
		IsKeyframe: true,
		WireData:   makeAVC1Data(5000),
		SPS:        []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:        []byte{0x68, 0xCE, 0x38, 0x80},
	}

	b.ResetTimer()
	// Reuse frame object — SendVideo deep-copies all data,
	// so mutating PTS/IsKeyframe between iterations is safe.
	for i := 0; i < b.N; i++ {
		frame.PTS = int64(i) * 3003
		frame.IsKeyframe = i%30 == 0
		v.SendVideo(frame)
	}
}
