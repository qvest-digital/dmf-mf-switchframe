package colorgrade

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateIdentityLUT(t *testing.T) {
	lut := GenerateIdentityLUT()

	if lut.Size != 33 {
		t.Fatalf("size = %d, want 33", lut.Size)
	}

	expected := 33 * 33 * 33 * 3
	if len(lut.Data) != expected {
		t.Fatalf("data length = %d, want %d", len(lut.Data), expected)
	}

	// Check corners: identity LUT maps input to itself.
	// Index (0,0,0) → (0,0,0)
	if lut.Data[0] != 0 || lut.Data[1] != 0 || lut.Data[2] != 0 {
		t.Errorf("(0,0,0) = [%f,%f,%f], want [0,0,0]", lut.Data[0], lut.Data[1], lut.Data[2])
	}
	// Index (32,32,32) → (1,1,1)
	last := (32*33*33 + 32*33 + 32) * 3
	if lut.Data[last] != 1 || lut.Data[last+1] != 1 || lut.Data[last+2] != 1 {
		t.Errorf("(32,32,32) = [%f,%f,%f], want [1,1,1]", lut.Data[last], lut.Data[last+1], lut.Data[last+2])
	}
	// Midpoint (16,16,16) → (0.5, 0.5, 0.5)
	mid := (16*33*33 + 16*33 + 16) * 3
	if lut.Data[mid] != 0.5 || lut.Data[mid+1] != 0.5 || lut.Data[mid+2] != 0.5 {
		t.Errorf("(16,16,16) = [%f,%f,%f], want [0.5,0.5,0.5]", lut.Data[mid], lut.Data[mid+1], lut.Data[mid+2])
	}
}

func TestWriteCubeRoundtrip(t *testing.T) {
	lut := GenerateIdentityLUT()
	lut.Title = "Roundtrip Test"

	var buf bytes.Buffer
	if err := WriteCube(&buf, lut); err != nil {
		t.Fatalf("WriteCube error: %v", err)
	}

	parsed, err := ParseCube(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("ParseCube error: %v", err)
	}

	if parsed.Title != lut.Title {
		t.Errorf("title = %q, want %q", parsed.Title, lut.Title)
	}
	if parsed.Size != lut.Size {
		t.Errorf("size = %d, want %d", parsed.Size, lut.Size)
	}
	if len(parsed.Data) != len(lut.Data) {
		t.Fatalf("data length = %d, want %d", len(parsed.Data), len(lut.Data))
	}

	for i := range lut.Data {
		if math.Abs(float64(parsed.Data[i]-lut.Data[i])) > 1e-5 {
			t.Errorf("data[%d] = %f, want %f", i, parsed.Data[i], lut.Data[i])
			break
		}
	}
}

func TestGenerateLook_DiffersFromIdentity(t *testing.T) {
	identity := GenerateIdentityLUT()
	looks := BuiltinLooks()
	// Pick the first look (golden-hour).
	lut := GenerateLook(looks[0])

	// The look should differ from identity.
	differences := 0
	for i := range identity.Data {
		if math.Abs(float64(lut.Data[i]-identity.Data[i])) > 1e-5 {
			differences++
		}
	}

	if differences == 0 {
		t.Errorf("%s look is identical to identity", looks[0].Name)
	}

	// All values should remain in [0, 1].
	for i, v := range lut.Data {
		if v < 0 || v > 1 {
			t.Errorf("data[%d] = %f, out of [0,1] range", i, v)
			break
		}
	}
}

func TestAllBuiltinLooks_Count(t *testing.T) {
	looks := BuiltinLooks()
	if len(looks) != 47 {
		t.Fatalf("expected 47 built-in looks, got %d", len(looks))
	}
}

func TestAllBuiltinLooks_UniqueNames(t *testing.T) {
	looks := BuiltinLooks()
	seen := make(map[string]bool)
	for _, p := range looks {
		if seen[p.Name] {
			t.Errorf("duplicate look name: %q", p.Name)
		}
		seen[p.Name] = true
	}
}

func TestAllBuiltinLooks_GroupsPresent(t *testing.T) {
	looks := BuiltinLooks()
	groupCounts := make(map[string]int)
	for _, p := range looks {
		groupCounts[p.Group]++
	}

	expected := map[string]int{
		"Light":     6,
		"Shadow":    6,
		"Film":      7,
		"Drama":     6,
		"Vivid":     6,
		"Vintage":   5,
		"Clean":     5,
		"Technical": 6,
	}

	if len(groupCounts) != len(expected) {
		t.Errorf("expected %d groups, got %d: %v", len(expected), len(groupCounts), groupCounts)
	}

	for group, want := range expected {
		got := groupCounts[group]
		if got != want {
			t.Errorf("group %q: count = %d, want %d", group, got, want)
		}
	}
}

