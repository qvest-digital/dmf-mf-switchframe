#!/usr/bin/env python3
"""
Export NVIDIA Parakeet-TDT 1.1B for cache-aware streaming inference via ONNX.

Produces three ONNX models plus vocabulary and config for the Go ASR sidecar:

  mel_frontend.onnx       — Mel spectrogram preprocessor
  encoder_streaming.onnx  — FastConformer encoder with cache I/O tensors
  decoder_joint.onnx      — TDT decoder-joint network
  vocab.txt               — SentencePiece vocabulary (one token per line)
  config.json             — Model metadata for the sidecar runtime

Usage:
  pip install -r requirements-asr-export.txt
  python export_parakeet_streaming.py --output-dir ./models/parakeet-tdt

Requires an NVIDIA GPU with CUDA for the export process itself. The exported
ONNX models can run on CPU or GPU via onnxruntime.

NeMo API notes (as of NeMo 2.x):
  - The model is loaded via nemo.collections.asr.models.EncDecRNNTBPEModel
  - Streaming is configured via encoder.setup_streaming_params()
  - ONNX export uses model.export() or torch.onnx.export on submodules
  - If NeMo changes its API, the key points to update are marked with
    "# NeMo API:" comments throughout this script.
"""

import argparse
import json
import os
import sys
import shutil
from pathlib import Path


def parse_args():
    parser = argparse.ArgumentParser(
        description="Export Parakeet-TDT 1.1B for streaming ONNX inference"
    )
    parser.add_argument(
        "--output-dir",
        type=str,
        required=True,
        help="Directory to write exported ONNX models and config",
    )
    parser.add_argument(
        "--model",
        type=str,
        default="nvidia/parakeet-tdt-1.1b",
        help="Pretrained model name on NGC (default: nvidia/parakeet-tdt-1.1b)",
    )
    parser.add_argument(
        "--chunk-size",
        type=int,
        default=80,
        help="Streaming chunk size in milliseconds (default: 80)",
    )
    return parser.parse_args()


def check_dependencies():
    """Verify that all required packages are importable."""
    missing = []
    for pkg in ["torch", "nemo", "onnx", "onnxruntime"]:
        try:
            __import__(pkg)
        except ImportError:
            # nemo_toolkit imports as "nemo"
            display = "nemo_toolkit[asr]" if pkg == "nemo" else pkg
            missing.append(display)
    if missing:
        print(f"ERROR: Missing required packages: {', '.join(missing)}")
        print("Install them with: pip install -r requirements-asr-export.txt")
        sys.exit(1)


def load_model(model_name: str):
    """
    Load the pretrained Parakeet-TDT model from NGC.

    NeMo API: Uses EncDecRNNTBPEModel.from_pretrained() which downloads
    and caches the model from NVIDIA NGC. If the API changes, update the
    model class and method name here.
    """
    print(f"Loading model: {model_name}")
    print("  (This may download ~1.1B parameters on first run)")

    # NeMo API: The model class for TDT (Token-and-Duration Transducer) models.
    # Parakeet-TDT uses the RNNT model class with TDT decoding.
    from nemo.collections.asr.models import EncDecRNNTBPEModel

    try:
        model = EncDecRNNTBPEModel.from_pretrained(model_name=model_name)
    except Exception as e:
        print(f"ERROR: Failed to load model '{model_name}': {e}")
        print("  Ensure the model name is correct and you have network access.")
        print("  For NGC models, you may need: ngc config set")
        sys.exit(1)

    model.eval()
    print(f"  Model loaded: {model.__class__.__name__}")
    return model


