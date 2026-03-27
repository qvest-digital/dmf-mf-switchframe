package arrender

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder
	"math"

	"github.com/qmuntal/gltf"
)

// Material holds PBR material parameters extracted from a glTF file.
// These map directly to the glTF metallic-roughness PBR model.
type Material struct {
	BaseColor [3]float64 `json:"baseColor"` // linear RGB albedo
	Metallic  float64    `json:"metallic"`  // 0 = dielectric, 1 = metal
	Roughness float64    `json:"roughness"` // 0 = mirror, 1 = diffuse
	Emission  [3]float64 `json:"emission"`  // emissive color (linear RGB)
}

// DefaultMaterial returns a neutral gray dielectric material.
func DefaultMaterial() Material {
	return Material{
		BaseColor: [3]float64{0.8, 0.8, 0.8},
		Metallic:  0.0,
		Roughness: 0.5,
	}
}

// GLTFMesh holds extracted mesh data ready for GPU upload.
type GLTFMesh struct {
	Name      string       // mesh name from glTF
	Vertices  [][3]float32 // positions
	Normals   [][3]float32 // per-vertex normals (may be empty)
	TexCoords [][2]float32 // per-vertex UVs (may be empty)
	Indices   []uint32     // triangle indices
	Material  Material     // PBR material
	// Texture data (decoded RGBA pixels, nil if no texture).
	BaseColorTexture  []byte // RGBA pixels (width * height * 4 bytes)
	BaseColorTextureW int    // texture width in pixels
	BaseColorTextureH int    // texture height in pixels
}

// ParseGLTF parses a glTF or GLB file and returns mesh data.
// The path can point to either a .gltf (JSON + external bins) or
// a .glb (binary container) file.
func ParseGLTF(path string) ([]GLTFMesh, error) {
	doc, err := gltf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("arrender: open glTF: %w", err)
	}
	return extractMeshes(doc)
}

// ParseGLTFBytes parses a GLB file from a byte slice and returns mesh data.
func ParseGLTFBytes(data []byte) ([]GLTFMesh, error) {
	doc, err := decodeGLB(data)
	if err != nil {
		return nil, fmt.Errorf("arrender: parse GLB: %w", err)
	}
	return extractMeshes(doc)
}

// LoadGLTF loads a glTF or GLB file and adds its contents to the scene.
// Returns the IDs of objects added to the scene.
func LoadGLTF(path string, scene *Scene) ([]string, error) {
	if scene == nil {
		return nil, fmt.Errorf("arrender: nil scene")
	}

	meshes, err := ParseGLTF(path)
	if err != nil {
		return nil, err
	}

	return addMeshesToScene(meshes, scene)
}

// LoadGLTFBytes loads a GLB from a byte slice and adds its contents to the scene.
// Returns the IDs of objects added to the scene.
func LoadGLTFBytes(data []byte, scene *Scene) ([]string, error) {
	if scene == nil {
		return nil, fmt.Errorf("arrender: nil scene")
	}

	meshes, err := ParseGLTFBytes(data)
	if err != nil {
		return nil, err
	}

	return addMeshesToScene(meshes, scene)
}

