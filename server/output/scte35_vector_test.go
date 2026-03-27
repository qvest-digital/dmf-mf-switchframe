package output

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	asscte "github.com/as/scte"
	astits "github.com/asticode/go-astits"
	"github.com/stretchr/testify/require"
	untangled "github.com/untangledco/streaming/scte35"
	"github.com/zsiec/switchframe/server/scte35"
)

// vectorCase defines a test vector to generate and cross-validate across
// three independent SCTE-35 decoders.
type vectorCase struct {
	name  string
	setup func(t *testing.T, h *testHarness)
	check func(t *testing.T, raw []byte)
}

func TestVector_GenerateAndCrossValidate(t *testing.T) {
	vectors := []vectorCase{
		{
			name: "splice_insert_immediate_cueout",
			setup: func(t *testing.T, h *testHarness) {
				t.Helper()
				msg := scte35.NewSpliceInsert(1001, 30*time.Second, true, true)
				msg.UniqueProgramID = 5
				msg.AvailNum = 1
				msg.AvailsExpected = 2
				_, err := h.injector.InjectCue(msg)
				require.NoError(t, err)
			},
			check: checkSpliceInsertImmediateCueout,
		},
		{
			name: "splice_insert_scheduled",
			setup: func(t *testing.T, h *testHarness) {
				t.Helper()
				msg := scte35.NewSpliceInsert(2001, 60*time.Second, true, true)
				_, err := h.injector.ScheduleCue(msg, 5000)
				require.NoError(t, err)
			},
			check: checkSpliceInsertScheduled,
		},
		{
			name: "splice_insert_cuein",
			setup: func(t *testing.T, h *testHarness) {
				t.Helper()
				msg := scte35.NewSpliceInsert(1001, 0, false, false)
				_, err := h.injector.InjectCue(msg)
				require.NoError(t, err)
			},
			check: checkSpliceInsertCuein,
		},
		{
			name: "time_signal_program_start",
			setup: func(t *testing.T, h *testHarness) {
				t.Helper()
				msg := scte35.NewTimeSignal(0x10, 0, 0x01, []byte("SHOW001"))
				_, err := h.injector.InjectCue(msg)
				require.NoError(t, err)
			},
			check: checkTimeSignalProgramStart,
		},
		{
			name: "time_signal_program_end",
			setup: func(t *testing.T, h *testHarness) {
				t.Helper()
				msg := scte35.NewTimeSignal(0x11, 0, 0x01, []byte("SHOW001"))
				_, err := h.injector.InjectCue(msg)
				require.NoError(t, err)
			},
			check: checkTimeSignalProgramEnd,
		},
		{
			name: "time_signal_break_start",
			setup: func(t *testing.T, h *testHarness) {
				t.Helper()
				msg := scte35.NewTimeSignal(0x22, 120*time.Second, 0x01, []byte("BRK001"))
				_, err := h.injector.InjectCue(msg)
				require.NoError(t, err)
			},
			check: checkTimeSignalBreakStart,
		},
		{
			name: "time_signal_provider_ad",
			setup: func(t *testing.T, h *testHarness) {
				t.Helper()
				msg := scte35.NewTimeSignal(0x30, 30*time.Second, 0x09, []byte("AD00001"))
				msg.Descriptors[0].SegNum = 1
				msg.Descriptors[0].SegExpected = 4
				_, err := h.injector.InjectCue(msg)
				require.NoError(t, err)
			},
			check: checkTimeSignalProviderAd,
		},
		{
			name: "time_signal_multi_descriptor",
			setup: func(t *testing.T, h *testHarness) {
				t.Helper()
				breakDur := uint64(120 * 90000)
				msg := scte35.NewTimeSignalMulti([]scte35.SegmentationDescriptor{
					{
						SegmentationType: 0x22,
						UPIDType:         0x01,
						UPID:             []byte("BRK002"),
						DurationTicks:    &breakDur,
					},
					{
						SegmentationType: 0x30,
						UPIDType:         0x09,
						UPID:             []byte("AD00002"),
						SegNum:           1,
						SegExpected:      3,
					},
				})
				_, err := h.injector.InjectCue(msg)
				require.NoError(t, err)
			},
			check: checkTimeSignalMultiDescriptor,
		},
		{
			name: "splice_null",
			setup: func(t *testing.T, h *testHarness) {
				t.Helper()
				msg := &scte35.CueMessage{CommandType: scte35.CommandSpliceNull}
				_, err := h.injector.InjectCue(msg)
				require.NoError(t, err)
			},
			check: checkSpliceNull,
		},
		{
			name: "splice_insert_with_tier",
			setup: func(t *testing.T, h *testHarness) {
				t.Helper()
				msg := scte35.NewSpliceInsert(3001, 30*time.Second, true, true)
				msg.Tier = 500
				_, err := h.injector.InjectCue(msg)
				require.NoError(t, err)
			},
			check: checkSpliceInsertWithTier,
		},
	}

	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			h := newTestHarness(t)
			defer h.close()
			h.initInjector(t)
			h.initMuxer(t)

			v.setup(t, h)

			// Clear output from setup, then feed a GOP to flush SCTE-35.
			h.output = nil
			h.feedGOP(t, 30)

			assertValidTSPackets(t, h.output)

			sections := extractSCTE35Sections(t, h.output, defaultSCTE35PID)
			require.GreaterOrEqual(t, len(sections), 1, "expected at least 1 SCTE-35 section")

			v.check(t, sections[0].raw)
		})
	}

	// TS structure validation with go-astits demuxer.
	t.Run("astits_ts_structure", func(t *testing.T) {
		h := newTestHarness(t)
		defer h.close()
		h.initInjector(t)
		h.initMuxer(t)

		// Inject a cue and feed several GOPs to produce a rich TS stream.
		msg := scte35.NewSpliceInsert(0, 30*time.Second, true, true)
		_, err := h.injector.InjectCue(msg)
		require.NoError(t, err)

		for i := 0; i < 3; i++ {
			h.feedGOP(t, 30)
		}

		require.NotEmpty(t, h.output)

		// Demux with go-astits.
		ctx := context.Background()
		dmx := astits.NewDemuxer(ctx, bytes.NewReader(h.output))

		var foundPMT, foundVideo, foundSCTE35PID bool
		var foundCUEI bool

		for {
			d, err := dmx.NextData()
			if err != nil {
				if err == io.EOF {
					break
				}
				// Non-fatal: some incomplete data at end is acceptable.
				break
			}

			if d.PMT != nil {
				foundPMT = true
				for _, es := range d.PMT.ElementaryStreams {
					if es.ElementaryPID == defaultSCTE35PID {
						foundSCTE35PID = true
						require.Equal(t, astits.StreamTypeSCTE35, es.StreamType,
							"SCTE-35 elementary stream must have StreamType 0x86")
					}
				}
				// CUEI registration descriptor lives in PMT program-level
				// descriptors per SCTE-35 section 8.1.
				for _, desc := range d.PMT.ProgramDescriptors {
					if desc.Tag == astits.DescriptorTagRegistration && desc.Registration != nil {
						if desc.Registration.FormatIdentifier == 0x43554549 {
							foundCUEI = true
						}
					}
				}
			}

			if d.PES != nil && d.PID == videoPID {
				foundVideo = true
			}
		}

		require.True(t, foundPMT, "PMT must be present in TS output")
		require.True(t, foundSCTE35PID, "PMT must list SCTE-35 PID 0x102 with StreamType 0x86")
		require.True(t, foundCUEI, "PMT program_info must include CUEI registration descriptor (0x43554549)")
		require.True(t, foundVideo, "video PES data must be present")
	})
}

