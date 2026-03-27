package arrender

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/qmuntal/gltf"
)

// buildTestGLB creates a minimal valid GLB (binary glTF 2.0) file with a
// single triangle mesh. The triangle has 3 vertices at known positions,
// 3 normals, and 3 indices. The material uses metallic-roughness PBR.
func buildTestGLB(t *testing.T) []byte {
	t.Helper()

	// Build the mesh data: a single triangle.
	// Vertices: (0,0,0), (1,0,0), (0,1,0)
	// Normals: all (0,0,1)
	// Indices: 0, 1, 2
	type vertex struct {
		x, y, z float32
	}
	vertices := []vertex{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}
	normals := []vertex{{0, 0, 1}, {0, 0, 1}, {0, 0, 1}}
	indices := []uint16{0, 1, 2}

	// Build binary buffer: vertices + normals + indices.
	vertexBytes := 3 * 12 // 3 vertices * 3 floats * 4 bytes
	normalBytes := 3 * 12
	indexBytes := 3 * 2 // 3 indices * 2 bytes (uint16)
	// Align index offset to 4-byte boundary.
	totalBytes := vertexBytes + normalBytes + indexBytes

	binBuf := make([]byte, totalBytes)
	off := 0

	// Write vertices.
	for _, v := range vertices {
		binary.LittleEndian.PutUint32(binBuf[off:], math.Float32bits(v.x))
		binary.LittleEndian.PutUint32(binBuf[off+4:], math.Float32bits(v.y))
		binary.LittleEndian.PutUint32(binBuf[off+8:], math.Float32bits(v.z))
		off += 12
	}

	// Write normals.
	for _, n := range normals {
		binary.LittleEndian.PutUint32(binBuf[off:], math.Float32bits(n.x))
		binary.LittleEndian.PutUint32(binBuf[off+4:], math.Float32bits(n.y))
		binary.LittleEndian.PutUint32(binBuf[off+8:], math.Float32bits(n.z))
		off += 12
	}

	// Write indices.
	for _, idx := range indices {
		binary.LittleEndian.PutUint16(binBuf[off:], idx)
		off += 2
	}

	// Build glTF JSON document.
	doc := map[string]interface{}{
		"asset": map[string]string{
			"version":   "2.0",
			"generator": "switchframe-test",
		},
		"scene": 0,
		"scenes": []map[string]interface{}{
			{"nodes": []int{0}},
		},
		"nodes": []map[string]interface{}{
			{"mesh": 0},
		},
		"meshes": []map[string]interface{}{
			{
				"name": "TestTriangle",
				"primitives": []map[string]interface{}{
					{
						"attributes": map[string]int{
							"POSITION": 0,
							"NORMAL":   1,
						},
						"indices":  2,
						"material": 0,
					},
				},
			},
		},
		"materials": []map[string]interface{}{
			{
				"name": "TestMaterial",
				"pbrMetallicRoughness": map[string]interface{}{
					"baseColorFactor": []float64{1.0, 0.0, 0.0, 1.0}, // red
					"metallicFactor":  0.5,
					"roughnessFactor": 0.3,
				},
				"emissiveFactor": []float64{0.1, 0.2, 0.3},
			},
		},
		"accessors": []map[string]interface{}{
			{ // 0: positions
				"bufferView":    0,
				"componentType": 5126, // FLOAT
				"count":         3,
				"type":          "VEC3",
				"max":           []float64{1, 1, 0},
				"min":           []float64{0, 0, 0},
			},
			{ // 1: normals
				"bufferView":    1,
				"componentType": 5126, // FLOAT
				"count":         3,
				"type":          "VEC3",
			},
			{ // 2: indices
				"bufferView":    2,
				"componentType": 5123, // UNSIGNED_SHORT
				"count":         3,
				"type":          "SCALAR",
			},
		},
		"bufferViews": []map[string]interface{}{
			{ // 0: vertex positions
				"buffer":     0,
				"byteOffset": 0,
				"byteLength": vertexBytes,
				"target":     34962, // ARRAY_BUFFER
			},
			{ // 1: normals
				"buffer":     0,
				"byteOffset": vertexBytes,
				"byteLength": normalBytes,
				"target":     34962,
			},
			{ // 2: indices
				"buffer":     0,
				"byteOffset": vertexBytes + normalBytes,
				"byteLength": indexBytes,
				"target":     34963, // ELEMENT_ARRAY_BUFFER
			},
		},
		"buffers": []map[string]interface{}{
			{
				"byteLength": totalBytes,
			},
		},
	}

	jsonData, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal glTF JSON: %v", err)
	}

	// Pad JSON to 4-byte alignment.
	for len(jsonData)%4 != 0 {
		jsonData = append(jsonData, ' ')
	}

	// Pad binary buffer to 4-byte alignment.
	for len(binBuf)%4 != 0 {
		binBuf = append(binBuf, 0)
	}

	// Build GLB container.
	// Header: magic(4) + version(4) + length(4) = 12 bytes
	// JSON chunk: length(4) + type(4) + data = 8 + len(jsonData)
	// BIN chunk: length(4) + type(4) + data = 8 + len(binBuf)
	glbSize := 12 + 8 + len(jsonData) + 8 + len(binBuf)
	glb := make([]byte, glbSize)

	// Header.
	binary.LittleEndian.PutUint32(glb[0:], 0x46546C67)     // "glTF"
	binary.LittleEndian.PutUint32(glb[4:], 2)              // version
	binary.LittleEndian.PutUint32(glb[8:], uint32(glbSize)) // total length

	// JSON chunk.
	off = 12
	binary.LittleEndian.PutUint32(glb[off:], uint32(len(jsonData)))
	binary.LittleEndian.PutUint32(glb[off+4:], 0x4E4F534A) // "JSON"
	copy(glb[off+8:], jsonData)
	off += 8 + len(jsonData)

	// BIN chunk.
	binary.LittleEndian.PutUint32(glb[off:], uint32(len(binBuf)))
	binary.LittleEndian.PutUint32(glb[off+4:], 0x004E4942) // "BIN\0"
	copy(glb[off+8:], binBuf)

	return glb
}

