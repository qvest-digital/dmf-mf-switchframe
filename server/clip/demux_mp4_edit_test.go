package clip

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	mp4 "github.com/abema/go-mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEditWindow(t *testing.T) {
	const all = math.MaxInt64
	tests := []struct {
		name              string
		edits             []mp4Edit
		start, end, shift int64
	}{
		{"no edit list", nil, 0, all, 0},
		// ffmpeg's AAC: 1024 priming samples, 2 s presented.
		{"one edit", []mp4Edit{{2000, 1024, 1}}, 1024, 1024 + 96000, -1024},
		{"zero duration presents all media", []mp4Edit{{0, 1024, 1}}, 1024, all, -1024},
		{"empty edit delays", []mp4Edit{{500, -1, 1}, {2000, 0, 1}}, 0, 96000, 24000},
		{"empty edit then priming", []mp4Edit{{500, -1, 1}, {2000, 2112, 1}}, 2112, 2112 + 96000, 24000 - 2112},
		{"two media edits fall back", []mp4Edit{{1000, 0, 1}, {1000, 48000, 1}}, 0, all, 0},
		{"dwell falls back", []mp4Edit{{2000, 1024, 0}}, 0, all, 0},
		{"lone empty edit falls back", []mp4Edit{{500, -1, 1}}, 0, all, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			track := mp4Track{timescale: 48000, movieTimescale: 1000, edits: tt.edits}
			start, end, shift := track.editWindow()
			assert.Equal(t, []int64{tt.start, tt.end, tt.shift}, []int64{start, end, shift})
		})
	}
}

// I P B B at 25 fps in a 12800 timescale, presented from 1024 to 3072:
// the earliest picture ends at 1536, the latest starts at 2560.
func TestEditHidesPictures(t *testing.T) {
	tests := []struct {
		name  string
		edits []mp4Edit
		want  bool
	}{
		{"no edit list", nil, false},
		{"reorder delay only", []mp4Edit{{160, 1024, 1}}, false},
		{"starts inside the earliest picture", []mp4Edit{{120, 1535, 1}}, false},
		{"starts at the second picture", []mp4Edit{{120, 1536, 1}}, true},
		// 80 ms = 1024 ticks: presents 1024 to 2048, before the latest.
		{"ends before the latest picture", []mp4Edit{{80, 1024, 1}}, true},
		{"falls back", []mp4Edit{{160, 1536, 0}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			track := mp4Track{
				timescale:      12800,
				movieTimescale: 1000,
				stts:           []mp4.SttsEntry{{SampleCount: 4, SampleDelta: 512}},
				ctts: []mp4.CttsEntry{
					{SampleCount: 1, SampleOffsetV0: 1024},
					{SampleCount: 1, SampleOffsetV0: 2048},
					{SampleCount: 2, SampleOffsetV0: 512},
				},
				stsz:  []uint32{8, 8, 8, 8},
				edits: tt.edits,
			}
			assert.Equal(t, tt.want, track.editHidesPictures())
		})
	}
}

// An AAC track with 2112 priming samples, as Apple and FDK encoders write,
// carries two frames wholly before the edit and one straddling it. Played
// in order, the two would delay all audio by 2048 samples (43 ms at 48 kHz).
func TestReadAudioSamples_EditListDropsPrimingAndPadding(t *testing.T) {
	const n = 6
	sizes := make([]uint32, n)
	data := make([]byte, 0, 4*n)
	for i := range sizes {
		sizes[i] = 4
		data = append(data, byte(i), 0, 0, 0)
	}
	track := &mp4Track{
		handlerType:     "soun",
		timescale:       48000,
		audioSampleRate: 48000,
		audioChannels:   2,
		stts:            []mp4.SttsEntry{{SampleCount: n, SampleDelta: 1024}},
		stsz:            sizes,
		stsc:            []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: n}},
		stco:            []uint64{0},
		movieTimescale:  1000,
		// 50 ms = 2400 samples presented from 2112: frame 5 at 5120 is
		// end padding.
		edits: []mp4Edit{{segmentDuration: 50, mediaTime: 2112, mediaRate: 1}},
	}
	f, err := os.Open(writeTemp(t, data, ".raw"))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	frames, err := readAudioSamples(f, track, int64(len(data)))
	require.NoError(t, err)

	var ids []byte
	var pts []int64
	for _, fr := range frames {
		ids = append(ids, fr.data[0])
		pts = append(pts, fr.pts)
	}
	assert.Equal(t, []byte{2, 3, 4}, ids, "frames kept")
	// (2048-2112)*90000/48000 = -120: the straddling frame starts 64
	// samples before the picture.
	assert.Equal(t, []int64{-120, 1800, 3720}, pts)
}

