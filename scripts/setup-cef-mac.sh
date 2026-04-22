#!/usr/bin/env bash
# Bootstrap the CEF sidecar on macOS for `make demo-cef`.
#
# Idempotent: each step short-circuits if its output already exists.
#
# Outputs:
#   ~/.cache/switchframe/cef/<dist>/                 - CEF distribution
#   ~/.cache/switchframe/cef/<dist>/build/.../*.a    - libcef_dll_wrapper static lib
#   cef-sidecar/build/switchframe-cef                - main sidecar binary
#   bin/switchframe-cef.app/                         - macOS app bundle
set -euo pipefail

# --- Configuration -----------------------------------------------------------

# Pin CEF version. Bump manually after testing a new release on cef-builds.spotifycdn.com.
CEF_VERSION="${CEF_VERSION:-138.0.62+g6981a09+chromium-138.0.7204.310}"

CACHE_ROOT="${HOME}/.cache/switchframe/cef"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CEF_SIDECAR_DIR="${REPO_ROOT}/cef-sidecar"
APP_BUNDLE="${REPO_ROOT}/bin/switchframe-cef.app"

# --- Helpers -----------------------------------------------------------------

log()  { printf '\033[1;34m[setup-cef]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[setup-cef]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[setup-cef ERROR]\033[0m %s\n' "$*" >&2; exit 1; }

# --- Pre-flight checks -------------------------------------------------------

[[ "$(uname -s)" == "Darwin" ]] || die "This script targets macOS. Use cef-sidecar/scripts/setup-linux.sh on Linux."
xcode-select -p >/dev/null 2>&1 || die "Xcode Command Line Tools not installed. Run: xcode-select --install"
command -v cmake >/dev/null   || die "cmake not found. Install via: brew install cmake"
command -v curl  >/dev/null   || die "curl not found (should ship with macOS)"

ARCH="$(uname -m)"
case "${ARCH}" in
  arm64)  CEF_ARCH_SUFFIX="macosarm64" ;;
  x86_64) CEF_ARCH_SUFFIX="macosx64" ;;
  *) die "Unsupported architecture: ${ARCH}" ;;
esac

CEF_DIST_NAME="cef_binary_${CEF_VERSION}_${CEF_ARCH_SUFFIX}"
CEF_DIST_DIR="${CACHE_ROOT}/${CEF_DIST_NAME}"
CEF_TARBALL="${CACHE_ROOT}/${CEF_DIST_NAME}.tar.bz2"
WRAPPER_LIB="${CEF_DIST_DIR}/build/libcef_dll_wrapper/libcef_dll_wrapper.a"
SIDECAR_BIN="${CEF_SIDECAR_DIR}/build/switchframe-cef"
HELPER_BIN="${CEF_SIDECAR_DIR}/build/switchframe-cef Helper"

# --- Step 1: Download CEF distribution if missing ----------------------------

mkdir -p "${CACHE_ROOT}"

if [[ -d "${CEF_DIST_DIR}" ]]; then
  log "CEF distribution already present: ${CEF_DIST_DIR}"
else
  CEF_URL="https://cef-builds.spotifycdn.com/${CEF_DIST_NAME}.tar.bz2"
  log "Downloading CEF: ${CEF_URL}"
  log "  (~250 MB, this takes a couple minutes the first time)"
  curl -fL --progress-bar -o "${CEF_TARBALL}.partial" "${CEF_URL}" || die "CEF download failed"
  mv "${CEF_TARBALL}.partial" "${CEF_TARBALL}"

  log "Extracting ${CEF_TARBALL}"
  tar -xjf "${CEF_TARBALL}" -C "${CACHE_ROOT}" || die "tar extract failed"
  rm -f "${CEF_TARBALL}"
  [[ -d "${CEF_DIST_DIR}" ]] || die "Extracted dir not found: ${CEF_DIST_DIR}"
fi

# --- Step 2: Build libcef_dll_wrapper if missing -----------------------------

if [[ -f "${WRAPPER_LIB}" ]]; then
  log "libcef_dll_wrapper already built: ${WRAPPER_LIB}"
else
  log "Building libcef_dll_wrapper (3-5 min)"
  mkdir -p "${CEF_DIST_DIR}/build"
  (
    cd "${CEF_DIST_DIR}/build"
    cmake -DPROJECT_ARCH="${ARCH}" .. >/dev/null
    cmake --build . --target libcef_dll_wrapper -j "$(sysctl -n hw.ncpu)"
  ) || die "wrapper build failed"
  [[ -f "${WRAPPER_LIB}" ]] || die "wrapper lib not produced at ${WRAPPER_LIB}"
