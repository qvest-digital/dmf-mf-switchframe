#!/usr/bin/env python3
"""Export SAM 2.1 Tiny encoder and decoder to ONNX for TensorRT inference.

Usage: python3 export_sam2.py /tmp/ar_models

Requires: pip install sam-2 (from github.com/facebookresearch/sam2)
"""
import sys
import os
import torch
import numpy as np

output_dir = sys.argv[1] if len(sys.argv) > 1 else "/tmp/ar_models"
os.makedirs(output_dir, exist_ok=True)

from sam2.build_sam import build_sam2
from huggingface_hub import hf_hub_download

ckpt = hf_hub_download("facebook/sam2.1-hiera-tiny", "sam2.1_hiera_tiny.pt", local_dir=output_dir)
model = build_sam2("configs/sam2.1/sam2.1_hiera_t.yaml", ckpt, device="cpu")
model.eval()
print("SAM 2.1 Tiny loaded")


# === Image Encoder ===
class EncoderWrapper(torch.nn.Module):
    """Wraps the SAM 2 encoder to output the same features the predictor stores.

    The predictor calls model.forward_image -> model._prepare_backbone_features
    to get vision_feats, then reshapes them. We replicate that flow here.
    The outputs match what predictor._features contains:
      image_embed: [1, 256, 64, 64]
      high_res_feat0: [1, 32, 256, 256]
      high_res_feat1: [1, 64, 128, 128]
    """
    def __init__(self, sam):
        super().__init__()
        self.sam = sam

    def forward(self, x):
        backbone_out = self.sam.forward_image(x)
        _, vision_feats, _, _ = self.sam._prepare_backbone_features(backbone_out)
        if self.sam.directly_add_no_mem_embed:
            vision_feats[-1] = vision_feats[-1] + self.sam.no_mem_embed
        # vision_feats are in ascending resolution order.
        # feat_sizes: [(256,256), (128,128), (64,64)] for 1024x1024 input
        feat_sizes = [(256, 256), (128, 128), (64, 64)]
        feats = [
            feat.permute(1, 2, 0).view(1, -1, *fs)
            for feat, fs in zip(vision_feats[::-1], feat_sizes[::-1])
        ][::-1]
        # feats[0] = high_res_feat0 [1, 32, 256, 256]
        # feats[1] = high_res_feat1 [1, 64, 128, 128]
        # feats[2] = image_embed    [1, 256, 64, 64]
        return feats[2], feats[0], feats[1]


enc = EncoderWrapper(model)
enc.eval()
dummy = torch.randn(1, 3, 1024, 1024)

with torch.no_grad():
    f0, f1, f2 = enc(dummy)
    print(f"Encoder: embed={f0.shape} hires0={f1.shape} hires1={f2.shape}")

enc_path = os.path.join(output_dir, "sam2_encoder.onnx")
torch.onnx.export(
    enc, dummy, enc_path,
    input_names=["image"],
    output_names=["image_embed", "high_res_feat0", "high_res_feat1"],
    opset_version=17,
    dynamo=False,
)
print(f"Encoder ONNX: {os.path.getsize(enc_path) // 1_000_000} MB")

import onnxruntime as ort

sess = ort.InferenceSession(enc_path, providers=["CPUExecutionProvider"])
r = sess.run(None, {"image": np.random.randn(1, 3, 1024, 1024).astype(np.float32)})
print(f"ORT encoder: {[x.shape for x in r]}")


# === Mask Decoder ===
class DecoderWrapper(torch.nn.Module):
    def __init__(self, sam):
        super().__init__()
        self.prompt_encoder = sam.sam_prompt_encoder
        self.mask_decoder = sam.sam_mask_decoder

    def forward(self, image_embed, hires0, hires1, point_coords, point_labels):
        sparse, dense = self.prompt_encoder(
            points=(point_coords, point_labels), boxes=None, masks=None
        )
        result = self.mask_decoder(
            image_embeddings=image_embed,
            image_pe=self.prompt_encoder.get_dense_pe(),
            sparse_prompt_embeddings=sparse,
            dense_prompt_embeddings=dense,
            multimask_output=False,
            repeat_image=False,
            high_res_features=[hires0, hires1],
        )
        # SAM 2.1 returns (masks, iou, tokens, object_score)
        return result[0], result[1]


dec = DecoderWrapper(model)
dec.eval()

dummy_emb = torch.randn(1, 256, 64, 64)
dummy_h0 = torch.randn(1, 32, 256, 256)
dummy_h1 = torch.randn(1, 64, 128, 128)
dummy_pts = torch.tensor([[[512.0, 512.0]]])
dummy_labels = torch.tensor([[1]])

with torch.no_grad():
    masks, iou = dec(dummy_emb, dummy_h0, dummy_h1, dummy_pts, dummy_labels)
    print(f"Decoder: masks={masks.shape} iou={iou.shape}")

dec_path = os.path.join(output_dir, "sam2_decoder.onnx")
torch.onnx.export(
    dec, (dummy_emb, dummy_h0, dummy_h1, dummy_pts, dummy_labels),
    dec_path,
    input_names=["image_embed", "high_res_feat0", "high_res_feat1", "point_coords", "point_labels"],
    output_names=["masks", "iou_predictions"],
    opset_version=17,
    dynamo=False,
)
print(f"Decoder ONNX: {os.path.getsize(dec_path) // 1_000_000} MB")

sess2 = ort.InferenceSession(dec_path, providers=["CPUExecutionProvider"])
r2 = sess2.run(None, {
    "image_embed": np.random.randn(1, 256, 64, 64).astype(np.float32),
    "high_res_feat0": np.random.randn(1, 32, 256, 256).astype(np.float32),
    "high_res_feat1": np.random.randn(1, 64, 128, 128).astype(np.float32),
    "point_coords": np.array([[[512.0, 512.0]]]).astype(np.float32),
    "point_labels": np.array([[1]]).astype(np.int64),
})
print(f"ORT decoder: masks={r2[0].shape} iou={r2[1].shape}")

# End-to-end
print("\n=== End-to-End Test ===")
test_img = np.random.randint(0, 255, (1, 3, 1024, 1024)).astype(np.float32) / 255.0
r_enc = sess.run(None, {"image": test_img})
r_dec = sess2.run(None, {
    "image_embed": r_enc[0],
    "high_res_feat0": r_enc[1],
    "high_res_feat1": r_enc[2],
    "point_coords": np.array([[[512.0, 512.0]]]).astype(np.float32),
    "point_labels": np.array([[1]]).astype(np.int64),
})
mask_binary = (r_dec[0] > 0).astype(np.uint8)
print(f"mask={r_dec[0].shape} fg={mask_binary.sum()}/{mask_binary.size} iou={r_dec[1][0][0]:.3f}")

print("\nALL SAM 2.1 EXPORTS SUCCESSFUL")
