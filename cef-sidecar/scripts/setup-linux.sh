#!/bin/bash
# Setup script for building the CEF sidecar on Linux x86_64.
# Run on the target server (Debian/Ubuntu).
set -euo pipefail

CEF_VERSION="131.3.5+gdd9de12+chromium-131.0.6778.264"
CEF_PLATFORM="linux64_minimal"
CEF_DIR="/opt/cef"
BUILD_DIR="/opt/switchframe-cef/build"
INSTALL_DIR="/opt/switchframe-cef"

echo "=== SwitchFrame CEF Sidecar Setup ==="
echo "CEF version: ${CEF_VERSION}"
echo "Platform: ${CEF_PLATFORM}"
echo ""

# 1. Install system dependencies
echo "--- Installing system dependencies ---"
sudo apt-get update -qq
sudo apt-get install -y -qq \
    cmake g++ make \
    xvfb \
    libx11-dev libxcomposite-dev libxdamage-dev libxext-dev libxfixes-dev libxrandr-dev \
    libnss3-dev libnspr4-dev \
    libatk1.0-dev libatk-bridge2.0-dev \
    libglib2.0-dev libgio-cil-dev \
    libdrm-dev libgbm-dev \
    libasound2-dev \
    libpango1.0-dev libcairo2-dev \
    libcups2-dev \
    libdbus-1-dev \
    libexpat1-dev \
    libxcb1-dev \
    libxkbcommon-dev \
    libatspi2.0-dev

# 2. Download CEF binary distribution if not present
if [ ! -d "${CEF_DIR}" ]; then
    echo "--- Downloading CEF binary distribution ---"
    CEF_BASENAME="cef_binary_${CEF_VERSION}_${CEF_PLATFORM}"
    CEF_URL="https://cef-builds.spotifycdn.com/${CEF_BASENAME}.tar.bz2"
    TEMP_DIR=$(mktemp -d)

    echo "Downloading from: ${CEF_URL}"
    curl -L --progress-bar -o "${TEMP_DIR}/cef.tar.bz2" "${CEF_URL}"

    echo "Extracting..."
    tar -xjf "${TEMP_DIR}/cef.tar.bz2" -C "${TEMP_DIR}"

    sudo mkdir -p "${CEF_DIR}"
    sudo mv "${TEMP_DIR}/${CEF_BASENAME}"/* "${CEF_DIR}/"
    rm -rf "${TEMP_DIR}"

    echo "CEF installed to ${CEF_DIR}"
else
    echo "--- CEF already present at ${CEF_DIR} ---"
fi

# 3. Build libcef_dll_wrapper (required by CEF, only needs to be done once)
if [ ! -f "${CEF_DIR}/build/libcef_dll_wrapper/libcef_dll_wrapper.a" ]; then
    echo "--- Building libcef_dll_wrapper ---"
    mkdir -p "${CEF_DIR}/build"
    cd "${CEF_DIR}/build"
    cmake -DCEF_ROOT="${CEF_DIR}" \
          -DCMAKE_BUILD_TYPE=Release \
          "${CEF_DIR}"
    make -j$(nproc) libcef_dll_wrapper
    echo "libcef_dll_wrapper built."
else
    echo "--- libcef_dll_wrapper already built ---"
fi

# 4. Build switchframe-cef
echo "--- Building switchframe-cef ---"
SIDECAR_SRC="$(dirname "$(dirname "$(readlink -f "$0")")")"
mkdir -p "${BUILD_DIR}"
cd "${BUILD_DIR}"

cmake -DCEF_ROOT="${CEF_DIR}" \
      -DCMAKE_BUILD_TYPE=Release \
      "${SIDECAR_SRC}"
make -j$(nproc) switchframe-cef

# Install
sudo mkdir -p "${INSTALL_DIR}/bin"
sudo cp switchframe-cef "${INSTALL_DIR}/bin/"
sudo cp -r "${SIDECAR_SRC}/resources" "${INSTALL_DIR}/bin/"
sudo cp -r "${SIDECAR_SRC}/demo-templates" "${INSTALL_DIR}/"

# Copy CEF runtime files
sudo cp -r "${CEF_DIR}/Release/"* "${INSTALL_DIR}/bin/"
sudo cp -r "${CEF_DIR}/Resources/"* "${INSTALL_DIR}/bin/"

echo ""
echo "=== Build complete ==="
echo "Binary: ${INSTALL_DIR}/bin/switchframe-cef"
echo "Templates: ${INSTALL_DIR}/demo-templates/"
echo ""
echo "To use with switchframe server, either:"
echo "  1. Symlink: ln -s ${INSTALL_DIR}/bin/switchframe-cef /usr/local/bin/"
echo "  2. Or set binary path next to switchframe binary"
echo ""
echo "For headless operation, start Xvfb before the sidecar:"
echo "  Xvfb :99 -screen 0 1920x1080x24 &"
echo "  export DISPLAY=:99"
