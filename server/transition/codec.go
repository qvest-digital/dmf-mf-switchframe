package transition

// VideoDecoder decodes AVC1/Annex B wire data into YUV420 planar buffers.
// Implementations: OpenH264Decoder (cgo), mockDecoder (tests).
type VideoDecoder interface {
	// Decode decodes encoded video data and returns YUV420 planar bytes,
	// width, height, and any error. The returned YUV buffer length is
	// width * height * 3/2.
	Decode(data []byte) (yuv []byte, width, height int, err error)

	// Close releases decoder resources.
	Close()
}

// VideoEncoder encodes YUV420 planar frames into AVC1/Annex B wire data.
// Implementations: OpenH264Encoder (cgo), mockEncoder (tests).
type VideoEncoder interface {
	// Encode encodes a YUV420 planar frame. If forceIDR is true, the
	// encoder produces a keyframe. Returns encoded data, whether the
	// frame is a keyframe, and any error.
	Encode(yuv []byte, forceIDR bool) (data []byte, isKeyframe bool, err error)

	// Close releases encoder resources.
	Close()
}

// DecoderFactory creates a new VideoDecoder.
// Allows tests to inject mock factories without cgo.
type DecoderFactory func() (VideoDecoder, error)

// EncoderFactory creates a new VideoEncoder with the given parameters.
// Allows tests to inject mock factories without cgo.
type EncoderFactory func(width, height, bitrate int, fps float32) (VideoEncoder, error)

// --- Mock implementations for testing ---

// mockDecoder returns pre-configured YUV data. Used in unit tests to
// avoid cgo/OpenH264 dependency.
type mockDecoder struct {
	width  int
	height int
	yuvOut []byte // if nil, allocates width*height*3/2 zeros
}

func (d *mockDecoder) Decode(data []byte) ([]byte, int, int, error) {
	if d.yuvOut != nil {
		return d.yuvOut, d.width, d.height, nil
	}
	return make([]byte, d.width*d.height*3/2), d.width, d.height, nil
}

func (d *mockDecoder) Close() {}

// mockEncoder returns pre-configured AVC data. Used in unit tests.
type mockEncoder struct {
	avcOut     []byte // if nil, returns minimal placeholder
	isKeyframe bool
}

func (e *mockEncoder) Encode(yuv []byte, forceIDR bool) ([]byte, bool, error) {
	isIDR := forceIDR || e.isKeyframe
	if e.avcOut != nil {
		return e.avcOut, isIDR, nil
	}
	return []byte{0x00, 0x00, 0x00, 0x01, 0x65}, isIDR, nil
}

func (e *mockEncoder) Close() {}