// ---------------------------------------------------------------------------
// Per-vector check functions: decode with 3 libraries and cross-validate
// ---------------------------------------------------------------------------

func checkSpliceInsertImmediateCueout(t *testing.T, raw []byte) {
	t.Helper()

	// Our decoder.
	ours, err := scte35.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandSpliceInsert), ours.CommandType)
	require.True(t, ours.IsOut)
	require.True(t, ours.AutoReturn)
	require.Equal(t, uint32(1001), ours.EventID)
	require.Equal(t, uint16(5), ours.UniqueProgramID)
	require.Equal(t, uint8(1), ours.AvailNum)
	require.Equal(t, uint8(2), ours.AvailsExpected)
	require.NotNil(t, ours.BreakDuration)
	require.Equal(t, 30*time.Second, *ours.BreakDuration)

	// untangledco decoder.
	theirs, err := untangled.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, untangled.CommandType(untangled.SpliceInsert), theirs.Command.Type)
	require.NotNil(t, theirs.Command.Insert)
	ins := theirs.Command.Insert
	require.True(t, ins.OutOfNetwork)
	require.Equal(t, uint32(1001), ins.ID)
	require.True(t, ins.Immediate)
	// NOTE: untangledco v0.0.4 has a bug where it reads break_duration from
	// the wrong buffer offset for immediate splices (buf is not advanced past
	// the skipped splice_time block). Skip Duration/ProgramID/Avail checks
	// for untangledco on immediate splices since the byte offset is wrong
	// for all subsequent fields.

	// as/scte decoder.
	pkt, err := asscte.Parse(raw)
	require.NoError(t, err)
	asIns, ok := pkt.Cmd.(asscte.SpliceInsert)
	require.True(t, ok, "expected SpliceInsert command, got %T", pkt.Cmd)
	require.True(t, asIns.OutOfNetwork)
	require.Equal(t, int(ours.EventID), asIns.EventID)
	require.True(t, asIns.Immediate)
	require.True(t, asIns.AutoReturn)
	require.Equal(t, 2700000, asIns.BreakDur)
	require.Equal(t, 5, asIns.ProgID)
	require.Equal(t, 1, asIns.Avail)
	require.Equal(t, 2, asIns.AvailExp)
}

