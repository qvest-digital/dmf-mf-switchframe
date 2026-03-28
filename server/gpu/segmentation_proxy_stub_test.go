//go:build !cgo || !cuda

package gpu

import (
	"testing"
)

func TestSegmentationProxyStub_NilSafe(t *testing.T) {
	var p *SegmentationProxy

	// All methods should be safe on nil proxy.
	if err := p.EnableSource("cam1", 1920, 1080, 0.5); err == nil {
		t.Error("expected error from nil proxy EnableSource")
	}
	p.DisableSource("cam1")
	p.SetPendingConfig("cam1", 0.5)
	if p.HasPendingConfig("cam1") {
		t.Error("expected HasPendingConfig=false")
	}
	if p.PendingSmoothing("cam1") != 0 {
		t.Error("expected PendingSmoothing=0")
	}
	p.ClearPendingConfig("cam1")
	if p.IsEnabled("cam1") {
		t.Error("expected IsEnabled=false")
	}
	p.Segment("cam1", nil)
	if p.MaskForSource("cam1") != nil {
		t.Error("expected MaskForSource=nil")
	}
	p.Close()
}

func TestSegmentationProxyStub_NewReturnsNil(t *testing.T) {
	p := NewSegmentationProxy(nil, nil)
	if p != nil {
		t.Error("expected nil proxy on stub build")
	}
}

func TestSegmentationProxyStub_ImplementsSegmenter(t *testing.T) {
	// Compile-time check.
	var _ Segmenter = (*SegmentationProxy)(nil)
}
