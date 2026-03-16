#!/usr/bin/env bash
# mxl-demo.sh — Start MXL GStreamer test sources + Switchframe + UI
#
# Prerequisites:
#   - MXL SDK built and installed (set MXL_ROOT)
#   - GStreamer installed (mxl-gst-testsrc needs it)
#   - A shared-memory domain directory (set MXL_DOMAIN)
#     macOS:  diskutil erasevolume HFS+ MXL $(hdiutil attach -nomount ram://2097152)
#     Linux:  mkdir -p /dev/shm/mxl   (or use any tmpfs path)
#   - Go, Node.js
#
# Environment variables:
#   MXL_ROOT    — MXL SDK install prefix (default: auto-detect by platform)
#   MXL_DOMAIN  — shared-memory domain path (default: /Volumes/MXL on macOS, /dev/shm/mxl on Linux)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# ─── Platform detection ─────────────────────────────────────────────────────

OS="$(uname -s)"
case "$OS" in
    Darwin)
        MXL_ROOT="${MXL_ROOT:-$HOME/dev/mxl/install/Darwin-Clang-Release}"
        MXL_DOMAIN="${MXL_DOMAIN:-/Volumes/MXL}"
        ;;
    Linux)
        MXL_ROOT="${MXL_ROOT:-$HOME/dev/mxl/install/Linux-GCC-Release}"
        MXL_DOMAIN="${MXL_DOMAIN:-/dev/shm/mxl}"
        ;;
    *)
        echo "ERROR: Unsupported platform: $OS"
        echo "MXL shared-memory transport requires macOS or Linux."
        exit 1
        ;;
esac

MXL_GST_TESTSRC="$MXL_ROOT/bin/mxl-gst-testsrc"
FLOW_DIR="$PROJECT_DIR/test/mxl"

# Source UUIDs (must match test/mxl/*.json files)
SRC1_VIDEO="a0000001-0000-0000-0000-000000000001"
SRC1_AUDIO="a0000001-0000-0000-0000-000000000002"
SRC2_VIDEO="a0000002-0000-0000-0000-000000000001"
SRC2_AUDIO="a0000002-0000-0000-0000-000000000002"

# Output UUIDs (must match test/mxl/output_*.json files)
OUT_VIDEO="b0000001-0000-0000-0000-000000000001"
OUT_AUDIO="b0000001-0000-0000-0000-000000000002"

PIDS=()

cleanup() {
    echo ""
    echo "Stopping MXL demo..."
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    wait 2>/dev/null || true
    echo "Done."
}
trap cleanup EXIT INT TERM

# ─── Prerequisites ───────────────────────────────────────────────────────────

if [ ! -d "$MXL_ROOT" ]; then
    echo "ERROR: MXL SDK not found at $MXL_ROOT"
    echo ""
    echo "Set MXL_ROOT to your MXL install directory, e.g.:"
    echo "  export MXL_ROOT=\$HOME/dev/mxl/install/Darwin-Clang-Release   # macOS"
    echo "  export MXL_ROOT=\$HOME/dev/mxl/install/Linux-GCC-Release      # Linux"
    exit 1
fi

if [ ! -x "$MXL_GST_TESTSRC" ]; then
    echo "ERROR: mxl-gst-testsrc not found at $MXL_GST_TESTSRC"
    echo "Build the MXL SDK with GStreamer tools enabled."
    exit 1
fi

if [ ! -d "$MXL_DOMAIN" ]; then
    echo "ERROR: MXL domain directory not found at $MXL_DOMAIN"
    echo ""
    if [ "$OS" = "Darwin" ]; then
        echo "Create a RamDisk:"
        echo "  diskutil erasevolume HFS+ MXL \$(hdiutil attach -nomount ram://2097152)"
    else
        echo "Create a shared-memory directory:"
        echo "  mkdir -p /dev/shm/mxl"
    fi
    exit 1
fi

# ─── Library path setup ─────────────────────────────────────────────────────

