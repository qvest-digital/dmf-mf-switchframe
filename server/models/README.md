# Bundled AI models (optional)

ONNX weights are **not** committed to this repository because of size and license constraints. Obtain them locally when you need CUDA/TensorRT segmentation or sidecar tests.

## RobustVideoMatting (RVM) MobileNetV3

Used for live person segmentation (`server/gpu/segmentation_engine.go`, AI sidecar integration tests). Expected filename when running tests or the sidecar:

`rvm_mobilenetv3_fp32_fixed.onnx`

Upstream: [PeterL1n/RobustVideoMatting](https://github.com/PeterL1n/RobustVideoMatting) — model weights are licensed under **GNU GPL 3.0**. If you distribute a binary built against this model or ship the weights, you must comply with the GPL. Switchframe itself is MIT-licensed; the ONNX file is a separate third-party work.

Place the file in this directory, or point your tooling at any directory that contains it (`--models-dir` for the AI sidecar).

## U2-Net (u2netp, optional)

Historical reference weights (`u2netp.onnx`) are not required for the current RVM-based path. If you experiment with U2-Net ONNX, see [xuebinqin/U-2-Net](https://github.com/xuebinqin/U-2-Net) (Apache 2.0).