func checkSpliceInsertScheduled(t *testing.T, raw []byte) {
	t.Helper()

	// Our decoder.
	ours, err := scte35.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandSpliceInsert), ours.CommandType)
	require.Equal(t, uint32(2001), ours.EventID)
	require.True(t, ours.IsOut)
	require.Equal(t, "scheduled", ours.Timing)
	require.NotNil(t, ours.SpliceTimePTS, "scheduled splice must have SpliceTimePTS")

	// untangledco decoder.
	theirs, err := untangled.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, untangled.CommandType(untangled.SpliceInsert), theirs.Command.Type)
	require.NotNil(t, theirs.Command.Insert)
	ins := theirs.Command.Insert
	require.True(t, ins.OutOfNetwork)
	require.Equal(t, uint32(2001), ins.ID)
	require.False(t, ins.Immediate, "scheduled splice must not be immediate")
	require.NotNil(t, ins.SpliceTime, "scheduled splice must have SpliceTime")

	// as/scte decoder.
	pkt, err := asscte.Parse(raw)
	require.NoError(t, err)
	asIns, ok := pkt.Cmd.(asscte.SpliceInsert)
	require.True(t, ok, "expected SpliceInsert, got %T", pkt.Cmd)
	require.True(t, asIns.OutOfNetwork)
	require.Equal(t, 2001, asIns.EventID)
	require.False(t, asIns.Immediate)
	require.True(t, asIns.Time.HasPTS, "scheduled splice must have PTS")

	// Cross-validate PTS values between our decoder and as/scte.
	require.Equal(t, int(*ours.SpliceTimePTS), asIns.Time.PTS)
}

