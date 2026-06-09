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

func TestApplyContrast_PositiveIncreasesContrast(t *testing.T) {
	lut := GenerateIdentityLUT()
	ApplyContrast(lut, 0.5)

	// Midpoint stays at 0.5.
	mid := (16*33*33 + 16*33 + 16) * 3
	if math.Abs(float64(lut.Data[mid]-0.5)) > 1e-5 {
		t.Errorf("midpoint = %f, want 0.5", lut.Data[mid])
	}

	// Quarter point should be pushed away from the midpoint (darkened).
	quarter := (8*33*33 + 8*33 + 8) * 3
	identityQuarter := float32(8) / 32.0
	if lut.Data[quarter] >= identityQuarter {
		t.Errorf("quarter point = %f, want < %f (pushed away from 0.5)",
			lut.Data[quarter], identityQuarter)
	}
}

func TestApplyContrast_NegativeReducesContrast(t *testing.T) {
	lut := GenerateIdentityLUT()
	original := make([]float32, len(lut.Data))
	copy(original, lut.Data)

	// Contrast is documented/validated as -1..+1 (CorrectorState, API,
	// UI slider). A negative amount must flatten values toward the 0.5
	// midpoint, not silently no-op.
	ApplyContrast(lut, -0.5)

	changed := false
	for i := range lut.Data {
		v, orig := lut.Data[i], original[i]
		if v != orig {
			changed = true
		}
		// No value may move away from the midpoint.
		if math.Abs(float64(v)-0.5) > math.Abs(float64(orig)-0.5)+1e-6 {
			t.Fatalf("data[%d] = %f moved away from midpoint (orig %f)", i, v, orig)
		}
	}
	if !changed {
		t.Fatal("negative contrast was a complete no-op on the LUT")
	}

	// Midpoint unchanged, quarter point pulled up toward 0.5.
	mid := (16*33*33 + 16*33 + 16) * 3
	if math.Abs(float64(lut.Data[mid]-0.5)) > 1e-5 {
		t.Errorf("midpoint = %f, want 0.5", lut.Data[mid])
	}
	quarter := (8*33*33 + 8*33 + 8) * 3
	identityQuarter := float32(8) / 32.0
	if lut.Data[quarter] <= identityQuarter {
		t.Errorf("quarter point = %f, want > %f (flattened toward 0.5)",
			lut.Data[quarter], identityQuarter)
	}
}

func TestApplyContrast_MinimumFlattensToGray(t *testing.T) {
	lut := GenerateIdentityLUT()
	ApplyContrast(lut, -1)

	for i, v := range lut.Data {
		if math.Abs(float64(v)-0.5) > 1e-5 {
			t.Fatalf("data[%d] = %f, want 0.5 (zero contrast at amount=-1)", i, v)
		}
	}
}

func TestApplyContrast_ZeroIsNoOp(t *testing.T) {
	lut := GenerateIdentityLUT()
	original := make([]float32, len(lut.Data))
	copy(original, lut.Data)

	ApplyContrast(lut, 0)

	for i := range lut.Data {
		if lut.Data[i] != original[i] {
			t.Fatalf("zero contrast changed data at index %d", i)
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

// builtinLookLUT generates the named built-in look's LUT.
func builtinLookLUT(t *testing.T, name string) *LUT3D {
	t.Helper()
	for _, p := range BuiltinLooks() {
		if p.Name == name {
			return GenerateLook(p)
		}
	}
	t.Fatalf("built-in look %q not found", name)
	return nil
}

// lutGrayIndex returns the data index of the neutral grid point (g,g,g).
func lutGrayIndex(g int) int {
	return (g*33*33 + g*33 + g) * 3
}

// TestSDRToPQ_ReferenceWhiteLevel verifies luminance normalization in the
// sdr-to-pq technical look. PQ is an absolute transfer function (linear 1.0 =
// 10,000 nits) while BT.709 linear is display-relative (1.0 = SDR reference
// white, 203 nits per BT.2408). SDR peak white must therefore map to
// pqOETF(203/10000) ≈ 0.58, not PQ code 1.0 (= 10,000 nits).
func TestSDRToPQ_ReferenceWhiteLevel(t *testing.T) {
	lut := builtinLookLUT(t, "sdr-to-pq")
	white := lut.Data[lutGrayIndex(32)]
	if math.Abs(float64(white)-0.58) > 0.02 {
		t.Errorf("sdr-to-pq maps SDR white to PQ signal %f, want ~0.58 (203 nits)", white)
	}
}

// TestPQToSDR_HighlightsNotCrushed verifies that pq-to-sdr normalizes PQ
// absolute luminance to SDR-relative before tone mapping. PQ signal 0.75 is
// ~1000 nits — bright highlight content that must land near SDR white, not
// at ~0.27 signal (dark midtone).
func TestPQToSDR_HighlightsNotCrushed(t *testing.T) {
	lut := builtinLookLUT(t, "pq-to-sdr")
	// Grid point 24/32 = PQ signal 0.75 ≈ 1000 nits.
	v := lut.Data[lutGrayIndex(24)]
	if v < 0.8 {
		t.Errorf("pq-to-sdr maps ~1000-nit content to SDR signal %f, want >= 0.8", v)
	}
}

// TestHLGToPQ_PeakLevel verifies that hlg-to-pq places HLG nominal peak
// (1000 nits per BT.2100) at the PQ code for 1000 nits (~0.751), not at
// PQ 1.0 (10,000 nits).
func TestHLGToPQ_PeakLevel(t *testing.T) {
	lut := builtinLookLUT(t, "hlg-to-pq")
	peak := lut.Data[lutGrayIndex(32)]
	if math.Abs(float64(peak)-0.751) > 0.02 {
		t.Errorf("hlg-to-pq maps HLG peak to PQ signal %f, want ~0.751 (1000 nits)", peak)
	}
}

// TestPQToHLG_HighlightsNotCrushed verifies that pq-to-hlg rescales PQ
// absolute luminance to the HLG 1000-nit range: ~1000-nit PQ content should
// land near HLG peak, not in the midtones.
func TestPQToHLG_HighlightsNotCrushed(t *testing.T) {
	lut := builtinLookLUT(t, "pq-to-hlg")
	// Grid point 24/32 = PQ signal 0.75 ≈ 1000 nits.
	v := lut.Data[lutGrayIndex(24)]
	if v < 0.8 {
		t.Errorf("pq-to-hlg maps ~1000-nit content to HLG signal %f, want >= 0.8", v)
	}
}

// TestEmbeddedTechnicalLooks_MatchGenerated verifies the embedded .cube files
// for the PQ technical looks were regenerated with luminance normalization.
func TestEmbeddedTechnicalLooks_MatchGenerated(t *testing.T) {
	for _, name := range []string{"sdr-to-pq", "pq-to-sdr", "hlg-to-pq", "pq-to-hlg"} {
		t.Run(name, func(t *testing.T) {
			data, err := builtinLooksFS.ReadFile("looks/" + name + ".cube")
			if err != nil {
				t.Fatalf("read embedded %s.cube: %v", name, err)
			}
			embedded, err := ParseCube(strings.NewReader(string(data)))
			if err != nil {
				t.Fatalf("parse embedded %s.cube: %v", name, err)
			}
			generated := builtinLookLUT(t, name)
			if !lutsEqual(embedded, generated, 1e-4) {
				t.Errorf("embedded %s.cube is stale: does not match GenerateLook output", name)
			}
		})
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
