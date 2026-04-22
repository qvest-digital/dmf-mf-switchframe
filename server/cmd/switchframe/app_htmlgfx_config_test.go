package main

import (
	"testing"

	"github.com/zsiec/switchframe/server/htmlgfx"
)

func TestBuildHTMLGfxConfig_DefaultsWhenFlagsEmpty(t *testing.T) {
	got := buildHTMLGfxConfig(AppConfig{}, 1920, 1080, 30)
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
	got := buildHTMLGfxConfig(AppConfig{CEFBinary: "/opt/custom/sf-cef"}, 1280, 720, 60)
	if got.BinaryPath != "/opt/custom/sf-cef" {
		t.Errorf("BinaryPath: got %q", got.BinaryPath)
	}
	if got.TemplatesDir != "/opt/switchframe-cef/demo-templates" {
		t.Errorf("TemplatesDir should keep default, got %q", got.TemplatesDir)
	}
}
