#pragma once

#include <cuda_runtime.h>
#include <stdint.h>

// BT.709 limited-range constants
#define BT709_Y_R   0.2126f
#define BT709_Y_G   0.7152f
#define BT709_Y_B   0.0722f
#define BT709_Y_OFF 16
#define BT709_UV_OFF 128

// NV12 helper: UV plane offset for given pitch and height
#define NV12_UV_OFFSET(pitch, height) ((pitch) * (height))

// Thread block sizes optimized for L4 (Ada Lovelace, SM 8.9)
// 32x8 = 256 threads per block, good occupancy for image processing
#define BLOCK_DIM_X 32
#define BLOCK_DIM_Y 8

// Error checking macro for use in device functions
#define CUDA_CHECK(call) do { \
    cudaError_t err = (call); \
    if (err != cudaSuccess) return err; \
} while(0)
