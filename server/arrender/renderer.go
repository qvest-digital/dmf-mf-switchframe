//go:build cgo && cuda && optix

package arrender

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/tracking"
)

// Renderer drives OptiX ray tracing for AR graphics compositing.
// It owns the scene graph, GPU pipeline, and denoiser resources.
type Renderer struct {
	ctx        *gpu.OptixContext
	module     *gpu.OptixModule
	pipeline   *gpu.OptixPipeline
	scene      *Scene
	settings   RenderSettings
	denoiser   *gpu.OptixDenoiser
	outputBuf   uintptr // float4 RGBA render output (device pointer)
	denoiseBuf  uintptr // float4 denoised output (device pointer)
	outputU8Buf uintptr // uint8 RGBA converted output (device pointer, W*H*4 bytes)
	ias         *gpu.OptixIAS
	gasList    []*gpu.OptixGAS            // per-object GAS (parallel to scene objects snapshot)
	normalBufs []uintptr                  // per-GAS normal device pointers (freed on rebuild)
	meshes     map[string][]GLTFMesh      // loaded mesh data keyed by asset ID (CPU-side, for GAS building)
	frameNum   int                        // monotonically increasing frame counter
	intrinsics *tracking.CameraIntrinsics
}

// NewRenderer creates a renderer backed by the given GPU context.
// Returns ErrRendererNotReady if the GPU context is nil.
func NewRenderer(gpuCtx *gpu.Context, settings RenderSettings) (*Renderer, error) {
	if gpuCtx == nil {
		return nil, ErrRendererNotReady
	}

	if settings.Width <= 0 || settings.Height <= 0 {
		return nil, fmt.Errorf("%w: invalid dimensions %dx%d", ErrRendererNotReady, settings.Width, settings.Height)
	}

	r := &Renderer{
		scene:  NewScene(),
		settings: settings,
		meshes: make(map[string][]GLTFMesh),
	}

	// OptiX context from CUDA context.
	optixCtx, err := gpu.NewOptixContext(gpuCtx.CUDAContext())
	if err != nil {
		return nil, fmt.Errorf("create OptiX context: %w", err)
	}
	r.ctx = optixCtx

	// I6: Allocate output and denoise GPU buffers (float4 per pixel).
	bufSize := settings.Width * settings.Height * 4 * 4 // float4 = 4 floats * 4 bytes
	outPtr, err := gpu.AllocDeviceBytes(bufSize)
	if err != nil {
		optixCtx.Close()
		return nil, fmt.Errorf("allocate output buffer: %w", err)
	}
	r.outputBuf = uintptr(outPtr)

	// Allocate uint8 RGBA output buffer (4 bytes per pixel) for the
	// AR compositor kernel which expects packed uint8 RGBA, not float4.
	u8Size := settings.Width * settings.Height * 4
	u8Ptr, err := gpu.AllocDeviceBytes(u8Size)
	if err != nil {
		gpu.FreeDeviceBytes(outPtr)
		optixCtx.Close()
		return nil, fmt.Errorf("allocate uint8 output buffer: %w", err)
	}
	r.outputU8Buf = uintptr(u8Ptr)

	// Create denoiser if requested.
	if settings.Denoise {
		d, err := gpu.NewOptixDenoiser(optixCtx, settings.Width, settings.Height, false)
		if err != nil {
			gpu.FreeDeviceBytes(u8Ptr)
			gpu.FreeDeviceBytes(outPtr)
			optixCtx.Close()
			return nil, fmt.Errorf("create denoiser: %w", err)
		}
		r.denoiser = d

		// Allocate separate denoise output buffer.
		denoisePtr, err := gpu.AllocDeviceBytes(bufSize)
		if err != nil {
			d.Close()
			gpu.FreeDeviceBytes(u8Ptr)
			gpu.FreeDeviceBytes(outPtr)
			optixCtx.Close()
			return nil, fmt.Errorf("allocate denoise buffer: %w", err)
		}
		r.denoiseBuf = uintptr(denoisePtr)
	}

	return r, nil
}

// SetIntrinsics sets the camera intrinsics used for projection matrix
// computation during rendering.
func (r *Renderer) SetIntrinsics(intrinsics *tracking.CameraIntrinsics) {
	if r == nil {
		return
	}
	r.intrinsics = intrinsics
}

