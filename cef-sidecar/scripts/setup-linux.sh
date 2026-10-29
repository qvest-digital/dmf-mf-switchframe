#!/bin/bash
# Setup script for building the CEF sidecar on Linux x86_64.
# Run with: sudo ./scripts/setup-linux.sh
set -euo pipefail

# Resolve paths before any cd changes the working directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SIDECAR_SRC="$(cd "${SCRIPT_DIR}/.." && pwd)"

CEF_VERSION="138.0.60+gd8bc8bd+chromium-138.0.7204.307"
CEF_PLATFORM="linux64_minimal"
CEF_DIR="/opt/cef"
BUILD_DIR="/opt/switchframe-cef/build"
INSTALL_DIR="/opt/switchframe-cef"

echo "=== SwitchFrame CEF Sidecar Setup ==="
echo "CEF version: ${CEF_VERSION}"
echo "Platform: ${CEF_PLATFORM}"
echo "Source: ${SIDECAR_SRC}"
echo ""

# 1. Install system dependencies
echo "--- Installing system dependencies ---"
apt-get update -qq
apt-get install -y -qq \
    cmake g++ make \
    xvfb \
    libx11-dev libxcomposite-dev libxdamage-dev libxext-dev libxfixes-dev libxrandr-dev \
    libnss3-dev libnspr4-dev \
    libatk1.0-dev libatk-bridge2.0-dev \
    libglib2.0-dev \
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
    curl -fL --progress-bar -o "${TEMP_DIR}/cef.tar.bz2" "${CEF_URL}"

    # Validate the download is actually a bzip2 file
    if ! file "${TEMP_DIR}/cef.tar.bz2" | grep -q "bzip2"; then
        echo "ERROR: Downloaded file is not a valid bzip2 archive."
        echo "The CEF version may not exist. Check https://cef-builds.spotifycdn.com/index.html"
        rm -rf "${TEMP_DIR}"
        exit 1
    fi

    echo "Extracting (this may take a minute)..."
    tar -xjf "${TEMP_DIR}/cef.tar.bz2" -C "${TEMP_DIR}"

    mkdir -p "${CEF_DIR}"
    mv "${TEMP_DIR}/${CEF_BASENAME}"/* "${CEF_DIR}/"
    rm -rf "${TEMP_DIR}"

    echo "CEF installed to ${CEF_DIR}"
else
    echo "--- CEF already present at ${CEF_DIR} ---"
fi

# 3. CEF resource files must be in Release/ alongside libcef.so.
#    The minimal distribution puts .pak files and locales/ in Resources/.
#    CEF hardcodes the Release/ path at compile time for ICU and resource bundles.
echo "--- Copying CEF resources to Release directory ---"
cp -n "${CEF_DIR}/Resources/icudtl.dat" "${CEF_DIR}/Release/" 2>/dev/null || true
cp -n "${CEF_DIR}/Resources/"*.pak "${CEF_DIR}/Release/" 2>/dev/null || true
cp -rn "${CEF_DIR}/Resources/locales" "${CEF_DIR}/Release/" 2>/dev/null || true

# 4. Build libcef_dll_wrapper (required by CEF, only needs to be done once)
CEF_WRAPPER_BUILD="${BUILD_DIR}/cef_wrapper"
if [ ! -f "${CEF_WRAPPER_BUILD}/libcef_dll_wrapper/libcef_dll_wrapper.a" ]; then
    echo "--- Building libcef_dll_wrapper ---"
    mkdir -p "${CEF_WRAPPER_BUILD}"
    cd "${CEF_WRAPPER_BUILD}"
    cmake -DCEF_ROOT="${CEF_DIR}" \
          -DCMAKE_BUILD_TYPE=Release \
          "${CEF_DIR}"
    make -j$(nproc) libcef_dll_wrapper
    echo "libcef_dll_wrapper built."
else
    echo "--- libcef_dll_wrapper already built ---"
fi

# 5. Build switchframe-cef
echo "--- Building switchframe-cef ---"
mkdir -p "${BUILD_DIR}"
cd "${BUILD_DIR}"

cmake -DCEF_ROOT="${CEF_DIR}" \
      -DCMAKE_BUILD_TYPE=Release \
      -S "${SIDECAR_SRC}" -B "${BUILD_DIR}"
make -j$(nproc) switchframe-cef

# 6. Install binary, CEF runtime, and templates
echo "--- Installing ---"
mkdir -p "${INSTALL_DIR}/bin"
cp switchframe-cef "${INSTALL_DIR}/bin/"

# CEF runtime files (libcef.so, .pak, locales, etc.)
cp -r "${CEF_DIR}/Release/"* "${INSTALL_DIR}/bin/"
cp -r "${CEF_DIR}/Resources/"* "${INSTALL_DIR}/bin/"

# Adapter.js and demo templates
cp -r "${SIDECAR_SRC}/resources" "${INSTALL_DIR}/bin/" 2>/dev/null || true
cp -r "${SIDECAR_SRC}/demo-templates" "${INSTALL_DIR}/"

# 7. Create wrapper script (CEF needs to run from its resource directory)
cat > /usr/local/bin/switchframe-cef << 'WRAPPER'
#!/bin/bash
cd /opt/switchframe-cef/bin
exec ./switchframe-cef "$@"
WRAPPER
chmod +x /usr/local/bin/switchframe-cef

# 8. Install and enable Xvfb systemd service
if [ ! -f /etc/systemd/system/xvfb.service ]; then
    echo "--- Installing Xvfb systemd service ---"
    cp "${SIDECAR_SRC}/scripts/xvfb.service" /etc/systemd/system/
    systemctl daemon-reload
    systemctl enable --now xvfb
else
    echo "--- Xvfb service already installed ---"
fi

# 9. Ensure DISPLAY=:99 is set globally
if ! grep -q "DISPLAY=:99" /etc/environment 2>/dev/null; then
    echo "DISPLAY=:99" >> /etc/environment
    echo "Added DISPLAY=:99 to /etc/environment"
fi

echo ""
echo "=== Setup complete ==="
echo ""
echo "Binary:    ${INSTALL_DIR}/bin/switchframe-cef"
echo "Wrapper:   /usr/local/bin/switchframe-cef"
echo "Templates: ${INSTALL_DIR}/demo-templates/"
echo "Xvfb:      systemctl status xvfb"
echo ""
echo "Test with:"
echo "  export DISPLAY=:99"
echo "  switchframe-cef --control-socket /tmp/test.sock --frame-socket /tmp/test-f.sock"
echo "  (should print READY)"
echo ""
echo "The switchframe Go server spawns the sidecar when run with --html5-graphics=true."