def configure_streaming(model, chunk_ms: int):
    """
    Configure the encoder for cache-aware streaming inference.

    NeMo API: FastConformer encoders support streaming via
    setup_streaming_params(). The key parameters are:
      - pre_encode_cache_size: encoder look-back cache
      - chunk_size: frames per streaming chunk
      - shift_size: how many frames to advance per chunk

    The chunk_size in frames depends on the model's subsampling factor
    and the desired chunk duration in milliseconds.
    """
    print(f"Configuring streaming with chunk_size={chunk_ms}ms")

    encoder = model.encoder

    # NeMo API: Compute chunk size in encoder frames from milliseconds.
    # The mel frontend produces one frame per 10ms (with default 10ms hop).
    # The encoder subsampling reduces this further.
    #
    # For FastConformer with 8x subsampling:
    #   chunk_frames = chunk_ms / 10 / subsampling_factor
    #   e.g., 80ms / 10ms / 8 = 1 frame (minimum streaming unit)
    #
    # However, setup_streaming_params expects the chunk size in pre-subsampling
    # frames, so we pass chunk_ms // 10 (i.e., mel frames).
    mel_frames_per_chunk = chunk_ms // 10  # 80ms -> 8 mel frames

    try:
        # NeMo API: Primary streaming configuration method.
        # This sets up the encoder's cache tensors (key/value caches for
        # self-attention and convolutional caches for conformer blocks).
        encoder.setup_streaming_params(
            chunk_size=mel_frames_per_chunk,
            shift_size=mel_frames_per_chunk,  # Non-overlapping chunks
            left_chunks=-1,  # Unlimited left context (use all cache)
        )
        print(f"  Streaming configured: {mel_frames_per_chunk} mel frames/chunk")
    except AttributeError:
        # NeMo API: Fallback for older NeMo versions that use a different method.
        print("  WARNING: setup_streaming_params() not found, trying alternative...")
        try:
            # Some versions use change_decoding_strategy or streaming_cfg
            if hasattr(model, "change_decoding_strategy"):
                model.change_decoding_strategy(
                    decoder_type="rnnt",
                    decoding_cfg={
                        "strategy": "greedy",
                        "greedy": {"max_symbols": 10},
                    },
                )
            encoder.streaming_cfg = {
                "chunk_size": mel_frames_per_chunk,
                "shift_size": mel_frames_per_chunk,
                "left_chunks": -1,
            }
            print("  Applied streaming config via fallback method")
        except Exception as e:
            print(f"  ERROR: Could not configure streaming: {e}")
            print("  The NeMo API may have changed. Check the encoder class for")
            print("  streaming configuration methods.")
            sys.exit(1)

    return mel_frames_per_chunk


def get_model_metadata(model, chunk_ms: int, model_name: str) -> dict:
    """
    Extract model metadata for the sidecar config.json.

    NeMo API: The key attributes we need are scattered across the model's
    submodules. If attribute names change, update the property access here.
    """
    metadata = {
        "model": model_name,
        "chunk_ms": chunk_ms,
        "sample_rate": 16000,
        "mel_bins": 128,
    }

    # NeMo API: Encoder hidden dimension
    try:
        metadata["encoder_dim"] = model.encoder.d_model
    except AttributeError:
        try:
            metadata["encoder_dim"] = model.encoder._cfg.d_model
        except AttributeError:
            metadata["encoder_dim"] = 1024  # Default for 1.1B model
            print("  WARNING: Could not detect encoder_dim, using default 1024")

    # NeMo API: Vocabulary size from the decoder's embedding
    try:
        metadata["vocab_size"] = model.decoder.vocab_size
    except AttributeError:
        try:
            metadata["vocab_size"] = len(model.tokenizer.vocab)
        except AttributeError:
            metadata["vocab_size"] = 1025  # Default for Parakeet-TDT
            print("  WARNING: Could not detect vocab_size, using default 1025")

    # TDT-specific: number of duration bins
    # NeMo API: TDT models have duration outputs in the joint network.
    try:
        metadata["num_duration_bins"] = model.joint.num_extra_outputs
    except AttributeError:
        metadata["num_duration_bins"] = 5  # Default for TDT
        print("  WARNING: Could not detect num_duration_bins, using default 5")

    # NeMo API: Blank token ID (usually 0 or vocab_size-1 for RNNT)
    try:
        metadata["blank_token_id"] = model.decoder.blank_idx
    except AttributeError:
        try:
            metadata["blank_token_id"] = model.decoding.blank_id
        except AttributeError:
            metadata["blank_token_id"] = metadata["vocab_size"] - 1
            print(
                f"  WARNING: Could not detect blank_token_id, using {metadata['blank_token_id']}"
            )

    # NeMo API: Subsampling factor (FastConformer default is 8 with 2x conv sub)
    try:
        metadata["subsampling_factor"] = model.encoder.subsampling_factor
    except AttributeError:
        try:
            metadata["subsampling_factor"] = model.encoder._cfg.subsampling_factor
        except AttributeError:
            metadata["subsampling_factor"] = 8  # Default for FastConformer
            print("  WARNING: Could not detect subsampling_factor, using default 8")

    # NeMo API: Mel spectrogram config
    try:
        preproc_cfg = model.preprocessor._cfg
        metadata["sample_rate"] = getattr(preproc_cfg, "sample_rate", 16000)
        metadata["mel_bins"] = getattr(preproc_cfg, "features", 128)
    except AttributeError:
        pass

    return metadata


