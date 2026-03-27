//go:build ignore

// Generator for bundled test GLB assets. Run from this directory:
//
//	go run main.go
//
// Produces podium.glb, clock.glb, and ltstand.glb in the parent directory.
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/qmuntal/gltf"
)

func main() {
	outDir := filepath.Join("..")

	models := []struct {
		name     string
		meshes   []meshDef
		material matDef
	}{
		{
			name: "podium",
			meshes: []meshDef{
				{name: "Podium", w: 0.6, h: 1.2, d: 0.4, ty: 0.6},
			},
			material: matDef{
				baseColor: [3]float64{0.4, 0.4, 0.4},
				metallic:  0,
				roughness: 0.8,
			},
		},
		{
			name: "clock",
			meshes: []meshDef{
				{name: "ClockFrame", w: 0.8, h: 0.5, d: 0.02, ty: 0},
			},
			material: matDef{
				baseColor: [3]float64{0.7, 0.7, 0.7},
				metallic:  0.8,
				roughness: 0.2,
			},
		},
		{
			name: "ltstand",
			meshes: []meshDef{
				{name: "Pole", w: 0.02, h: 1.0, d: 0.02, ty: 0.5},
				{name: "Bar", w: 0.6, h: 0.02, d: 0.02, ty: 1.01},
			},
			material: matDef{
				baseColor: [3]float64{0.1, 0.1, 0.1},
				metallic:  0,
				roughness: 0.9,
			},
		},
	}

	for _, m := range models {
		outPath := filepath.Join(outDir, m.name+".glb")
		if err := generateGLB(outPath, m.meshes, m.material); err != nil {
			fmt.Fprintf(os.Stderr, "error generating %s: %v\n", m.name, err)
			os.Exit(1)
		}
		fi, _ := os.Stat(outPath)
		fmt.Printf("generated %s (%d bytes)\n", outPath, fi.Size())
	}
}

type meshDef struct {
	name       string
	w, h, d    float64 // full dimensions
	ty         float64 // Y translation (center offset)
}

type matDef struct {
	baseColor [3]float64
	metallic  float64
	roughness float64
}

// generateGLB creates a GLB file with the given box meshes and a shared material.
func generateGLB(path string, meshes []meshDef, mat matDef) error {
	doc := gltf.NewDocument()
	doc.Asset.Generator = "switchframe/arrender/testdata/gen"

	// Single shared material.
	doc.Materials = []*gltf.Material{{
		Name: "PBR",
		PBRMetallicRoughness: &gltf.PBRMetallicRoughness{
			BaseColorFactor: &[4]float64{mat.baseColor[0], mat.baseColor[1], mat.baseColor[2], 1.0},
			MetallicFactor:  gltf.Float(mat.metallic),
			RoughnessFactor: gltf.Float(mat.roughness),
		},
	}}
	materialIdx := 0

	// Build one binary buffer with all mesh data.
	var bufData []byte
	var bufferViews []*gltf.BufferView
	var accessors []*gltf.Accessor
	var gltfMeshes []*gltf.Mesh
	var nodeIndices []int

	for i, md := range meshes {
		positions, normals, indices := boxGeometry(md.w, md.h, md.d)

		// Compute bounding box for positions.
		minPos := [3]float64{math.MaxFloat64, math.MaxFloat64, math.MaxFloat64}
		maxPos := [3]float64{-math.MaxFloat64, -math.MaxFloat64, -math.MaxFloat64}
		for _, p := range positions {
			for j := 0; j < 3; j++ {
				if float64(p[j]) < minPos[j] {
					minPos[j] = float64(p[j])
				}
				if float64(p[j]) > maxPos[j] {
					maxPos[j] = float64(p[j])
				}
			}
		}

		// Encode positions.
		posBytes := encodeVec3(positions)
		posBVOffset := len(bufData)
		bufData = append(bufData, posBytes...)
		// Pad to 4-byte alignment.
		bufData = padTo4(bufData)

		posBVIdx := len(bufferViews)
		bufferViews = append(bufferViews, &gltf.BufferView{
			Buffer:     0,
			ByteOffset: posBVOffset,
			ByteLength: len(posBytes),
			Target:     gltf.TargetArrayBuffer,
		})

		posAccIdx := len(accessors)
		accessors = append(accessors, &gltf.Accessor{
			BufferView:    gltf.Index(posBVIdx),
			ComponentType: gltf.ComponentFloat,
			Count:         len(positions),
			Type:          gltf.AccessorVec3,
			Min:           []float64{minPos[0], minPos[1], minPos[2]},
			Max:           []float64{maxPos[0], maxPos[1], maxPos[2]},
		})

		// Encode normals.
		normBytes := encodeVec3(normals)
		normBVOffset := len(bufData)
		bufData = append(bufData, normBytes...)
		bufData = padTo4(bufData)

		normBVIdx := len(bufferViews)
		bufferViews = append(bufferViews, &gltf.BufferView{
			Buffer:     0,
			ByteOffset: normBVOffset,
			ByteLength: len(normBytes),
			Target:     gltf.TargetArrayBuffer,
		})

		normAccIdx := len(accessors)
		accessors = append(accessors, &gltf.Accessor{
			BufferView:    gltf.Index(normBVIdx),
			ComponentType: gltf.ComponentFloat,
			Count:         len(normals),
			Type:          gltf.AccessorVec3,
		})

		// Encode indices as uint16.
		idxBytes := encodeUint16(indices)
		idxBVOffset := len(bufData)
		bufData = append(bufData, idxBytes...)
		bufData = padTo4(bufData)

		idxBVIdx := len(bufferViews)
		bufferViews = append(bufferViews, &gltf.BufferView{
			Buffer:     0,
			ByteOffset: idxBVOffset,
			ByteLength: len(idxBytes),
			Target:     gltf.TargetElementArrayBuffer,
		})

		idxAccIdx := len(accessors)
		accessors = append(accessors, &gltf.Accessor{
			BufferView:    gltf.Index(idxBVIdx),
			ComponentType: gltf.ComponentUshort,
			Count:         len(indices),
			Type:          gltf.AccessorScalar,
		})

		// Create mesh.
		gltfMeshes = append(gltfMeshes, &gltf.Mesh{
			Name: md.name,
			Primitives: []*gltf.Primitive{{
				Attributes: gltf.PrimitiveAttributes{
					gltf.POSITION: posAccIdx,
					gltf.NORMAL:   normAccIdx,
				},
				Indices:  gltf.Index(idxAccIdx),
				Material: gltf.Index(materialIdx),
				Mode:     gltf.PrimitiveTriangles,
			}},
		})

		// Create node with Y translation.
		nodeIdx := i
		nodeIndices = append(nodeIndices, nodeIdx)
		doc.Nodes = append(doc.Nodes, &gltf.Node{
			Name:        md.name,
			Mesh:        gltf.Index(i),
			Translation: [3]float64{0, md.ty, 0},
			Rotation:    gltf.DefaultRotation,
			Scale:       gltf.DefaultScale,
		})
	}

	doc.Buffers = []*gltf.Buffer{{
		ByteLength: len(bufData),
		Data:       bufData,
	}}
	doc.BufferViews = bufferViews
	doc.Accessors = accessors
	doc.Meshes = gltfMeshes
	doc.Scenes[0].Nodes = nodeIndices

	return gltf.SaveBinary(doc, path)
}

