package main

import (
	"testing"

	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/internal"
)

// Snapshot sync pushes one engine's snapshot to the other. When the source
// engine has no layout, the target must end up without one too; an empty
// layout reads as a different state and the engines never compare equal.
func TestSetDVELayoutEmptySnapshotClearsLayout(t *testing.T) {
	app := &App{dveCompositor: dve.NewCompositor(64, 36)}
	app.dveCompositor.SetLayout(&dve.Layout{Name: "pip", Slots: []dve.Slot{{SourceKey: "cam1", Enabled: true}}})

	if err := app.SetDVELayout(internal.LayoutSnapshot{}); err != nil {
		t.Fatalf("SetDVELayout: %v", err)
	}
	// enrichState reports a layout only when the compositor holds one.
	if l := app.dveCompositor.GetLayout(); l != nil {
		t.Fatalf("layout after empty snapshot = %+v, want nil", l)
	}
}

func TestSetDVELayoutNamedSnapshotSetsLayout(t *testing.T) {
	app := &App{dveCompositor: dve.NewCompositor(64, 36)}

	if err := app.SetDVELayout(internal.LayoutSnapshot{ActivePreset: "pip"}); err != nil {
		t.Fatalf("SetDVELayout: %v", err)
	}
	if l := app.dveCompositor.GetLayout(); l == nil || l.Name != "pip" {
		t.Fatalf("layout = %+v, want preset pip", l)
	}
}
