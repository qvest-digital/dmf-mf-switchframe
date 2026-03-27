package tracking

import (
	"math"
	"net"
	"testing"
	"time"
)

func TestDecode24Signed(t *testing.T) {
	tests := []struct {
		name   string
		input  []byte
		expect int32
	}{
		{
			name:   "zero",
			input:  []byte{0x00, 0x00, 0x00},
			expect: 0,
		},
		{
			name:   "positive one",
			input:  []byte{0x00, 0x00, 0x01},
			expect: 1,
		},
		{
			name:   "positive large",
			input:  []byte{0x7F, 0xFF, 0xFF},
			expect: 8388607, // 2^23 - 1 (max positive)
		},
		{
			name:   "negative one",
			input:  []byte{0xFF, 0xFF, 0xFF},
			expect: -1,
		},
		{
			name:   "negative min",
			input:  []byte{0x80, 0x00, 0x00},
			expect: -8388608, // -2^23
		},
		{
			name:   "negative mid",
			input:  []byte{0xFF, 0x00, 0x00},
			expect: -65536, // 0xFF0000 = 16711680 unsigned, sign-extend: 16711680 - 16777216 = -65536
		},
		{
			name:   "32768 counts (1 degree pan)",
			input:  []byte{0x00, 0x80, 0x00},
			expect: 32768,
		},
		{
			name:   "negative 32768 counts (-1 degree pan)",
			input:  []byte{0xFF, 0x80, 0x00},
			expect: -32768,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decode24Signed(tc.input)
			if got != tc.expect {
				t.Errorf("decode24Signed(%x) = %d, want %d", tc.input, got, tc.expect)
			}
		})
	}
}

func TestDecode24Unsigned(t *testing.T) {
	tests := []struct {
		name   string
		input  []byte
		expect uint32
	}{
		{
			name:   "zero",
			input:  []byte{0x00, 0x00, 0x00},
			expect: 0,
		},
		{
			name:   "max",
			input:  []byte{0xFF, 0xFF, 0xFF},
			expect: 16777215, // 2^24 - 1
		},
		{
			name:   "one",
			input:  []byte{0x00, 0x00, 0x01},
			expect: 1,
		},
		{
			name:   "typical zoom",
			input:  []byte{0x08, 0x00, 0x00},
			expect: 524288,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decode24Unsigned(tc.input)
			if got != tc.expect {
				t.Errorf("decode24Unsigned(%x) = %d, want %d", tc.input, got, tc.expect)
			}
		})
	}
}

// buildFreeDPacket constructs a valid 29-byte Free-D packet with a correct checksum.
func buildFreeDPacket(cameraID byte, pan, tilt, roll, x, y, z int32, zoom, focus uint32) []byte {
	pkt := make([]byte, 29)
	pkt[0] = 0xD1
	pkt[1] = cameraID

	encode24Signed := func(v int32) [3]byte {
		u := uint32(v) & 0xFFFFFF
		return [3]byte{byte(u >> 16), byte(u >> 8), byte(u)}
	}

	encode24Unsigned := func(v uint32) [3]byte {
		return [3]byte{byte(v >> 16), byte(v >> 8), byte(v)}
	}

	s := encode24Signed(pan)
	copy(pkt[2:5], s[:])
	s = encode24Signed(tilt)
	copy(pkt[5:8], s[:])
	s = encode24Signed(roll)
	copy(pkt[8:11], s[:])
	s = encode24Signed(x)
	copy(pkt[11:14], s[:])
	s = encode24Signed(y)
	copy(pkt[14:17], s[:])
	s = encode24Signed(z)
	copy(pkt[17:20], s[:])
	u := encode24Unsigned(zoom)
	copy(pkt[20:23], u[:])
	u = encode24Unsigned(focus)
	copy(pkt[23:26], u[:])

	// Spare bytes
	pkt[26] = 0x00
	pkt[27] = 0x00

	// Checksum: sum of bytes 0-27 mod 256
	var sum byte
	for i := 0; i < 28; i++ {
		sum += pkt[i]
	}
	pkt[28] = sum

	return pkt
}