func TestAllBuiltinLooks_GroupAndDescriptionPopulated(t *testing.T) {
	for _, p := range BuiltinLooks() {
		if p.Group == "" {
			t.Errorf("look %q has empty Group", p.Name)
		}
		if p.Description == "" {
			t.Errorf("look %q has empty Description", p.Name)
		}
	}
}

func TestAllBuiltinLooks_ValidLUTs(t *testing.T) {
	looks := BuiltinLooks()

	expectedLen := 33 * 33 * 33 * 3

	for _, params := range looks {
		t.Run(params.Name, func(t *testing.T) {
			lut := GenerateLook(params)

			if lut.Title != params.Name {
				t.Errorf("title = %q, want %q", lut.Title, params.Name)
			}
			if lut.Size != 33 {
				t.Errorf("size = %d, want 33", lut.Size)
			}

			if len(lut.Data) != expectedLen {
				t.Fatalf("data length = %d, want %d", len(lut.Data), expectedLen)
			}

			// All values should be in [0, 1].
			for i, v := range lut.Data {
				if v < 0 || v > 1 {
					t.Errorf("data[%d] = %f, out of [0,1] range", i, v)
					break
				}
			}

			// Should roundtrip through .cube format.
			var buf bytes.Buffer
			if err := WriteCube(&buf, lut); err != nil {
				t.Fatalf("WriteCube error: %v", err)
			}
			parsed, err := ParseCube(strings.NewReader(buf.String()))
			if err != nil {
				t.Fatalf("ParseCube error: %v", err)
			}
			if parsed.Size != lut.Size {
				t.Errorf("roundtrip size mismatch: %d vs %d", parsed.Size, lut.Size)
			}
		})
	}
}

func TestAllBuiltinLooks_EachDiffersFromIdentity(t *testing.T) {
	identity := GenerateIdentityLUT()
	for _, params := range BuiltinLooks() {
		t.Run(params.Name, func(t *testing.T) {
			lut := GenerateLook(params)
			differences := 0
			for i := range identity.Data {
				if math.Abs(float64(lut.Data[i]-identity.Data[i])) > 1e-5 {
					differences++
				}
			}
			if differences == 0 {
				t.Errorf("look %q is identical to identity", params.Name)
			}
		})
	}
}

func TestApplyCDL_Identity(t *testing.T) {
	lut := GenerateIdentityLUT()
	original := make([]float32, len(lut.Data))
	copy(original, lut.Data)

	// Identity CDL: slope=1, offset=0, power=1
	ApplyCDL(lut, 1, 1, 1, 0, 0, 0, 1, 1, 1)

	for i := range lut.Data {
		if math.Abs(float64(lut.Data[i]-original[i])) > 1e-6 {
			t.Errorf("identity CDL changed data[%d]: %f → %f", i, original[i], lut.Data[i])
			break
		}
	}
}

func TestApplySaturation_Zero(t *testing.T) {
	lut := GenerateIdentityLUT()
	ApplySaturation(lut, 0) // Should produce grayscale.

	// For each triplet, R=G=B (luminance only).
	for i := 0; i < len(lut.Data); i += 3 {
		r, g, b := lut.Data[i], lut.Data[i+1], lut.Data[i+2]
		if math.Abs(float64(r-g)) > 1e-5 || math.Abs(float64(g-b)) > 1e-5 {
			t.Errorf("not grayscale at %d: [%f, %f, %f]", i/3, r, g, b)
			break
		}
	}
}

func TestGenerateLookFiles(t *testing.T) {
	dir := t.TempDir()

	for _, params := range BuiltinLooks() {
		lut := GenerateLook(params)
		path := filepath.Join(dir, params.Name+".cube")

		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		if err := WriteCube(f, lut); err != nil {
			_ = f.Close()
			t.Fatalf("write %s: %v", path, err)
		}
		_ = f.Close()

		// Verify the file parses back.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		parsed, err := ParseCube(strings.NewReader(string(data)))
		if err != nil {
			t.Fatalf("parse %s: %v", params.Name, err)
		}
		if parsed.Size != 33 {
			t.Errorf("%s: size = %d, want 33", params.Name, parsed.Size)
		}
	}
}

