package main

import (
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/nmos"
	"github.com/zsiec/switchframe/server/switcher"
)

// The program video Flow and its Sender's caps state the pipeline format, and
// the format can change at runtime. A change republishes both: the Flow under
// the same id at the new geometry and rate, the caps under a new version, so
// a controller re-reading on the version sees what the program now is.
func TestST2110ProgramFlowAndCapsFollowThePipelineFormat(t *testing.T) {
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)
	a := &App{sw: sw}
	sw.SetOnFormatChange(a.onPipelineFormatChange)

	store := nmos.NewResourceStore()
	flow := programVideoFlow("flow-1", "source-1", "device-1", 1920, 1080, 50, 1)
	store.AddFlow(flow)
	store.AddSender(&nmos.Sender{ID: "sender-1", FlowID: flow.ID,
		Caps: nmos.ST2110SenderCaps(flow, 0, st2110PacketTimeMs, nmos.NewVersion())})
	a.st2110State = &st2110State{store: store, programVideoSenderID: "sender-1"}
	before := store.GetSender("sender-1")
	time.Sleep(time.Millisecond)

	if err := sw.SetPipelineFormat(switcher.PipelineFormat{Width: 1280, Height: 720,
		FPSNum: 30000, FPSDen: 1001, Name: "720p29.97"}); err != nil {
		t.Fatal(err)
	}

	after := store.GetSender("sender-1")
	got := store.GetFlow(after.FlowID)
	if after.FlowID != "flow-1" {
		t.Errorf("the sender moved to flow %s; a resolution change keeps the Flow id", after.FlowID)
	}
	if got.FrameWidth != 1280 || got.FrameHeight != 720 ||
		got.GrainRate.Numerator != 30000 || got.GrainRate.Denominator != 1001 {
		t.Errorf("flow states %dx%d at %+v, want 1280x720 at 30000/1001",
			got.FrameWidth, got.FrameHeight, *got.GrainRate)
	}
	set := after.Caps["constraint_sets"].([]map[string]any)[0]
	enum := func(k string) any { return set[k].(map[string]any)["enum"].([]any)[0] }
	if enum(nmos.CapFrameWidth) != 1280 || enum(nmos.CapFrameHeight) != 720 ||
		enum(nmos.CapGrainRate) != (nmos.Rational{Numerator: 30000, Denominator: 1001}) {
		t.Errorf("caps state %v", set)
	}
	was, now := string(before.Caps["version"].(nmos.Version)), string(after.Caps["version"].(nmos.Version))
	if nmos.CompareVersions(now, was) <= 0 {
		t.Errorf("caps version %s did not move from %s", now, was)
	}
	if nmos.CompareVersions(string(after.Version), string(before.Version)) <= 0 {
		t.Errorf("sender version %s did not move from %s", after.Version, before.Version)
	}
	if nmos.CompareVersions(now, string(after.Version)) > 0 {
		t.Errorf("caps version %s is later than the sender's %s", now, after.Version)
	}
}

// Without ST 2110 configured, a format change touches nothing of it.
func TestAFormatChangeWithoutST2110(t *testing.T) {
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)
	a := &App{sw: sw}
	sw.SetOnFormatChange(a.onPipelineFormatChange)
	if err := sw.SetPipelineFormat(switcher.PipelineFormat{Width: 1280, Height: 720,
		FPSNum: 50, FPSDen: 1}); err != nil {
		t.Fatal(err)
	}
}
