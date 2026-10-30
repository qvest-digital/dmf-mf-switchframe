package clip

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"

	mp4 "github.com/abema/go-mp4"
)

// demuxMP4 reads an MP4/M4V/MOV file and extracts H.264 or HEVC video frames and AAC
// audio frames. Video frames are returned in wire format (4-byte length-prefixed NALUs)
// with parameter sets (SPS/PPS for H.264, VPS/SPS/PPS for HEVC) set on keyframes.
// Audio frames contain raw AAC payloads (no ADTS headers).
func demuxMP4(path string) ([]BufferedFrame, []BufferedAudioFrame, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() == 0 {
		return nil, nil, fmt.Errorf("empty file: %s", path)
	}

	// Parse the MP4 box tree to extract track information. The file size bounds
	// attacker-controlled sample counts so a crafted box cannot force a huge
	// allocation (see the stsz handler in parseMP4Tracks).
	tracks, err := parseMP4Tracks(f, info.Size())
	if err != nil {
		return nil, nil, fmt.Errorf("parse MP4: %w", err)
	}

	// Find video and audio tracks.
	var videoTrack, audioTrack *mp4Track
	for i := range tracks {
		switch tracks[i].handlerType {
		case "vide":
			if videoTrack == nil {
				videoTrack = &tracks[i]
			}
		case "soun":
			if audioTrack == nil {
				audioTrack = &tracks[i]
			}
		}
	}

	if videoTrack == nil {
		return nil, nil, fmt.Errorf("no video track found in MP4")
	}

	// An edit hiding whole pictures is applied to no track, so the video
	// and audio stay together as the players pair them.
	if videoTrack.editHidesPictures() {
		videoTrack.edits = nil
		if audioTrack != nil {
			audioTrack.edits = nil
		}
	}

	// Build sample tables and read samples.
	videoFrames, err := readVideoSamples(f, videoTrack, info.Size())
	if err != nil {
		return nil, nil, fmt.Errorf("read video samples: %w", err)
	}

	var audioFrames []BufferedAudioFrame
	if audioTrack != nil {
		audioFrames, err = readAudioSamples(f, audioTrack, info.Size())
		if err != nil {
			return nil, nil, fmt.Errorf("read audio samples: %w", err)
		}
	}

	// B-frame handling: video frames are kept in sample table order (decode order)
	// because the decoder needs reference frames before B-frames that depend on
	// them. Sorting by PTS would reorder to display order, breaking B-frame
	// decoding. The FFmpeg decoder handles reordering internally, returning
	// EAGAIN while it buffers reference frames. After playback, the player
	// drains remaining buffered frames via SendEOS + ReceiveFrame (see
	// player.go drain phase). The output encoder uses tune=zerolatency
	// (bframes=0), so the output stream never contains B-frames.

	// Audio frames have no B-frame reordering -- sort by PTS for proper playback.
	sort.Slice(audioFrames, func(i, j int) bool {
		return audioFrames[i].pts < audioFrames[j].pts
	})

	if len(videoFrames) == 0 {
		return nil, nil, fmt.Errorf("no video frames found in MP4")
	}

	return videoFrames, audioFrames, nil
}

// mp4Track holds parsed metadata for a single track.
type mp4Track struct {
	handlerType string
	timescale   uint32

	// Video-specific: parameter sets from avcC or hvcC box.
	vps            []byte // VPS NAL unit (HEVC only)
	sps            []byte
	pps            []byte
	naluLengthSize int // typically 4
	width          uint16
	height         uint16
	videoCodec     string // "h264" or "h265"

	// Audio-specific: from esds and mp4a boxes.
	audioObjectType  int
	sampleRateIndex  int
	channelConfig    int
	audioSampleRate  int
	audioChannels    int
	audioSpecificCfg []byte

	// Sample table boxes.
	stts []mp4.SttsEntry // time-to-sample
	ctts []mp4.CttsEntry // composition time offset
	stss []uint32        // sync sample numbers (keyframes)
	stsz []uint32        // sample sizes
	stsc []mp4.StscEntry // sample-to-chunk
	stco []uint64        // chunk offsets (unified: stco + co64)

	cttsVersion uint8 // version of ctts box (affects offset interpretation)

	// Edit list (edts/elst). Segment durations are in movieTimescale (mvhd),
	// media times in timescale (mdhd).
	edits          []mp4Edit
	movieTimescale uint32
}