fi

# --- Step 3: Configure + build the sidecar binary ----------------------------

if [[ -x "${SIDECAR_BIN}" && -x "${HELPER_BIN}" \
      && "${SIDECAR_BIN}" -nt "${CEF_SIDECAR_DIR}/src/main.cpp" \
      && "${HELPER_BIN}" -nt "${CEF_SIDECAR_DIR}/src/process_helper_mac.cc" ]]; then
  log "switchframe-cef + helper already up-to-date"
else
  log "Configuring + building switchframe-cef + helper"
  mkdir -p "${CEF_SIDECAR_DIR}/build"
  (
    cd "${CEF_SIDECAR_DIR}/build"
    cmake -DCEF_ROOT="${CEF_DIST_DIR}" .. >/dev/null
    cmake --build . --target switchframe-cef switchframe-cef-helper -j "$(sysctl -n hw.ncpu)"
  ) || die "sidecar build failed"
  [[ -x "${SIDECAR_BIN}" ]] || die "sidecar binary not produced at ${SIDECAR_BIN}"
  [[ -x "${HELPER_BIN}" ]] || die "helper binary not produced at ${HELPER_BIN}"
fi

# --- Step 4: Assemble the .app bundle ----------------------------------------

log "Assembling app bundle: ${APP_BUNDLE}"
rm -rf "${APP_BUNDLE}"
mkdir -p "${APP_BUNDLE}/Contents/MacOS"
mkdir -p "${APP_BUNDLE}/Contents/Frameworks"

cat > "${APP_BUNDLE}/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key>      <string>switchframe-cef</string>
  <key>CFBundleIdentifier</key>      <string>dev.switchframe.cef</string>
  <key>CFBundleName</key>            <string>switchframe-cef</string>
  <key>CFBundlePackageType</key>     <string>APPL</string>
  <key>LSMinimumSystemVersion</key>  <string>11.0</string>
  <key>NSHighResolutionCapable</key> <true/>
  <key>LSUIElement</key>             <true/>
</dict>
</plist>
PLIST

cp "${SIDECAR_BIN}" "${APP_BUNDLE}/Contents/MacOS/switchframe-cef"

CEF_FRAMEWORK_SRC="${CEF_DIST_DIR}/Release/Chromium Embedded Framework.framework"
[[ -d "${CEF_FRAMEWORK_SRC}" ]] || die "CEF framework not found at: ${CEF_FRAMEWORK_SRC}"
rsync -a --delete "${CEF_FRAMEWORK_SRC}" "${APP_BUNDLE}/Contents/Frameworks/"

# CEF on macOS auto-discovers per-subprocess-type Helper bundles in
# Contents/Frameworks/. We create Helper.app plus the four type-specific
# variants (GPU/Renderer/Plugin/Alerts), each containing the same helper
# binary but with a distinct CFBundleIdentifier. Without all of them,
# CefInitialize fails with "GPU process launch failed: error_code=1003".
make_helper() {
  local suffix="$1"      # empty for generic, e.g. " (GPU)" for type-specific
  local id_suffix="$2"   # bundle ID disambiguator, e.g. "gpu"
  local exec_name="switchframe-cef Helper${suffix}"
  local helper_app="${APP_BUNDLE}/Contents/Frameworks/${exec_name}.app"
  local id="dev.switchframe.cef.helper"
  if [[ -n "${id_suffix}" ]]; then id="${id}.${id_suffix}"; fi
  mkdir -p "${helper_app}/Contents/MacOS"
  cat > "${helper_app}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key>      <string>${exec_name}</string>
  <key>CFBundleIdentifier</key>      <string>${id}</string>
  <key>CFBundleName</key>            <string>${exec_name}</string>
  <key>CFBundlePackageType</key>     <string>APPL</string>
  <key>LSMinimumSystemVersion</key>  <string>11.0</string>
  <key>LSUIElement</key>             <true/>
</dict>
</plist>
PLIST
  cp -p "${HELPER_BIN}" "${helper_app}/Contents/MacOS/${exec_name}"
}

make_helper ""              ""
make_helper " (GPU)"        "gpu"
make_helper " (Renderer)"   "renderer"
make_helper " (Plugin)"     "plugin"
make_helper " (Alerts)"     "alerts"

log "Done."
log "  Bundle:  ${APP_BUNDLE}"
log "  Run:     ${APP_BUNDLE}/Contents/MacOS/switchframe-cef --control-socket /tmp/c --frame-socket /tmp/f"
