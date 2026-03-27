package arrender

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// minimalUSDA is a minimal valid USDA file containing a single cube mesh.
const minimalUSDA = `#usda 1.0

def Mesh "Cube" {
    point3f[] points = [(1,1,1), (-1,1,1), (-1,-1,1), (1,-1,1), (1,1,-1), (-1,1,-1), (-1,-1,-1), (1,-1,-1)]
    int[] faceVertexIndices = [0,1,2,3, 4,7,6,5, 0,4,5,1, 2,6,7,3, 0,3,7,4, 1,5,6,2]
    int[] faceVertexCounts = [4, 4, 4, 4, 4, 4]
}
`

// requirePythonUSD skips the test if python3 with pxr is not available.
func requirePythonUSD(t *testing.T) string {
	t.Helper()

	pythonPath, err := usdPythonPath()
	if err != nil {
		t.Skipf("python3 not available: %v", err)
	}

	// Verify pxr module is installed.
	cmd := exec.Command(pythonPath, "-c", "from pxr import Usd")
	if err := cmd.Run(); err != nil {
		t.Skipf("pxr (usd-core) not installed: %v", err)
	}

	return pythonPath
}

func TestParseUSD_Cube(t *testing.T) {
	requirePythonUSD(t)

	// Write a minimal USDA to a temp file.
	dir := t.TempDir()
	path := filepath.Join(dir, "cube.usda")
	if err := os.WriteFile(path, []byte(minimalUSDA), 0o644); err != nil {
		t.Fatalf("write test USDA: %v", err)
	}

	meshes, err := ParseUSD(path)
	if err != nil {
		t.Fatalf("ParseUSD: %v", err)
	}

	if len(meshes) != 1 {
		t.Fatalf("got %d meshes, want 1", len(meshes))
	}

	mesh := meshes[0]

	// Verify mesh name contains "Cube".
	if !strings.Contains(mesh.Name, "Cube") {
		t.Errorf("Name = %q, want to contain %q", mesh.Name, "Cube")
	}

	// Verify 8 vertices (cube corners).
	if len(mesh.Vertices) != 8 {
		t.Errorf("got %d vertices, want 8", len(mesh.Vertices))
	}

	// Verify indices are triangulated: 6 quad faces -> 12 triangles -> 36 indices.
	if len(mesh.Indices) != 36 {
		t.Errorf("got %d indices, want 36 (6 quads triangulated)", len(mesh.Indices))
	}

	// All indices should be valid (< vertex count).
	for i, idx := range mesh.Indices {
		if idx >= uint32(len(mesh.Vertices)) {
			t.Errorf("index[%d] = %d, out of range (vertices=%d)", i, idx, len(mesh.Vertices))
		}
	}

	// Verify default material is applied (no material in this USDA).
	if mesh.Material.BaseColor != [3]float64{0.8, 0.8, 0.8} {
		t.Errorf("BaseColor = %v, want default [0.8, 0.8, 0.8]", mesh.Material.BaseColor)
	}
	if mesh.Material.Metallic != 0.0 {
		t.Errorf("Metallic = %f, want 0.0", mesh.Material.Metallic)
	}
	if mesh.Material.Roughness != 0.5 {
		t.Errorf("Roughness = %f, want 0.5", mesh.Material.Roughness)
	}
}

func TestLoadUSD_AddsToScene(t *testing.T) {
	requirePythonUSD(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "cube.usda")
	if err := os.WriteFile(path, []byte(minimalUSDA), 0o644); err != nil {
		t.Fatalf("write test USDA: %v", err)
	}

	scene := NewScene()
	ids, err := LoadUSD(path, scene)
	if err != nil {
		t.Fatalf("LoadUSD: %v", err)
	}

	if len(ids) != 1 {
		t.Fatalf("got %d IDs, want 1", len(ids))
	}

	// Verify object was added to scene.
	obj, err := scene.GetObject(ids[0])
	if err != nil {
		t.Fatalf("GetObject(%q): %v", ids[0], err)
	}
	if !obj.Visible {
		t.Error("object should be visible")
	}
	if obj.Scale != [3]float64{1, 1, 1} {
		t.Errorf("Scale = %v, want [1,1,1]", obj.Scale)
	}
}

func TestLoadUSD_NilScene(t *testing.T) {
	_, err := LoadUSD("/any/path.usda", nil)
	if err == nil {
		t.Error("expected error for nil scene")
	}
}