// ffmpeg writes B-frames with ctts offsets from DTS 0 and an edit starting
// at the first picture's composition time, so that picture presents at 0.
func TestReadVideoSamples_EditListShiftsCompositionDelay(t *testing.T) {
	const n = 4
	sizes := make([]uint32, n)
	data := make([]byte, 0, 8*n)
	for i := range sizes {
		sizes[i] = 8
		data = append(data, 0, 0, 0, 4, 0x65, byte(i), 0, 0)
	}
	track := &mp4Track{
		handlerType:    "vide",
		timescale:      12800,
		naluLengthSize: 4,
		sps:            []byte{0x67, 0x42},
		pps:            []byte{0x68, 0xce},
		// I P B B in decode order, presented I B B P; 512 ticks per frame
		// at 25 fps, 1024 ticks of reorder delay.
		stts: []mp4.SttsEntry{{SampleCount: n, SampleDelta: 512}},
		ctts: []mp4.CttsEntry{
			{SampleCount: 1, SampleOffsetV0: 1024},
			{SampleCount: 1, SampleOffsetV0: 2048},
			{SampleCount: 2, SampleOffsetV0: 512},
		},
		stss:           []uint32{1},
		stsz:           sizes,
		stsc:           []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: n}},
		stco:           []uint64{0},
		movieTimescale: 1000,
		edits:          []mp4Edit{{segmentDuration: 160, mediaTime: 1024, mediaRate: 1}},
	}
	f, err := os.Open(writeTemp(t, data, ".raw"))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	frames, err := readVideoSamples(f, track, int64(len(data)))
	require.NoError(t, err)
	var pts []int64
	for _, fr := range frames {
		pts = append(pts, fr.pts)
	}
	assert.Equal(t, []int64{0, 10800, 3600, 7200}, pts)
}

type ffprobePacket struct {
	CodecType string `json:"codec_type"`
	PTS       *int64 `json:"pts"`
	Duration  int64  `json:"duration"`
}

// probePackets returns each stream's packet PTS in 90 kHz, keeping the
// packets ffmpeg presents: those ending after the start. ffmpeg applies the
// edit list by shifting timestamps and skipping the priming samples.
func probePackets(t *testing.T, path string) (video, audio []int64) {
	t.Helper()
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not available")
	}
	tb := map[string][2]int64{}
	out, err := exec.Command(ffprobe, "-v", "error", "-show_entries",
		"stream=codec_type,time_base", "-of", "csv=p=0", path).Output()
	require.NoError(t, err)
	for _, line := range strings.Fields(string(out)) {
		var kind string
		var num, den int64
		parts := strings.Split(line, ",")
		require.Len(t, parts, 2, line)
		kind = parts[0]
		_, err := fmt.Sscanf(parts[1], "%d/%d", &num, &den)
		require.NoError(t, err)
		tb[kind] = [2]int64{num, den}
	}
	out, err = exec.Command(ffprobe, "-v", "error", "-show_entries",
		"packet=codec_type,pts,duration", "-of", "json", path).Output()
	require.NoError(t, err)
	var probe struct {
		Packets []ffprobePacket `json:"packets"`
	}
	require.NoError(t, json.Unmarshal(out, &probe))
	for _, p := range probe.Packets {
		require.NotNil(t, p.PTS)
		r := tb[p.CodecType]
		to90k := func(v int64) int64 { return v * 90000 * r[0] / r[1] }
		switch p.CodecType {
		case "video":
			video = append(video, to90k(*p.PTS))
		case "audio":
			if *p.PTS+p.Duration > 0 {
				audio = append(audio, to90k(*p.PTS))
			}
		}
	}
	return video, audio
}