// mp4Edit is one elst entry. mediaTime -1 is an empty edit.
type mp4Edit struct {
	segmentDuration uint64
	mediaTime       int64
	mediaRate       int16
}

// editWindow reads the track's edit list in media timescale units: the
// media in [start, end) is presented, and presentation time = composition
// time + shift. It handles one edit, optionally after one empty edit (an
// initial delay), at normal rate. Any other list presents all the media at
// its composition times, as before edit lists were read.
func (t *mp4Track) editWindow() (start, end, shift int64) {
	end = math.MaxInt64
	edits := t.edits
	var delay int64
	if len(edits) == 2 && edits[0].mediaTime == -1 {
		if t.movieTimescale == 0 {
			return 0, end, 0
		}
		// The delay moves the audio PTS only: the players pair audio with
		// pictures by order, so they do not play it.
		delay = int64(edits[0].segmentDuration * uint64(t.timescale) / uint64(t.movieTimescale))
		edits = edits[1:]
	}
	if len(edits) != 1 || edits[0].mediaTime < 0 || edits[0].mediaRate != 1 {
		return 0, end, 0
	}
	e := edits[0]
	// A zero duration, as fragmented files write, presents all the media.
	if e.segmentDuration > 0 && t.movieTimescale > 0 {
		end = e.mediaTime + int64(e.segmentDuration*uint64(t.timescale)/uint64(t.movieTimescale))
	}
	return e.mediaTime, end, delay - e.mediaTime
}

// editHidesPictures reports whether the track's edit leaves whole pictures
// out of its window, as a stream-copy cut writes: it starts at the
// keyframe before the cut point and the edit starts at the cut point.
// Pictures outside the edit are kept (see readVideoSamples) and neither
// fromFirstKeyframe nor playout drops one by its PTS against the edit,
// while audio outside it is dropped. The players spread the audio over the
// pictures by order, so applying such an edit would play the hidden
// pictures with audio from after them and leave each pass short of audio.
//
// A picture is presented until the next composition time, so the earliest
// is hidden exactly when the second earliest is at or before the edit
// start, and the latest when it is at or after the edit end. An edit that
// only removes the B-frame reorder delay starts at the earliest
// composition time and hides nothing; one that starts inside the earliest
// picture hides part of it, which the picture's negative PTS carries.
func (t *mp4Track) editHidesPictures() bool {
	start, end, _ := t.editWindow()
	n := len(t.stsz)
	if n == 0 {
		return false
	}
	ct := buildDTSTable(t.stts, n)
	if offsets := buildCTSOffsets(t.ctts, t.cttsVersion, n); offsets != nil {
		for i := range ct {
			ct[i] += offsets[i]
		}
	}
	slices.Sort(ct)
	return n > 1 && ct[1] <= start || ct[n-1] >= end
}

