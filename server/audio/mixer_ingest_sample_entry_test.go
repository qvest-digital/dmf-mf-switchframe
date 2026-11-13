//go:build cgo

package audio

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/clip"
)

// ISO 14496-12 lets an MP4 audio sample entry give its channelcount as 2
// whatever the stream carries; the AudioSpecificConfig says what it
// carries. The 7.1 fixture with its sample entry rewritten to 2 channels
// was framed in ADTS as stereo, and every frame failed to decode: the
// clip aired silent.
func TestMixer_IngestFrame_DecodesAACWhoseSampleEntrySaysStereo(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/aac-7.1.mp4")
	require.NoError(t, err)
	i := bytes.Index(data, []byte("mp4a"))
	require.Positive(t, i)
	// AudioSampleEntry: 6 reserved bytes, data_reference_index, 8 reserved
	// bytes, then channelcount.
	cc := i + 4 + 6 + 2 + 8
	require.Equal(t, uint16(8), binary.BigEndian.Uint16(data[cc:]))
	binary.BigEndian.PutUint16(data[cc:], 2)
	path := filepath.Join(t.TempDir(), "sample-entry-2.mp4")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	_, frames, err := clip.DemuxFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, frames)

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		DecoderFactory: func(sr, ch int) (Decoder, error) {
			return NewFDKDecoder(sr, ch)
		},
	})
	defer func() { _ = m.Close() }()
	m.AddChannel("clip:1")
	m.SetActive("clip:1", true)
	for _, f := range frames {
		m.IngestFrame("clip:1", &media.AudioFrame{Data: f.Data(), SampleRate: f.SampleRate(), Channels: f.Channels()})
	}
	require.Zero(t, m.decodeErrors.Load(), "frames failed to decode")
	m.mu.RLock()
	got := m.channels["clip:1"].ringBuf.Len()
	m.mu.RUnlock()
	require.Greater(t, got, 2*4096, "the clip's audio did not reach the ring")
}
