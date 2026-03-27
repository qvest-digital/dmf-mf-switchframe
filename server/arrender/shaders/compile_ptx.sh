#!/bin/bash
# Compile OptiX PTX shaders for AR graphics rendering.
# Combines all .cu files into a single PTX module that OptiX loads at runtime.
# Requires: nvcc, OptiX SDK headers
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OPTIX_INCLUDE="${OPTIX_INCLUDE:-/usr/local/NVIDIA-OptiX-SDK-8.1.0/include}"
CUDA_INCLUDE="${CUDA_INCLUDE:-/usr/local/cuda/include}"
ARCH="${ARCH:-sm_89}"
OUTPUT="${SCRIPT_DIR}/ar_shaders.ptx"

if ! command -v nvcc &>/dev/null; then
    echo "WARNING: nvcc not found, skipping PTX compilation" >&2
    exit 0
fi

if [ ! -d "$OPTIX_INCLUDE" ]; then
    echo "WARNING: OptiX SDK not found at $OPTIX_INCLUDE, skipping PTX compilation" >&2
    exit 0
fi

# Combine all shader .cu files into one, deduplicating shared declarations.
COMBINED=$(mktemp /tmp/ar_shaders_combined_XXXXXX.cu)
trap "rm -f $COMBINED" EXIT

python3 - "$SCRIPT_DIR" "$COMBINED" << 'PYEOF'
import re, sys, os

shader_dir = sys.argv[1]
output = sys.argv[2]
files = ['raygen.cu', 'closest_hit.cu', 'miss.cu', 'shadow.cu']

combined = '#include "common.cuh"\nextern "C" { __constant__ LaunchParams params; }\n\n'
pack_emitted = False

for fname in files:
    path = os.path.join(shader_dir, fname)
    if not os.path.exists(path):
        continue
    content = open(path).read()
    content = re.sub(r'#include\s*"common\.cuh"', '', content)
    content = re.sub(r'extern\s*"C"\s*\{\s*\n\s*__constant__\s+LaunchParams\s+params;\s*\n\s*\}', '', content)

    lines = content.strip().split('\n')
    skip = False
    filtered = []
    for line in lines:
        if not pack_emitted and 'unsigned int pack_rgb' in line and '__device__' in line:
            pack_emitted = True
            filtered.append(line)
        elif pack_emitted and 'unsigned int pack_rgb' in line and '__device__' in line:
            skip = True
            continue
        elif skip:
            if line.strip() == '}':
                skip = False
            continue
        else:
            filtered.append(line)
    combined += f'// ===== {fname} =====\n' + '\n'.join(filtered) + '\n\n'

with open(output, 'w') as f:
    f.write(combined)
PYEOF

nvcc -ptx -O3 -arch="$ARCH" \
    -I"$OPTIX_INCLUDE" -I"$CUDA_INCLUDE" -I"$SCRIPT_DIR" \
    "$COMBINED" -o "$OUTPUT" 2>&1

echo "PTX compiled: $OUTPUT ($(wc -l < "$OUTPUT") lines)"
