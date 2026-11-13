package clip

import (
	"testing"

	"github.com/abema/go-mp4"
)

func TestASCChannelCount(t *testing.T) {
	for cfg, want := range map[int]int{0: 0, 1: 1, 2: 2, 6: 6, 7: 8, 8: 0, 11: 7, 12: 8, 13: 24, 14: 0, 15: 0} {
		if got := ascChannelCount(cfg); got != want {
			t.Errorf("channelConfiguration %d: %d channels, want %d", cfg, got, want)
		}
	}
}

// An AudioSpecificConfig with an escaped sampling frequency index (15, a
// 24-bit frequency follows) or object type (31) has its channel
// configuration at later bits; the bits where it otherwise sits must not
// decide the channel count. The sample entry's does.
func TestParseEsdsDescriptors_EscapedConfigKeepsTheSampleEntryCount(t *testing.T) {
	for name, asc := range map[string][]byte{
		// AOT 2, srIdx 15 (then 24-bit 48000), chanCfg 2: the bits read as
		// channelConfiguration hold part of the frequency.
		"escaped frequency": {0x17, 0x80, 0x5D, 0xC0, 0x10},
		// AOT 31 with extension 1 (AOT 33), srIdx 3, chanCfg 2: the bits
		// read as channelConfiguration say 4.
		"escaped object type": {0xF8, 0x26, 0x40},
		"plain stereo":        {0x11, 0x90},
	} {
		track := &mp4Track{audioChannels: 2}
		parseEsdsDescriptors(track, []mp4.Descriptor{{Tag: mp4.DecSpecificInfoTag, Data: asc}})
		if track.audioChannels != 2 {
			t.Errorf("%s: %d channels, want the sample entry's 2", name, track.audioChannels)
		}
	}
}