// InitPipeline compiles PTX shader source and creates the OptiX module
// and pipeline. After this call, Render() is operational. The ptxSource
// should contain all shader entry points compiled by nvcc.
func (r *Renderer) InitPipeline(ptxSource []byte) error {
	if r == nil || r.ctx == nil {
		return ErrRendererNotReady
	}
	if len(ptxSource) == 0 {
		return fmt.Errorf("%w: empty PTX source", ErrRendererNotReady)
	}

	// Create OptiX module from PTX.
	mod, err := r.ctx.CreateModule(ptxSource)
	if err != nil {
		return fmt.Errorf("create module: %w", err)
	}
	r.module = mod

	// Build pipeline with all shader entry points.
	pipeline, err := r.ctx.CreatePipeline(gpu.OptixPipelineConfig{
		Module:          mod,
		RaygenEntry:     "__raygen__render",
		MissEntry:       "__miss__transparent",
		ShadowMissEntry: "__miss__shadow",
		ClosestHitEntry: "__closesthit__pbr",
		AnyHitEntry:     "__anyhit__shadow",
		MaxTraceDepth:   2,
	})
	if err != nil {
		mod.Close()
		r.module = nil
		return fmt.Errorf("create pipeline: %w", err)
	}
	r.pipeline = pipeline

	return nil
}

// Scene returns the renderer's scene graph.
func (r *Renderer) Scene() *Scene {
	if r == nil {
		return nil
	}
	return r.scene
}

// SetScene replaces the renderer's scene graph with an external one.
// This allows the REST API and the renderer to share the same scene instance.
func (r *Renderer) SetScene(s *Scene) {
	if r == nil || s == nil {
		return
	}
	r.scene = s
	s.MarkDirty() // force re-render with the new scene
}

// Settings returns the current render settings.
func (r *Renderer) Settings() RenderSettings {
	if r == nil {
		return DefaultRenderSettings()
	}
	return r.settings
}

// SetSettings updates the render settings. If the resolution changes,
// GPU buffers are reallocated.
func (r *Renderer) SetSettings(s RenderSettings) error {
	if r == nil {
		return ErrRendererNotReady
	}
	if s.Width <= 0 || s.Height <= 0 {
		return fmt.Errorf("%w: invalid dimensions %dx%d", ErrRendererNotReady, s.Width, s.Height)
	}

	// Reallocate GPU buffers if resolution changed.
	if s.Width != r.settings.Width || s.Height != r.settings.Height {
		bufSize := s.Width * s.Height * 4 * 4 // float4 = 4 floats * 4 bytes

		// Free old output buffer and allocate new one.
		if r.outputBuf != 0 {
			gpu.FreeDeviceBytes(unsafe.Pointer(r.outputBuf))
			r.outputBuf = 0
		}
		outPtr, err := gpu.AllocDeviceBytes(bufSize)
		if err != nil {
			return fmt.Errorf("reallocate output buffer (%dx%d): %w", s.Width, s.Height, err)
		}
		r.outputBuf = uintptr(outPtr)

		// Reallocate uint8 RGBA output buffer.
		u8Size := s.Width * s.Height * 4
		if r.outputU8Buf != 0 {
			gpu.FreeDeviceBytes(unsafe.Pointer(r.outputU8Buf))
			r.outputU8Buf = 0
		}
		u8Ptr, err := gpu.AllocDeviceBytes(u8Size)
		if err != nil {
			return fmt.Errorf("reallocate uint8 output buffer (%dx%d): %w", s.Width, s.Height, err)
		}
		r.outputU8Buf = uintptr(u8Ptr)

		// Reallocate denoise buffer if denoiser is active.
		if r.denoiser != nil {
			if r.denoiseBuf != 0 {
				gpu.FreeDeviceBytes(unsafe.Pointer(r.denoiseBuf))
				r.denoiseBuf = 0
			}
			denoisePtr, err := gpu.AllocDeviceBytes(bufSize)
			if err != nil {
				return fmt.Errorf("reallocate denoise buffer (%dx%d): %w", s.Width, s.Height, err)
			}
			r.denoiseBuf = uintptr(denoisePtr)
		}
	}

	r.settings = s
	r.scene.MarkDirty()
	return nil
}

