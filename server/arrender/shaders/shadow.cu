// shadow.cu — Shadow ray any-hit program for Switchframe OptiX renderer.
// Terminates immediately on any intersection to report occlusion.
// Used with OPTIX_RAY_FLAG_TERMINATE_ON_FIRST_HIT for efficiency.

#include "common.cuh"

extern "C" {
    __constant__ LaunchParams params;
}

// Any-hit program for shadow rays: the ray hit something, so the
// light source is occluded from this point.
extern "C" __global__ void __anyhit__shadow() {
    // Set payload register 0 to 1 (occluded).
    optixSetPayload_0(1);
    // Terminate traversal immediately — we only need to know IF
    // something is hit, not what or where.
    optixTerminateRay();
}