func TestParseGLTFBytes_Triangle(t *testing.T) {
	glb := buildTestGLB(t)

	meshes, err := ParseGLTFBytes(glb)
	if err != nil {
		t.Fatalf("ParseGLTFBytes: %v", err)
	}

	if len(meshes) != 1 {
		t.Fatalf("got %d meshes, want 1", len(meshes))
	}

	mesh := meshes[0]

	// Verify mesh name.
	if mesh.Name != "TestTriangle" {
		t.Errorf("Name = %q, want %q", mesh.Name, "TestTriangle")
	}

	// Verify vertices.
	if len(mesh.Vertices) != 3 {
		t.Fatalf("got %d vertices, want 3", len(mesh.Vertices))
	}
	expectedVerts := [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}
	for i, v := range mesh.Vertices {
		if v != expectedVerts[i] {
			t.Errorf("vertex[%d] = %v, want %v", i, v, expectedVerts[i])
		}
	}

	// Verify normals.
	if len(mesh.Normals) != 3 {
		t.Fatalf("got %d normals, want 3", len(mesh.Normals))
	}
	for i, n := range mesh.Normals {
		if n != [3]float32{0, 0, 1} {
			t.Errorf("normal[%d] = %v, want [0,0,1]", i, n)
		}
	}

	// Verify indices.
	if len(mesh.Indices) != 3 {
		t.Fatalf("got %d indices, want 3", len(mesh.Indices))
	}
	expectedIndices := []uint32{0, 1, 2}
	for i, idx := range mesh.Indices {
		if idx != expectedIndices[i] {
			t.Errorf("index[%d] = %d, want %d", i, idx, expectedIndices[i])
		}
	}

	// Verify material.
	mat := mesh.Material
	if mat.BaseColor != [3]float64{1.0, 0.0, 0.0} {
		t.Errorf("BaseColor = %v, want [1,0,0]", mat.BaseColor)
	}
	if mat.Metallic != 0.5 {
		t.Errorf("Metallic = %f, want 0.5", mat.Metallic)
	}
	if mat.Roughness != 0.3 {
		t.Errorf("Roughness = %f, want 0.3", mat.Roughness)
	}
	if mat.Emission != [3]float64{0.1, 0.2, 0.3} {
		t.Errorf("Emission = %v, want [0.1,0.2,0.3]", mat.Emission)
	}
}

