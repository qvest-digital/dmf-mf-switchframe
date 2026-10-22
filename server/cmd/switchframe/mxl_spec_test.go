package main

import (
	"errors"
	"testing"
)

const (
	specV = "d4d00000-0000-0000-0000-000000000001"
	specA = "a0d10000-0000-0000-0000-000000000001"
	specD = "c3000000-0000-0000-0000-000000000001"
)

func TestParseMXLSourceSpec(t *testing.T) {
	cases := []struct {
		spec      string
		want      mxlSourceSpec
		key       string
		label     string
		wantError bool
	}{
		// The original forms stay keyed by the video flow.
		{spec: specV, want: mxlSourceSpec{Video: specV}, key: "mxl:" + specV, label: "MXL:D4D00000-0000-0000-0000-000000000001"},
		{spec: specV + ":" + specA + ":" + specD, want: mxlSourceSpec{Video: specV, Audio: specA, Data: specD}, key: "mxl:" + specV},
		// A named input is keyed by its name, so moving it to other flows
		// keeps its key.
		{spec: "cam1=" + specV + ":" + specA, want: mxlSourceSpec{Name: "cam1", Video: specV, Audio: specA}, key: "mxl:cam1", label: "cam1"},
		// Booked without flows: read nothing until connected.
		{spec: "cam2=", want: mxlSourceSpec{Name: "cam2"}, key: "mxl:cam2", label: "cam2"},
		{spec: "Cam1=" + specV, wantError: true},
		{spec: "cam1=:" + specA, wantError: true},
		{spec: "", wantError: true},
	}
	for _, c := range cases {
		got, err := parseMXLSourceSpec(c.spec)
		if c.wantError {
			if err == nil {
				t.Errorf("%q: parsed as %+v, want an error", c.spec, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.spec, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: %+v, want %+v", c.spec, got, c.want)
		}
		if got.Key() != c.key {
			t.Errorf("%q: key %q, want %q", c.spec, got.Key(), c.key)
		}
		if c.label != "" && got.Label() != c.label {
			t.Errorf("%q: label %q, want %q", c.spec, got.Label(), c.label)
		}
	}
}

type fakeRetargetable struct{ ids []string }

func (f *fakeRetargetable) Retarget(id string) { f.ids = append(f.ids, id) }
func (f *fakeRetargetable) FlowID() string {
	if len(f.ids) == 0 {
		return ""
	}
	return f.ids[len(f.ids)-1]
}

func TestRetargetMXLInput(t *testing.T) {
	a := &App{}
	v, au := &fakeRetargetable{}, &fakeRetargetable{}
	a.namedMXLInputs.add("cam1", &mxlInput{Video: v, Audio: au})

	video, empty := specV, ""
	if err := a.retargetMXLInput("cam1", &video, nil, nil); err != nil {
		t.Fatal(err)
	}
	if v.FlowID() != specV || len(au.ids) != 0 {
		t.Errorf("video %v audio %v: only video should move", v.ids, au.ids)
	}
	if err := a.retargetMXLInput("cam1", nil, &empty, nil); err != nil {
		t.Fatal(err)
	}
	if len(au.ids) != 1 || au.ids[0] != "" {
		t.Errorf("audio %v, want one disconnect", au.ids)
	}

	// A data flow needs the data reader the input was booked with.
	data := specD
	if err := a.retargetMXLInput("cam1", nil, nil, &data); !errors.Is(err, errNoDataReader) {
		t.Errorf("data on an input without a data reader: %v", err)
	}
	if err := a.retargetMXLInput("cam9", &video, nil, nil); !errors.Is(err, errNoMXLInput) {
		t.Errorf("unknown input: %v", err)
	}
}
