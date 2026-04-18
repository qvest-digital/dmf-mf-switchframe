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

func TestResolveWhisperThreads(t *testing.T) {
	tests := []struct {
		name      string
		requested int
		numCPU    int
		want      int
	}{
		{"explicit positive passes through", 6, 16, 6},
		{"zero falls back to numCPU clamped at 8", 0, 16, 8},
		{"zero on small box uses numCPU", 0, 4, 4},
		{"zero on tiny box gets at least 1", 0, 0, 1},
		{"negative clamped to at least 1", -3, 16, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveWhisperThreads(tt.requested, tt.numCPU)
			if got != tt.want {
				t.Errorf("resolveWhisperThreads(%d, %d) = %d, want %d", tt.requested, tt.numCPU, got, tt.want)
			}
		})
	}
}

func TestComputeAudioCtx(t *testing.T) {
	tests := []struct {
		name      string
		samples   int
		want      int
		comment   string
	}{
		{"empty audio returns default (0)", 0, 0, "defensive"},
		{"1s rounds up from 5s floor", 16000, 384, "5s -> 378 -> 384"},
		{"5s exact returns 384", 16000 * 5, 384, "(5/30)*1500+128=378 -> 384"},
		{"10s returns 640", 16000 * 10, 640, "(10/30)*1500+128=628 -> 640"},
		{"15s returns 896", 16000 * 15, 896, "(15/30)*1500+128=878 -> 896"},
		{"20s returns 1152", 16000 * 20, 1152, "(20/30)*1500+128=1128 -> 1152"},
		{"25s returns 1408", 16000 * 25, 1408, "(25/30)*1500+128=1378 -> 1408"},
		{"30s returns 0 (use whisper default)", 16000 * 30, 0, "no truncation needed"},
		{"40s returns 0", 16000 * 40, 0, "above default already"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeAudioCtx(tt.samples, 16000)
			if got != tt.want {
				t.Errorf("computeAudioCtx(%d samples) = %d, want %d (%s)", tt.samples, got, tt.want, tt.comment)
			}
		})
	}
}

func TestComputeAudioCtx_AlwaysMultipleOf64OrZero(t *testing.T) {
	for samples := 0; samples <= 16000*30; samples += 16000 {
		got := computeAudioCtx(samples, 16000)
		if got != 0 && got%64 != 0 {
			t.Errorf("computeAudioCtx(%d) = %d, not multiple of 64", samples, got)
		}
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
