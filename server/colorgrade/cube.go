package colorgrade

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// LUT3D holds a parsed 3D color lookup table.
type LUT3D struct {
	Title string
	Size  int       // cube dimension (e.g., 33)
	Data  []float32 // R,G,B triplets in red-major order (Size^3 * 3 floats)
}

// ParseCube parses a .cube LUT file from the given reader.
// The .cube format is a simple text format:
//   - Lines starting with # are comments
//   - TITLE "name" is optional metadata
//   - LUT_3D_SIZE N declares cube dimension (typically 33)
//   - DOMAIN_MIN/DOMAIN_MAX are optional (default 0.0-1.0)
//   - Remaining lines are R G B float triplets, red-major order
//   - Total data lines = N^3
func ParseCube(r io.Reader) (*LUT3D, error) {
	scanner := bufio.NewScanner(r)
	// Increase buffer size for large lines (shouldn't be needed for .cube but be safe).
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	lut := &LUT3D{}
	sizeSet := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "TITLE") {
			// TITLE "name" or TITLE name
			title := strings.TrimPrefix(line, "TITLE")
			title = strings.TrimSpace(title)
			title = strings.Trim(title, "\"")
			lut.Title = title
			continue
		}

		if strings.HasPrefix(line, "LUT_3D_SIZE") {
			parts := strings.Fields(line)
			if len(parts) != 2 {
				return nil, fmt.Errorf("colorgrade: invalid LUT_3D_SIZE line: %q", line)
			}
			size, err := strconv.Atoi(parts[1])
			if err != nil {
				return nil, fmt.Errorf("colorgrade: invalid LUT_3D_SIZE value: %w", err)
			}
			if size < 2 || size > 256 {
				return nil, fmt.Errorf("colorgrade: LUT_3D_SIZE %d out of range [2, 256]", size)
			}
			lut.Size = size
			sizeSet = true
			lut.Data = make([]float32, 0, size*size*size*3)
			continue
		}

		// Skip DOMAIN_MIN, DOMAIN_MAX, LUT_1D_SIZE, and other metadata keywords.
		if strings.HasPrefix(line, "DOMAIN_MIN") ||
			strings.HasPrefix(line, "DOMAIN_MAX") ||
			strings.HasPrefix(line, "LUT_1D_SIZE") ||
			strings.HasPrefix(line, "LUT_1D_INPUT_RANGE") ||
			strings.HasPrefix(line, "LUT_3D_INPUT_RANGE") {
			continue
		}

		// Must be a data line (R G B triplet).
		if !sizeSet {
			return nil, fmt.Errorf("colorgrade: data line before LUT_3D_SIZE declaration")
		}

		parts := strings.Fields(line)
		if len(parts) != 3 {
			return nil, fmt.Errorf("colorgrade: expected 3 values per line, got %d: %q", len(parts), line)
		}

		for _, p := range parts {
			v, err := strconv.ParseFloat(p, 32)
			if err != nil {
				return nil, fmt.Errorf("colorgrade: invalid float value %q: %w", p, err)
			}
			lut.Data = append(lut.Data, float32(v))
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("colorgrade: read error: %w", err)
	}

	if !sizeSet {
		return nil, fmt.Errorf("colorgrade: missing LUT_3D_SIZE declaration")
	}

	expected := lut.Size * lut.Size * lut.Size * 3
	if len(lut.Data) != expected {
		return nil, fmt.Errorf("colorgrade: expected %d values (%d^3 * 3), got %d",
			expected, lut.Size, len(lut.Data))
	}

	return lut, nil
}