// addMeshesToScene adds parsed meshes to the scene as SceneObjects.
func addMeshesToScene(meshes []GLTFMesh, scene *Scene) ([]string, error) {
	var ids []string
	for i, mesh := range meshes {
		id := fmt.Sprintf("gltf-%d", i)
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

// extractMeshes walks the document and extracts mesh data from all primitives.
func extractMeshes(doc *gltf.Document) ([]GLTFMesh, error) {
	if doc == nil {
		return nil, fmt.Errorf("arrender: nil document")
	}

	var results []GLTFMesh

	for _, mesh := range doc.Meshes {
		for primIdx, prim := range mesh.Primitives {
			if prim.Mode != gltf.PrimitiveTriangles {
				continue // only handle triangles
			}

			gm := GLTFMesh{
				Name:     mesh.Name,
				Material: DefaultMaterial(),
			}

			// Extract positions (required).
			posIdx, ok := prim.Attributes[gltf.POSITION]
			if !ok {
				return nil, fmt.Errorf("arrender: mesh %q primitive %d: missing POSITION attribute", mesh.Name, primIdx)
			}
			positions, err := readVec3Accessor(doc, posIdx)
			if err != nil {
				return nil, fmt.Errorf("arrender: mesh %q primitive %d positions: %w", mesh.Name, primIdx, err)
			}
			gm.Vertices = positions

			// Extract normals (optional).
			if normIdx, ok := prim.Attributes[gltf.NORMAL]; ok {
				normals, err := readVec3Accessor(doc, normIdx)
				if err != nil {
					return nil, fmt.Errorf("arrender: mesh %q primitive %d normals: %w", mesh.Name, primIdx, err)
				}
				gm.Normals = normals
			}

			// Extract texture coordinates (optional).
			if tcIdx, ok := prim.Attributes["TEXCOORD_0"]; ok {
				texcoords, err := readVec2Accessor(doc, tcIdx)
				if err != nil {
					return nil, fmt.Errorf("arrender: mesh %q primitive %d texcoords: %w", mesh.Name, primIdx, err)
				}
				gm.TexCoords = texcoords
			}

			// Extract indices (optional — if absent, use sequential indices).
			if prim.Indices != nil {
				indices, err := readScalarAccessor(doc, *prim.Indices)
				if err != nil {
					return nil, fmt.Errorf("arrender: mesh %q primitive %d indices: %w", mesh.Name, primIdx, err)
				}
				gm.Indices = indices
			} else {
				// Non-indexed geometry: create sequential indices.
				gm.Indices = make([]uint32, len(gm.Vertices))
				for i := range gm.Indices {
					gm.Indices[i] = uint32(i)
				}
			}

			// Extract material (optional).
			if prim.Material != nil && *prim.Material < len(doc.Materials) {
				mat := doc.Materials[*prim.Material]
				gm.Material = extractMaterial(mat)

				// Extract base color texture if available.
				if pbr := mat.PBRMetallicRoughness; pbr != nil {
					if tex := pbr.BaseColorTexture; tex != nil {
						rgba, w, h, texErr := extractTextureRGBA(doc, int(tex.Index))
						if texErr == nil && rgba != nil {
							gm.BaseColorTexture = rgba
							gm.BaseColorTextureW = w
							gm.BaseColorTextureH = h
						}
					}
				}
			}

			results = append(results, gm)
		}
	}

	return results, nil
}

// extractMaterial converts a glTF material to our Material type.
func extractMaterial(mat *gltf.Material) Material {
	m := DefaultMaterial()
	if mat == nil {
		return m
	}

	m.Emission = mat.EmissiveFactor

	if pbr := mat.PBRMetallicRoughness; pbr != nil {
		bc := pbr.BaseColorFactorOrDefault()
		m.BaseColor = [3]float64{bc[0], bc[1], bc[2]}
		m.Metallic = pbr.MetallicFactorOrDefault()
		m.Roughness = pbr.RoughnessFactorOrDefault()
	}

	return m
}

// readVec3Accessor reads a VEC3 FLOAT accessor and returns [][3]float32.
func readVec3Accessor(doc *gltf.Document, accessorIdx int) ([][3]float32, error) {
	if accessorIdx < 0 || accessorIdx >= len(doc.Accessors) {
		return nil, fmt.Errorf("accessor index %d out of range", accessorIdx)
	}

	acc := doc.Accessors[accessorIdx]
	if acc.Type != gltf.AccessorVec3 {
		return nil, fmt.Errorf("expected VEC3 accessor, got %v", acc.Type)
	}

	data, stride, err := accessorBytes(doc, acc)
	if err != nil {
		return nil, err
	}

	elementSize := 12 // 3 * float32
	if stride == 0 {
		stride = elementSize
	}

	result := make([][3]float32, acc.Count)
	for i := 0; i < acc.Count; i++ {
		offset := i * stride
		if offset+elementSize > len(data) {
			return nil, fmt.Errorf("accessor data overflow at element %d", i)
		}
		result[i][0] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
		result[i][1] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset+4:]))
		result[i][2] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset+8:]))
	}

	return result, nil
}