// An ffmpeg MP4 with B-frames and AAC: its video starts after the reorder
// delay and its audio with a 1024-sample priming frame, and both edit lists
// shift that back to 0. The demuxer must match ffmpeg's timestamps, so the
// first audio frame plays with the first picture.
func TestDemuxMP4_EditListMatchesFFmpeg(t *testing.T) {
	ffmpegPath := requireFFmpeg(t)
	for _, fps := range []int{25, 30} {
		t.Run(strconv.Itoa(fps)+"fps", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bframes.mp4")
			out, err := exec.Command(ffmpegPath, "-v", "error", "-y",
				"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=2:size=160x120:rate=%d", fps),
				"-f", "lavfi", "-i", "sine=frequency=440:duration=2:sample_rate=48000",
				"-c:v", "libx264", "-bf", "2", "-g", strconv.Itoa(fps), "-pix_fmt", "yuv420p",
				"-c:a", "aac", "-b:a", "64k", "-ac", "2", path).CombinedOutput()
			require.NoError(t, err, string(out))

			wantVideo, wantAudio := probePackets(t, path)
			video, audio, err := demuxMP4(path)
			require.NoError(t, err)

			var gotVideo, gotAudio []int64
			first := int64(math.MaxInt64)
			for _, f := range video {
				gotVideo = append(gotVideo, f.pts)
				first = min(first, f.pts)
			}
			for _, a := range audio {
				gotAudio = append(gotAudio, a.pts)
			}
			assert.Equal(t, wantVideo, gotVideo, "video PTS")
			assert.Equal(t, wantAudio, gotAudio, "audio PTS")

			require.NotEmpty(t, audio)
			offset := audio[0].pts - first
			t.Logf("MEASURE: %d fps first audio - first picture = %.2f ms, %d audio frames (ffprobe presents %d)",
				fps, float64(offset)/90, len(audio), len(wantAudio))
			assert.Less(t, abs64(offset), aacFrameTicks(audio[0]),
				"first audio frame %d ticks from the first picture", offset)
		})
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// A stream-copy cut, as `ffmpeg -ss 1.5 -c copy` and trim-without-re-encode
// tools write, starts at the keyframe before the cut point and hides the
// 0.5 s of pictures up to it with the edit list, the audio there too. The
// players keep every picture after the keyframe and spread the audio over
// them by order, so audio dropped by the edit would leave each pass 0.5 s
// short of audio and play the first picture with audio from 0.5 s later.
// The edit is not applied: video and audio cover the same time from the
// keyframe, as before edit lists were read. 0.25 s, half of what the cut
// hides, separates the two; the mux leaves the audio a few frames apart.
func TestDemuxMP4_StreamCopyCutKeepsAudioWithPictures(t *testing.T) {
	ffmpegPath := requireFFmpeg(t)
	dir := t.TempDir()
	enc := filepath.Join(dir, "enc.mp4")
	cut := filepath.Join(dir, "cut.mp4")
	out, err := exec.Command(ffmpegPath, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=160x120:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4:sample_rate=48000",
		"-c:v", "libx264", "-bf", "2", "-g", "25", "-keyint_min", "25", "-sc_threshold", "0",
		"-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "64k", "-ac", "2", enc).CombinedOutput()
	require.NoError(t, err, string(out))
	out, err = exec.Command(ffmpegPath, "-v", "error", "-y",
		"-ss", "1.5", "-i", enc, "-c", "copy", cut).CombinedOutput()
	require.NoError(t, err, string(out))

	video, audio, err := demuxMP4(cut)
	require.NoError(t, err)
	video, audio = fromFirstKeyframe(video, audio)
	require.NotEmpty(t, video)
	require.NotEmpty(t, audio)

	const tolerance = 90000 / 4
	frame := int64(90000 / 25)
	lo, hi := int64(math.MaxInt64), int64(math.MinInt64)
	for _, f := range video {
		lo, hi = min(lo, f.pts), max(hi, f.pts)
	}
	videoSpan := hi + frame - lo
	audioSpan := int64(len(audio)) * aacFrameTicks(audio[0])
	t.Logf("MEASURE: %d pictures over %.3f s, %d audio frames over %.3f s, first audio %.3f s from the keyframe",
		len(video), float64(videoSpan)/90000, len(audio), float64(audioSpan)/90000,
		float64(audio[0].pts-video[0].pts)/90000)
	assert.Less(t, abs64(videoSpan-audioSpan), int64(tolerance),
		"video covers %d ticks, audio %d", videoSpan, audioSpan)
	assert.Less(t, abs64(audio[0].pts-video[0].pts), int64(tolerance),
		"first audio frame %d ticks, keyframe %d", audio[0].pts, video[0].pts)
}