func TestParseGLTFBytes_InvalidData(t *testing.T) {
	// Too short.
	_, err := ParseGLTFBytes([]byte{1, 2, 3})
	if err == nil {
		t.Error("expected error for too-short data")
	}

	// Wrong magic.
	bad := make([]byte, 12)
	binary.LittleEndian.PutUint32(bad[0:], 0xDEADBEEF)
	_, err = ParseGLTFBytes(bad)
	if err == nil {
		t.Error("expected error for wrong magic")
	}
}

func TestParseGLTF_File(t *testing.T) {
	glb := buildTestGLB(t)

	// Write to temp file.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.glb")
	if err := os.WriteFile(path, glb, 0o644); err != nil {
		t.Fatalf("write test GLB: %v", err)
	}

	meshes, err := ParseGLTF(path)
	if err != nil {
		t.Fatalf("ParseGLTF: %v", err)
	}

	if len(meshes) != 1 {
		t.Fatalf("got %d meshes, want 1", len(meshes))
	}

	if len(meshes[0].Vertices) != 3 {
		t.Errorf("got %d vertices, want 3", len(meshes[0].Vertices))
	}
}

func TestParseGLTF_FileNotFound(t *testing.T) {
	_, err := ParseGLTF("/nonexistent/path/model.glb")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadGLTFBytes_AddsToScene(t *testing.T) {
	glb := buildTestGLB(t)
	scene := NewScene()

	ids, err := LoadGLTFBytes(glb, scene)
	if err != nil {
		t.Fatalf("LoadGLTFBytes: %v", err)
	}

	if len(ids) != 1 {
		t.Fatalf("got %d IDs, want 1", len(ids))
	}

	// The mesh is named "TestTriangle" so that should be the object ID.
	if ids[0] != "TestTriangle" {
		t.Errorf("ID = %q, want %q", ids[0], "TestTriangle")
	}

	// Verify object was added to scene.
	obj, err := scene.GetObject("TestTriangle")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if !obj.Visible {
		t.Error("object should be visible")
	}
	if obj.Scale != [3]float64{1, 1, 1} {
		t.Errorf("Scale = %v, want [1,1,1]", obj.Scale)
	}
}

func TestLoadGLTFBytes_NilScene(t *testing.T) {
	glb := buildTestGLB(t)

	_, err := LoadGLTFBytes(glb, nil)
	if err == nil {
		t.Error("expected error for nil scene")
	}
}

func TestLoadGLTF_NilScene(t *testing.T) {
	_, err := LoadGLTF("/any/path.glb", nil)
	if err == nil {
		t.Error("expected error for nil scene")
	}
}

func TestDefaultMaterial(t *testing.T) {
	m := DefaultMaterial()
	if m.BaseColor != [3]float64{0.8, 0.8, 0.8} {
		t.Errorf("BaseColor = %v, want [0.8,0.8,0.8]", m.BaseColor)
	}
	if m.Metallic != 0.0 {
		t.Errorf("Metallic = %f, want 0", m.Metallic)
	}
	if m.Roughness != 0.5 {
		t.Errorf("Roughness = %f, want 0.5", m.Roughness)
	}
	if m.Emission != [3]float64{0, 0, 0} {
		t.Errorf("Emission = %v, want [0,0,0]", m.Emission)
	}
}

func TestExtractMaterial_Nil(t *testing.T) {
	m := extractMaterial(nil)
	if m != DefaultMaterial() {
		t.Errorf("extractMaterial(nil) = %+v, want default", m)
	}
}

func TestExtractMaterial_NoPBR(t *testing.T) {
	mat := &gltf.Material{
		EmissiveFactor: [3]float64{0.5, 0.6, 0.7},
	}
	m := extractMaterial(mat)
	if m.Emission != [3]float64{0.5, 0.6, 0.7} {
		t.Errorf("Emission = %v, want [0.5,0.6,0.7]", m.Emission)
	}
	// Without PBR, should use default base color.
	if m.BaseColor != [3]float64{0.8, 0.8, 0.8} {
		t.Errorf("BaseColor = %v, want default", m.BaseColor)
	}
}

func TestExtractMeshes_NilDoc(t *testing.T) {
	_, err := extractMeshes(nil)
	if err == nil {
		t.Error("expected error for nil document")
	}
}
