package main

import (
	"strings"
	"testing"

	"github.com/zsiec/switchframe/server/htmlgfx"
)

func TestBuildHTMLGfxConfig_DefaultsWhenFlagsEmpty(t *testing.T) {
	got := buildHTMLGfxConfig(AppConfig{HTML5Graphics: true}, 1920, 1080, 30)
	want := htmlgfx.Config{
		BinaryPath:   "switchframe-cef",
		TemplatesDir: "/opt/switchframe-cef/demo-templates",
		PoolSize:     4,
		Width:        1920,
		Height:       1080,
		FPS:          30,
	}
	if got != want {
		t.Fatalf("default config: got %+v, want %+v", got, want)
	}
}

func TestBuildHTMLGfxConfig_OverridesWhenFlagsSet(t *testing.T) {
	cfg := AppConfig{
		HTML5Graphics:   true,
		CEFBinary:       "bin/switchframe-cef.app/Contents/MacOS/switchframe-cef",
		CEFTemplatesDir: "cef-sidecar/demo-templates",
	}
	got := buildHTMLGfxConfig(cfg, 1920, 1080, 30)
	if got.BinaryPath != "bin/switchframe-cef.app/Contents/MacOS/switchframe-cef" {
		t.Errorf("BinaryPath: got %q", got.BinaryPath)
	}
	if got.TemplatesDir != "cef-sidecar/demo-templates" {
		t.Errorf("TemplatesDir: got %q", got.TemplatesDir)
	}
	if got.Width != 1920 || got.Height != 1080 || got.FPS != 30 {
		t.Errorf("dimensions/fps got mangled: %+v", got)
	}
}

func TestBuildHTMLGfxConfig_PartialOverride_BinaryOnly(t *testing.T) {
	got := buildHTMLGfxConfig(AppConfig{HTML5Graphics: true, CEFBinary: "/opt/custom/sf-cef"}, 1280, 720, 60)
	if got.BinaryPath != "/opt/custom/sf-cef" {
		t.Errorf("BinaryPath: got %q", got.BinaryPath)
	}
	if got.TemplatesDir != "/opt/switchframe-cef/demo-templates" {
		t.Errorf("TemplatesDir should keep default, got %q", got.TemplatesDir)
	}
}

// With HTML5 graphics off, the sidecar must not be spawned even where its
// binary exists, so a deployment that does not use HTML5 graphics carries no
// Chromium process. An empty BinaryPath is what makes Manager.Start skip the
// spawn.
func TestBuildHTMLGfxConfig_DisabledLeavesNoBinary(t *testing.T) {
	for _, cfg := range []AppConfig{
		{},
		{CEFBinary: "/opt/custom/sf-cef"},
	} {
		got := buildHTMLGfxConfig(cfg, 1920, 1080, 30)
		if got.BinaryPath != "" {
			t.Errorf("CEFBinary %q: BinaryPath = %q, want empty", cfg.CEFBinary, got.BinaryPath)
		}
	}
}

// An operator whose HTML5 graphics are off has to see that the flag turned
// them off, not a missing binary, which sends them looking for a file.
func TestBuildHTMLGfxConfig_LogsWhyTheSidecarIsNotSpawned(t *testing.T) {
	logged := captureSlog(t)
	buildHTMLGfxConfig(AppConfig{}, 1920, 1080, 30)
	if !strings.Contains(logged.String(), "--html5-graphics is false") {
		t.Errorf("disabled config logged %q, want the flag named", logged.String())
	}

	logged.Reset()
	buildHTMLGfxConfig(AppConfig{HTML5Graphics: true}, 1920, 1080, 30)
	if strings.Contains(logged.String(), "--html5-graphics") {
		t.Errorf("enabled config logged %q", logged.String())
	}
}
