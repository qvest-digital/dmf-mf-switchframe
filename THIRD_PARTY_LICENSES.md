# Third-party materials

This repository is MIT-licensed (see `LICENSE`). The following bundled or referenced assets have their own terms.

## Fonts

**Inter** (server-side text overlay) — see `server/graphics/textrender/fonts/OFL.txt` (SIL Open Font License).

## ONNX / segmentation

**RobustVideoMatting MobileNetV3 weights** (`rvm_mobilenetv3_fp32_fixed.onnx`, not vendored — obtain per `server/models/README.md`): GNU General Public License v3.0, [upstream](https://github.com/PeterL1n/RobustVideoMatting).

**U-2-Net** (optional `u2netp.onnx` experiments): Apache License 2.0, [upstream](https://github.com/xuebinqin/U-2-Net).

## Test media

### Blender Open Movies (.ts excerpts under `test/clips/`)

Big Buck Bunny, Sintel, Tears of Steel, Elephants Dream — [Creative Commons Attribution 3.0](https://creativecommons.org/licenses/by/3.0/). © Blender Foundation — [film pages](https://www.blender.org/get-involved/).

### Synthetic ASR fixtures (`test/asr-fixtures/*.wav`)

Generated locally via macOS `say` and scripts under `scripts/asr-eval/` (project-original). Exception: **`real_jfk.wav`** — public-domain speech sample used for transcription regression; cite your source copy if redistributing a modified corpus.