func checkSpliceInsertCuein(t *testing.T, raw []byte) {
	t.Helper()

	// Our decoder.
	ours, err := scte35.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandSpliceInsert), ours.CommandType)
	require.Equal(t, uint32(1001), ours.EventID)
	require.False(t, ours.IsOut, "cue-in must have IsOut=false")

	// untangledco decoder.
	theirs, err := untangled.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, untangled.CommandType(untangled.SpliceInsert), theirs.Command.Type)
	require.NotNil(t, theirs.Command.Insert)
	require.False(t, theirs.Command.Insert.OutOfNetwork, "cue-in must have OutOfNetwork=false")
	require.Equal(t, uint32(1001), theirs.Command.Insert.ID)

	// as/scte decoder.
	pkt, err := asscte.Parse(raw)
	require.NoError(t, err)
	asIns, ok := pkt.Cmd.(asscte.SpliceInsert)
	require.True(t, ok, "expected SpliceInsert, got %T", pkt.Cmd)
	require.False(t, asIns.OutOfNetwork)
	require.Equal(t, 1001, asIns.EventID)
}

func checkTimeSignalProgramStart(t *testing.T, raw []byte) {
	t.Helper()

	// Our decoder.
	ours, err := scte35.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandTimeSignal), ours.CommandType)
	require.Len(t, ours.Descriptors, 1)
	require.Equal(t, uint8(0x10), ours.Descriptors[0].SegmentationType)
	require.Equal(t, uint8(0x01), ours.Descriptors[0].UPIDType)
	require.Equal(t, []byte("SHOW001"), ours.Descriptors[0].UPID)

	// untangledco decoder.
	theirs, err := untangled.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, untangled.CommandType(untangled.TimeSignal), theirs.Command.Type)
	require.GreaterOrEqual(t, len(theirs.Descriptors), 1)
	segDesc := findUntangledSegDesc(t, theirs.Descriptors)
	require.Equal(t, uint8(0x10), segDesc.Type)
	require.Equal(t, []byte("SHOW001"), segDesc.UPID.Value)

	// as/scte decoder.
	pkt, err := asscte.Parse(raw)
	require.NoError(t, err)
	_, ok := pkt.Cmd.(asscte.TimeSignal)
	require.True(t, ok, "expected TimeSignal, got %T", pkt.Cmd)
	asDesc := findAsScteSegDesc(t, pkt.Trailer.Desc)
	require.Equal(t, byte(0x10), asDesc.SegType)
	require.Equal(t, []byte("SHOW001"), asDesc.UPID)
}

func checkTimeSignalProgramEnd(t *testing.T, raw []byte) {
	t.Helper()

	// Our decoder.
	ours, err := scte35.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandTimeSignal), ours.CommandType)
	require.Len(t, ours.Descriptors, 1)
	require.Equal(t, uint8(0x11), ours.Descriptors[0].SegmentationType)

	// untangledco decoder.
	theirs, err := untangled.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, untangled.CommandType(untangled.TimeSignal), theirs.Command.Type)
	segDesc := findUntangledSegDesc(t, theirs.Descriptors)
	require.Equal(t, uint8(0x11), segDesc.Type)

	// as/scte decoder.
	pkt, err := asscte.Parse(raw)
	require.NoError(t, err)
	_, ok := pkt.Cmd.(asscte.TimeSignal)
	require.True(t, ok, "expected TimeSignal, got %T", pkt.Cmd)
	asDesc := findAsScteSegDesc(t, pkt.Trailer.Desc)
	require.Equal(t, byte(0x11), asDesc.SegType)
}

func checkTimeSignalBreakStart(t *testing.T, raw []byte) {
	t.Helper()

	// 120s * 90000 Hz = 10,800,000 ticks.
	const expectedTicks = uint64(10800000)

	// Our decoder.
	ours, err := scte35.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandTimeSignal), ours.CommandType)
	require.Len(t, ours.Descriptors, 1)
	require.Equal(t, uint8(0x22), ours.Descriptors[0].SegmentationType)
	require.NotNil(t, ours.Descriptors[0].DurationTicks)
	require.Equal(t, expectedTicks, *ours.Descriptors[0].DurationTicks)

	// untangledco decoder.
	theirs, err := untangled.Decode(raw)
	require.NoError(t, err)
	segDesc := findUntangledSegDesc(t, theirs.Descriptors)
	require.Equal(t, uint8(0x22), segDesc.Type)
	require.NotNil(t, segDesc.Duration)
	require.Equal(t, expectedTicks, *segDesc.Duration)

	// as/scte decoder.
	pkt, err := asscte.Parse(raw)
	require.NoError(t, err)
	asDesc := findAsScteSegDesc(t, pkt.Trailer.Desc)
	require.Equal(t, byte(0x22), asDesc.SegType)
	require.True(t, asDesc.HasDuration)
	require.Equal(t, int(expectedTicks), asDesc.Duration)
}

