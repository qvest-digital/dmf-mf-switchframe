//go:build !cgo || !cuda

package tracking

import "testing"

func TestVisualTrackingProxyStub_NilSafe(t *testing.T) {
	var p *VisualTrackingProxy

	if p.Name() != "visual-stub" {
		t.Errorf("expected name=visual-stub, got %q", p.Name())
	}
	if p.Type() != "visual" {
		t.Errorf("expected type=visual, got %q", p.Type())
	}
	if err := p.Start(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if p.Running() {
		t.Error("expected not running")
	}
	if p.LatestPose() != nil {
		t.Error("expected nil pose")
	}
	p.ProcessFrame(0x1234, 1920, 1080, 2048)
	p.HandleMessage(nil)
	if err := p.Close(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestVisualTrackingProxyStub_NewReturnsNil(t *testing.T) {
	p := NewVisualTrackingProxy(nil, nil, "cam1", nil)
	if p != nil {
		t.Error("expected nil proxy on stub build")
	}
}

func TestVisualTrackingProxyStub_ImplementsTrackingSource(t *testing.T) {
	var _ TrackingSource = (*VisualTrackingProxy)(nil)
}
