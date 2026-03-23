# switchframe-cef

CEF (Chromium Embedded Framework) sidecar process for HTML5 broadcast graphics rendering in Switchframe.

This process runs alongside the Go server and renders HTML5 graphics templates (CasparCG-format and OGraf web components) into raw BGRA frames via offscreen rendering. The Go server communicates with the sidecar over Unix domain sockets: a JSON control socket for commands and a binary frame socket for pixel data.

## Prerequisites

- CMake 3.19+
- C++17 compiler (clang or gcc)
- CEF binary distribution

## Getting the CEF Distribution

Download the CEF binary distribution for your platform from:
https://cef-builds.spotifycdn.com/index.html

Choose the "Standard Distribution" for your OS/arch, extract it, and note the path.

Build the CEF wrapper library first:

```bash
cd /path/to/cef_binary
mkdir build && cd build
cmake ..
make -j$(nproc) libcef_dll_wrapper
```

## Building

```bash
cd cef-sidecar
mkdir build && cd build
cmake -DCEF_ROOT=/path/to/cef_binary ..
make -j$(nproc)
```

## Running

The sidecar is launched by the Go server automatically, but can be run manually for testing:

```bash
./switchframe-cef \
    --control-socket /tmp/switchframe-cef-ctrl.sock \
    --frame-socket /tmp/switchframe-cef-frame.sock
```

The process prints `READY` to stdout once CEF is initialized and both sockets are listening.

## Test Templates

The `test-templates/` directory contains example graphics for testing:

- `solid-red.html` -- CasparCG-format template with `play()`, `stop()`, `update()` functions
- `ograf-lower-third/` -- OGraf web component with `manifest.ograf.json` and a custom element

## Architecture

```
Go server                       CEF sidecar
   |                                |
   |--- control socket (JSON) ---->|  load/play/stop/update commands
   |<-- frame socket (binary) -----|  BGRA pixel data per frame
   |                                |
```

Tasks 10-11 will implement the browser pool, offscreen rendering, and socket protocols.
