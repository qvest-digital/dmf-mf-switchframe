//go:build !cgo || !cuda

package arinfer

import (
	"testing"
)

func TestInferenceProxyStub_NilSafe(t *testing.T) {
	var p *InferenceProxy

	p.ProcessFrame(0x1234, 1920, 1080, 2048)
	if p.LastOcclusion() != nil {
		t.Error("expected nil occlusion")
	}
	if p.IsActive() {
		t.Error("expected not active")
	}
	hook := p.Hook()
	if hook == nil {
		t.Error("expected non-nil hook function")
	}
	if err := p.EnableDepth(""); err != ErrDisabled {
		t.Errorf("expected ErrDisabled, got %v", err)
	}
	p.DisableDepth()
	if err := p.EnableSAM("", ""); err != ErrDisabled {
		t.Errorf("expected ErrDisabled, got %v", err)
	}
	p.DisableSAM()
	if err := p.AddPrompt(SAMPrompt{}); err != ErrDisabled {
		t.Errorf("expected ErrDisabled, got %v", err)
	}
	if err := p.RemovePrompt("x"); err != ErrDisabled {
		t.Errorf("expected ErrDisabled, got %v", err)
	}
	if p.Prompts() != nil {
		t.Error("expected nil prompts")
	}
	_ = p.Status()
	d, err := p.DepthAtPixel(0, 0, 1920, 1080)
	if err != nil || d != 2.0 {
		t.Errorf("expected depth=2.0, got %f (err=%v)", d, err)
	}
	p.HandleMessage(nil)
	p.RecoverStaleSlots()
	p.Close()
}

func TestInferenceProxyStub_NewReturnsNil(t *testing.T) {
	p := NewInferenceProxy(nil, nil, nil)
	if p != nil {
		t.Error("expected nil proxy on stub build")
	}
}

func TestInferenceProxyStub_ImplementsInferrer(t *testing.T) {
	var _ Inferrer = (*InferenceProxy)(nil)
}
