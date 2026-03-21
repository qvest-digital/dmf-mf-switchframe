package stmap

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ReadRaw reads an ST map from raw float32 binary format.
// Format: [uint32 BE width][uint32 BE height][float32 LE S[w*h]][float32 LE T[w*h]]
func ReadRaw(data []byte, name string) (*STMap, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("stmap: raw data too short for header (%d bytes)", len(data))
	}

	w := int(binary.BigEndian.Uint32(data[0:4]))
	h := int(binary.BigEndian.Uint32(data[4:8]))

	if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 {
		return nil, ErrInvalidDimensions
	}

	n := w * h
	expected := 8 + n*4*2
	if len(data) < expected {
		return nil, fmt.Errorf("stmap: raw data too short: have %d bytes, need %d", len(data), expected)
	}

	s := make([]float32, n)
	t := make([]float32, n)

	off := 8
	for i := 0; i < n; i++ {
		s[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4]))
		off += 4
	}
	for i := 0; i < n; i++ {
		t[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4]))
		off += 4
	}

	return &STMap{
		Name:   name,
		Width:  w,
		Height: h,
		S:      s,
		T:      t,
	}, nil
}

// WriteRaw writes an ST map to raw float32 binary format.
// Format: [uint32 BE width][uint32 BE height][float32 LE S[w*h]][float32 LE T[w*h]]
func WriteRaw(m *STMap) ([]byte, error) {
	n := m.Width * m.Height
	buf := make([]byte, 8+n*4*2)

	binary.BigEndian.PutUint32(buf[0:4], uint32(m.Width))
	binary.BigEndian.PutUint32(buf[4:8], uint32(m.Height))

	off := 8
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint32(buf[off:off+4], math.Float32bits(m.S[i]))
		off += 4
	}
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint32(buf[off:off+4], math.Float32bits(m.T[i]))
		off += 4
	}

	return buf, nil
}