# Ensure the MXL shared libraries can be found at runtime.
if [ "$OS" = "Darwin" ]; then
    # On macOS, try adding rpath if the binary uses @rpath and doesn't have it.
    # This is idempotent — install_name_tool silently succeeds if already set.
    if command -v otool &>/dev/null; then
        if ! otool -l "$MXL_GST_TESTSRC" 2>/dev/null | grep -q "$MXL_ROOT/lib"; then
            echo "Adding rpath to mxl-gst-testsrc for $MXL_ROOT/lib..."
            install_name_tool -add_rpath "$MXL_ROOT/lib" "$MXL_GST_TESTSRC" 2>/dev/null || true
        fi
    fi
    export DYLD_LIBRARY_PATH="${MXL_ROOT}/lib${DYLD_LIBRARY_PATH:+:$DYLD_LIBRARY_PATH}"
else
    export LD_LIBRARY_PATH="${MXL_ROOT}/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
fi

# ─── Start GStreamer test sources ────────────────────────────────────────────

echo "Starting MXL test sources..."

"$MXL_GST_TESTSRC" \
    -d "$MXL_DOMAIN" \
    -v "$FLOW_DIR/src1_video.json" \
    -a "$FLOW_DIR/src1_audio.json" \
    -p smpte \
    -t "Source 1" \
    -g "switchframe-src1" &
PIDS+=($!)

"$MXL_GST_TESTSRC" \
    -d "$MXL_DOMAIN" \
    -v "$FLOW_DIR/src2_video.json" \
    -a "$FLOW_DIR/src2_audio.json" \
    -p checkers-8 \
    -t "Source 2" \
    -g "switchframe-src2" &
PIDS+=($!)

echo "Waiting for flows to register..."
sleep 2

# ─── Build Switchframe with MXL support ──────────────────────────────────────

echo "Building Switchframe with MXL SDK..."
cd "$PROJECT_DIR/server"
PKG_CONFIG_PATH="${MXL_ROOT}/lib/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}" \
    go build -tags "cgo mxl" -o "$PROJECT_DIR/bin/switchframe" ./cmd/switchframe
cd "$PROJECT_DIR"

# ─── Start Switchframe ───────────────────────────────────────────────────────

MXL_SOURCES="${SRC1_VIDEO}:${SRC1_AUDIO},${SRC2_VIDEO}:${SRC2_AUDIO}"

# --demo disables API auth and adds 4 synthetic cameras alongside the 2 real
# MXL sources, which is useful for verifying both pipelines coexist.
"$PROJECT_DIR/bin/switchframe" \
    --demo \
    --http-fallback \
    --mxl-domain "$MXL_DOMAIN" \
    --mxl-sources "$MXL_SOURCES" \
    --mxl-output program \
    --mxl-output-video-def "$FLOW_DIR/output_video.json" \
    --mxl-output-audio-def "$FLOW_DIR/output_audio.json" &
PIDS+=($!)

# ─── Start UI dev server ────────────────────────────────────────────────────

cd "$PROJECT_DIR/ui"
npm run dev &
PIDS+=($!)
cd "$PROJECT_DIR"

# ─── Banner ──────────────────────────────────────────────────────────────────

echo ""
echo "  SwitchFrame MXL Demo"
echo ""
echo "  UI:      http://localhost:5173"
echo "  Domain:  $MXL_DOMAIN"
echo ""
echo "  Sources:"
echo "    Source 1: SMPTE bars + audio"
echo "    Source 2: Checkerboard + audio"
echo ""
echo "  Program Output (MXL):"
echo "    Video: $OUT_VIDEO"
echo "    Audio: $OUT_AUDIO"
echo ""
echo "  Monitor output with mxl-gst-sink:"
echo "    export DYLD_LIBRARY_PATH=$MXL_ROOT/lib"
echo "    $MXL_ROOT/bin/mxl-gst-sink -d $MXL_DOMAIN -v $OUT_VIDEO -a $OUT_AUDIO"
echo ""
echo "  Press Ctrl+C to stop"
echo ""

wait
