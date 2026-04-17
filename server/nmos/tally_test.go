package nmos

import (
	"testing"
)

func TestTallyPublisher_EmitsOnChange(t *testing.T) {
	var events []TallyEvent
	tp := NewTallyPublisher(func(e TallyEvent) {
		events = append(events, e)
	})

	tp.SetProgram("s1")
	tp.SetPreview("s2")

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// First event: s1 goes on program
	if events[0].SourceID != "s1" || !events[0].Program || events[0].Preview {
		t.Errorf("event[0] = %+v, want {SourceID:s1 Program:true Preview:false}", events[0])
	}

	// Second event: s2 goes on preview
	if events[1].SourceID != "s2" || events[1].Program || !events[1].Preview {
		t.Errorf("event[1] = %+v, want {SourceID:s2 Program:false Preview:true}", events[1])
	}
}

func TestTallyPublisher_EmitsOffOnChange(t *testing.T) {
	var events []TallyEvent
	tp := NewTallyPublisher(func(e TallyEvent) {
		events = append(events, e)
	})

	tp.SetProgram("s1")
	tp.SetProgram("s2")

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// First event: s1 goes on program
	if events[0].SourceID != "s1" || !events[0].Program || events[0].Preview {
		t.Errorf("event[0] = %+v, want {SourceID:s1 Program:true Preview:false}", events[0])
	}

	// Second event: s1 goes off program (still not on preview)
	if events[1].SourceID != "s1" || events[1].Program || events[1].Preview {
		t.Errorf("event[1] = %+v, want {SourceID:s1 Program:false Preview:false}", events[1])
	}

	// Third event: s2 goes on program
	if events[2].SourceID != "s2" || !events[2].Program || events[2].Preview {
		t.Errorf("event[2] = %+v, want {SourceID:s2 Program:true Preview:false}", events[2])
	}
}

func TestTallyPublisher_NoEmitOnSameValue(t *testing.T) {
	var events []TallyEvent
	tp := NewTallyPublisher(func(e TallyEvent) {
		events = append(events, e)
	})

	tp.SetProgram("s1")
	tp.SetProgram("s1") // no-op

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].SourceID != "s1" || !events[0].Program {
		t.Errorf("event[0] = %+v, want {SourceID:s1 Program:true Preview:false}", events[0])
	}
}

func TestTallyPublisher_ProgramAndPreviewOverlap(t *testing.T) {
	var events []TallyEvent
	tp := NewTallyPublisher(func(e TallyEvent) {
		events = append(events, e)
	})

	tp.SetProgram("s1")
	tp.SetPreview("s1")

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// First event: s1 goes on program
	if events[0].SourceID != "s1" || !events[0].Program || events[0].Preview {
		t.Errorf("event[0] = %+v, want {SourceID:s1 Program:true Preview:false}", events[0])
	}

	// Second event: s1 is now on both program AND preview
	if events[1].SourceID != "s1" || !events[1].Program || !events[1].Preview {
		t.Errorf("event[1] = %+v, want {SourceID:s1 Program:true Preview:true}", events[1])
	}
}

func TestTallyPublisher_Getters(t *testing.T) {
	tp := NewTallyPublisher(nil)

	if got := tp.Program(); got != "" {
		t.Errorf("Program() = %q, want empty", got)
	}
	if got := tp.Preview(); got != "" {
		t.Errorf("Preview() = %q, want empty", got)
	}

	tp.SetProgram("s1")
	tp.SetPreview("s2")

	if got := tp.Program(); got != "s1" {
		t.Errorf("Program() = %q, want s1", got)
	}
	if got := tp.Preview(); got != "s2" {
		t.Errorf("Preview() = %q, want s2", got)
	}
}

func TestTallyPublisher_NilOnChange(t *testing.T) {
	// Should not panic with nil onChange
	tp := NewTallyPublisher(nil)
	tp.SetProgram("s1")
	tp.SetPreview("s2")
}

func TestTallyPublisher_KeyFillEmitsRed(t *testing.T) {
	var events []TallyEvent
	tp := NewTallyPublisher(func(e TallyEvent) {
		events = append(events, e)
	})

	tp.SetKeyRoles([]string{"cam3"}, nil)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	got := events[0]
	if got.SourceID != "cam3" || got.Program || got.Preview || !got.KeyFill || got.Auxiliary {
		t.Errorf("event[0] = %+v, want {SourceID:cam3 KeyFill:true}", got)
	}
}

func TestTallyPublisher_KeyAlphaEmitsAuxiliary(t *testing.T) {
	var events []TallyEvent
	tp := NewTallyPublisher(func(e TallyEvent) {
		events = append(events, e)
	})

	tp.SetKeyRoles(nil, []string{"cam1"})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	got := events[0]
	if got.SourceID != "cam1" || got.Program || got.Preview || got.KeyFill || !got.Auxiliary {
		t.Errorf("event[0] = %+v, want {SourceID:cam1 Auxiliary:true}", got)
	}
}

func TestTallyPublisher_ProgramOverridesKeyFill(t *testing.T) {
	var events []TallyEvent
	tp := NewTallyPublisher(func(e TallyEvent) {
		events = append(events, e)
	})

	tp.SetProgram("cam1")
	tp.SetKeyRoles([]string{"cam1"}, nil)

	if len(events) == 0 {
		t.Fatalf("expected at least one event")
	}

	// Program source that is also a key fill should still be Program:true
	// (no duplicate RED transitions).
	last := events[len(events)-1]
	if last.SourceID != "cam1" || !last.Program {
		t.Errorf("last event for cam1 must preserve Program=true, got %+v", last)
	}
}

func TestTallyPublisher_ClearKeyRolesEmitsOff(t *testing.T) {
	var events []TallyEvent
	tp := NewTallyPublisher(func(e TallyEvent) {
		events = append(events, e)
	})

	tp.SetKeyRoles([]string{"cam3"}, nil)
	startLen := len(events)

	tp.SetKeyRoles(nil, nil)

	if len(events) != startLen+1 {
		t.Fatalf("expected 1 off event, got %d new events", len(events)-startLen)
	}
	off := events[len(events)-1]
	if off.SourceID != "cam3" || off.KeyFill || off.Auxiliary || off.Program || off.Preview {
		t.Errorf("off event = %+v, want all-false for cam3", off)
	}
}

func TestTallyPublisher_KeyFillOnPreviewIsRed(t *testing.T) {
	var events []TallyEvent
	tp := NewTallyPublisher(func(e TallyEvent) {
		events = append(events, e)
	})

	tp.SetPreview("cam3")
	tp.SetKeyRoles([]string{"cam3"}, nil)

	last := events[len(events)-1]
	if last.SourceID != "cam3" || !last.KeyFill || !last.Preview {
		t.Errorf("last event for cam3 = %+v, want KeyFill:true Preview:true", last)
	}
}