func TestParseUSD_FileNotFound(t *testing.T) {
	requirePythonUSD(t)

	_, err := ParseUSD("/nonexistent/path/model.usda")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestParseUSD_InvalidFile(t *testing.T) {
	requirePythonUSD(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.usda")
	if err := os.WriteFile(path, []byte("this is not valid USD content"), 0o644); err != nil {
		t.Fatalf("write bad file: %v", err)
	}

	_, err := ParseUSD(path)
	if err == nil {
		t.Error("expected error for invalid USD file")
	}
}

func TestParseUSD_NoPython(t *testing.T) {
	// Override the env var to a path that doesn't exist.
	t.Setenv("SWITCHFRAME_USD_PYTHON", "/nonexistent/python3")

	_, err := ParseUSD("/any/path.usda")
	if err == nil {
		t.Error("expected error when python3 is not available")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestIsUSDExtension(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		{".usd", true},
		{".usda", true},
		{".usdc", true},
		{".usdz", true},
		{".USD", true},
		{".USDA", true},
		{".USDZ", true},
		{".glb", false},
		{".gltf", false},
		{".obj", false},
		{".fbx", false},
		{"", false},
	}

	for _, tt := range tests {
		got := IsUSDExtension(tt.ext)
		if got != tt.want {
			t.Errorf("IsUSDExtension(%q) = %v, want %v", tt.ext, got, tt.want)
		}
	}
}

func TestUSDExtensionsAcceptedByStore(t *testing.T) {
	// Verify that USD extensions are registered in the asset store.
	usdExts := []string{".usd", ".usda", ".usdz"}
	for _, ext := range usdExts {
		if _, ok := acceptedExtensions[ext]; !ok {
			t.Errorf("extension %q not in acceptedExtensions map", ext)
		}
	}
}

func TestConvertUSDMeshes(t *testing.T) {
	// Test the pure Go conversion function (no Python needed).
	input := []usdMeshData{
		{
			Name: "/World/Cube",
			Vertices: [][3]float64{
				{1.0, 2.0, 3.0},
				{4.0, 5.0, 6.0},
				{7.0, 8.0, 9.0},
			},
			Normals: [][3]float64{
				{0.0, 0.0, 1.0},
				{0.0, 0.0, 1.0},
				{0.0, 0.0, 1.0},
			},
			UVs: [][2]float64{
				{0.0, 0.0},
				{1.0, 0.0},
				{0.5, 1.0},
			},
			Indices: []int{0, 1, 2},
			Material: usdMaterial{
				BaseColor: [3]float64{1.0, 0.0, 0.0},
				Metallic:  0.8,
				Roughness: 0.2,
			},
		},
	}

	result := convertUSDMeshes(input)

	if len(result) != 1 {
		t.Fatalf("got %d meshes, want 1", len(result))
	}

	mesh := result[0]

	if mesh.Name != "/World/Cube" {
		t.Errorf("Name = %q, want %q", mesh.Name, "/World/Cube")
	}

	// Verify float64 -> float32 conversion.
	if len(mesh.Vertices) != 3 {
		t.Fatalf("got %d vertices, want 3", len(mesh.Vertices))
	}
	if mesh.Vertices[0] != [3]float32{1.0, 2.0, 3.0} {
		t.Errorf("Vertices[0] = %v, want [1,2,3]", mesh.Vertices[0])
	}

	if len(mesh.Normals) != 3 {
		t.Fatalf("got %d normals, want 3", len(mesh.Normals))
	}
	if mesh.Normals[0] != [3]float32{0, 0, 1} {
		t.Errorf("Normals[0] = %v, want [0,0,1]", mesh.Normals[0])
	}

	if len(mesh.TexCoords) != 3 {
		t.Fatalf("got %d texcoords, want 3", len(mesh.TexCoords))
	}
	if mesh.TexCoords[2] != [2]float32{0.5, 1.0} {
		t.Errorf("TexCoords[2] = %v, want [0.5,1.0]", mesh.TexCoords[2])
	}

	if len(mesh.Indices) != 3 {
		t.Fatalf("got %d indices, want 3", len(mesh.Indices))
	}
	if mesh.Indices[0] != 0 || mesh.Indices[1] != 1 || mesh.Indices[2] != 2 {
		t.Errorf("Indices = %v, want [0,1,2]", mesh.Indices)
	}

	// Verify material.
	if mesh.Material.BaseColor != [3]float64{1.0, 0.0, 0.0} {
		t.Errorf("BaseColor = %v, want [1,0,0]", mesh.Material.BaseColor)
	}
	if mesh.Material.Metallic != 0.8 {
		t.Errorf("Metallic = %f, want 0.8", mesh.Material.Metallic)
	}
	if mesh.Material.Roughness != 0.2 {
		t.Errorf("Roughness = %f, want 0.2", mesh.Material.Roughness)
	}
}

func TestConvertUSDMeshes_Empty(t *testing.T) {
	result := convertUSDMeshes(nil)
	if len(result) != 0 {
		t.Errorf("got %d meshes, want 0", len(result))
	}
}

func TestConvertUSDMeshes_NoNormals(t *testing.T) {
	input := []usdMeshData{
		{
			Name:     "/World/Simple",
			Vertices: [][3]float64{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}},
			Indices:  []int{0, 1, 2},
			Material: usdMaterial{
				BaseColor: [3]float64{0.8, 0.8, 0.8},
				Metallic:  0.0,
				Roughness: 0.5,
			},
		},
	}

	result := convertUSDMeshes(input)
	if len(result) != 1 {
		t.Fatalf("got %d meshes, want 1", len(result))
	}

	if result[0].Normals != nil {
		t.Error("Normals should be nil when USD mesh has no normals")
	}
	if result[0].TexCoords != nil {
		t.Error("TexCoords should be nil when USD mesh has no UVs")
	}
}

func TestUSDExtractResult_JSONRoundtrip(t *testing.T) {
	// Verify the JSON struct tags match what the Python script produces.
	jsonStr := `{
		"meshes": [{
			"name": "/Test",
			"vertices": [[1,2,3],[4,5,6]],
			"indices": [0,1],
			"normals": [[0,0,1]],
			"uvs": [[0.5,0.5]],
			"transform": [[1,0,0,0],[0,1,0,0],[0,0,1,0],[0,0,0,1]],
			"material": {"baseColor":[1,0,0],"metallic":0.5,"roughness":0.3}
		}]
	}`

	var result usdExtractResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(result.Meshes) != 1 {
		t.Fatalf("got %d meshes, want 1", len(result.Meshes))
	}

	m := result.Meshes[0]
	if m.Name != "/Test" {
		t.Errorf("Name = %q, want %q", m.Name, "/Test")
	}
	if len(m.Vertices) != 2 {
		t.Errorf("got %d vertices, want 2", len(m.Vertices))
	}
	if m.Vertices[0] != [3]float64{1, 2, 3} {
		t.Errorf("Vertices[0] = %v, want [1,2,3]", m.Vertices[0])
	}
	if len(m.UVs) != 1 {
		t.Errorf("got %d UVs, want 1", len(m.UVs))
	}
	if m.Material.BaseColor != [3]float64{1, 0, 0} {
		t.Errorf("BaseColor = %v, want [1,0,0]", m.Material.BaseColor)
	}
	if m.Material.Metallic != 0.5 {
		t.Errorf("Metallic = %f, want 0.5", m.Material.Metallic)
	}
}

func TestUSDPythonPath_EnvVar(t *testing.T) {
	// Test that SWITCHFRAME_USD_PYTHON is respected.
	dir := t.TempDir()
	fakePython := filepath.Join(dir, "python3")
	if err := os.WriteFile(fakePython, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}

	t.Setenv("SWITCHFRAME_USD_PYTHON", fakePython)

	path, err := usdPythonPath()
	if err != nil {
		t.Fatalf("usdPythonPath: %v", err)
	}
	if path != fakePython {
		t.Errorf("path = %q, want %q", path, fakePython)
	}
}

func TestUSDPythonPath_EnvVarNotFound(t *testing.T) {
	t.Setenv("SWITCHFRAME_USD_PYTHON", "/nonexistent/python3")

	_, err := usdPythonPath()
	if err == nil {
		t.Error("expected error for nonexistent SWITCHFRAME_USD_PYTHON")
	}
}

func TestWriteConvertScript(t *testing.T) {
	scriptPath, cleanup, err := writeConvertScript()
	if err != nil {
		t.Fatalf("writeConvertScript: %v", err)
	}
	defer cleanup()

	// Verify the script was written.
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("script file is empty")
	}
	if !strings.Contains(string(data), "usd_convert") {
		t.Error("script should contain usd_convert identifier")
	}

	// Verify it's in a temp dir.
	if !strings.Contains(scriptPath, "switchframe-usd") {
		t.Errorf("scriptPath = %q, should contain temp dir prefix", scriptPath)
	}

	// Verify cleanup removes it.
	cleanup()
	if _, err := os.Stat(scriptPath); !os.IsNotExist(err) {
		t.Error("cleanup should remove script file")
	}
}

func TestUSDConvertScriptEmbedded(t *testing.T) {
	// Verify the go:embed directive worked.
	if len(usdConvertScript) == 0 {
		t.Fatal("usdConvertScript is empty; go:embed may have failed")
	}
	if !strings.Contains(string(usdConvertScript), "def extract(") {
		t.Error("embedded script should contain the extract function")
	}
	if !strings.Contains(string(usdConvertScript), "json.dump") {
		t.Error("embedded script should contain json.dump call")
	}
}
