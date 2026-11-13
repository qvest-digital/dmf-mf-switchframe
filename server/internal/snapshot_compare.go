package internal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
)

// replayMarkToleranceMs is how far apart the two engines' replay marks may
// lie and still be the same mark: each engine stamps a mark from its own
// clock when the command reaches it.
const replayMarkToleranceMs = 2000

// ComparableSnapshot returns snap with what legitimately differs between
// the two engines of a pair left out, as SnapshotDiff compares it:
//
//   - Version, CapturedAt, GraphicsContent and GraphicsMotion describe the
//     capture, not the engine.
//   - A graphics layer's Image is replaced by its hash, so the comparable
//     form stays small; Frame is left out, as a template or HTML layer
//     renders it on each engine at its own moments.
//   - A clip player's ClipID: replays and recordings carry their own ID on
//     each engine. ClipPlayerPositions names the clip instead; its
//     PositionMs runs on each engine's own clock.
//   - A destination's Active: one engine of a pair sends a destination and
//     the other stands by.
//   - Recording's Engine, playout channels' ElapsedMs, TransitionProgress
//     and SchedulerState, and SCTE-35 events' RemainingMs are measured on
//     each engine as it runs.
//   - MacroExecution, OperatorSessions and CommsParticipants: macro runs,
//     operator sessions and comms connections are each engine's own.
//   - StingerLoaded lists the stinger store, which converges with the
//     stores, not with the state.
func ComparableSnapshot(snap FullStateSnapshot) FullStateSnapshot {
	c := snap
	c.Version, c.CapturedAt = "", 0
	c.GraphicsContent, c.GraphicsMotion = false, false
	c.MacroExecution, c.OperatorSessions, c.CommsParticipants = nil, nil, nil
	c.StingerLoaded = nil

	if len(snap.Graphics) > 0 {
		c.Graphics = make([]GraphicsLayerSnapshot, len(snap.Graphics))
		for i, g := range snap.Graphics {
			if len(g.Image) > 0 {
				sum := sha256.Sum256(g.Image)
				g.Image = []byte(hex.EncodeToString(sum[:]))
			}
			g.Frame = nil
			c.Graphics[i] = g
		}
		sort.Slice(c.Graphics, func(i, j int) bool { return c.Graphics[i].ID < c.Graphics[j].ID })
	}
	if len(snap.Tickers) > 0 {
		c.Tickers = append([]TickerSnapshot(nil), snap.Tickers...)
		sort.Slice(c.Tickers, func(i, j int) bool { return c.Tickers[i].LayerID < c.Tickers[j].LayerID })
	}
	if len(snap.Sequences) > 0 {
		c.Sequences = append([]SequenceSnapshot(nil), snap.Sequences...)
		sort.Slice(c.Sequences, func(i, j int) bool { return c.Sequences[i].LayerID < c.Sequences[j].LayerID })
	}
	if len(snap.ClipPlayers) > 0 {
		c.ClipPlayers = make([]ClipPlayerSnapshot, len(snap.ClipPlayers))
		for i, p := range snap.ClipPlayers {
			p.ClipID = ""
			c.ClipPlayers[i] = p
		}
		sort.Slice(c.ClipPlayers, func(i, j int) bool { return c.ClipPlayers[i].ID < c.ClipPlayers[j].ID })
	}
	if len(snap.ClipPlayerPositions) > 0 {
		c.ClipPlayerPositions = make([]ClipPositionSnapshot, len(snap.ClipPlayerPositions))
		for i, p := range snap.ClipPlayerPositions {
			p.PositionMs = 0
			c.ClipPlayerPositions[i] = p
		}
		sort.Slice(c.ClipPlayerPositions, func(i, j int) bool {
			return c.ClipPlayerPositions[i].PlayerIndex < c.ClipPlayerPositions[j].PlayerIndex
		})
	}
	if len(snap.OutputDestinations) > 0 {
		c.OutputDestinations = make([]DestinationSnapshot, len(snap.OutputDestinations))
		for i, d := range snap.OutputDestinations {
			d.Active = false
			c.OutputDestinations[i] = d
		}
		sort.Slice(c.OutputDestinations, func(i, j int) bool { return c.OutputDestinations[i].ID < c.OutputDestinations[j].ID })
	}
	if snap.Recording != nil {
		r := *snap.Recording
		r.Engine = ""
		c.Recording = &r
	}
	if len(snap.PlayoutChannels) > 0 {
		c.PlayoutChannels = make([]PlayoutChannelSnapshot, len(snap.PlayoutChannels))
		for i, ch := range snap.PlayoutChannels {
			ch.ElapsedMs, ch.TransitionProgress, ch.SchedulerState = 0, 0, ""
			c.PlayoutChannels[i] = ch
		}
		sort.Slice(c.PlayoutChannels, func(i, j int) bool {
			return c.PlayoutChannels[i].ChannelIndex < c.PlayoutChannels[j].ChannelIndex
		})
	}
	if len(snap.SCTE35Active) > 0 {
		c.SCTE35Active = make([]SCTE35ActiveEventSnapshot, len(snap.SCTE35Active))
		for i, ev := range snap.SCTE35Active {
			ev.RemainingMs = 0
			c.SCTE35Active[i] = ev
		}
		sort.Slice(c.SCTE35Active, func(i, j int) bool { return c.SCTE35Active[i].EventID < c.SCTE35Active[j].EventID })
	}
	return c
}

