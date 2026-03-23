package htmlgfx

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// FrameHeaderSize is the size of the binary frame header in bytes:
// 1 byte browser_id + 2 bytes width + 2 bytes height.
const FrameHeaderSize = 5

// ControlCommand is a JSON command sent to the CEF sidecar over the
// control socket. Fields use omitempty so only relevant fields appear
// in the serialized JSON for each command type.
type ControlCommand struct {
	Cmd     string `json:"cmd"`
	ID      *int   `json:"id,omitempty"`      // nil for shutdown
	Width   int    `json:"width,omitempty"`    // create only
	Height  int    `json:"height,omitempty"`   // create only
	FPS     int    `json:"fps,omitempty"`      // create only
	URL     string `json:"url,omitempty"`      // load only
	Data    string `json:"data,omitempty"`     // update only
	Action  string `json:"action,omitempty"`   // custom only
	Payload string `json:"payload,omitempty"`  // custom only
}

// ControlResponse is a JSON response received from the CEF sidecar.
type ControlResponse struct {
	ID     int    `json:"id"`
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
	Event  string `json:"event,omitempty"`
	Format string `json:"format,omitempty"`
	Data   string `json:"data,omitempty"`
}

// WriteControlCommand serializes a ControlCommand as a newline-delimited
// JSON message and writes it to w.
func WriteControlCommand(w io.Writer, cmd ControlCommand) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal control command: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// readControlResponse reads a single newline-delimited JSON response
// from r and parses it into a ControlResponse.
func readControlResponse(r io.Reader) (ControlResponse, error) {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return ControlResponse{}, fmt.Errorf("read control response: %w", err)
		}
		return ControlResponse{}, io.EOF
	}

	var resp ControlResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return ControlResponse{}, fmt.Errorf("unmarshal control response: %w", err)
	}
	return resp, nil
}

// Frame represents a single rendered frame from the CEF sidecar.
// Pixels are BGRA premultiplied alpha, row-major.
type Frame struct {
	BrowserID uint8
	Width     uint16
	Height    uint16
	Pixels    []byte // len must equal int(Width) * int(Height) * 4
}

// WriteFrame writes a length-prefixed binary frame to w.
//
// Wire format:
//
//	[4 bytes: total_length (uint32 big-endian)] — length of everything after these 4 bytes
//	[1 byte:  browser_id]
//	[2 bytes: width (uint16 big-endian)]
//	[2 bytes: height (uint16 big-endian)]
//	[width * height * 4 bytes: BGRA pixels]
func WriteFrame(w io.Writer, f Frame) error {
	expectedSize := int(f.Width) * int(f.Height) * 4
	if len(f.Pixels) != expectedSize {
		return fmt.Errorf("pixel data size mismatch: got %d bytes, want %d (width=%d height=%d)",
			len(f.Pixels), expectedSize, f.Width, f.Height)
	}

	totalLen := uint32(FrameHeaderSize + len(f.Pixels))

	// Write length prefix.
	var header [4 + FrameHeaderSize]byte
	binary.BigEndian.PutUint32(header[0:4], totalLen)
	header[4] = f.BrowserID
	binary.BigEndian.PutUint16(header[5:7], f.Width)
	binary.BigEndian.PutUint16(header[7:9], f.Height)

	if _, err := w.Write(header[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if _, err := w.Write(f.Pixels); err != nil {
		return fmt.Errorf("write frame pixels: %w", err)
	}
	return nil
}

// maxFramePixels is the maximum pixel data size in bytes (8K UHD limit: 7680x4320x4).
const maxFramePixels = 7680 * 4320 * 4

// ReadFrame reads a single length-prefixed binary frame from r.
// Returns io.EOF if there is no more data to read.
func ReadFrame(r io.Reader) (Frame, error) {
	// Read length prefix.
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return Frame{}, io.EOF
		}
		return Frame{}, fmt.Errorf("read frame length: %w", err)
	}
	totalLen := binary.BigEndian.Uint32(lenBuf[:])

	if totalLen < uint32(FrameHeaderSize) {
		return Frame{}, fmt.Errorf("frame too small: total_length=%d, minimum=%d", totalLen, FrameHeaderSize)
	}

	// Read header.
	var header [FrameHeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, fmt.Errorf("read frame header: %w", err)
	}

	browserID := header[0]
	width := binary.BigEndian.Uint16(header[1:3])
	height := binary.BigEndian.Uint16(header[3:5])

	pixelLen := totalLen - uint32(FrameHeaderSize)
	expectedPixelLen := uint32(width) * uint32(height) * 4

	// Reject frames exceeding the maximum supported resolution.
	if pixelLen > maxFramePixels {
		if pixelLen > 0 {
			_, _ = io.CopyN(io.Discard, r, int64(pixelLen))
		}
		return Frame{}, fmt.Errorf("frame too large: %d bytes exceeds max %d (width=%d height=%d)",
			pixelLen, maxFramePixels, width, height)
	}

	if pixelLen != expectedPixelLen {
		// Drain remaining bytes to keep the stream in sync.
		if pixelLen > 0 {
			_, _ = io.CopyN(io.Discard, r, int64(pixelLen))
		}
		return Frame{}, fmt.Errorf("pixel data size mismatch: got %d bytes, want %d (width=%d height=%d)",
			pixelLen, expectedPixelLen, width, height)
	}

	pixels := make([]byte, pixelLen)
	if _, err := io.ReadFull(r, pixels); err != nil {
		return Frame{}, fmt.Errorf("read frame pixels: %w", err)
	}

	return Frame{
		BrowserID: browserID,
		Width:     width,
		Height:    height,
		Pixels:    pixels,
	}, nil
}