// Render performs one frame of ray tracing using the given camera pose.
// The render pipeline:
//  1. If scene dirty -> rebuild BVH (OptiX IAS from scene objects)
//  2. Build camera vectors from CameraPose + intrinsics
//  3. Set up launch params (camera vectors, output buffer, SPP, frame number)
//  4. Launch OptiX pipeline
//  5. Run denoiser on output (if enabled)
//  6. Store result in output buffer
//
// Returns ErrRendererNotReady if the pipeline is not initialized.
// The pipeline requires PTX shaders to be compiled and loaded first,
// which is done on the GPU server with nvcc.
func (r *Renderer) Render(pose *tracking.CameraPose) error {
	if r == nil || r.pipeline == nil {
		return ErrRendererNotReady
	}
	if pose == nil {
		return fmt.Errorf("%w: nil camera pose", ErrRendererNotReady)
	}

	// Step 1: Rebuild IAS if scene changed.
	if r.scene.IsDirty() {
		if err := r.rebuildIAS(); err != nil {
			return fmt.Errorf("rebuild IAS: %w", err)
		}
		r.scene.ClearDirty()
	}

	// Step 2: Compute camera vectors from pose and intrinsics.
	camPos, camU, camV, camW := r.computeCameraVectors(pose)

	// Step 3: Set up launch params.
	spp := r.settings.SPP
	if spp <= 0 {
		spp = 1
	}
	r.frameNum++

	// Get IAS traversable handle for ray tracing.
	var iasHandle unsafe.Pointer
	if r.ias != nil {
		iasHandle = unsafe.Pointer(r.ias.TraversableHandle())
	}

	params := gpu.OptixLaunchParams{
		OutputBuffer:    unsafe.Pointer(r.outputBuf),
		Width:           r.settings.Width,
		Height:          r.settings.Height,
		IASHandle:       iasHandle,
		CameraPos:       camPos,
		CameraU:         camU,
		CameraV:         camV,
		CameraW:         camW,
		SamplesPerPixel: spp,
		FrameNumber:     r.frameNum,
	}

	// Step 4: Launch OptiX pipeline.
	if err := r.pipeline.Launch(params, nil); err != nil {
		return fmt.Errorf("launch pipeline: %w", err)
	}

	// Step 5: Run denoiser if enabled.
	if r.denoiser != nil && r.denoiseBuf != 0 {
		err := r.denoiser.Denoise(
			unsafe.Pointer(r.outputBuf),
			unsafe.Pointer(r.denoiseBuf),
			nil, // no flow buffer for non-temporal mode
			r.settings.Width, r.settings.Height,
			nil, // default stream
		)
		if err != nil {
			return fmt.Errorf("denoise: %w", err)
		}
	}

	// Step 6: Convert float4 RGBA to uint8 RGBA for the AR compositor kernel.
	// The compositor expects packed uint8 RGBA but OptiX outputs float4.
	srcBuf := r.outputBuf
	if r.denoiser != nil && r.denoiseBuf != 0 {
		srcBuf = r.denoiseBuf
	}
	if err := gpu.Float4ToRGBA8(unsafe.Pointer(r.outputU8Buf), unsafe.Pointer(srcBuf), r.settings.Width, r.settings.Height); err != nil {
		return fmt.Errorf("float4 to rgba8 conversion: %w", err)
	}

	return nil
}