// boxGeometry generates a box with per-face normals (24 vertices, 36 indices).
// The box is centered at the origin.
func boxGeometry(w, h, d float64) (positions [][3]float32, normals [][3]float32, indices []uint16) {
	hw, hh, hd := float32(w/2), float32(h/2), float32(d/2)

	// 6 faces, 4 vertices each = 24 vertices
	type face struct {
		verts   [4][3]float32
		normal  [3]float32
	}

	faces := []face{
		// Front (+Z)
		{
			verts:  [4][3]float32{{-hw, -hh, hd}, {hw, -hh, hd}, {hw, hh, hd}, {-hw, hh, hd}},
			normal: [3]float32{0, 0, 1},
		},
		// Back (-Z)
		{
			verts:  [4][3]float32{{hw, -hh, -hd}, {-hw, -hh, -hd}, {-hw, hh, -hd}, {hw, hh, -hd}},
			normal: [3]float32{0, 0, -1},
		},
		// Right (+X)
		{
			verts:  [4][3]float32{{hw, -hh, hd}, {hw, -hh, -hd}, {hw, hh, -hd}, {hw, hh, hd}},
			normal: [3]float32{1, 0, 0},
		},
		// Left (-X)
		{
			verts:  [4][3]float32{{-hw, -hh, -hd}, {-hw, -hh, hd}, {-hw, hh, hd}, {-hw, hh, -hd}},
			normal: [3]float32{-1, 0, 0},
		},
		// Top (+Y)
		{
			verts:  [4][3]float32{{-hw, hh, hd}, {hw, hh, hd}, {hw, hh, -hd}, {-hw, hh, -hd}},
			normal: [3]float32{0, 1, 0},
		},
		// Bottom (-Y)
		{
			verts:  [4][3]float32{{-hw, -hh, -hd}, {hw, -hh, -hd}, {hw, -hh, hd}, {-hw, -hh, hd}},
			normal: [3]float32{0, -1, 0},
		},
	}

	for i, f := range faces {
		base := uint16(i * 4)
		for _, v := range f.verts {
			positions = append(positions, v)
			normals = append(normals, f.normal)
		}
		// Two triangles per face: 0,1,2 and 0,2,3
		indices = append(indices, base, base+1, base+2, base, base+2, base+3)
	}

	return
}

// encodeVec3 encodes a slice of [3]float32 to little-endian bytes.
func encodeVec3(data [][3]float32) []byte {
	buf := make([]byte, len(data)*12)
	for i, v := range data {
		binary.LittleEndian.PutUint32(buf[i*12:], math.Float32bits(v[0]))
		binary.LittleEndian.PutUint32(buf[i*12+4:], math.Float32bits(v[1]))
		binary.LittleEndian.PutUint32(buf[i*12+8:], math.Float32bits(v[2]))
	}
	return buf
}

// encodeUint16 encodes a slice of uint16 to little-endian bytes.
func encodeUint16(data []uint16) []byte {
	buf := make([]byte, len(data)*2)
	for i, v := range data {
		binary.LittleEndian.PutUint16(buf[i*2:], v)
	}
	return buf
}

// padTo4 pads the byte slice to 4-byte alignment.
func padTo4(data []byte) []byte {
	rem := len(data) % 4
	if rem == 0 {
		return data
	}
	padding := make([]byte, 4-rem)
	return append(data, padding...)
}