// parseMP4Tracks walks the MP4 box tree and extracts track metadata. fileSize
// is the on-disk size of the source file; it bounds attacker-controlled sample
// counts (e.g. the uniform-size stsz branch) so a tiny crafted file cannot
// force a multi-gigabyte allocation.
func parseMP4Tracks(r io.ReadSeeker, fileSize int64) ([]mp4Track, error) {
	var tracks []mp4Track
	var currentTrack *mp4Track
	var movieTimescale uint32

	_, err := mp4.ReadBoxStructure(r, func(h *mp4.ReadHandle) (interface{}, error) {
		switch h.BoxInfo.Type {
		case mp4.BoxType(mp4.BoxTypeTrak()):
			// Start a new track.
			tracks = append(tracks, mp4Track{naluLengthSize: 4})
			currentTrack = &tracks[len(tracks)-1]
			// Expand to parse children.
			_, err := h.Expand()
			return nil, err

		case mp4.BoxType(mp4.BoxTypeMoov()),
			mp4.BoxType(mp4.BoxTypeEdts()),
			mp4.BoxType(mp4.BoxTypeMdia()),
			mp4.BoxType(mp4.BoxTypeMinf()),
			mp4.BoxType(mp4.BoxTypeStbl()),
			mp4.BoxType(mp4.BoxTypeStsd()):
			// Container boxes: expand to parse children.
			_, err := h.Expand()
			return nil, err

		case mp4.BoxType(mp4.BoxTypeMvhd()):
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			movieTimescale = box.(*mp4.Mvhd).Timescale
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeElst()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			elst := box.(*mp4.Elst)
			currentTrack.edits = make([]mp4Edit, len(elst.Entries))
			for i, e := range elst.Entries {
				currentTrack.edits[i] = mp4Edit{
					segmentDuration: elst.GetSegmentDuration(i),
					mediaTime:       elst.GetMediaTime(i),
					mediaRate:       e.MediaRateInteger,
				}
			}
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeHdlr()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			hdlr := box.(*mp4.Hdlr)
			currentTrack.handlerType = string(hdlr.HandlerType[:])
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeMdhd()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			mdhd := box.(*mp4.Mdhd)
			currentTrack.timescale = mdhd.Timescale
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeAvc1()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			vse := box.(*mp4.VisualSampleEntry)
			currentTrack.width = vse.Width
			currentTrack.height = vse.Height
			currentTrack.videoCodec = "h264"
			// Expand to find avcC child box.
			_, err = h.Expand()
			return nil, err

		case mp4.BoxType(mp4.BoxTypeAvcC()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			avcc := box.(*mp4.AVCDecoderConfiguration)
			currentTrack.naluLengthSize = int(avcc.LengthSizeMinusOne) + 1
			if len(avcc.SequenceParameterSets) > 0 {
				sps := avcc.SequenceParameterSets[0].NALUnit
				currentTrack.sps = make([]byte, len(sps))
				copy(currentTrack.sps, sps)
			}
			if len(avcc.PictureParameterSets) > 0 {
				pps := avcc.PictureParameterSets[0].NALUnit
				currentTrack.pps = make([]byte, len(pps))
				copy(currentTrack.pps, pps)
			}
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeHvc1()), mp4.BoxType(mp4.BoxTypeHev1()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			vse := box.(*mp4.VisualSampleEntry)
			currentTrack.width = vse.Width
			currentTrack.height = vse.Height
			currentTrack.videoCodec = "h265"
			// Expand to find hvcC child box.
			_, err = h.Expand()
			return nil, err

		case mp4.BoxType(mp4.BoxTypeHvcC()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			hvcc := box.(*mp4.HvcC)
			currentTrack.naluLengthSize = int(hvcc.LengthSizeMinusOne) + 1
			// Extract VPS, SPS, PPS from HEVC NALU arrays.
			for _, arr := range hvcc.NaluArrays {
				if len(arr.Nalus) == 0 {
					continue
				}
				nalu := arr.Nalus[0].NALUnit
				switch arr.NaluType {
				case 32: // VPS
					currentTrack.vps = make([]byte, len(nalu))
					copy(currentTrack.vps, nalu)
				case 33: // SPS
					currentTrack.sps = make([]byte, len(nalu))
					copy(currentTrack.sps, nalu)
				case 34: // PPS
					currentTrack.pps = make([]byte, len(nalu))
					copy(currentTrack.pps, nalu)
				}
			}
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeMp4a()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			ase := box.(*mp4.AudioSampleEntry)
			currentTrack.audioSampleRate = int(ase.GetSampleRateInt())
			currentTrack.audioChannels = int(ase.ChannelCount)
			// Expand to find esds child box.
			_, err = h.Expand()
			return nil, err

		case mp4.BoxType(mp4.BoxTypeEsds()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			esds := box.(*mp4.Esds)
			parseEsdsDescriptors(currentTrack, esds.Descriptors)
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeStts()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			stts := box.(*mp4.Stts)
			currentTrack.stts = stts.Entries
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeCtts()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			ctts := box.(*mp4.Ctts)
			currentTrack.ctts = ctts.Entries
			currentTrack.cttsVersion = ctts.GetVersion()
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeStss()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			stss := box.(*mp4.Stss)
			currentTrack.stss = stss.SampleNumber
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeStsz()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			stsz := box.(*mp4.Stsz)
			if stsz.SampleSize != 0 {
				// Uniform sample size: SampleCount is a fixed scalar read
				// verbatim from the box (go-mp4 leaves EntrySize empty in this
				// case), so it is fully attacker-controlled. Bound it by the
				// file size before allocating: the uniform media occupies
				// SampleCount*SampleSize bytes, which cannot exceed the file.
				// Without this guard a ~50-byte box with SampleSize=1,
				// SampleCount=0xFFFFFFFF would force a ~16 GiB allocation.
				if uint64(stsz.SampleCount)*uint64(stsz.SampleSize) > uint64(fileSize) {
					return nil, fmt.Errorf("stsz sample count %d (size %d) exceeds file size %d",
						stsz.SampleCount, stsz.SampleSize, fileSize)
				}
				// Expand to per-sample.
				sizes := make([]uint32, stsz.SampleCount)
				for i := range sizes {
					sizes[i] = stsz.SampleSize
				}
				currentTrack.stsz = sizes
			} else {
				currentTrack.stsz = stsz.EntrySize
			}
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeStsc()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			stsc := box.(*mp4.Stsc)
			currentTrack.stsc = stsc.Entries
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeStco()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			stco := box.(*mp4.Stco)
			offsets := make([]uint64, len(stco.ChunkOffset))
			for i, o := range stco.ChunkOffset {
				offsets[i] = uint64(o)
			}
			currentTrack.stco = offsets
			return nil, nil

		case mp4.BoxType(mp4.BoxTypeCo64()):
			if currentTrack == nil {
				return nil, nil
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			co64 := box.(*mp4.Co64)
			currentTrack.stco = co64.ChunkOffset
			return nil, nil

		default:
			// For other boxes under stsd (like wave), try expanding.
			if currentTrack != nil {
				// Only expand container-like boxes we might need.
				boxType := h.BoxInfo.Type.String()
				if boxType == "wave" {
					_, err := h.Expand()
					return nil, err
				}
			}
			return nil, nil
		}
	})

	if err != nil {
		return nil, err
	}
	for i := range tracks {
		tracks[i].movieTimescale = movieTimescale
	}

	return tracks, nil
}

// parseEsdsDescriptors extracts audio codec config from ESDS descriptors.
func parseEsdsDescriptors(track *mp4Track, descriptors []mp4.Descriptor) {
	for _, desc := range descriptors {
		if desc.Tag == mp4.ESDescrTag && desc.ESDescriptor != nil {
			// The ES descriptor may contain nested descriptors.
			// But in go-mp4, the nested descriptors are in the array.
			continue
		}
		if desc.Tag == mp4.DecoderConfigDescrTag && desc.DecoderConfigDescriptor != nil {
			// ObjectTypeIndication 0x40 = Audio ISO/IEC 14496-3 (AAC).
			continue
		}
		if desc.Tag == mp4.DecSpecificInfoTag && len(desc.Data) >= 2 {
			// AudioSpecificConfig (ISO 14496-3).
			track.audioSpecificCfg = make([]byte, len(desc.Data))
			copy(track.audioSpecificCfg, desc.Data)

			// Parse AudioSpecificConfig to get audioObjectType,
			// sampleRateIndex, and channelConfig.
			aot := int((desc.Data[0] >> 3) & 0x1F)
			srIdx := int((desc.Data[0]&0x07)<<1) | int((desc.Data[1]>>7)&0x01)
			chanCfg := int((desc.Data[1] >> 3) & 0x0F)

			track.audioObjectType = aot
			track.sampleRateIndex = srIdx
			track.channelConfig = chanCfg

			// Also update sample rate from index if not set from mp4a.
			if track.audioSampleRate == 0 {
				track.audioSampleRate = sampleRateFromASCIndex(srIdx)
			}
			if track.audioChannels == 0 {
				track.audioChannels = chanCfg
			}
		}
	}
}

// sampleRateFromASCIndex maps ISO 14496-3 sampling_frequency_index to Hz.
func sampleRateFromASCIndex(idx int) int {
	rates := [13]int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}
	if idx >= 0 && idx < len(rates) {
		return rates[idx]
	}
	return 0
}

// sampleTableEntry holds the file offset and size of one sample.
type sampleTableEntry struct {
	offset uint64
	size   uint32
}

// buildSampleTable resolves sample-to-chunk mapping to produce a flat list
// of (offset, size) for every sample in the track. Returns an error if the
// stsc box references chunk indices beyond the stco/co64 chunk offset table.
// fileSize is the on-disk size of the source file; per-sample sizes (from the
// non-uniform stsz branch) and chunk offsets (stco/co64) are attacker
// controlled, so every sample must fit within the file and the cumulative
// sample bytes must not exceed the file size (samples cannot legitimately
// overlap), or a tiny crafted file could force multi-gigabyte allocations.
func buildSampleTable(track *mp4Track, fileSize int64) ([]sampleTableEntry, error) {
	if len(track.stsz) == 0 || len(track.stsc) == 0 || len(track.stco) == 0 {
		return nil, nil
	}

	// Validate that stsc does not reference chunks beyond stco.
	// stsc FirstChunk is 1-based, so FirstChunk=N requires at least N
	// entries in the chunk offset table (stco/co64).
	numChunks := len(track.stco)
	for _, entry := range track.stsc {
		if int(entry.FirstChunk) > numChunks {
			return nil, fmt.Errorf("stsc references chunk %d but only %d chunks in stco",
				entry.FirstChunk, numChunks)
		}
	}

	numSamples := len(track.stsz)
	entries := make([]sampleTableEntry, 0, numSamples)

	// Cumulative media bytes across all samples. In a well-formed file
	// samples occupy disjoint byte ranges, so their total size cannot exceed
	// the file size. Without this bound a crafted file whose stco chunks all
	// overlap (e.g. every chunk at offset 0 with size==fileSize) would pass
	// the per-sample check yet retain numSamples*fileSize bytes in memory.
	var totalBytes uint64

	sampleIdx := 0
	for chunkIdx := 0; chunkIdx < numChunks && sampleIdx < numSamples; chunkIdx++ {
		// Find the stsc entry that applies to this chunk (1-based chunk number).
		chunkNum := uint32(chunkIdx + 1)
		samplesInChunk := uint32(0)
		for i := len(track.stsc) - 1; i >= 0; i-- {
			if chunkNum >= track.stsc[i].FirstChunk {
				samplesInChunk = track.stsc[i].SamplesPerChunk
				break
			}
		}

		offset := track.stco[chunkIdx]
		for s := uint32(0); s < samplesInChunk && sampleIdx < numSamples; s++ {
			size := track.stsz[sampleIdx]
			// Per-sample bound: the sample must lie entirely within the
			// file. offset and size are both attacker-controlled, so guard
			// against uint64 wraparound of offset+size as well.
			end := offset + uint64(size)
			if end < offset || end > uint64(fileSize) {
				return nil, fmt.Errorf("sample %d (offset %d, size %d) extends past file size %d",
					sampleIdx, offset, size, fileSize)
			}
			totalBytes += uint64(size)
			if totalBytes > uint64(fileSize) {
				return nil, fmt.Errorf("total sample size %d exceeds file size %d (overlapping samples)",
					totalBytes, fileSize)
			}
			entries = append(entries, sampleTableEntry{
				offset: offset,
				size:   size,
			})
			offset = end
			sampleIdx++
		}
	}

	return entries, nil
}

// buildDTSTable computes the decode timestamp for each sample from stts entries.
// Returns DTS values in track timescale units.
func buildDTSTable(stts []mp4.SttsEntry, numSamples int) []int64 {
	dts := make([]int64, numSamples)
	var t int64
	sampleIdx := 0
	for _, entry := range stts {
		for i := uint32(0); i < entry.SampleCount && sampleIdx < numSamples; i++ {
			dts[sampleIdx] = t
			t += int64(entry.SampleDelta)
			sampleIdx++
		}
	}
	// Fill remaining with last DTS + delta if stts runs short.
	for sampleIdx < numSamples {
		dts[sampleIdx] = t
		sampleIdx++
	}
	return dts
}

// buildCTSOffsets returns the composition time offset for each sample.
// Returns nil if there are no ctts entries (no B-frames).
func buildCTSOffsets(ctts []mp4.CttsEntry, cttsVersion uint8, numSamples int) []int64 {
	if len(ctts) == 0 {
		return nil
	}

	offsets := make([]int64, numSamples)
	sampleIdx := 0
	for _, entry := range ctts {
		for i := uint32(0); i < entry.SampleCount && sampleIdx < numSamples; i++ {
			if cttsVersion == 0 {
				offsets[sampleIdx] = int64(entry.SampleOffsetV0)
			} else {
				offsets[sampleIdx] = int64(entry.SampleOffsetV1)
			}
			sampleIdx++
		}
	}
	return offsets
}

// isSyncSample checks if a 1-based sample number is a sync (keyframe) sample.
// Uses binary search since stss is sorted per the MP4 spec.
func isSyncSample(sampleNum uint32, stss []uint32) bool {
	// If there's no stss box, all samples are sync samples.
	if len(stss) == 0 {
		return true
	}
	idx := sort.Search(len(stss), func(i int) bool {
		return stss[i] >= sampleNum
	})
	return idx < len(stss) && stss[idx] == sampleNum
}

// readVideoSamples reads all video samples from the file, constructing
// BufferedFrame entries with proper PTS and keyframe markers. fileSize bounds
// attacker-controlled sample sizes (see buildSampleTable).
func readVideoSamples(r io.ReadSeeker, track *mp4Track, fileSize int64) ([]BufferedFrame, error) {
	// The timescale comes from the (optional) mdhd box; a crafted file can
	// omit it or carry Timescale=0, which would make the PTS division below
	// panic with an integer divide-by-zero.
	if track.timescale == 0 {
		return nil, fmt.Errorf("video track has missing or zero mdhd timescale")
	}

	sampleTable, err := buildSampleTable(track, fileSize)
	if err != nil {
		return nil, fmt.Errorf("build sample table: %w", err)
	}
	if len(sampleTable) == 0 {
		return nil, fmt.Errorf("empty video sample table")
	}

	numSamples := len(sampleTable)
	dts := buildDTSTable(track.stts, numSamples)
	ctsOffsets := buildCTSOffsets(track.ctts, track.cttsVersion, numSamples)
	// Pictures outside the edit stay: dropping one could drop a reference.
	// Those before it get a negative PTS, which marks them as leading.
	_, _, shift := track.editWindow()

	frames := make([]BufferedFrame, 0, numSamples)

	for i, sample := range sampleTable {
		// Read sample data from file.
		if _, err := r.Seek(int64(sample.offset), io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek to sample %d: %w", i, err)
		}

		data := make([]byte, sample.size)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, fmt.Errorf("read sample %d: %w", i, err)
		}

		// Normalize NALU length prefix to 4 bytes if needed.
		wireData := normalizeNALULengthSize(data, track.naluLengthSize)

		// Compute PTS in 90kHz. A negative PTS is a picture presented before
		// the stream starts; clamping it to 0 would read as "no PTS" and keep
		// it as shown.
		compositionTime := dts[i] + shift
		if ctsOffsets != nil {
			compositionTime += ctsOffsets[i]
		}
		pts90k := compositionTime * 90000 / int64(track.timescale)

		// Determine if this is a keyframe.
		sampleNum := uint32(i + 1) // 1-based
		isKey := isSyncSample(sampleNum, track.stss)

		frame := BufferedFrame{
			wireData:   wireData,
			pts:        pts90k,
			isKeyframe: isKey,
			codec:      track.videoCodec,
		}

		if isKey {
			// Deep-copy parameter sets so each keyframe owns independent slices,
			// matching the TS demux path. Shared references would let a
			// mutation of one frame's sets corrupt all other keyframes.
			if len(track.vps) > 0 {
				frame.vps = append([]byte(nil), track.vps...)
			}
			frame.sps = append([]byte(nil), track.sps...)
			frame.pps = append([]byte(nil), track.pps...)
		}

		frames = append(frames, frame)
	}

	return frames, nil
}

// readAudioSamples reads all audio samples from the file, constructing
// BufferedAudioFrame entries with proper PTS. MP4 stores raw AAC payloads
// (no ADTS headers), and BufferedAudioFrame.data expects raw AAC, so we
// use the sample data directly without any ADTS round-trip. fileSize bounds
// attacker-controlled sample sizes (see buildSampleTable).
func readAudioSamples(r io.ReadSeeker, track *mp4Track, fileSize int64) ([]BufferedAudioFrame, error) {
	// See readVideoSamples: a missing/zero mdhd timescale would panic the
	// PTS division below.
	if track.timescale == 0 {
		return nil, fmt.Errorf("audio track has missing or zero mdhd timescale")
	}

	sampleTable, err := buildSampleTable(track, fileSize)
	if err != nil {
		return nil, fmt.Errorf("build sample table: %w", err)
	}
	if len(sampleTable) == 0 {
		return nil, nil // No audio samples is not an error.
	}

	numSamples := len(sampleTable)
	// One entry past the last sample is where the last sample ends.
	dts := buildDTSTable(track.stts, numSamples+1)
	start, end, shift := track.editWindow()

	sampleRate := track.audioSampleRate
	channels := track.audioChannels
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if channels <= 0 {
		channels = 2
	}

	frames := make([]BufferedAudioFrame, 0, numSamples)

	for i, sample := range sampleTable {
		// Frames wholly outside the edit are not presented: before it the
		// encoder priming, after it the padding of the last frame. Playout
		// and the clip player send audio frames in order without their PTS,
		// so a priming frame left in would delay all audio by its length.
		// A frame straddling the start is kept whole; raw AAC cannot be cut.
		if start > 0 && dts[i+1] <= start || dts[i] >= end {
			continue
		}

		// Read raw AAC frame from file.
		if _, err := r.Seek(int64(sample.offset), io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek to audio sample %d: %w", i, err)
		}

		rawAAC := make([]byte, sample.size)
		if _, err := io.ReadFull(r, rawAAC); err != nil {
			return nil, fmt.Errorf("read audio sample %d: %w", i, err)
		}

		// PTS in 90kHz.
		pts90k := (dts[i] + shift) * 90000 / int64(track.timescale)

		frames = append(frames, BufferedAudioFrame{
			data:       rawAAC,
			pts:        pts90k,
			sampleRate: sampleRate,
			channels:   channels,
		})
	}

	return frames, nil
}

// normalizeNALULengthSize converts NALUs with non-4-byte length prefixes
// to the standard 4-byte length prefix format used throughout the codebase.
func normalizeNALULengthSize(data []byte, lengthSize int) []byte {
	if lengthSize == 4 {
		return data
	}

	// Parse with the original length size and rebuild with 4-byte lengths.
	var result []byte
	pos := 0
	for pos+lengthSize <= len(data) {
		var naluLen uint32
		switch lengthSize {
		case 1:
			naluLen = uint32(data[pos])
		case 2:
			naluLen = uint32(binary.BigEndian.Uint16(data[pos:]))
		case 3:
			naluLen = uint32(data[pos])<<16 | uint32(data[pos+1])<<8 | uint32(data[pos+2])
		default:
			return data
		}
		pos += lengthSize

		if int(naluLen) > len(data)-pos {
			break
		}

		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], naluLen)
		result = append(result, lenBuf[:]...)
		result = append(result, data[pos:pos+int(naluLen)]...)
		pos += int(naluLen)
	}

	if len(result) == 0 {
		return data
	}
	return result
}