// computeCameraVectors derives the camera basis vectors (right, up, forward)
// from a CameraPose and the stored intrinsics. These vectors define the
// virtual camera's viewing frustum for ray generation.
func (r *Renderer) computeCameraVectors(pose *tracking.CameraPose) (
	pos, u, v, w [3]float32,
) {
	// Camera position in world space.
	pos = [3]float32{
		float32(pose.Position[0]),
		float32(pose.Position[1]),
		float32(pose.Position[2]),
	}

	// Compute the view matrix to get the camera's orientation.
	view := tracking.ViewMatrix(*pose)

	// The view matrix rows (transposed from the column-major layout)
	// give us the camera's basis vectors in world space:
	//   Row 0 = right (u)
	//   Row 1 = up (v)
	//   Row 2 = -forward (w points into the scene)
	// In column-major: m[0],m[4],m[8] = right; m[1],m[5],m[9] = up; m[2],m[6],m[10] = -forward

	// Compute field of view from intrinsics for proper ray spread.
	aspect := float64(r.settings.Width) / float64(r.settings.Height)
	halfFovY := math.Pi / 4.0 // default 45 degree half-FOV

	if r.intrinsics != nil {
		sensorW := r.intrinsics.SensorWidth
		if sensorW <= 0 {
			sensorW = 36.0
		}
		focalMM := float64(0)
		if len(r.intrinsics.FocalLengthCurve) > 0 {
			focalMM = r.intrinsics.FocalLengthAtZoom(pose.FocalLength)
		}
		if focalMM <= 0 {
			focalMM = 50.0
		}
		// halfFovY = atan(sensorH / (2 * focalMM))
		//          = atan((sensorW/aspect) / (2 * focalMM))
		//          = atan(sensorW / (2 * aspect * focalMM))
		halfFovY = math.Atan(sensorW / (2.0 * aspect * focalMM))
	}

	// Scale the camera basis vectors by the FOV.
	tanHalfFov := math.Tan(halfFovY)

	// Camera right vector (scaled by aspect * tan(fov/2)).
	u = [3]float32{
		float32(view[0] * aspect * tanHalfFov),
		float32(view[4] * aspect * tanHalfFov),
		float32(view[8] * aspect * tanHalfFov),
	}

	// Camera up vector (scaled by tan(fov/2)).
	v = [3]float32{
		float32(view[1] * tanHalfFov),
		float32(view[5] * tanHalfFov),
		float32(view[9] * tanHalfFov),
	}

	// Camera forward vector (negative of view Z axis).
	w = [3]float32{
		float32(-view[2]),
		float32(-view[6]),
		float32(-view[10]),
	}

	return pos, u, v, w
}

