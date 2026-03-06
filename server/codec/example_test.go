package codec_test

import (
	"fmt"

	"github.com/zsiec/switchframe/server/codec"
)

func ExampleAVC1ToAnnexB() {
	// AVC1: 4-byte big-endian length prefix + NALU data.
	// This encodes a single 3-byte NALU (length=3).
	avc1 := []byte{0x00, 0x00, 0x00, 0x03, 0x65, 0x01, 0x02}

	annexB := codec.AVC1ToAnnexB(avc1)

	// Annex B: 4-byte start code (00 00 00 01) replaces the length prefix.
	fmt.Printf("%x\n", annexB)
	// Output:
	// 00000001650102
}

func ExampleIsADTS() {
	adtsFrame := []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}
	rawAAC := []byte{0x01, 0x02, 0x03}

	fmt.Println(codec.IsADTS(adtsFrame))
	fmt.Println(codec.IsADTS(rawAAC))
	// Output:
	// true
	// false
}

func ExampleBuildADTS() {
	// Build a 7-byte ADTS header for a 10-byte AAC-LC frame at 48kHz stereo.
	header := codec.BuildADTS(48000, 2, 10)

	fmt.Println("length:", len(header))
	// Verify sync word (first 12 bits = 0xFFF).
	fmt.Printf("sync: %02x %02x\n", header[0], header[1]&0xF0)
	fmt.Println("is_adts:", codec.IsADTS(header))
	// Output:
	// length: 7
	// sync: ff f0
	// is_adts: true
}
