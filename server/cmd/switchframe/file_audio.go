package main

// ingestFilePCM hands the mixer the PCM a clip player or a playout channel
// decoded from a file, placed by its PTS. The first PCM of a start places
// the channel afresh: a channel that played before is otherwise read on
// as it was placed then, up to a period off the new audio.
func (a *App) ingestFilePCM(key string, pcm []float32, pts int64, channels int, start bool) {
	if a.mixer.ChannelPair(key) == 0 {
		pcm, channels = spreadAACLayout(pcm, channels)
	}
	a.mixer.IngestPCM(key, pcm, pts, channels)
	if start {
		a.mixer.PlaceChannel(key)
	}
}

// spreadAACLayout lays the channels of AAC decoded in WAV order out as
// 5.1 (L R C LFE Ls Rs) when the AAC layout puts them elsewhere. The mixer
// folds 5.1 PCM to stereo as it folds 5.1 AAC, but keeps the first two
// channels of other PCM layouts, which for 3.0, 4.0, 5.0 and 7.1 AAC
// would drop the centre and surround channels its AAC fold takes in:
//
//	3: L R C            4: L R C Cs          5: L R C Ls Rs
//	8: L R C LFE Ls Rs Lc Rc
//
// Cs goes to both surrounds. Of 7.1, Lc/Rc are the front pair and L/R
// go into it at -3 dB, as the AAC fold does. A selected channel pair
// takes the channels as they are decoded instead.
func spreadAACLayout(pcm []float32, channels int) ([]float32, int) {
	const k = float32(0.7071067811865475)
	var lay func(s, d []float32)
	switch channels {
	case 3:
		lay = func(s, d []float32) { d[0], d[1], d[2] = s[0], s[1], s[2] }
	case 4:
		lay = func(s, d []float32) { d[0], d[1], d[2], d[4], d[5] = s[0], s[1], s[2], s[3], s[3] }
	case 5:
		lay = func(s, d []float32) { d[0], d[1], d[2], d[4], d[5] = s[0], s[1], s[2], s[3], s[4] }
	case 8:
		lay = func(s, d []float32) {
			d[0], d[1], d[2], d[3], d[4], d[5] = s[6]+k*s[0], s[7]+k*s[1], s[2], s[3], s[4], s[5]
		}
	default:
		return pcm, channels
	}
	frames := len(pcm) / channels
	out := make([]float32, frames*6)
	for i := 0; i < frames; i++ {
		lay(pcm[i*channels:(i+1)*channels], out[i*6:(i+1)*6])
	}
	return out, 6
}