def export_mel_frontend(model, output_dir: Path):
    """
    Export the mel spectrogram preprocessor to ONNX.

    NeMo API: The preprocessor converts raw audio waveforms to mel
    spectrograms. It's typically an AudioToMelSpectrogramPreprocessor.
    """
    import torch

    print("Exporting mel_frontend.onnx...")
    output_path = output_dir / "mel_frontend.onnx"

    preprocessor = model.preprocessor

    # NeMo API: The preprocessor expects (batch, samples) audio input
    # and returns (batch, mel_bins, time_frames) mel spectrograms.
    # Use 1 second of audio at 16kHz as the example input.
    sample_rate = 16000
    dummy_audio = torch.randn(1, sample_rate, device="cpu")
    dummy_length = torch.tensor([sample_rate], dtype=torch.long, device="cpu")

    preprocessor = preprocessor.cpu()
    preprocessor.eval()

    try:
        # NeMo API: Export preprocessor via torch.onnx.export.
        # The preprocessor's forward() takes (audio_signal, length) and
        # returns (mel_spectrogram, mel_length).
        torch.onnx.export(
            preprocessor,
            (dummy_audio, dummy_length),
            str(output_path),
            input_names=["audio_signal", "length"],
            output_names=["mel_spectrogram", "mel_length"],
            dynamic_axes={
                "audio_signal": {0: "batch", 1: "samples"},
                "length": {0: "batch"},
                "mel_spectrogram": {0: "batch", 2: "time"},
                "mel_length": {0: "batch"},
            },
            opset_version=17,
            do_constant_folding=True,
        )
        print(f"  Saved: {output_path} ({output_path.stat().st_size / 1024:.0f} KB)")
    except Exception as e:
        print(f"  ERROR exporting mel frontend: {e}")
        print("  Trying NeMo's built-in export method...")
        try:
            # NeMo API: Fallback using NeMo's export utility
            model.preprocessor.export(str(output_path))
            print(f"  Saved via NeMo export: {output_path}")
        except Exception as e2:
            print(f"  ERROR: Both export methods failed: {e2}")
            sys.exit(1)


