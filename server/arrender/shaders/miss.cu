// miss.cu — Miss program for Switchframe OptiX renderer.
// Returns transparent (alpha=0) for AR graphics compositing.
// AR-rendered objects are composited over live video, so rays that
// miss all geometry produce a fully transparent pixel.
//
// Payload convention (2 registers):
//   p0: packed RGB (10 bits per channel)
//   p1: alpha as float bits

#include "common.cuh"

extern "C" {
    __constant__ LaunchParams params;
}

// Primary ray miss: transparent background for AR compositing.
extern "C" __global__ void __miss__transparent() {
    // Set both payload registers to 0 — fully transparent pixel.
    optixSetPayload_0(0); // packed RGB = black
    optixSetPayload_1(0); // alpha = 0.0 (__uint_as_float(0) = 0.0)
}

// Shadow ray miss: light is NOT occluded.
// When a shadow ray misses all geometry, the point is illuminated.
extern "C" __global__ void __miss__shadow() {
    optixSetPayload_0(0); // not occluded
}