// rebuildIAS rebuilds the instance acceleration structure from the current
// scene objects. Each visible object with loaded mesh data gets a GAS
// and is instanced into the IAS.
func (r *Renderer) rebuildIAS() error {
	// Release old GAS entries.
	for _, gas := range r.gasList {
		if gas != nil {
			gas.Close()
		}
	}
	r.gasList = nil

	// Release old normal device buffers.
	for _, nPtr := range r.normalBufs {
		if nPtr != 0 {
			gpu.FreeDeviceBytes(unsafe.Pointer(nPtr))
		}
	}
	r.normalBufs = nil

	if r.ias != nil {
		r.ias.Close()
		r.ias = nil
	}

	objects := r.scene.Objects()
	if len(objects) == 0 {
		return nil // empty scene is valid
	}

	type meshRef struct {
		mesh GLTFMesh
		obj  SceneObject
	}

	var instances []gpu.OptixInstance
	var gasList []*gpu.OptixGAS
	var normalBufs []uintptr
	var meshRefs []meshRef

	for _, obj := range objects {
		if !obj.Visible {
			continue
		}

		// I8: Look up meshes by asset ID instead of array index.
		assetMeshes, ok := r.meshes[obj.AssetID]
		if !ok || len(assetMeshes) == 0 {
			continue // no mesh data loaded for this object's asset
		}

		for _, mesh := range assetMeshes {
			if len(mesh.Vertices) == 0 {
				continue
			}

			// Flatten vertices to float32 slice for GAS building.
			verts := make([]float32, len(mesh.Vertices)*3)
			for j, v := range mesh.Vertices {
				verts[j*3] = v[0]
				verts[j*3+1] = v[1]
				verts[j*3+2] = v[2]
			}

			gas, err := r.ctx.BuildTriangleGAS(verts, mesh.Indices, nil)
			if err != nil {
				// Clean up previously built GAS entries.
				for _, g := range gasList {
					g.Close()
				}
				for _, nPtr := range normalBufs {
					if nPtr != 0 {
						gpu.FreeDeviceBytes(unsafe.Pointer(nPtr))
					}
				}
				return fmt.Errorf("build GAS for object %q: %w", obj.ID, err)
			}
			gasList = append(gasList, gas)
			meshRefs = append(meshRefs, meshRef{mesh: mesh, obj: obj})

			// Upload normals to GPU if available (not retained by GAS builder).
			var normPtr uintptr
			if len(mesh.Normals) > 0 {
				normData := make([]float32, len(mesh.Normals)*3)
				for j, n := range mesh.Normals {
					normData[j*3] = n[0]
					normData[j*3+1] = n[1]
					normData[j*3+2] = n[2]
				}
				normBytes := len(normData) * 4
				devNorm, allocErr := gpu.AllocDeviceBytes(normBytes)
				if allocErr == nil {
					if copyErr := gpu.CopyHostToDevice(devNorm, unsafe.Pointer(&normData[0]), normBytes); copyErr == nil {
						normPtr = uintptr(devNorm)
					} else {
						gpu.FreeDeviceBytes(devNorm)
					}
				}
			}
			normalBufs = append(normalBufs, normPtr)

			// Build 3x4 row-major affine transform from position/rotation/scale.
			transform := objectTransform(obj)

			instances = append(instances, gpu.OptixInstance{
				GAS:            gas,
				Transform:      transform,
				SBTOffset:      uint32(len(instances)),
				VisibilityMask: 0xFF,
			})
		}
	}

	r.gasList = gasList
	r.normalBufs = normalBufs

	if len(instances) == 0 {
		return nil // no visible geometry
	}

	ias, err := r.ctx.BuildIAS(instances, nil)
	if err != nil {
		return fmt.Errorf("build IAS: %w", err)
	}
	r.ias = ias

	// Populate SBT hitgroup data per instance with vertex/index/normal/material.
	if r.pipeline != nil {
		for i, gas := range gasList {
			vertPtr, _ := gas.DeviceVertices()
			idxPtr, _ := gas.DeviceIndices()
			normPtr := normalBufs[i]
			mat := meshRefs[i].mesh.Material
			baseColor := [3]float32{
				float32(mat.BaseColor[0]),
				float32(mat.BaseColor[1]),
				float32(mat.BaseColor[2]),
			}
			emission := [3]float32{
				float32(mat.Emission[0]),
				float32(mat.Emission[1]),
				float32(mat.Emission[2]),
			}

			if err := r.pipeline.SetHitGroupData(
				i,
				unsafe.Pointer(vertPtr),
				unsafe.Pointer(normPtr),
				nil, // texcoords (not yet supported)
				unsafe.Pointer(idxPtr),
				baseColor,
				float32(mat.Metallic),
				float32(mat.Roughness),
				emission,
				nil, // default stream
			); err != nil {
				return fmt.Errorf("set hitgroup data for instance %d: %w", i, err)
			}
		}
	}

	return nil
}

// objectTransform builds a 3x4 row-major affine transform from a
// SceneObject's position, rotation (Euler XYZ degrees), and scale.
func objectTransform(obj SceneObject) [12]float32 {
	// Convert Euler angles to rotation matrix.
	rx := obj.Rotation[0] * math.Pi / 180.0
	ry := obj.Rotation[1] * math.Pi / 180.0
	rz := obj.Rotation[2] * math.Pi / 180.0

	cx, sx := math.Cos(rx), math.Sin(rx)
	cy, sy := math.Cos(ry), math.Sin(ry)
	cz, sz := math.Cos(rz), math.Sin(rz)

	// Rotation matrix R = Rz * Ry * Rx (extrinsic XYZ).
	r00 := cy * cz
	r01 := cz*sy*sx - sz*cx
	r02 := cz*sy*cx + sz*sx
	r10 := cy * sz
	r11 := sz*sy*sx + cz*cx
	r12 := sz*sy*cx - cz*sx
	r20 := -sy
	r21 := cy * sx
	r22 := cy * cx

	// Apply scale.
	scx := obj.Scale[0]
	scy := obj.Scale[1]
	scz := obj.Scale[2]
	if scx == 0 {
		scx = 1
	}
	if scy == 0 {
		scy = 1
	}
	if scz == 0 {
		scz = 1
	}

	// 3x4 row-major: [R*S | T]
	return [12]float32{
		float32(r00 * scx), float32(r01 * scy), float32(r02 * scz), float32(obj.Position[0]),
		float32(r10 * scx), float32(r11 * scy), float32(r12 * scz), float32(obj.Position[1]),
		float32(r20 * scx), float32(r21 * scy), float32(r22 * scz), float32(obj.Position[2]),
	}
}