func TestParseFreeDPacket(t *testing.T) {
	// 45 degrees pan = 45 * 32768 = 1474560 counts
	// -30 degrees tilt = -30 * 32768 = -983040 counts
	// 0 roll
	// X = 1.5m = 1500mm * 64 = 96000 counts
	// Y = 2.0m = 2000mm * 64 = 128000 counts
	// Z = -0.5m = -500mm * 64 = -32000 counts
	// Zoom = 500000 (raw)
	// Focus = 100000 (raw)

	panCounts := int32(45 * 32768)       // 1474560
	tiltCounts := int32(-30 * 32768)     // -983040
	rollCounts := int32(0)               // 0
	xCounts := int32(1500 * 64)          // 96000
	yCounts := int32(2000 * 64)          // 128000
	zCounts := int32(-500 * 64)          // -32000
	zoomRaw := uint32(500000)
	focusRaw := uint32(100000)

	pkt := buildFreeDPacket(42, panCounts, tiltCounts, rollCounts, xCounts, yCounts, zCounts, zoomRaw, focusRaw)

	pose, camID, err := parseFreeDPacket(pkt)
	if err != nil {
		t.Fatalf("parseFreeDPacket returned error: %v", err)
	}
	if camID != 42 {
		t.Errorf("camera ID = %d, want 42", camID)
	}

	// Check orientation (degrees)
	if math.Abs(pose.Orientation[0]-45.0) > 0.001 {
		t.Errorf("pan = %f, want 45.0", pose.Orientation[0])
	}
	if math.Abs(pose.Orientation[1]-(-30.0)) > 0.001 {
		t.Errorf("tilt = %f, want -30.0", pose.Orientation[1])
	}
	if math.Abs(pose.Orientation[2]-0.0) > 0.001 {
		t.Errorf("roll = %f, want 0.0", pose.Orientation[2])
	}

	// Check position (meters)
	if math.Abs(pose.Position[0]-1.5) > 0.001 {
		t.Errorf("X = %f, want 1.5", pose.Position[0])
	}
	if math.Abs(pose.Position[1]-2.0) > 0.001 {
		t.Errorf("Y = %f, want 2.0", pose.Position[1])
	}
	if math.Abs(pose.Position[2]-(-0.5)) > 0.001 {
		t.Errorf("Z = %f, want -0.5", pose.Position[2])
	}

	// Zoom and focus are raw unsigned values stored as FocalLength/FocusDistance
	if pose.FocalLength != float64(zoomRaw) {
		t.Errorf("FocalLength (zoom raw) = %f, want %f", pose.FocalLength, float64(zoomRaw))
	}
	if pose.FocusDistance != float64(focusRaw) {
		t.Errorf("FocusDistance (focus raw) = %f, want %f", pose.FocusDistance, float64(focusRaw))
	}

	// Confidence and source
	if pose.Confidence != 1.0 {
		t.Errorf("Confidence = %f, want 1.0", pose.Confidence)
	}
	if pose.Source != "freed" {
		t.Errorf("Source = %q, want %q", pose.Source, "freed")
	}
}

func TestParseFreeDPacket_InvalidChecksum(t *testing.T) {
	pkt := buildFreeDPacket(1, 0, 0, 0, 0, 0, 0, 0, 0)
	pkt[28] ^= 0xFF // corrupt the checksum

	_, _, err := parseFreeDPacket(pkt)
	if err == nil {
		t.Error("expected checksum error, got nil")
	}
}

func TestParseFreeDPacket_WrongType(t *testing.T) {
	pkt := buildFreeDPacket(1, 0, 0, 0, 0, 0, 0, 0, 0)
	pkt[0] = 0xD2 // wrong packet type
	// Recalculate checksum since we changed byte 0
	var sum byte
	for i := 0; i < 28; i++ {
		sum += pkt[i]
	}
	pkt[28] = sum

	_, _, err := parseFreeDPacket(pkt)
	if err == nil {
		t.Error("expected wrong-type error, got nil")
	}
}

func TestParseFreeDPacket_TooShort(t *testing.T) {
	short := make([]byte, 20)
	short[0] = 0xD1

	_, _, err := parseFreeDPacket(short)
	if err == nil {
		t.Error("expected too-short error, got nil")
	}
}

