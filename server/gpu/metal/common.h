#ifndef SWITCHFRAME_METAL_COMMON_H
#define SWITCHFRAME_METAL_COMMON_H

#include <metal_stdlib>
using namespace metal;

// BT.709 limited-range constants (matches cuda/common.cuh)
constant float BT709_Y_R = 0.2126f;
constant float BT709_Y_G = 0.7152f;
constant float BT709_Y_B = 0.0722f;
constant uint BT709_Y_OFF = 16;
constant uint BT709_UV_OFF = 128;

// NV12 helper: UV plane offset for given pitch and height
#define NV12_UV_OFFSET(pitch, height) ((pitch) * (height))

// Thread group sizes optimized for Apple Silicon GPU
// 32x8 = 256 threads per group, matching CUDA block dimensions
#define GROUP_DIM_X 32
#define GROUP_DIM_Y 8

#endif
