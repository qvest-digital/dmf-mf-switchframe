package dve

import (
	"fmt"
	"testing"

	"github.com/zsiec/switchframe/server/internal/video"
)

func BenchmarkCPUTransition1080p(b *testing.B) {
	const w, h = 1920, 1080
	for _, f := range []video.Format{video.YUV420_8bit, video.YUV422_10bit} {
		from := video.NewBlackFrame(f, w, h)
		to := video.NewBlackFrame(f, w, h)
		for i := range from {
			from[i] = byte(i * 7)
			to[i] = byte(i * 13)
		}
		if f == video.YUV422_10bit {
			for i := 1; i < len(from); i += 2 {
				from[i] &= 3
				to[i] &= 3
			}
		}
		dst := make([]byte, f.FrameSize(w, h))
		for _, ty := range []string{"dve-push-left", "dve-spin", "dve-shrink-grow", "dve-barn-h", "dve-corner-peel"} {
			fn := CPUTransitionForType(ty)
			b.Run(fmt.Sprintf("%s/%v", ty, f), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					fn(dst, from, to, w, h, f, 0.5)
				}
			})
		}
	}
}
