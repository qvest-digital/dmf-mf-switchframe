package arrender

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed usd_convert.py
var usdConvertScript []byte

// ErrUSDPythonNotFound indicates the Python interpreter for USD conversion
// is not installed or not on PATH.
var ErrUSDPythonNotFound = fmt.Errorf("arrender: python3 with usd-core not found")

// usdExtractResult is the JSON output from usd_convert.py.
type usdExtractResult struct {
	Meshes []usdMeshData `json:"meshes"`
	Error  string        `json:"error,omitempty"`
}

// usdMeshData holds raw mesh data as parsed from the Python JSON output.
type usdMeshData struct {
	Name      string        `json:"name"`
	Vertices  [][3]float64  `json:"vertices"`
	Indices   []int         `json:"indices"`
	Normals   [][3]float64  `json:"normals"`
	UVs       [][2]float64  `json:"uvs"`
	Transform [4][4]float64 `json:"transform"`
	Material  usdMaterial   `json:"material"`
}

// usdMaterial mirrors the material JSON from usd_convert.py.
type usdMaterial struct {
	BaseColor [3]float64 `json:"baseColor"`
	Metallic  float64    `json:"metallic"`
	Roughness float64    `json:"roughness"`
}

// usdPythonPath returns the Python interpreter path to use for USD conversion.
// It checks the SWITCHFRAME_USD_PYTHON env var first, then falls back to
// looking for python3 on PATH.
func usdPythonPath() (string, error) {
	if p := os.Getenv("SWITCHFRAME_USD_PYTHON"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("%w: SWITCHFRAME_USD_PYTHON=%q not found", ErrUSDPythonNotFound, p)
	}

	// Look for python3 on PATH.
	p, err := exec.LookPath("python3")
	if err != nil {
		return "", fmt.Errorf("%w: python3 not on PATH", ErrUSDPythonNotFound)
	}
	return p, nil
}

// writeConvertScript writes the embedded usd_convert.py to a temp directory
// and returns the path. The caller is responsible for cleanup.
func writeConvertScript() (string, func(), error) {
	dir, err := os.MkdirTemp("", "switchframe-usd-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}

	scriptPath := filepath.Join(dir, "usd_convert.py")
	if err := os.WriteFile(scriptPath, usdConvertScript, 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("write convert script: %w", err)
	}

	cleanup := func() { _ = os.RemoveAll(dir) }
	return scriptPath, cleanup, nil
}

// ParseUSD parses a USD/USDA/USDC/USDZ file by shelling out to Python
// and returns mesh data compatible with the glTF pipeline.
func ParseUSD(path string) ([]GLTFMesh, error) {
	pythonPath, err := usdPythonPath()
	if err != nil {
		return nil, err
	}

	scriptPath, cleanup, err := writeConvertScript()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Resolve absolute path for the input file.
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("arrender: resolve path: %w", err)
	}

	// Run conversion with a 30-second timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, pythonPath, scriptPath, absPath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return nil, fmt.Errorf("arrender: usd conversion failed: %s", stderrStr)
		}
		return nil, fmt.Errorf("arrender: usd conversion failed: %w", err)
	}

	// Parse JSON output.
	var result usdExtractResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("arrender: parse usd output: %w", err)
	}

	if result.Error != "" {
		return nil, fmt.Errorf("arrender: usd conversion: %s", result.Error)
	}

	// Convert to GLTFMesh slice.
	return convertUSDMeshes(result.Meshes), nil
}

// LoadUSD loads a USD/USDA/USDC/USDZ file and adds its contents to the scene.
// Returns the IDs of objects added to the scene.
func LoadUSD(path string, scene *Scene) ([]string, error) {
	if scene == nil {
		return nil, fmt.Errorf("arrender: nil scene")
	}

	meshes, err := ParseUSD(path)
	if err != nil {
		return nil, err
	}

	return addUSDMeshesToScene(meshes, scene)
}

// addUSDMeshesToScene adds parsed USD meshes to the scene as SceneObjects.
func addUSDMeshesToScene(meshes []GLTFMesh, scene *Scene) ([]string, error) {
	var ids []string
	for i, mesh := range meshes {
		id := fmt.Sprintf("usd-%d", i)
		if mesh.Name != "" {
			id = mesh.Name
		}

		obj := SceneObject{
			ID:      id,
			Visible: true,
			Scale:   [3]float64{1, 1, 1},
		}
		if err := scene.AddObject(obj); err != nil {
			return ids, fmt.Errorf("add object %q: %w", id, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// convertUSDMeshes converts the raw USD mesh data from Python into GLTFMesh
// structs, converting float64 coordinates to float32.
func convertUSDMeshes(meshes []usdMeshData) []GLTFMesh {
	result := make([]GLTFMesh, 0, len(meshes))

	for _, m := range meshes {
		gm := GLTFMesh{
			Name: m.Name,
			Material: Material{
				BaseColor: m.Material.BaseColor,
				Metallic:  m.Material.Metallic,
				Roughness: m.Material.Roughness,
			},
		}

		// Convert vertices (float64 -> float32).
		gm.Vertices = make([][3]float32, len(m.Vertices))
		for i, v := range m.Vertices {
			gm.Vertices[i] = [3]float32{float32(v[0]), float32(v[1]), float32(v[2])}
		}

		// Convert normals (float64 -> float32).
		if len(m.Normals) > 0 {
			gm.Normals = make([][3]float32, len(m.Normals))
			for i, n := range m.Normals {
				gm.Normals[i] = [3]float32{float32(n[0]), float32(n[1]), float32(n[2])}
			}
		}

		// Convert UVs (float64 -> float32).
		if len(m.UVs) > 0 {
			gm.TexCoords = make([][2]float32, len(m.UVs))
			for i, uv := range m.UVs {
				gm.TexCoords[i] = [2]float32{float32(uv[0]), float32(uv[1])}
			}
		}

		// Convert indices (int -> uint32).
		gm.Indices = make([]uint32, len(m.Indices))
		for i, idx := range m.Indices {
			gm.Indices[i] = uint32(idx)
		}

		result = append(result, gm)
	}

	return result
}

// IsUSDExtension reports whether the given file extension (e.g., ".usd")
// is a recognized USD format.
func IsUSDExtension(ext string) bool {
	ext = strings.ToLower(ext)
	switch ext {
	case ".usd", ".usda", ".usdc", ".usdz":
		return true
	default:
		return false
	}
}