func TestFreeDReceiver_UDPIntegration(t *testing.T) {
	// Create receiver on ephemeral port
	recv := NewFreeDReceiver("127.0.0.1:0", 0) // 0 = accept all camera IDs

	if err := recv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer recv.Close()

	if !recv.Running() {
		t.Fatal("receiver should be running after Start")
	}

	// Get the actual address the receiver bound to.
	addr := recv.Addr()
	if addr == nil {
		t.Fatal("receiver address is nil")
	}

	// Send a Free-D packet via UDP.
	conn, err := net.Dial("udp", addr.String())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	panCounts := int32(10 * 32768)
	pkt := buildFreeDPacket(5, panCounts, 0, 0, 0, 0, 0, 0, 0)
	if _, err := conn.Write(pkt); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Poll for up to 1 second for the pose to arrive.
	var pose *CameraPose
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		pose = recv.LatestPose()
		if pose != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if pose == nil {
		t.Fatal("did not receive pose within timeout")
	}

	if math.Abs(pose.Orientation[0]-10.0) > 0.001 {
		t.Errorf("pan = %f, want 10.0", pose.Orientation[0])
	}
	if pose.Source != "freed" {
		t.Errorf("source = %q, want %q", pose.Source, "freed")
	}
}

func TestFreeDReceiver_CameraIDFilter(t *testing.T) {
	// Receiver only accepts camera ID 3.
	recv := NewFreeDReceiver("127.0.0.1:0", 3)

	if err := recv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer recv.Close()

	addr := recv.Addr()
	conn, err := net.Dial("udp", addr.String())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	// Send packet from camera ID 5 — should be ignored.
	pkt5 := buildFreeDPacket(5, int32(20*32768), 0, 0, 0, 0, 0, 0, 0)
	if _, err := conn.Write(pkt5); err != nil {
		t.Fatalf("Write pkt5 failed: %v", err)
	}

	// Give receiver time to process the filtered packet.
	time.Sleep(50 * time.Millisecond)

	if pose := recv.LatestPose(); pose != nil {
		t.Errorf("expected nil pose for filtered camera ID, got %+v", pose)
	}

	// Send packet from camera ID 3 — should be accepted.
	pkt3 := buildFreeDPacket(3, int32(30*32768), 0, 0, 0, 0, 0, 0, 0)
	if _, err := conn.Write(pkt3); err != nil {
		t.Fatalf("Write pkt3 failed: %v", err)
	}

	var pose *CameraPose
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		pose = recv.LatestPose()
		if pose != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if pose == nil {
		t.Fatal("did not receive pose for matching camera ID")
	}
	if math.Abs(pose.Orientation[0]-30.0) > 0.001 {
		t.Errorf("pan = %f, want 30.0", pose.Orientation[0])
	}
}

func TestFreeDReceiver_Interface(t *testing.T) {
	var _ TrackingSource = (*FreeDReceiver)(nil)
}

func TestFreeDReceiver_StopAndRestart(t *testing.T) {
	recv := NewFreeDReceiver("127.0.0.1:0", 0)

	if err := recv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := recv.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if recv.Running() {
		t.Error("receiver should not be running after Stop")
	}

	// Should be able to start again.
	if err := recv.Start(); err != nil {
		t.Fatalf("Restart failed: %v", err)
	}

	if !recv.Running() {
		t.Error("receiver should be running after restart")
	}

	recv.Close()
}

func TestFreeDReceiver_DoubleStart(t *testing.T) {
	recv := NewFreeDReceiver("127.0.0.1:0", 0)

	if err := recv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer recv.Close()

	err := recv.Start()
	if err == nil {
		t.Error("expected error on double Start")
	}
}

func TestFreeDReceiver_NameAndType(t *testing.T) {
	recv := NewFreeDReceiver("127.0.0.1:0", 0)
	if recv.Name() != "freed" {
		t.Errorf("Name() = %q, want %q", recv.Name(), "freed")
	}
	if recv.Type() != "freed" {
		t.Errorf("Type() = %q, want %q", recv.Type(), "freed")
	}
}
