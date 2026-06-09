package gpu

import "testing"

func TestValidateLUT3D(t *testing.T) {
	// Helper: a correctly-sized flat LUT for a given cube dimension.
	lut := func(size int) []float32 {
		return make([]float32, size*size*size*3)
	}

	tests := []struct {
		name    string
		data    []float32
		size    int
		wantErr bool
	}{
		{"valid size 2", lut(2), 2, false},
		{"valid size 17", lut(17), 17, false},
		{"valid size 33", lut(33), 33, false},
		{"valid size 64 (max)", lut(64), 64, false},
		// Mismatch: declared size larger than the data supplied -> kernel would
		// read past the device allocation. Must fail closed.
		{"data too small for declared size", lut(2), 33, true},
		{"data too large for declared size", lut(33), 2, true},
		// One float short of size^3*3 must still be rejected (off-by-one OOB).
		{"one float short", lut(33)[:33*33*33*3-1], 33, true},
		// Out-of-range cube dimensions.
		{"size 0", nil, 0, true},
		{"size 1 (below min)", lut(1), 1, true},
		{"size 65 (above max)", lut(65), 65, true},
		{"negative size", nil, -1, true},
		// Empty data with a plausible size.
		{"empty data nonzero size", nil, 17, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLUT3D(tt.data, tt.size)
			if tt.wantErr && err == nil {
				t.Fatalf("validateLUT3D(len=%d, size=%d) = nil, want error", len(tt.data), tt.size)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateLUT3D(len=%d, size=%d) = %v, want nil", len(tt.data), tt.size, err)
			}
		})
	}
}