// RenderedFrame returns the device pointer to the most recent RGBA
// render output as packed uint8 (R,G,B,A per pixel, Width*Height*4
// bytes). Returns 0 if no frame has been rendered yet.
//
// The returned buffer is the uint8 conversion of the float4 OptiX
// output, performed at the end of each Render() call.
func (r *Renderer) RenderedFrame() uintptr {
	if r == nil {
		return 0
	}
	return r.outputU8Buf
}

// SceneState returns a snapshot of the scene for state broadcast.
func (r *Renderer) SceneState() SceneState {
	if r == nil {
		return SceneState{Settings: DefaultRenderSettings()}
	}
	state := r.scene.State()
	state.Settings = r.settings
	return state
}

// defaultCameraPose returns a static camera suitable for rendering objects
// placed via depth-based placement (no tracking required).
// Camera is at origin, looking along +Z, with a standard 50mm lens on 36mm sensor.
func defaultCameraPose() *tracking.CameraPose {
	return &tracking.CameraPose{
		Position:    [3]float64{0, 0, 0},
		Orientation: [3]float64{0, 0, 0}, // looking along +Z
		FocalLength: 50,
		Confidence:  1.0,
		Source:      "default",
	}
}

// DefaultCameraPose returns a static camera suitable for rendering objects
// placed via depth-based placement (no tracking required). Exported for
// use by the compositor state adapter.
func DefaultCameraPose() *tracking.CameraPose {
	return defaultCameraPose()
}

// MeshesForAsset returns the loaded mesh data for the given asset ID
// and whether any data was found. Returns false if the asset has no
// meshes loaded.
func (r *Renderer) MeshesForAsset(assetID string) ([]GLTFMesh, bool) {
	if r == nil {
		return nil, false
	}
	m, ok := r.meshes[assetID]
	return m, ok
}

// LoadAssetMeshes stores parsed mesh data for an asset ID so that
// rebuildIAS can build geometry acceleration structures. Marks the
// scene dirty so the next render picks up the new geometry.
func (r *Renderer) LoadAssetMeshes(assetID string, meshes []GLTFMesh) {
	if r == nil {
		return
	}
	r.meshes[assetID] = meshes
	if r.scene != nil {
		r.scene.MarkDirty()
	}
}

// PipelineReady reports whether the OptiX pipeline has been initialized
// and the renderer is ready to produce frames.
func (r *Renderer) PipelineReady() bool {
	if r == nil {
		return false
	}
	return r.pipeline != nil
}

// Close releases all GPU resources held by the renderer.
func (r *Renderer) Close() error {
	if r == nil {
		return nil
	}
	for _, gas := range r.gasList {
		if gas != nil {
			gas.Close()
		}
	}
	r.gasList = nil
	for _, nPtr := range r.normalBufs {
		if nPtr != 0 {
			gpu.FreeDeviceBytes(unsafe.Pointer(nPtr))
		}
	}
	r.normalBufs = nil
	if r.ias != nil {
		r.ias.Close()
	}
	// Close denoiser before freeing output/denoise buffers it may reference.
	if r.denoiser != nil {
		r.denoiser.Close()
	}
	// I6: Free GPU output buffers.
	if r.outputBuf != 0 {
		gpu.FreeDeviceBytes(unsafe.Pointer(r.outputBuf))
		r.outputBuf = 0
	}
	if r.outputU8Buf != 0 {
		gpu.FreeDeviceBytes(unsafe.Pointer(r.outputU8Buf))
		r.outputU8Buf = 0
	}
	if r.denoiseBuf != 0 {
		gpu.FreeDeviceBytes(unsafe.Pointer(r.denoiseBuf))
		r.denoiseBuf = 0
	}
	if r.pipeline != nil {
		r.pipeline.Close()
	}
	if r.module != nil {
		r.module.Close()
	}
	if r.ctx != nil {
		r.ctx.Close()
	}
	return nil
}