func TestHDRLooks_DifferFromIdentity(t *testing.T) {
	identity := GenerateIdentityLUT()

	hdrNames := []string{
		"sdr-to-hlg", "sdr-to-pq",
		"hlg-to-sdr", "pq-to-sdr",
		"hlg-to-pq", "pq-to-hlg",
	}

	lookMap := make(map[string]LookParams)
	for _, p := range BuiltinLooks() {
		lookMap[p.Name] = p
	}

	for _, name := range hdrNames {
		t.Run(name, func(t *testing.T) {
			params, ok := lookMap[name]
			if !ok {
				t.Fatalf("HDR look %q not found in BuiltinLooks()", name)
			}
			lut := GenerateLook(params)

			differences := 0
			for i := range identity.Data {
				if math.Abs(float64(lut.Data[i]-identity.Data[i])) > 1e-5 {
					differences++
				}
			}
			if differences == 0 {
				t.Errorf("HDR look %q is identical to identity", name)
			}

			// All values must be in [0,1].
			for i, v := range lut.Data {
				if v < 0 || v > 1 {
					t.Errorf("data[%d] = %f, out of [0,1] range", i, v)
					break
				}
			}
		})
	}
}

func TestHDRRoundtrip_SDR_HLG_SDR(t *testing.T) {
	// SDR → HLG → SDR should be close to identity.
	identity := GenerateIdentityLUT()

	sdrToHLG := GenerateHDRLook("fwd", bt709EOTF, hlgOETF, nil)
	hlgToSDR := GenerateHDRLook("rev", hlgInverseOETF, bt709OETF, nil)

	// Apply the forward transform, then the reverse.
	roundtrip := GenerateIdentityLUT()
	for i := 0; i < len(roundtrip.Data); i += 3 {
		for c := 0; c < 3; c++ {
			// Forward: SDR signal → linear → HLG signal
			hlgVal := sdrToHLG.Data[i+c]
			// Reverse: HLG signal → linear → SDR signal
			lin := hlgInverseOETF(hlgVal)
			roundtrip.Data[i+c] = clampf(bt709OETF(lin), 0, 1)
		}
	}

	maxErr := float32(0)
	for i := range identity.Data {
		diff := float32(math.Abs(float64(roundtrip.Data[i] - identity.Data[i])))
		if diff > maxErr {
			maxErr = diff
		}
	}

	// The SDR→HLG→SDR roundtrip should be nearly lossless (HLG fully
	// contains the SDR range). Allow a small tolerance for floating-point
	// and LUT quantization.
	_ = hlgToSDR // used conceptually; we did manual roundtrip above
	if maxErr > 0.01 {
		t.Errorf("SDR→HLG→SDR roundtrip max error = %f, want < 0.01", maxErr)
	}
}

func TestTransferFunctions_Boundaries(t *testing.T) {
	// Black stays black, white stays white for all transfer functions.
	tol := float32(1e-5)

	// BT.709
	if v := bt709EOTF(0); v > tol {
		t.Errorf("bt709EOTF(0) = %f, want 0", v)
	}
	if v := bt709EOTF(1); math.Abs(float64(v-1)) > float64(tol) {
		t.Errorf("bt709EOTF(1) = %f, want 1", v)
	}
	if v := bt709OETF(0); v > tol {
		t.Errorf("bt709OETF(0) = %f, want 0", v)
	}
	if v := bt709OETF(1); math.Abs(float64(v-1)) > float64(tol) {
		t.Errorf("bt709OETF(1) = %f, want 1", v)
	}

	// HLG
	if v := hlgOETF(0); v > tol {
		t.Errorf("hlgOETF(0) = %f, want 0", v)
	}
	if v := hlgOETF(1); math.Abs(float64(v-1)) > float64(tol) {
		t.Errorf("hlgOETF(1) = %f, want 1", v)
	}
	if v := hlgInverseOETF(0); v > tol {
		t.Errorf("hlgInverseOETF(0) = %f, want 0", v)
	}
	if v := hlgInverseOETF(1); math.Abs(float64(v-1)) > float64(tol) {
		t.Errorf("hlgInverseOETF(1) = %f, want 1", v)
	}

	// PQ
	if v := pqEOTF(0); v > tol {
		t.Errorf("pqEOTF(0) = %f, want 0", v)
	}
	if v := pqEOTF(1); math.Abs(float64(v-1)) > float64(tol) {
		t.Errorf("pqEOTF(1) = %f, want 1", v)
	}
	if v := pqOETF(0); v > tol {
		t.Errorf("pqOETF(0) = %f, want 0", v)
	}
	if v := pqOETF(1); math.Abs(float64(v-1)) > float64(tol) {
		t.Errorf("pqOETF(1) = %f, want 1", v)
	}
}
