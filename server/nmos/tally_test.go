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
