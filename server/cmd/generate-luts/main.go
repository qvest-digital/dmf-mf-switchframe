// Command generate-luts creates the built-in .cube LUT files for color grading.
//
//go:generate go run .
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zsiec/switchframe/server/colorgrade"
)

func main() {
	outDir := filepath.Join("colorgrade", "looks")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}

	for _, params := range colorgrade.BuiltinLooks() {
		lut := colorgrade.GenerateLook(params)
		path := filepath.Join(outDir, params.Name+".cube")

		f, err := os.Create(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create %s: %v\n", path, err)
			os.Exit(1)
		}

		if err := colorgrade.WriteCube(f, lut); err != nil {
			f.Close()
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			os.Exit(1)
		}
		f.Close()

		fi, _ := os.Stat(path)
		fmt.Printf("Generated %s (%s)\n", path, humanSize(fi.Size()))
	}
}

func humanSize(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	kb := float64(b) / 1024
	if kb < 1024 {
		return fmt.Sprintf("%.1f KB", kb)
	}
	mb := kb / 1024
	return fmt.Sprintf("%.1f MB", mb)
}