def export_encoder(model, output_dir: Path, mel_frames_per_chunk: int):
    """
    Export the FastConformer encoder with streaming cache I/O tensors.

    NeMo API: After setup_streaming_params(), the encoder's forward()
    accepts and returns cache tensors for streaming inference. The cache
    tensors hold key/value states for self-attention and convolutional
    states for conformer blocks.
    """
    import torch

    print("Exporting encoder_streaming.onnx...")
    output_path = output_dir / "encoder_streaming.onnx"

    encoder = model.encoder.cpu()
    encoder.eval()

    # NeMo API: Determine cache shapes from the encoder configuration.
    # FastConformer uses:
    #   - Self-attention KV cache: (num_layers, batch, cache_len, d_model)
    #   - Conv cache: (num_layers, batch, d_model, kernel_size-1)
    try:
        num_layers = encoder.num_layers
    except AttributeError:
        num_layers = len(encoder.layers) if hasattr(encoder, "layers") else 24

    try:
        d_model = encoder.d_model
    except AttributeError:
        d_model = 1024

    # NeMo API: Get subsampling factor to compute actual encoder input frames
    try:
        sub_factor = encoder.subsampling_factor
    except AttributeError:
        sub_factor = 8

    # Dummy inputs for the streaming encoder
    # Input: mel spectrogram chunk (batch, mel_bins, chunk_frames)
    mel_bins = 128
    dummy_mel = torch.randn(1, mel_bins, mel_frames_per_chunk, device="cpu")
    dummy_mel_len = torch.tensor([mel_frames_per_chunk], dtype=torch.long, device="cpu")

    # NeMo API: Cache tensors for streaming. The exact shape depends on
    # the encoder architecture. We let the encoder create initial caches
    # and use those shapes.
    try:
        # NeMo API: Get initial cache from the encoder
        cache_last_channel, cache_last_time, cache_last_channel_len = (
            encoder.get_initial_cache_state(batch_size=1)
        )
        cache_inputs = (cache_last_channel, cache_last_time, cache_last_channel_len)
        input_names = [
            "mel_spectrogram",
            "mel_length",
            "cache_last_channel",
            "cache_last_time",
            "cache_last_channel_len",
        ]
        output_names = [
            "encoder_output",
            "encoder_length",
            "cache_last_channel_out",
            "cache_last_time_out",
            "cache_last_channel_len_out",
        ]
        export_args = (dummy_mel, dummy_mel_len) + cache_inputs

        # Build dynamic axes for all inputs/outputs
        dynamic_axes = {
            "mel_spectrogram": {0: "batch", 2: "time"},
            "mel_length": {0: "batch"},
            "encoder_output": {0: "batch", 1: "time"},
            "encoder_length": {0: "batch"},
        }
        # Cache tensors have batch dimension
        for name in [
            "cache_last_channel",
            "cache_last_time",
            "cache_last_channel_out",
            "cache_last_time_out",
        ]:
            dynamic_axes[name] = {1: "batch"}  # (layers, batch, ...)

    except (AttributeError, TypeError) as e:
        print(f"  WARNING: Could not get initial cache state: {e}")
        print("  Exporting encoder without explicit cache tensors.")
        print("  The encoder may still handle caching internally.")
        export_args = (dummy_mel, dummy_mel_len)
        input_names = ["mel_spectrogram", "mel_length"]
        output_names = ["encoder_output", "encoder_length"]
        dynamic_axes = {
            "mel_spectrogram": {0: "batch", 2: "time"},
            "mel_length": {0: "batch"},
            "encoder_output": {0: "batch", 1: "time"},
            "encoder_length": {0: "batch"},
        }

    try:
        torch.onnx.export(
            encoder,
            export_args,
            str(output_path),
            input_names=input_names,
            output_names=output_names,
            dynamic_axes=dynamic_axes,
            opset_version=17,
            do_constant_folding=True,
        )
        print(
            f"  Saved: {output_path} ({output_path.stat().st_size / 1024 / 1024:.1f} MB)"
        )
    except Exception as e:
        print(f"  ERROR exporting encoder: {e}")
        print("  Trying NeMo's built-in export...")
        try:
            model.encoder.export(str(output_path))
            print(f"  Saved via NeMo export: {output_path}")
        except Exception as e2:
            print(f"  ERROR: Both export methods failed: {e2}")
            sys.exit(1)


