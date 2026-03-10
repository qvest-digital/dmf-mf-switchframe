package audio_test

import (
	"fmt"

	"github.com/zsiec/switchframe/server/audio"
)

func ExamplePeakLevel() {
	// Stereo interleaved PCM: [L0, R0, L1, R1]
	pcm := []float32{0.5, -0.3, 0.8, -0.9}
	peakL, peakR := audio.PeakLevel(pcm, 2)

	fmt.Printf("L=%.1f R=%.1f\n", peakL, peakR)
	// Output:
	// L=0.8 R=0.9
}

func ExampleLinearToDBFS() {
	fmt.Printf("%.1f dBFS\n", audio.LinearToDBFS(1.0)) // full scale
	fmt.Printf("%.1f dBFS\n", audio.LinearToDBFS(0.5)) // half amplitude
	fmt.Printf("%.1f dBFS\n", audio.LinearToDBFS(0.0)) // silence
	// Output:
	// 0.0 dBFS
	// -6.0 dBFS
	// -96.0 dBFS
}

func ExampleEqualPowerCrossfade() {
	// Crossfade from silence to a constant signal over 4 samples.
	old := []float32{0, 0, 0, 0}
	new := []float32{1, 1, 1, 1}

	result := audio.EqualPowerCrossfade(old, new)

	// The new source fades in following a sin curve: sin(t * pi/2).
	// At t=0 the new source is silent; at t=1 it reaches full volume.
	// With 4 samples and 3 intervals, positions are t=0, 1/3, 2/3, 1.
	for i, v := range result {
		fmt.Printf("sample[%d] = %.4f\n", i, v)
	}
	// Output:
	// sample[0] = 0.0000
	// sample[1] = 0.5000
	// sample[2] = 0.8660
	// sample[3] = 1.0000
}