// readVec2Accessor reads a VEC2 FLOAT accessor and returns [][2]float32.
func readVec2Accessor(doc *gltf.Document, accessorIdx int) ([][2]float32, error) {
	if accessorIdx < 0 || accessorIdx >= len(doc.Accessors) {
		return nil, fmt.Errorf("accessor index %d out of range", accessorIdx)
	}

	acc := doc.Accessors[accessorIdx]
	if acc.Type != gltf.AccessorVec2 {
		return nil, fmt.Errorf("expected VEC2 accessor, got %v", acc.Type)
	}

	data, stride, err := accessorBytes(doc, acc)
	if err != nil {
		return nil, err
	}

	elementSize := 8 // 2 * float32
	if stride == 0 {
		stride = elementSize
	}

	result := make([][2]float32, acc.Count)
	for i := 0; i < acc.Count; i++ {
		offset := i * stride
		if offset+elementSize > len(data) {
			return nil, fmt.Errorf("accessor data overflow at element %d", i)
		}
		result[i][0] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
		result[i][1] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset+4:]))
	}

	return result, nil
}

// readScalarAccessor reads a SCALAR accessor (uint8/uint16/uint32) and returns []uint32.
func readScalarAccessor(doc *gltf.Document, accessorIdx int) ([]uint32, error) {
	if accessorIdx < 0 || accessorIdx >= len(doc.Accessors) {
		return nil, fmt.Errorf("accessor index %d out of range", accessorIdx)
	}

	acc := doc.Accessors[accessorIdx]
	if acc.Type != gltf.AccessorScalar {
		return nil, fmt.Errorf("expected SCALAR accessor, got %v", acc.Type)
	}

	data, stride, err := accessorBytes(doc, acc)
	if err != nil {
		return nil, err
	}

	result := make([]uint32, acc.Count)

	switch acc.ComponentType {
	case gltf.ComponentUshort:
		elementSize := 2
		if stride == 0 {
			stride = elementSize
		}
		for i := 0; i < acc.Count; i++ {
			offset := i * stride
			if offset+elementSize > len(data) {
				return nil, fmt.Errorf("accessor data overflow at element %d", i)
			}
			result[i] = uint32(binary.LittleEndian.Uint16(data[offset:]))
		}
	case gltf.ComponentUint:
		elementSize := 4
		if stride == 0 {
			stride = elementSize
		}
		for i := 0; i < acc.Count; i++ {
			offset := i * stride
			if offset+elementSize > len(data) {
				return nil, fmt.Errorf("accessor data overflow at element %d", i)
			}
			result[i] = binary.LittleEndian.Uint32(data[offset:])
		}
	case gltf.ComponentUbyte:
		elementSize := 1
		if stride == 0 {
			stride = elementSize
		}
		for i := 0; i < acc.Count; i++ {
			offset := i * stride
			if offset+elementSize > len(data) {
				return nil, fmt.Errorf("accessor data overflow at element %d", i)
			}
			result[i] = uint32(data[offset])
		}
	default:
		return nil, fmt.Errorf("unsupported index component type: %v", acc.ComponentType)
	}

	return result, nil
}

// accessorBytes returns the raw byte slice for an accessor's data and the stride.
func accessorBytes(doc *gltf.Document, acc *gltf.Accessor) ([]byte, int, error) {
	if acc.BufferView == nil {
		return nil, 0, fmt.Errorf("accessor has no buffer view")
	}

	bvIdx := *acc.BufferView
	if bvIdx < 0 || bvIdx >= len(doc.BufferViews) {
		return nil, 0, fmt.Errorf("buffer view index %d out of range", bvIdx)
	}

	bv := doc.BufferViews[bvIdx]
	if bv.Buffer < 0 || bv.Buffer >= len(doc.Buffers) {
		return nil, 0, fmt.Errorf("buffer index %d out of range", bv.Buffer)
	}

	buf := doc.Buffers[bv.Buffer]
	if buf.Data == nil {
		return nil, 0, fmt.Errorf("buffer %d has no data", bv.Buffer)
	}

	start := bv.ByteOffset + acc.ByteOffset
	end := bv.ByteOffset + bv.ByteLength
	if start >= len(buf.Data) || end > len(buf.Data) || start > end {
		return nil, 0, fmt.Errorf("buffer view/accessor offset out of range")
	}

	return buf.Data[start:end], bv.ByteStride, nil
}

