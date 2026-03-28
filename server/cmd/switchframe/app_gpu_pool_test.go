package main

import (
	"testing"

	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/switcher"
)

func TestGPUPoolFormatForColor(t *testing.T) {
	tests := []struct {
		name       string
		colorFmt   switcher.PipelineColorFormat
		wantFormat gpu.GPUFrameFormat
	}{
		{"standard_8bit", switcher.ColorYUV420_8bit, gpu.GPUFormatNV12},
		{"professional_10bit", switcher.ColorYUV422_10bit, gpu.GPUFormatP210},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gpuPoolFormatForColor(tt.colorFmt)
			if got != tt.wantFormat {
				t.Errorf("gpuPoolFormatForColor(%v) = %v, want %v", tt.colorFmt, got, tt.wantFormat)
			}
		})
	}
}
