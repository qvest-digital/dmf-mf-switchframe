package main

import "testing"

func TestBuildWhisperFullOpts_LiveBroadcastDefaults(t *testing.T) {
	o := buildWhisperFullOpts(whisperRequest{
		Language:  "en",
		Translate: false,
		NThreads:  4,
	})

	if !o.NoContext {
		t.Error("NoContext = false, want true (avoid cross-chunk hallucination contagion)")
	}
	if !o.SingleSegment {
		t.Error("SingleSegment = false, want true (one segment per chunk for streaming)")
	}
	if o.MaxTokens != 32 {
		t.Errorf("MaxTokens = %d, want 32 (worst-case latency bound per chunk)", o.MaxTokens)
	}
	if o.Temperature != 0 {
		t.Errorf("Temperature = %f, want 0 (deterministic decode)", o.Temperature)
	}
	if o.TemperatureInc != 0 {
		t.Errorf("TemperatureInc = %f, want 0 (disable 6-step fallback)", o.TemperatureInc)
	}
	if !o.SuppressBlank {
		t.Error("SuppressBlank = false, want true")
	}
	if !o.SuppressNonSpeech {
		t.Error("SuppressNonSpeech = false, want true")
	}
	if !o.TokenTimestamps {
		t.Error("TokenTimestamps = false, want true (per-word confidence + timing)")
	}
	if o.NThreads != 4 {
		t.Errorf("NThreads = %d, want 4 (passthrough)", o.NThreads)
	}
	if o.Language != "en" {
		t.Errorf("Language = %q, want en", o.Language)
	}
	if o.Translate {
		t.Error("Translate = true, want false (passthrough)")
	}
}

func TestBuildWhisperFullOpts_PassesThroughTranslate(t *testing.T) {
	o := buildWhisperFullOpts(whisperRequest{Language: "fr", Translate: true, NThreads: 2})
	if !o.Translate {
		t.Error("Translate = false, want true when requested")
	}
	if o.Language != "fr" {
		t.Errorf("Language = %q, want fr", o.Language)
	}
}

func TestBuildWhisperContextOpts_FlashAttnAndGPU(t *testing.T) {
	o := buildWhisperContextOpts(true)
	if !o.FlashAttn {
		t.Error("FlashAttn = false, want true (~2x attention speedup, both CUDA and Metal)")
	}
	if !o.UseGPU {
		t.Error("UseGPU = false, want true when requested")
	}

	o2 := buildWhisperContextOpts(false)
	if o2.UseGPU {
		t.Error("UseGPU = true, want false when GPU disabled")
	}
	if !o2.FlashAttn {
		t.Error("FlashAttn should still be true even on CPU (cheap, sometimes a small win)")
	}
}
