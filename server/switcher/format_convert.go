package switcher

import "github.com/zsiec/switchframe/server/internal/video"

// Upconvert420to422_10 converts a YUV420P 8-bit frame to YUV422P10LE.
func Upconvert420to422_10(src, dst []byte, width, height int) error {
	return video.Upconvert420to422_10(src, dst, width, height)
}

// Downconvert422_10to420 converts a YUV422P10LE frame to YUV420P 8-bit.
func Downconvert422_10to420(src, dst []byte, width, height int) error {
	return video.Downconvert422_10to420(src, dst, width, height)
}