def export_decoder_joint(model, output_dir: Path):
    """
    Export the TDT decoder-joint network to ONNX.

    NeMo API: The RNNT/TDT decoder takes the encoder output and previous
    tokens, producing next-token logits. The joint network combines
    encoder and decoder outputs. For TDT models, the joint also outputs
    duration logits alongside token logits.
    """
    import torch

    print("Exporting decoder_joint.onnx...")
    output_path = output_dir / "decoder_joint.onnx"

    # NeMo API: Try NeMo's built-in export for decoder+joint first,
    # as the RNNT decoder-joint interaction is complex to export manually.
    try:
        # NeMo API: Some NeMo versions support exporting decoder+joint together.
        # The model.export() method can produce separate decoder and joint ONNX
        # files which we then combine.
        decoder = model.decoder.cpu()
        joint = model.joint.cpu()
        decoder.eval()
        joint.eval()

        # NeMo API: The decoder takes previous token IDs and hidden state.
        # For the first step, the hidden state is zeros.
        try:
            pred_hidden = decoder.predict(
                y=None, state=None, add_sos=True, batch_size=1
            )
            # pred_hidden is typically (output, hidden_state)
            if isinstance(pred_hidden, tuple):
                decoder_output, hidden = pred_hidden
            else:
                decoder_output = pred_hidden
                hidden = None
        except Exception:
            # Fallback: create dummy decoder input
            decoder_dim = getattr(
                decoder, "prednet_length", getattr(decoder, "pred_hidden", 640)
            )
            if hasattr(decoder, "_cfg") and hasattr(decoder._cfg, "prednet"):
                decoder_dim = decoder._cfg.prednet.pred_hidden
            decoder_output = torch.randn(1, 1, decoder_dim)
            hidden = None

        # NeMo API: The joint network takes encoder_output and decoder_output.
        # For TDT models, it returns (token_logits, duration_logits).
        encoder_dim = 1024
        try:
            encoder_dim = model.encoder.d_model
        except AttributeError:
            pass

        dummy_encoder_out = torch.randn(1, 1, encoder_dim)

        # Ensure decoder_output has correct shape (batch, 1, decoder_dim)
        if decoder_output.dim() == 2:
            decoder_output = decoder_output.unsqueeze(1)

        # Export the joint network
        try:
            torch.onnx.export(
                joint,
                (dummy_encoder_out, decoder_output),
                str(output_path),
                input_names=["encoder_output", "decoder_output"],
                output_names=["token_logits", "duration_logits"],
                dynamic_axes={
                    "encoder_output": {0: "batch"},
                    "decoder_output": {0: "batch"},
                    "token_logits": {0: "batch"},
                    "duration_logits": {0: "batch"},
                },
                opset_version=17,
                do_constant_folding=True,
            )
            print(
                f"  Saved: {output_path} ({output_path.stat().st_size / 1024:.0f} KB)"
            )
        except Exception as e:
            # NeMo API: If direct export fails, try model-level export
            print(f"  WARNING: Direct joint export failed: {e}")
            print("  Trying model-level ONNX export...")
            raise

    except Exception as e:
        print(f"  Attempting NeMo model.export() for decoder+joint...")
        try:
            # NeMo API: model.export() exports all components.
            # We need to find the decoder-joint output and rename it.
            tmp_dir = output_dir / "_tmp_export"
            tmp_dir.mkdir(exist_ok=True)
            model.export(str(tmp_dir / "model.onnx"))

            # NeMo typically produces: model_decoder.onnx, model_joint.onnx
            # or a combined decoder_joint.onnx
            exported = list(tmp_dir.glob("*.onnx"))
            print(f"  NeMo exported: {[f.name for f in exported]}")

            # Find and copy the decoder-joint files
            for f in exported:
                if "decoder" in f.name.lower() or "joint" in f.name.lower():
                    shutil.copy2(f, output_path)
                    break
            else:
                # If no specific decoder/joint file, use the main model
                main_onnx = tmp_dir / "model.onnx"
                if main_onnx.exists():
                    shutil.copy2(main_onnx, output_path)

            # Clean up temp directory
            shutil.rmtree(tmp_dir, ignore_errors=True)

            if output_path.exists():
                print(
                    f"  Saved: {output_path} ({output_path.stat().st_size / 1024:.0f} KB)"
                )
            else:
                print("  ERROR: No decoder-joint ONNX produced")
                sys.exit(1)
        except Exception as e2:
            print(f"  ERROR: All export methods failed: {e2}")
            sys.exit(1)


def extract_vocabulary(model, output_dir: Path):
    """
    Extract the SentencePiece vocabulary to a text file.

    NeMo API: The tokenizer is accessible via model.tokenizer, which
    wraps a SentencePiece model. We extract all tokens to vocab.txt
    with one token per line.
    """
    print("Extracting vocabulary...")
    output_path = output_dir / "vocab.txt"

    try:
        tokenizer = model.tokenizer

        # NeMo API: Get vocabulary from the tokenizer
        vocab = []

        if hasattr(tokenizer, "vocab"):
            # Direct vocab attribute (list or dict)
            if isinstance(tokenizer.vocab, dict):
                # Sort by ID to get ordered vocabulary
                sorted_vocab = sorted(tokenizer.vocab.items(), key=lambda x: x[1])
                vocab = [token for token, _ in sorted_vocab]
            else:
                vocab = list(tokenizer.vocab)
        elif hasattr(tokenizer, "tokenizer") and hasattr(
            tokenizer.tokenizer, "get_piece_size"
        ):
            # SentencePiece tokenizer wrapper
            sp = tokenizer.tokenizer
            vocab = [sp.id_to_piece(i) for i in range(sp.get_piece_size())]
        elif hasattr(tokenizer, "vocab_size"):
            # Try index-based access
            vocab_size = tokenizer.vocab_size
            for i in range(vocab_size):
                try:
                    vocab.append(tokenizer.ids_to_tokens([i])[0])
                except (IndexError, KeyError):
                    vocab.append(f"<unk_{i}>")

        if not vocab:
            print("  WARNING: Could not extract vocabulary from tokenizer")
            print("  Trying to find SentencePiece model file...")

            # NeMo API: Fallback - find the .model file in NeMo's cache
            if hasattr(tokenizer, "vocab_file"):
                sp_model = tokenizer.vocab_file
            elif hasattr(tokenizer, "tokenizer_model"):
                sp_model = tokenizer.tokenizer_model
            else:
                sp_model = None

            if sp_model and os.path.exists(sp_model):
                import sentencepiece as spm

                sp = spm.SentencePieceProcessor()
                sp.load(sp_model)
                vocab = [sp.id_to_piece(i) for i in range(sp.get_piece_size())]

        if vocab:
            with open(output_path, "w", encoding="utf-8") as f:
                for token in vocab:
                    f.write(f"{token}\n")
            print(f"  Saved: {output_path} ({len(vocab)} tokens)")
        else:
            print("  ERROR: Could not extract vocabulary")
            print("  You may need to manually extract vocab.txt from the model")
            sys.exit(1)

    except Exception as e:
        print(f"  ERROR extracting vocabulary: {e}")
        sys.exit(1)