// extractTextureRGBA extracts a texture from the glTF document and decodes it
// to raw RGBA pixels. The texture index references doc.Textures, which points
// to an image, which points to a buffer view containing the raw PNG/JPEG bytes.
func extractTextureRGBA(doc *gltf.Document, texIdx int) ([]byte, int, int, error) {
	if texIdx < 0 || texIdx >= len(doc.Textures) {
		return nil, 0, 0, fmt.Errorf("texture index %d out of range", texIdx)
	}

	tex := doc.Textures[texIdx]
	if tex.Source == nil {
		return nil, 0, 0, fmt.Errorf("texture %d has no source image", texIdx)
	}

	imgIdx := *tex.Source
	if imgIdx >= len(doc.Images) {
		return nil, 0, 0, fmt.Errorf("image index %d out of range", imgIdx)
	}

	gltfImg := doc.Images[imgIdx]

	// Get the image bytes: either from a buffer view (embedded) or URI (external).
	var imgData []byte
	if gltfImg.BufferView != nil {
		bvIdx := *gltfImg.BufferView
		if bvIdx >= len(doc.BufferViews) {
			return nil, 0, 0, fmt.Errorf("image buffer view index %d out of range", bvIdx)
		}
		bv := doc.BufferViews[bvIdx]
		if bv.Buffer >= len(doc.Buffers) {
			return nil, 0, 0, fmt.Errorf("image buffer index %d out of range", bv.Buffer)
		}
		buf := doc.Buffers[bv.Buffer]
		if buf.Data == nil {
			return nil, 0, 0, fmt.Errorf("image buffer %d has no data", bv.Buffer)
		}
		start := bv.ByteOffset
		end := bv.ByteOffset + bv.ByteLength
		if start >= len(buf.Data) || end > len(buf.Data) {
			return nil, 0, 0, fmt.Errorf("image buffer view out of range")
		}
		imgData = buf.Data[start:end]
	} else {
		// External URI — not supported for embedded GLB parsing.
		return nil, 0, 0, fmt.Errorf("external image URIs not supported")
	}

	// Decode the PNG/JPEG image.
	img, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode image: %w", err)
	}

	// Convert to NRGBA for consistent RGBA byte layout.
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	nrgba := image.NewNRGBA(bounds)
	draw.Draw(nrgba, bounds, img, bounds.Min, draw.Src)

	// The NRGBA pixel data is already in the right layout: R, G, B, A as
	// sequential bytes. The Pix slice has stride RowStride = 4 * width,
	// so we can use it directly if the image bounds start at (0,0).
	// For safety, copy row-by-row to ensure packed layout.
	rgba := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		srcOff := nrgba.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		dstOff := y * w * 4
		copy(rgba[dstOff:dstOff+w*4], nrgba.Pix[srcOff:srcOff+w*4])
	}

	return rgba, w, h, nil
}

// decodeGLB decodes a GLB binary container from a byte slice using the gltf library.
func decodeGLB(data []byte) (*gltf.Document, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("GLB too short: %d bytes", len(data))
	}

	// Validate magic number.
	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != 0x46546C67 { // "glTF"
		return nil, fmt.Errorf("invalid GLB magic: 0x%08X", magic)
	}

	decoder := gltf.NewDecoder(bytes.NewReader(data))
	doc := gltf.NewDocument()
	if err := decoder.Decode(doc); err != nil {
		return nil, fmt.Errorf("decode GLB: %w", err)
	}

	return doc, nil
}
