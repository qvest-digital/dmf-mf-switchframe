# dmf-mf-switchframe - working rules

A fork of thiagopnts/switchframe that publishes it as container images with
MXL input and output, for CPU and for CUDA. Everything upstream builds still
builds the same way.

## Fork boundaries

- `main` carries the fork. `upstream-main` tracks thiagopnts/switchframe
  unmodified and is the base for merging upstream in.
- The fork adds; it does not rewrite upstream logic. Keep each change small
  and in upstream's style, so it can go upstream as a pull request of its own
  and an upstream merge stays mechanical.
- What the fork carries beyond upstream:
  - `Dockerfile.mxl`, the `cpu` and `cuda` image targets.
  - `.github/workflows/mxl-image.yml`, which tests with the `mxl` build tag
    and publishes the images, and the release train in
    `.github/workflows/release-please.yml`.
  - Upstream's image publish job and benchmark publishing are removed: they
    push under the repository's own name. Do not reintroduce them from an
    upstream merge.
  - Source changes, each a fix or a flag upstream can take as is.
- No Helm chart lives here.

## Building

`Dockerfile.mxl` builds on `ghcr.io/qvest-digital/go-mxl-builder`, which
ships libmxl prebuilt under `/opt/libmxl`, and runs on
`ghcr.io/qvest-digital/go-mxl-runtime`, which carries the same libmxl. Every
reader and writer of one MXL domain has to link the same libmxl, so
`GO_MXL_TAG` follows the libmxl the rest of the domain runs.

The `build-base` stage is also the test environment: it has libmxl and the
FFmpeg headers the cgo packages need.

    docker build -f Dockerfile.mxl --target build-base -t switchframe-build .
    docker run --rm -v "$PWD/server:/src/server" switchframe-build \
      go test -tags mxl ./mxl/... ./output/...

The CUDA kernels are compiled to native code for every architecture in
`CUDA_GENCODE`, plus Turing PTX, which a later card outside that list
JIT-compiles at first start.

## Releases

release-please cuts `mxl-v<version>` tags; the `mxl-` prefix keeps them apart
from any version upstream starts. A tag publishes
`ghcr.io/qvest-digital/dmf-mf-switchframe:<version>` and `:<version>-cuda`.

## Voice

- Terse. Comments explain why, not what. No pronouns, no addressing a reader.
- Conventional commits, imperative, subject at most 72 characters, body
  wrapped at 72. No ticket numbers, no `Co-Authored-By` trailers.
- ASCII only: `-`, `--`, `"`, `'`, `->`, `...`.
- This repository is public. Nothing here names or links a private
  repository, tracker or environment.
