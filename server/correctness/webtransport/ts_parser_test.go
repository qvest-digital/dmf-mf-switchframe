//go:build correctness

package webtransport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTSInterfaceFields_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.ts")
	src := `
export interface ControlRoomState {
	programSource: string;
	previewSource: string;
	seq: number;
	timestamp: number;
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := parseTSInterfaceFields(path, "ControlRoomState")
	if err != nil {
		t.Fatalf("parseTSInterfaceFields: %v", err)
	}

	want := map[string]bool{
		"programSource": true,
		"previewSource": true,
		"seq":           true,
		"timestamp":     true,
	}
	assertFieldSet(t, got, want)
}

func TestParseTSInterfaceFields_OptionalMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.ts")
	src := `
interface ControlRoomState {
	required: string;
	optional?: number;
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := parseTSInterfaceFields(path, "ControlRoomState")
	if err != nil {
		t.Fatalf("parseTSInterfaceFields: %v", err)
	}

	assertFieldSet(t, got, map[string]bool{
		"required": true,
		"optional": true,
	})
}

func TestParseTSInterfaceFields_SkipsNestedObjectFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.ts")
	src := `
interface ControlRoomState {
	top: string;
	neuralKey?: {
		available: boolean;
		strength: number;
	};
	after: boolean;
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := parseTSInterfaceFields(path, "ControlRoomState")
	if err != nil {
		t.Fatalf("parseTSInterfaceFields: %v", err)
	}

	// `available` and `strength` are nested and must not appear at the top.
	assertFieldSet(t, got, map[string]bool{
		"top":       true,
		"neuralKey": true,
		"after":     true,
	})
}

func TestParseTSInterfaceFields_StripsBlockCommentsBeforeParsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.ts")
	// The JSDoc block contains an indented line that looks like a field
	// declaration. Without the comment strip, the parser would pick it up
	// as a phantom field in the result.
	src := `
/**
 * ControlRoomState shape documentation.
 *   bogus: string;
 */
interface ControlRoomState {
	real: string;
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := parseTSInterfaceFields(path, "ControlRoomState")
	if err != nil {
		t.Fatalf("parseTSInterfaceFields: %v", err)
	}

	if got["bogus"] {
		t.Errorf("parser picked up phantom field from inside /* ... */: %v", got)
	}
	if !got["real"] {
		t.Errorf("parser missed real field after comment strip: %v", got)
	}
}

func TestParseTSInterfaceFields_IgnoresUnrelatedInterfaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.ts")
	src := `
interface Other {
	shouldNotBeIncluded: string;
}

interface ControlRoomState {
	wanted: string;
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := parseTSInterfaceFields(path, "ControlRoomState")
	if err != nil {
		t.Fatalf("parseTSInterfaceFields: %v", err)
	}

	if got["shouldNotBeIncluded"] {
		t.Errorf("parser pulled a field from a different interface: %v", got)
	}
	if !got["wanted"] {
		t.Errorf("parser missed field from target interface: %v", got)
	}
}

func TestParseTSInterfaceFields_ErrorsOnMissingFile(t *testing.T) {
	_, err := parseTSInterfaceFields("/no/such/path/types.ts", "ControlRoomState")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestParseTSInterfaceFields_ErrorsOnMissingInterface(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.ts")
	src := `interface SomeOther { field: string; }`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := parseTSInterfaceFields(path, "ControlRoomState")
	if err == nil {
		t.Fatal("expected error when target interface is absent, got nil")
	}
}

// Regression: parser must find the real ControlRoomState in the checked-in
// types.ts and include fields that can desync TS parsing from Go structs.
func TestParseTSInterfaceFields_RealTypesFile_CoversDriftFields(t *testing.T) {
	path := findRepoTypesTS(t)

	got, err := parseTSInterfaceFields(path, "ControlRoomState")
	if err != nil {
		t.Fatalf("parseTSInterfaceFields(%s): %v", path, err)
	}

	// These fields were added alongside the structured Go wire types and
	// captions/DSK fade / FTB duration / transition-abort threshold work; all
	// are declared in types.ts today and must be picked up by the parser.
	driftFields := []string{
		"ftbDurationMs",
		"transitionAbortThreshold",
		"dskFadeDurationMs",
		"peerReachable",
		"peerLastSeenMs",
		"engineLabel",
		"role",
		"sessionId",
		"engineBUrl",
		"lastCommandSeq",
		"lastExecutedSeq",
		"lastExecutedErr",
		"lateCommands",
		"recentCommandErrors",
	}
	for _, f := range driftFields {
		if !got[f] {
			t.Errorf("parser missed field %q from real types.ts", f)
		}
	}
	// Sanity: parser should not pull nested `neuralKey` members into the top set.
	if got["available"] {
		t.Errorf("parser pulled nested field 'available' into top-level set")
	}
}

func assertFieldSet(t *testing.T, got, want map[string]bool) {
	t.Helper()
	for k := range want {
		if !got[k] {
			t.Errorf("missing expected field %q; got=%v", k, keysOf(got))
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("unexpected field %q; want=%v", k, keysOf(want))
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