// SnapshotDigest returns a hash of snap's comparable form.
func SnapshotDigest(snap FullStateSnapshot) string {
	data, _ := json.Marshal(ComparableSnapshot(snap))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// DiffDigest returns a hash of what a and b hold in fields, in their
// comparable forms: it changes when the difference does, and not when the
// two change alike elsewhere.
func DiffDigest(a, b FullStateSnapshot, fields []string) string {
	pick := func(s FullStateSnapshot) map[string]json.RawMessage {
		var all map[string]json.RawMessage
		data, _ := json.Marshal(ComparableSnapshot(s))
		_ = json.Unmarshal(data, &all)
		out := make(map[string]json.RawMessage, len(fields))
		for _, f := range fields {
			out[f] = all[f]
		}
		return out
	}
	data, _ := json.Marshal([2]map[string]json.RawMessage{pick(a), pick(b)})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SnapshotDiff returns the JSON names of the fields in which the two
// engines' states differ, compared in their comparable forms, sorted; nil
// while they agree.
//
// What is kept per source is compared over the sources both engines hold:
// one engine cannot take settings for an input the other has and it lacks.
// Replay marks agree within replayMarkToleranceMs.
func SnapshotDiff(a, b FullStateSnapshot) []string {
	a, b = ComparableSnapshot(a), ComparableSnapshot(b)
	shared := func(m1, m2 map[string]bool) map[string]bool {
		out := map[string]bool{}
		for k := range m1 {
			if m2[k] {
				out[k] = true
			}
		}
		return out
	}
	sources := shared(sourceKeys(a), sourceKeys(b))
	a.AudioChannels, b.AudioChannels = restrict(a.AudioChannels, b.AudioChannels, nil)
	a.Sources, b.Sources = restrict(a.Sources, b.Sources, nil)
	a.UpstreamKeys, b.UpstreamKeys = restrict(a.UpstreamKeys, b.UpstreamKeys, sources)
	if a.STMaps != nil && b.STMaps != nil {
		sa, sb := *a.STMaps, *b.STMaps
		sa.Sources, sb.Sources = restrict(sa.Sources, sb.Sources, sources)
		a.STMaps, b.STMaps = &sa, &sb
	}
	if a.ReplayMarks != nil && b.ReplayMarks != nil {
		ma, mb := *a.ReplayMarks, *b.ReplayMarks
		if near(ma.MarkIn, mb.MarkIn) {
			mb.MarkIn = ma.MarkIn
		}
		if near(ma.MarkOut, mb.MarkOut) {
			mb.MarkOut = ma.MarkOut
		}
		a.ReplayMarks, b.ReplayMarks = &ma, &mb
	}

	var diff []string
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	t := va.Type()
	for i := 0; i < t.NumField(); i++ {
		ja, _ := json.Marshal(va.Field(i).Interface())
		jb, _ := json.Marshal(vb.Field(i).Interface())
		if !bytes.Equal(normalizeEmpty(ja), normalizeEmpty(jb)) {
			diff = append(diff, jsonName(t.Field(i)))
		}
	}
	sort.Strings(diff)
	return diff
}

// sourceKeys returns every source a snapshot holds settings for.
func sourceKeys(s FullStateSnapshot) map[string]bool {
	keys := map[string]bool{}
	for k := range s.Sources {
		keys[k] = true
	}
	for k := range s.AudioChannels {
		keys[k] = true
	}
	return keys
}

// restrict returns a and b holding only keys within keep, or, keep nil,
// only the keys both hold.
func restrict[V any](a, b map[string]V, keep map[string]bool) (map[string]V, map[string]V) {
	in := func(k string) bool {
		if keep != nil {
			return keep[k]
		}
		_, inA := a[k]
		_, inB := b[k]
		return inA && inB
	}
	filter := func(m map[string]V) map[string]V {
		if m == nil {
			return nil
		}
		out := make(map[string]V, len(m))
		for k, v := range m {
			if in(k) {
				out[k] = v
			}
		}
		return out
	}
	return filter(a), filter(b)
}

func near(x, y int64) bool {
	d := x - y
	return d >= -replayMarkToleranceMs && d <= replayMarkToleranceMs
}

// normalizeEmpty folds an absent value and an empty one together.
func normalizeEmpty(j []byte) []byte {
	switch string(j) {
	case "null", "{}", "[]", `""`, "0", "false":
		return nil
	}
	return j
}

func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if i := bytes.IndexByte([]byte(tag), ','); i >= 0 {
		tag = tag[:i]
	}
	if tag == "" {
		return f.Name
	}
	return tag
}
