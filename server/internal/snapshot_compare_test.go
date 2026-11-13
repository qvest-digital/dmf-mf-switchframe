package internal

import (
	"reflect"
	"slices"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

// fullSnapshot sets every field of a FullStateSnapshot.
func fullSnapshot() FullStateSnapshot {
	return FullStateSnapshot{
		Version:              "1",
		CapturedAt:           1,
		ProgramSource:        "cam1",
		PreviewSource:        "cam2",
		TransitionType:       "mix",
		TransitionDurationMs: 500,
		TransitionEasing:     "linear",
		FTBActive:            true,
		Encoder:              "libx264",
		AudioChannels:        map[string]AudioChannelSnapshot{"cam1": {Level: -6}},
		AudioMaster:          AudioMasterSnapshot{MasterLevel: -3},
		Graphics:             []GraphicsLayerSnapshot{{ID: 1, Active: true, Image: []byte("png"), Frame: []byte("frame")}},
		GraphicsContent:      true,
		Tickers:              []TickerSnapshot{{LayerID: 1, Text: "news", FontSize: 30, Speed: 80}},
		Sequences:            []SequenceSnapshot{{LayerID: 2, Hash: "abc", Size: 3, SourceFPS: 25, Speed: 1}},
		GraphicsMotion:       true,
		Layout:               LayoutSnapshot{ActivePreset: "pip"},
		CaptionMode:          "author",
		ClipPlayers:          []ClipPlayerSnapshot{{ID: 1, ClipID: "a1", State: "playing"}},
		Sources:              map[string]SourceConfigSnapshot{"cam1": {Label: "Cam 1"}},
		UpstreamKeys:         map[string]UpstreamKeySnapshot{"cam1": {Type: "chroma", Enabled: true}},
		ColorGrade:           &ColorGradeSnapshot{Enabled: true, LookName: "warm"},
		STMaps:               &STMapSnapshot{Program: "barrel"},
		Recording:            &RecordingSnapshot{Active: true, Engine: "a"},
		OutputDestinations:   []DestinationSnapshot{{ID: "d1", Config: DestinationSnapshotConfig{Type: "srt"}, Active: true, Armed: boolPtr(true)}},
		ReplayMarks:          &ReplayMarksSnapshot{Source: "cam1", MarkIn: 1000, MarkOut: 5000},
		ReplayPlayer:         &ReplayPlayerSnapshot{Source: "cam1", State: "playing", Speed: 0.5},
		MacroExecution:       &MacroExecutionSnapshot{MacroID: "m", State: "running"},
		OperatorSessions:     []OperatorSessionSnapshot{{OperatorID: "o"}},
		CommsParticipants:    []string{"o"},
		PlayoutChannels:      []PlayoutChannelSnapshot{{ChannelIndex: 0, CurrentItemIndex: 2, ElapsedMs: 1500, SchedulerState: "playing"}},
		SCTE35Active:         []SCTE35ActiveEventSnapshot{{EventID: 7, Type: "splice_insert", RemainingMs: 3000}},
		StingerLoaded:        []string{"wipe"},
		ClipPlayerPositions:  []ClipPositionSnapshot{{ClipName: "intro", PlayerIndex: 0, PositionMs: 400, State: "playing"}},
	}
}

// Fields the two engines of a pair hold each their own of.
var notCompared = []string{"version", "capturedAt", "graphicsContent", "graphicsMotion", "macroExecution", "operatorSessions", "commsParticipants", "stingerLoaded"}

// Every field of the snapshot is compared, but those that are each
// engine's own: a field added to the snapshot is compared unless it is
// left out on purpose.
func TestSnapshotDiffCoversEveryField(t *testing.T) {
	full := fullSnapshot()
	v := reflect.ValueOf(full)
	for i := 0; i < v.NumField(); i++ {
		name := jsonName(v.Type().Field(i))
		if v.Field(i).IsZero() {
			t.Fatalf("fullSnapshot leaves %s unset", name)
		}
		other := full
		reflect.ValueOf(&other).Elem().Field(i).SetZero()
		// Settings per source are compared over the sources both hold, so
		// one engine's are changed rather than cleared.
		switch name {
		case "audioChannels":
			other.AudioChannels = map[string]AudioChannelSnapshot{"cam1": {}}
		case "sources":
			other.Sources = map[string]SourceConfigSnapshot{"cam1": {}}
		}
		diff := SnapshotDiff(full, other)
		if slices.Contains(notCompared, name) {
			if diff != nil {
				t.Errorf("%s is each engine's own, yet compared: %v", name, diff)
			}
			continue
		}
		if !slices.Equal(diff, []string{name}) {
			t.Errorf("%s cleared on one engine: diff %v, want [%s]", name, diff, name)
		}
	}
}

// What each engine measures or renders on its own does not make the
// engines differ.
func TestSnapshotDiffIgnoresEachEnginesOwn(t *testing.T) {
	a, b := fullSnapshot(), fullSnapshot()
	b.CapturedAt = 99
	b.Graphics = []GraphicsLayerSnapshot{{ID: 1, Active: true, Image: []byte("png"), Frame: []byte("other frame")}}
	b.ClipPlayers = []ClipPlayerSnapshot{{ID: 1, ClipID: "b7", State: "playing"}}
	b.ClipPlayerPositions = []ClipPositionSnapshot{{ClipName: "intro", PlayerIndex: 0, PositionMs: 480, State: "playing"}}
	b.OutputDestinations = []DestinationSnapshot{{ID: "d1", Config: DestinationSnapshotConfig{Type: "srt"}, Active: false, Armed: boolPtr(true)}}
	b.Recording = &RecordingSnapshot{Active: true, Engine: "b"}
	b.PlayoutChannels = []PlayoutChannelSnapshot{{ChannelIndex: 0, CurrentItemIndex: 2, ElapsedMs: 1700, SchedulerState: "transitioning"}}
	b.SCTE35Active = []SCTE35ActiveEventSnapshot{{EventID: 7, Type: "splice_insert", RemainingMs: 2800}}
	b.ReplayMarks = &ReplayMarksSnapshot{Source: "cam1", MarkIn: 1040, MarkOut: 4990}
	b.MacroExecution, b.OperatorSessions, b.CommsParticipants, b.StingerLoaded = nil, nil, nil, nil
	if diff := SnapshotDiff(a, b); diff != nil {
		t.Fatalf("engines differ in %v", diff)
	}
}

// Settings of a source only one engine holds are not compared; settings of
// a source both hold are.
func TestSnapshotDiffComparesSharedSources(t *testing.T) {
	a, b := fullSnapshot(), fullSnapshot()
	a.AudioChannels["cam9"] = AudioChannelSnapshot{Level: -20}
	a.Sources["cam9"] = SourceConfigSnapshot{Label: "Only on A"}
	a.UpstreamKeys["cam9"] = UpstreamKeySnapshot{Type: "luma"}
	if diff := SnapshotDiff(a, b); diff != nil {
		t.Fatalf("a source only one engine holds made them differ in %v", diff)
	}

	b.Sources["cam9"] = SourceConfigSnapshot{Label: "Only on A"}
	if diff := SnapshotDiff(a, b); !slices.Equal(diff, []string{"upstreamKeys"}) {
		t.Fatalf("a key on a source both hold, set on one: diff %v, want [upstreamKeys]", diff)
	}
}

func TestSnapshotDiffComparesContent(t *testing.T) {
	a, b := fullSnapshot(), fullSnapshot()
	b.Graphics = []GraphicsLayerSnapshot{{ID: 1, Active: true, Image: []byte("another png"), Frame: []byte("frame")}}
	b.ReplayMarks = &ReplayMarksSnapshot{Source: "cam1", MarkIn: 9000, MarkOut: 5000}
	b.AudioChannels = map[string]AudioChannelSnapshot{"cam1": {Level: -7}}
	want := []string{"audioChannels", "graphics", "replayMarks"}
	if diff := SnapshotDiff(a, b); !slices.Equal(diff, want) {
		t.Fatalf("diff %v, want %v", diff, want)
	}
}

// A channel pair selected on one engine of a pair only is a difference the
// engines converge on, like any other audio setting.
func TestSnapshotDiffComparesChannelPair(t *testing.T) {
	a, b := fullSnapshot(), fullSnapshot()
	pair := b.AudioChannels["cam1"]
	pair.FirstChannel = 3
	b.AudioChannels = map[string]AudioChannelSnapshot{"cam1": pair}
	if diff := SnapshotDiff(a, b); !slices.Equal(diff, []string{"audioChannels"}) {
		t.Fatalf("diff %v, want [audioChannels]", diff)
	}
}

func TestSnapshotDigestFollowsComparableState(t *testing.T) {
	a, b := fullSnapshot(), fullSnapshot()
	b.CapturedAt = 1234
	if SnapshotDigest(a) != SnapshotDigest(b) {
		t.Fatal("digest depends on the capture time")
	}
	b.PreviewSource = "cam3"
	if SnapshotDigest(a) == SnapshotDigest(b) {
		t.Fatal("digest ignores the preview source")
	}
}