def write_config(metadata: dict, output_dir: Path):
    """Write config.json with model metadata for the Go sidecar."""
    print("Writing config.json...")
    output_path = output_dir / "config.json"

    with open(output_path, "w", encoding="utf-8") as f:
        json.dump(metadata, f, indent=2)
        f.write("\n")

    print(f"  Saved: {output_path}")


def print_summary(output_dir: Path, metadata: dict):
    """Print a summary of what was exported."""
    print()
    print("=" * 60)
    print("Export complete!")
    print("=" * 60)
    print()
    print(f"  Output directory: {output_dir}")
    print(f"  Model:            {metadata['model']}")
    print(f"  Chunk size:       {metadata['chunk_ms']}ms")
    print(f"  Sample rate:      {metadata['sample_rate']} Hz")
    print(f"  Mel bins:         {metadata['mel_bins']}")
    print(f"  Encoder dim:      {metadata['encoder_dim']}")
    print(f"  Vocab size:       {metadata['vocab_size']}")
    print(f"  Duration bins:    {metadata['num_duration_bins']}")
    print(f"  Blank token ID:   {metadata['blank_token_id']}")
    print(f"  Subsampling:      {metadata['subsampling_factor']}x")
    print()
    print("  Files:")
    for f in sorted(output_dir.iterdir()):
        if f.name.startswith("_"):
            continue
        size = f.stat().st_size
        if size > 1024 * 1024:
            size_str = f"{size / 1024 / 1024:.1f} MB"
        elif size > 1024:
            size_str = f"{size / 1024:.0f} KB"
        else:
            size_str = f"{size} B"
        print(f"    {f.name:30s} {size_str}")
    print()
    print("  Usage with asr-sidecar:")
    print(f"    asr-sidecar --model-dir {output_dir}")
    print()


def main():
    args = parse_args()
    output_dir = Path(args.output_dir)

    # Check dependencies before doing anything else
    check_dependencies()

    # Create output directory
    output_dir.mkdir(parents=True, exist_ok=True)
    print(f"Output directory: {output_dir}")
    print()

    # Step 1: Load the pretrained model from NGC
    model = load_model(args.model)
    print()

    # Step 2: Configure cache-aware streaming on the encoder
    mel_frames_per_chunk = configure_streaming(model, args.chunk_size)
    print()

    # Step 3: Extract model metadata (before export, while model is intact)
    metadata = get_model_metadata(model, args.chunk_size, args.model)
    print()

    # Step 4: Export mel frontend
    export_mel_frontend(model, output_dir)
    print()

    # Step 5: Export streaming encoder with cache I/O
    export_encoder(model, output_dir, mel_frames_per_chunk)
    print()

    # Step 6: Export decoder-joint network
    export_decoder_joint(model, output_dir)
    print()

    # Step 7: Extract vocabulary
    extract_vocabulary(model, output_dir)
    print()

    # Step 8: Write config.json
    write_config(metadata, output_dir)
    print()

    # Step 9: Summary
    print_summary(output_dir, metadata)


if __name__ == "__main__":
    main()