func checkTimeSignalProviderAd(t *testing.T, raw []byte) {
	t.Helper()

	// Our decoder.
	ours, err := scte35.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandTimeSignal), ours.CommandType)
	require.Len(t, ours.Descriptors, 1)
	d := ours.Descriptors[0]
	require.Equal(t, uint8(0x30), d.SegmentationType)
	require.Equal(t, uint8(0x09), d.UPIDType)
	require.Equal(t, []byte("AD00001"), d.UPID)
	require.Equal(t, uint8(1), d.SegNum)
	require.Equal(t, uint8(4), d.SegExpected)

	// untangledco decoder.
	theirs, err := untangled.Decode(raw)
	require.NoError(t, err)
	segDesc := findUntangledSegDesc(t, theirs.Descriptors)
	require.Equal(t, uint8(0x30), segDesc.Type)
	require.Equal(t, []byte("AD00001"), segDesc.UPID.Value)
	require.Equal(t, uint8(1), segDesc.Number)
	require.Equal(t, uint8(4), segDesc.Expected)

	// as/scte decoder.
	pkt, err := asscte.Parse(raw)
	require.NoError(t, err)
	asDesc := findAsScteSegDesc(t, pkt.Trailer.Desc)
	require.Equal(t, byte(0x30), asDesc.SegType)
	require.Equal(t, []byte("AD00001"), asDesc.UPID)
	require.Equal(t, 1, asDesc.SegNum)
	require.Equal(t, 4, asDesc.SegExp)
}

func checkTimeSignalMultiDescriptor(t *testing.T, raw []byte) {
	t.Helper()

	const expectedBreakTicks = uint64(120 * 90000)

	// Our decoder.
	ours, err := scte35.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandTimeSignal), ours.CommandType)
	require.Len(t, ours.Descriptors, 2)
	require.Equal(t, uint8(0x22), ours.Descriptors[0].SegmentationType)
	require.NotNil(t, ours.Descriptors[0].DurationTicks)
	require.Equal(t, expectedBreakTicks, *ours.Descriptors[0].DurationTicks)
	require.Equal(t, uint8(0x30), ours.Descriptors[1].SegmentationType)
	require.Equal(t, uint8(1), ours.Descriptors[1].SegNum)
	require.Equal(t, uint8(3), ours.Descriptors[1].SegExpected)

	// untangledco decoder.
	theirs, err := untangled.Decode(raw)
	require.NoError(t, err)
	segDescs := findAllUntangledSegDescs(t, theirs.Descriptors)
	require.GreaterOrEqual(t, len(segDescs), 2, "expected at least 2 segmentation descriptors")
	// Find each by type.
	var foundBreak, foundAd bool
	for _, sd := range segDescs {
		switch sd.Type {
		case 0x22:
			foundBreak = true
			require.NotNil(t, sd.Duration)
			require.Equal(t, expectedBreakTicks, *sd.Duration)
		case 0x30:
			foundAd = true
			require.Equal(t, uint8(1), sd.Number)
			require.Equal(t, uint8(3), sd.Expected)
		}
	}
	require.True(t, foundBreak, "expected 0x22 descriptor in untangledco output")
	require.True(t, foundAd, "expected 0x30 descriptor in untangledco output")

	// as/scte decoder.
	pkt, err := asscte.Parse(raw)
	require.NoError(t, err)
	asDescs := findAllAsScteSegDescs(t, pkt.Trailer.Desc)
	require.GreaterOrEqual(t, len(asDescs), 2, "expected at least 2 segment descriptors from as/scte")
	var asFoundBreak, asFoundAd bool
	for _, ad := range asDescs {
		switch ad.SegType {
		case 0x22:
			asFoundBreak = true
			require.True(t, ad.HasDuration)
			require.Equal(t, int(expectedBreakTicks), ad.Duration)
		case 0x30:
			asFoundAd = true
			require.Equal(t, 1, ad.SegNum)
			require.Equal(t, 3, ad.SegExp)
		}
	}
	require.True(t, asFoundBreak, "expected 0x22 descriptor in as/scte output")
	require.True(t, asFoundAd, "expected 0x30 descriptor in as/scte output")
}

