package clip

import (
	"testing"
	"time"
)

// TestParseSPSDimensions_HugeNumRefFramesInPicOrderCntCycle verifies that a
// crafted SPS with pic_order_cnt_type=1 and an enormous
// num_ref_frames_in_pic_order_cnt_cycle (readUE can yield up to 2^32-2) does
// not spin the offset_for_ref_frame loop billions of times. The H.264 spec
// (7.4.2.1.1) caps the field at 255; an out-of-spec value must be rejected
// quickly instead of burning seconds of CPU per uploaded file.
func TestParseSPSDimensions_HugeNumRefFramesInPicOrderCntCycle(t *testing.T) {
	// Hand-crafted SPS bitstream (no emulation prevention sequences):
	//   0x67        NAL header (SPS)
	//   0x42 (66)   profile_idc = Baseline (skips the High-profile branch)
	//   0x00        constraint flags
	//   0x1E (30)   level_idc
	//   0xD3        bits: 1 (ue=0 sps_id), 1 (ue=0 log2_max_frame_num_minus4),
	//               010 (ue=1 pic_order_cnt_type), 0 (delta_pic_order flag),
	//               1 (se=0 offset_for_non_ref_pic),
	//               1 (se=0 offset_for_top_to_bottom_field)
	//   0x00 0x00 0x00 0x01 0xFF 0xFF 0xFF 0xFE
	//               ue with 31 leading zeros + 31 value bits (all ones)
	//               = num_ref_frames_in_pic_order_cnt_cycle = 0xFFFFFFFE,
	//               then the bitstream ends, so every subsequent readSE
	//               errors out immediately.
	sps := []byte{
		0x67, 0x42, 0x00, 0x1E, 0xD3,
		0x00, 0x00, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFE,
	}

	type dims struct{ w, h int }
	done := make(chan dims, 1)
	go func() {
		w, h := ParseSPSDimensions(sps)
		done <- dims{w, h}
	}()

	select {
	case d := <-done:
		if d.w != 0 || d.h != 0 {
			t.Errorf("malformed SPS should yield (0,0), got (%d,%d)", d.w, d.h)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ParseSPSDimensions spun on attacker-controlled num_ref_frames_in_pic_order_cnt_cycle (CPU-exhaustion DoS)")
	}
}
