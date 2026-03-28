//go:build !cgo || !cuda || !tensorrt

package arinfer

// sam2Model is a stub for builds without TensorRT/CUDA support.
type sam2Model struct {
	prompts []SAMPrompt
}

// newSAM2Model returns ErrDisabled on non-TensorRT builds.
func newSAM2Model(encoderPlanPath, decoderPlanPath string) (*sam2Model, error) {
	return nil, ErrDisabled
}

// Encode returns ErrDisabled on non-TensorRT builds.
func (m *sam2Model) Encode(inputPtr uintptr, w, h, pitch int, format FrameFormat) error {
	return ErrDisabled
}

// Propagate returns ErrDisabled on non-TensorRT builds.
func (m *sam2Model) Propagate(inputPtr uintptr, w, h, pitch int, outputPtr uintptr, format FrameFormat) error {
	return ErrDisabled
}

// MaskOutputPtr returns 0 on non-TensorRT builds (no GPU buffer).
func (m *sam2Model) MaskOutputPtr() uintptr { return 0 }

// AddPrompt stores the prompt locally even on stub builds.
// This allows prompt management to be tested without GPU hardware.
func (m *sam2Model) AddPrompt(p SAMPrompt) error {
	if m == nil {
		return ErrDisabled
	}
	if p.ID == "" {
		return ErrPromptFailed
	}
	if p.Type != "point" && p.Type != "box" {
		return ErrPromptFailed
	}

	for _, existing := range m.prompts {
		if existing.ID == p.ID {
			return ErrPromptFailed
		}
	}

	m.prompts = append(m.prompts, p)
	return nil
}

// RemovePrompt removes a prompt by ID even on stub builds.
func (m *sam2Model) RemovePrompt(id string) error {
	if m == nil {
		return ErrDisabled
	}

	for i, p := range m.prompts {
		if p.ID == id {
			m.prompts = append(m.prompts[:i], m.prompts[i+1:]...)
			return nil
		}
	}

	return ErrPromptFailed
}

// Prompts returns a copy of all active prompts.
func (m *sam2Model) Prompts() []SAMPrompt {
	if m == nil {
		return nil
	}

	if len(m.prompts) == 0 {
		return nil
	}

	out := make([]SAMPrompt, len(m.prompts))
	copy(out, m.prompts)
	return out
}

// PreallocMask returns 0 on non-TensorRT builds.
func (m *sam2Model) PreallocMask(w, h int) uintptr { return 0 }

// Close is a no-op on non-TensorRT builds.
func (m *sam2Model) Close() {}