func checkSpliceNull(t *testing.T, raw []byte) {
	t.Helper()

	// Our decoder.
	ours, err := scte35.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandSpliceNull), ours.CommandType)

	// untangledco decoder.
	theirs, err := untangled.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, untangled.CommandType(untangled.SpliceNull), theirs.Command.Type)

	// as/scte decoder.
	pkt, err := asscte.Parse(raw)
	require.NoError(t, err)
	_, ok := pkt.Cmd.(asscte.SpliceNull)
	require.True(t, ok, "expected SpliceNull, got %T", pkt.Cmd)
}

func checkSpliceInsertWithTier(t *testing.T, raw []byte) {
	t.Helper()

	// Our decoder.
	ours, err := scte35.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandSpliceInsert), ours.CommandType)
	require.Equal(t, uint16(500), ours.Tier)

	// untangledco decoder.
	theirs, err := untangled.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, uint16(500), theirs.Tier)

	// as/scte decoder.
	pkt, err := asscte.Parse(raw)
	require.NoError(t, err)
	require.Equal(t, 500, pkt.Header.Tier)
}

// ---------------------------------------------------------------------------
// Helper functions: extract segmentation descriptors from third-party types
// ---------------------------------------------------------------------------

// findUntangledSegDesc returns the first SegmentationDescriptor from
// untangledco's descriptor list. untangledco returns descriptors as value
// types (not pointers).
func findUntangledSegDesc(t *testing.T, descs []untangled.SpliceDescriptor) untangled.SegmentationDescriptor {
	t.Helper()
	for _, d := range descs {
		if sd, ok := d.(untangled.SegmentationDescriptor); ok {
			return sd
		}
	}
	t.Fatal("no SegmentationDescriptor found in untangledco descriptors")
	return untangled.SegmentationDescriptor{}
}

// findAllUntangledSegDescs returns all SegmentationDescriptors from
// untangledco's descriptor list.
func findAllUntangledSegDescs(t *testing.T, descs []untangled.SpliceDescriptor) []untangled.SegmentationDescriptor {
	t.Helper()
	var result []untangled.SegmentationDescriptor
	for _, d := range descs {
		if sd, ok := d.(untangled.SegmentationDescriptor); ok {
			result = append(result, sd)
		}
	}
	require.NotEmpty(t, result, "no SegmentationDescriptor found in untangledco descriptors")
	return result
}

// findAsScteSegDesc returns the first DescSegment from as/scte's descriptor list.
func findAsScteSegDesc(t *testing.T, descs []asscte.Desc) asscte.DescSegment {
	t.Helper()
	for _, d := range descs {
		if sd, ok := d.(asscte.DescSegment); ok {
			return sd
		}
	}
	t.Fatal("no DescSegment found in as/scte descriptors")
	return asscte.DescSegment{}
}

// findAllAsScteSegDescs returns all DescSegment from as/scte's descriptor list.
func findAllAsScteSegDescs(t *testing.T, descs []asscte.Desc) []asscte.DescSegment {
	t.Helper()
	var result []asscte.DescSegment
	for _, d := range descs {
		if sd, ok := d.(asscte.DescSegment); ok {
			result = append(result, sd)
		}
	}
	require.NotEmpty(t, result, "no DescSegment found in as/scte descriptors")
	return result
}
