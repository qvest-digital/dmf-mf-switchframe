package stinger

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ParseWAV parses a WAV file and returns interleaved float32 PCM samples,
// sample rate, and channel count. Supports PCM int16 (audioFormat=1) and
// IEEE float32 (audioFormat=3).
func ParseWAV(data []byte) ([]float32, int, int, error) {
	if len(data) < 12 {
		return nil, 0, 0, fmt.Errorf("wav: data too short (%d bytes)", len(data))
	}

	// Validate RIFF header
	if string(data[0:4]) != "RIFF" {
		return nil, 0, 0, fmt.Errorf("wav: not a RIFF file")
	}
	if string(data[8:12]) != "WAVE" {
		return nil, 0, 0, fmt.Errorf("wav: not a WAVE file")
	}

	// Scan chunks for "fmt " and "data"
	var (
		audioFormat   uint16
		numChannels   uint16
		sampleRate    uint32
		bitsPerSample uint16
		fmtFound      bool
		dataChunk     []byte
	)

	pos := 12 // skip RIFF header
	for pos+8 <= len(data) {
		chunkID := string(data[pos : pos+4])
		chunkSize := binary.LittleEndian.Uint32(data[pos+4 : pos+8])
		chunkDataStart := pos + 8

		if int(chunkSize) < 0 || chunkDataStart+int(chunkSize) > len(data) {
			// Truncated chunk — stop scanning
			break
		}

		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, 0, 0, fmt.Errorf("wav: fmt chunk too small (%d bytes)", chunkSize)
			}
			fmtData := data[chunkDataStart : chunkDataStart+int(chunkSize)]
			audioFormat = binary.LittleEndian.Uint16(fmtData[0:2])
			numChannels = binary.LittleEndian.Uint16(fmtData[2:4])
			sampleRate = binary.LittleEndian.Uint32(fmtData[4:8])
			// bytes 8-11: byteRate (skip)
			// bytes 12-13: blockAlign (skip)
			bitsPerSample = binary.LittleEndian.Uint16(fmtData[14:16])
			fmtFound = true

		case "data":
			dataChunk = data[chunkDataStart : chunkDataStart+int(chunkSize)]
		}

		// Advance to next chunk (word-aligned)
		advance := int(chunkSize)
		if advance%2 != 0 {
			advance++ // RIFF chunks are word-aligned
		}
		pos = chunkDataStart + advance
	}

	if !fmtFound {
		return nil, 0, 0, fmt.Errorf("wav: no fmt chunk found")
	}
	if dataChunk == nil {
		return nil, 0, 0, fmt.Errorf("wav: no data chunk found")
	}

	// Convert samples to float32
	switch audioFormat {
	case 1: // PCM int16
		if bitsPerSample != 16 {
			return nil, 0, 0, fmt.Errorf("wav: unsupported PCM bits per sample: %d (only 16 supported)", bitsPerSample)
		}
		return decodeInt16PCM(dataChunk), int(sampleRate), int(numChannels), nil

	case 3: // IEEE float
		if bitsPerSample != 32 {
			return nil, 0, 0, fmt.Errorf("wav: unsupported IEEE float bits per sample: %d (only 32 supported)", bitsPerSample)
		}
		return decodeFloat32PCM(dataChunk), int(sampleRate), int(numChannels), nil

	default:
		return nil, 0, 0, fmt.Errorf("wav: unsupported audio format: %d (only PCM=1 and IEEE float=3 supported)", audioFormat)
	}
}

// decodeInt16PCM converts little-endian int16 samples to float32 [-1.0, 1.0).
func decodeInt16PCM(data []byte) []float32 {
	numSamples := len(data) / 2
	pcm := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		sample := int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
		pcm[i] = float32(sample) / 32768.0
	}
	return pcm
}

// decodeFloat32PCM converts little-endian IEEE float32 samples.
func decodeFloat32PCM(data []byte) []float32 {
	numSamples := len(data) / 4
	pcm := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		bits := binary.LittleEndian.Uint32(data[i*4 : i*4+4])
		pcm[i] = math.Float32frombits(bits)
	}
	return pcm
}
