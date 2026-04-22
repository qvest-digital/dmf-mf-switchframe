//go:build correctness

package webtransport

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// parseTSInterfaceFields reads a TypeScript source file, locates
// `interface <name> {`, and returns the set of top-level field names
// declared in that interface. Nested object-type members (e.g. the `available`
// inside `neuralKey: { available: boolean; }`) are intentionally excluded —
// brace depth is tracked line-by-line so only depth-0 fields are collected.
//
// Contract expected of the input: one field per line, identifier directly
// followed by optional `?` and `:`, no inline `/* */` between the identifier
// and the colon. The project's types.ts follows this contract. Patterns that
// violate it fail safely: the parser either misses a field (→ consuming
// drift test RED) or, very rarely, picks up a phantom field (→ consuming
// test reports a Go-side gap). Both fail the test, which is the intended
// surfacing of any drift from expected style.
//
// Block comments `/* ... */` are stripped up-front as a defensive measure
// against JSDoc examples that might otherwise look like fields.
func parseTSInterfaceFields(path, ifaceName string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	data := blockCommentRe.ReplaceAll(raw, nil)

	startRe := regexp.MustCompile(`interface\s+` + regexp.QuoteMeta(ifaceName) + `\s*\{`)
	loc := startRe.FindIndex(data)
	if loc == nil {
		return nil, fmt.Errorf("interface %s not found in %s", ifaceName, path)
	}

	// Start just after the opening brace so depth 0 is the interface body.
	body := string(data[loc[1]:])

	fieldRe := regexp.MustCompile(`^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\??\s*:`)
	fields := make(map[string]bool)

	depth := 0
	for _, line := range strings.Split(body, "\n") {
		if depth == 0 {
			if m := fieldRe.FindStringSubmatch(line); m != nil {
				fields[m[1]] = true
			}
		}
		for _, c := range line {
			switch c {
			case '{':
				depth++
			case '}':
				depth--
				if depth < 0 {
					return fields, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("unterminated interface %s in %s", ifaceName, path)
}

var blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)

// findRepoTypesTS resolves the path to `ui/src/lib/api/types.ts` starting
// from the test file's directory. The test's runtime cwd is the package
// directory; walking up to the first directory containing `go.mod` locates
// the server module root, and `../ui/...` from there is the TS file.
func findRepoTypesTS(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked past filesystem root looking for go.mod from %s", filepath.Dir(thisFile))
		}
		dir = parent
	}

	// `dir` is now `…/switchframe/server`. The TS file is one level up.
	path := filepath.Join(dir, "..", "ui", "src", "lib", "api", "types.ts")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ui/src/lib/api/types.ts not found at %s: %v", path, err)
	}
	return path
}
