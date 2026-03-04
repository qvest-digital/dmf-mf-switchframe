package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// mockDecoder implements AudioDecoder for testing.
type mockDecoder struct {
	calls   int
	samples []float32
}

func (m *mockDecoder) Decode(aacFrame []byte) ([]float32, error) {
	m.calls++
	return m.samples, nil
}
func (m *mockDecoder) Close() error { return nil }

// mockEncoder implements AudioEncoder for testing.
type mockEncoder struct {
	calls int
	data  []byte
}

func (m *mockEncoder) Encode(pcm []float32) ([]byte, error) {
	m.calls++
	return m.data, nil
}
func (m *mockEncoder) Close() error { return nil }

func TestCodecInterfaces(t *testing.T) {
	var dec AudioDecoder = &mockDecoder{samples: []float32{0.5, -0.5}}
	samples, err := dec.Decode([]byte{0x01})
	require.NoError(t, err)
	require.Equal(t, []float32{0.5, -0.5}, samples)

	var enc AudioEncoder = &mockEncoder{data: []byte{0xFF}}
	data, err := enc.Encode([]float32{0.5})
	require.NoError(t, err)
	require.Equal(t, []byte{0xFF}, data)
}
