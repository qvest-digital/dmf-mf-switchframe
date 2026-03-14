package clip

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"

	mp4 "github.com/abema/go-mp4"
)

// demuxMP4 reads an MP4/M4V/MOV file and extracts H.264 video frames and AAC
// audio frames. Video frames are returned in AVC1 format with SPS/PPS set on
// keyframes. Audio frames contain raw AAC payloads (no ADTS headers).
func demuxMP4(path string) ([]bufferedFrame, []bufferedAudioFrame, error) {
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

	// Parse the MP4 box tree to extract track information.
	tracks, err := parseMP4Tracks(f)
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

	// Build sample tables and read samples.
	videoFrames, err := readVideoSamples(f, videoTrack)
	if err != nil {
		return nil, nil, fmt.Errorf("read video samples: %w", err)
	}

	var audioFrames []bufferedAudioFrame
	if audioTrack != nil {
		audioFrames, err = readAudioSamples(f, audioTrack)
		if err != nil {
			return nil, nil, fmt.Errorf("read audio samples: %w", err)
		}
	}

	// Video frames are kept in sample table order (decode order) because the
	// H.264 decoder needs reference frames before B-frames that depend on them.
	// Sorting by PTS would reorder to display order, breaking B-frame decoding.

	// Audio frames have no B-frame reordering — sort by PTS for proper playback.
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

	// Video-specific: SPS/PPS from avcC box.
	sps            []byte
	pps            []byte
	naluLengthSize int // typically 4
	width          uint16
	height         uint16

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
}

// parseMP4Tracks walks the MP4 box tree and extracts track metadata.
func parseMP4Tracks(r io.ReadSeeker) ([]mp4Track, error) {
	var tracks []mp4Track
	var currentTrack *mp4Track

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
			mp4.BoxType(mp4.BoxTypeMdia()),
			mp4.BoxType(mp4.BoxTypeMinf()),
			mp4.BoxType(mp4.BoxTypeStbl()),
			mp4.BoxType(mp4.BoxTypeStsd()):
			// Container boxes: expand to parse children.
			_, err := h.Expand()
			return nil, err

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
				// Uniform sample size: expand to per-sample.
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
func buildSampleTable(track *mp4Track) ([]sampleTableEntry, error) {
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
			entries = append(entries, sampleTableEntry{
				offset: offset,
				size:   size,
			})
			offset += uint64(size)
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
// bufferedFrame entries with proper PTS and keyframe markers.
func readVideoSamples(r io.ReadSeeker, track *mp4Track) ([]bufferedFrame, error) {
	sampleTable, err := buildSampleTable(track)
	if err != nil {
		return nil, fmt.Errorf("build sample table: %w", err)
	}
	if len(sampleTable) == 0 {
		return nil, fmt.Errorf("empty video sample table")
	}

	numSamples := len(sampleTable)
	dts := buildDTSTable(track.stts, numSamples)
	ctsOffsets := buildCTSOffsets(track.ctts, track.cttsVersion, numSamples)

	frames := make([]bufferedFrame, 0, numSamples)

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

		// Compute PTS in 90kHz.
		var compositionTime int64
		if ctsOffsets != nil {
			compositionTime = dts[i] + ctsOffsets[i]
		} else {
			compositionTime = dts[i]
		}
		pts90k := compositionTime * 90000 / int64(track.timescale)

		// Determine if this is a keyframe.
		sampleNum := uint32(i + 1) // 1-based
		isKey := isSyncSample(sampleNum, track.stss)

		frame := bufferedFrame{
			wireData:   wireData,
			pts:        pts90k,
			isKeyframe: isKey,
		}

		if isKey {
			frame.sps = track.sps
			frame.pps = track.pps
		}

		frames = append(frames, frame)
	}

	return frames, nil
}

// readAudioSamples reads all audio samples from the file, constructing
// bufferedAudioFrame entries with proper PTS. MP4 stores raw AAC payloads
// (no ADTS headers), and bufferedAudioFrame.data expects raw AAC, so we
// use the sample data directly without any ADTS round-trip.
func readAudioSamples(r io.ReadSeeker, track *mp4Track) ([]bufferedAudioFrame, error) {
	sampleTable, err := buildSampleTable(track)
	if err != nil {
		return nil, fmt.Errorf("build sample table: %w", err)
	}
	if len(sampleTable) == 0 {
		return nil, nil // No audio samples is not an error.
	}

	numSamples := len(sampleTable)
	dts := buildDTSTable(track.stts, numSamples)

	sampleRate := track.audioSampleRate
	channels := track.audioChannels
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if channels <= 0 {
		channels = 2
	}

	frames := make([]bufferedAudioFrame, 0, numSamples)

	for i, sample := range sampleTable {
		// Read raw AAC frame from file.
		if _, err := r.Seek(int64(sample.offset), io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek to audio sample %d: %w", i, err)
		}

		rawAAC := make([]byte, sample.size)
		if _, err := io.ReadFull(r, rawAAC); err != nil {
			return nil, fmt.Errorf("read audio sample %d: %w", i, err)
		}

		// PTS in 90kHz.
		pts90k := dts[i] * 90000 / int64(track.timescale)

		frames = append(frames, bufferedAudioFrame{
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
