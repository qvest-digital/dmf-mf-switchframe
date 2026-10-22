package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/zsiec/switchframe/server/mxl"
)

// mxlInputNameRe is what a named MXL input may be called. The name becomes
// part of the source key, a URL path segment in the API and a stream name.
var mxlInputNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// mxlSourceSpec is one --mxl-sources entry: "video[:audio[:data]]" keyed by
// the video flow id, or "name=[video[:audio[:data]]]" keyed by the name.
//
// A named input keeps its key whichever flows it reads, so it can be moved
// to other flows while it runs without its stream ending; one booked
// without flows reads nothing until it is connected.
type mxlSourceSpec struct {
	Name  string
	Video string
	Audio string
	Data  string
}

func parseMXLSourceSpec(spec string) (mxlSourceSpec, error) {
	var s mxlSourceSpec
	flows := spec
	if name, rest, ok := strings.Cut(spec, "="); ok {
		if !mxlInputNameRe.MatchString(name) {
			return s, fmt.Errorf("mxl source %q: name %q must be a lowercase DNS label", spec, name)
		}
		s.Name, flows = name, rest
	}
	if flows == "" {
		if s.Name == "" {
			return s, errors.New("mxl source: empty spec")
		}
		return s, nil
	}
	parts := strings.SplitN(flows, ":", 3)
	s.Video = parts[0]
	if len(parts) > 1 {
		s.Audio = parts[1]
	}
	if len(parts) > 2 {
		s.Data = parts[2]
	}
	if s.Video == "" {
		return s, fmt.Errorf("mxl source %q: a video flow id is required when flows are given", spec)
	}
	return s, nil
}

// Key is the source key the input is registered under.
func (s mxlSourceSpec) Key() string {
	if s.Name != "" {
		return "mxl:" + s.Name
	}
	return "mxl:" + s.Video
}

// Label is the label the input is shown under until one is set.
func (s mxlSourceSpec) Label() string {
	if s.Name != "" {
		return s.Name
	}
	return strings.ToUpper(s.Key())
}

// mxlInput is a named MXL input's readers, which retargetMXLInput moves to
// other flows. Data is nil when the input was booked without a data flow.
type mxlInput struct {
	Video mxl.Retargetable
	Audio mxl.Retargetable
	Data  mxl.Retargetable
}

// mxlInputs holds the named MXL inputs by name.
type mxlInputs struct {
	mu     sync.Mutex
	byName map[string]*mxlInput
}

func (m *mxlInputs) add(name string, in *mxlInput) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byName == nil {
		m.byName = map[string]*mxlInput{}
	}
	m.byName[name] = in
}

func (m *mxlInputs) get(name string) (*mxlInput, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.byName[name]
	return in, ok
}

// errNoMXLInput is returned for a name no named MXL input has.
var errNoMXLInput = errors.New("no such MXL input")

// errNoDataReader is returned when a data flow is named for an input booked
// without one: its data reader, and the SCTE-104 path it feeds, do not exist.
var errNoDataReader = errors.New("MXL input has no data reader")

// retargetMXLInput moves a named input's readers to other flows. A nil id
// leaves that essence as it is and an empty one disconnects it. The readers
// keep reading their current flows until the new ones are open, so the
// input's stream continues without a gap.
func (a *App) retargetMXLInput(name string, video, audio, data *string) error {
	in, ok := a.namedMXLInputs.get(name)
	if !ok {
		return fmt.Errorf("%w: %q", errNoMXLInput, name)
	}
	if data != nil && *data != "" && in.Data == nil {
		return fmt.Errorf("%w: %q", errNoDataReader, name)
	}
	if video != nil {
		in.Video.Retarget(*video)
	}
	if audio != nil {
		in.Audio.Retarget(*audio)
	}
	if data != nil && in.Data != nil {
		in.Data.Retarget(*data)
	}
	return nil
}
