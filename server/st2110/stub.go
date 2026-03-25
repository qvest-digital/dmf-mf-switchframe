//go:build !cgo || !st2110

package st2110

import "context"

// Instance is a stub for builds without the st2110 build tag.
type Instance struct{}

// NewInstance returns ErrST2110NotAvailable when the st2110 build tag is not set.
func NewInstance(cfg InstanceConfig) (*Instance, error) {
	return nil, ErrST2110NotAvailable
}

// Close is a no-op stub.
func (i *Instance) Close() error {
	return nil
}

// PTPStatus returns ErrST2110NotAvailable.
func (i *Instance) PTPStatus() (PTPStatus, error) {
	return PTPStatus{}, ErrST2110NotAvailable
}

// VideoRx is a stub ST 2110-20 video receiver.
type VideoRx struct{}

// VideoTx is a stub ST 2110-20 video transmitter.
type VideoTx struct{}

// AudioRx is a stub ST 2110-30 audio receiver.
type AudioRx struct{}

// AudioTx is a stub ST 2110-30 audio transmitter.
type AudioTx struct{}

// AncillaryRx is a stub ST 2110-40 ancillary data receiver.
type AncillaryRx struct{}

// AncillaryTx is a stub ST 2110-40 ancillary data transmitter.
type AncillaryTx struct{}

// CreateVideoRx returns ErrST2110NotAvailable.
func (i *Instance) CreateVideoRx(cfg RxVideoConfig) (*VideoRx, error) {
	return nil, ErrST2110NotAvailable
}

// CreateVideoTx returns ErrST2110NotAvailable.
func (i *Instance) CreateVideoTx(cfg TxVideoConfig) (*VideoTx, error) {
	return nil, ErrST2110NotAvailable
}

// CreateAudioRx returns ErrST2110NotAvailable.
func (i *Instance) CreateAudioRx(cfg RxAudioConfig) (*AudioRx, error) {
	return nil, ErrST2110NotAvailable
}

// CreateAudioTx returns ErrST2110NotAvailable.
func (i *Instance) CreateAudioTx(cfg TxAudioConfig) (*AudioTx, error) {
	return nil, ErrST2110NotAvailable
}

// CreateAncillaryRx returns ErrST2110NotAvailable.
func (i *Instance) CreateAncillaryRx(cfg RxAncillaryConfig) (*AncillaryRx, error) {
	return nil, ErrST2110NotAvailable
}

// CreateAncillaryTx returns ErrST2110NotAvailable.
func (i *Instance) CreateAncillaryTx(cfg TxAncillaryConfig) (*AncillaryTx, error) {
	return nil, ErrST2110NotAvailable
}

// --- VideoRx stub methods ---

// GetFrame returns ErrST2110NotAvailable.
func (rx *VideoRx) GetFrame(ctx context.Context) ([]byte, int, int, int64, error) {
	return nil, 0, 0, 0, ErrST2110NotAvailable
}

// Close is a no-op stub.
func (rx *VideoRx) Close() error { return nil }

// --- AudioRx stub methods ---

// GetFrame returns ErrST2110NotAvailable.
func (rx *AudioRx) GetFrame(ctx context.Context) ([]float32, int, error) {
	return nil, 0, ErrST2110NotAvailable
}

// Close is a no-op stub.
func (rx *AudioRx) Close() error { return nil }

// --- AncillaryRx stub methods ---

// GetFrame returns ErrST2110NotAvailable.
func (rx *AncillaryRx) GetFrame(ctx context.Context) ([]byte, error) {
	return nil, ErrST2110NotAvailable
}

// Close is a no-op stub.
func (rx *AncillaryRx) Close() error { return nil }

// --- VideoTx stub methods ---

// PutFrame returns ErrST2110NotAvailable.
func (tx *VideoTx) PutFrame(yuv []byte) error { return ErrST2110NotAvailable }

// Close is a no-op stub.
func (tx *VideoTx) Close() error { return nil }

// --- AudioTx stub methods ---

// PutFrame returns ErrST2110NotAvailable.
func (tx *AudioTx) PutFrame(pcm []float32) error { return ErrST2110NotAvailable }

// Close is a no-op stub.
func (tx *AudioTx) Close() error { return nil }

// --- AncillaryTx stub methods ---

// PutFrame returns ErrST2110NotAvailable.
func (tx *AncillaryTx) PutFrame(data []byte) error { return ErrST2110NotAvailable }

// Close is a no-op stub.
func (tx *AncillaryTx) Close() error { return nil }
