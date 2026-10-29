package control

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// replayEngine is one engine of a pair as far as replay export goes: an API
// with a replay buffer holding a few frames of camera1 and the clip IDs its
// exports were handed.
type replayEngine struct {
	api      *API
	mgr      *replay.Manager
	exported chan string
}

func newReplayEngine(t *testing.T) *replayEngine {
	t.Helper()
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	sw.RegisterSource("camera1", distribution.NewRelay())
	mgr := replay.NewManager(distribution.NewRelay(), replay.DefaultConfig(),
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(320, 240), nil },
		func(_, _, _, _, _ int) (transition.VideoEncoder, error) { return transition.NewMockEncoder(), nil })
	t.Cleanup(mgr.Close)
	store, err := macro.NewStore(filepath.Join(t.TempDir(), "macros.json"))
	require.NoError(t, err)
	e := &replayEngine{
		api:      NewAPI(sw, WithReplayManager(mgr), WithMacroStore(store)),
		mgr:      mgr,
		exported: make(chan string, 4),
	}
	mgr.SetOnClipExported(func(_, filePath, clipID string) {
		_ = os.Remove(filePath)
		e.exported <- clipID
	})
	require.NoError(t, mgr.AddSource("camera1"))
	for i, key := range []bool{true, false, true, false} {
		mgr.RecordFrame("camera1", avc1Frame(int64(i)*3003, key))
		if i == 1 {
			require.NoError(t, mgr.MarkIn("camera1"))
			time.Sleep(10 * time.Millisecond)
		}
	}
	require.NoError(t, mgr.MarkOut("camera1"))
	return e
}

func avc1Frame(pts int64, keyframe bool) *media.VideoFrame {
	data := make([]byte, 100)
	data[3] = byte(len(data) - 4)
	data[4] = 0x65
	f := &media.VideoFrame{PTS: pts, IsKeyframe: keyframe, WireData: data, Codec: "avc1.42C01E"}
	if keyframe {
		f.SPS = []byte{0x67, 0x42, 0xC0, 0x1E}
		f.PPS = []byte{0x68, 0xCE, 0x38, 0x80}
	}
	return f
}

func (e *replayEngine) post(t *testing.T, path, body string) int {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.api.Mux().ServeHTTP(rec, req)
	return rec.Code
}

func (e *replayEngine) exportedID(t *testing.T) string {
	t.Helper()
	select {
	case id := <-e.exported:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("replay clip not exported")
		return ""
	}
}

// The UI sends one replay request to both engines of a pair, and each
// stores the clip it exports. A later delete or pin names one clip ID for
// both, so both have to store the replay under the ID the request carries.
func TestReplayExport_TakesRequestClipID(t *testing.T) {
	const id = "00112233445566778899aabbccddeeff"
	for _, tc := range []struct{ path, body string }{
		{"/api/replay/play", `{"source":"camera1","speed":1,"clipId":"` + id + `"}`},
		{"/api/replay/quick", `{"source":"camera1","seconds":5,"speed":1,"clipId":"` + id + `"}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			e := newReplayEngine(t)
			e.post(t, tc.path, tc.body)
			require.Equal(t, id, e.exportedID(t))
		})
	}
}

func TestReplayExport_RejectsMalformedClipID(t *testing.T) {
	for _, path := range []string{"/api/replay/play", "/api/replay/quick"} {
		e := newReplayEngine(t)
		code := e.post(t, path, `{"source":"camera1","seconds":5,"clipId":"../x"}`)
		require.Equal(t, http.StatusBadRequest, code, path)
	}
}

// A macro runs on each engine of a pair, and a replay step in it exports a
// clip on each. The run ID the request carries gives every such export one
// ID on both engines, and distinct IDs for distinct replays of a run.
func TestMacroReplay_ClipIDDerivedFromRunID(t *testing.T) {
	const runID = "ffeeddccbbaa99887766554433221100"
	m := macro.Macro{Name: "replay", Steps: []macro.Step{
		{Action: macro.ActionReplayPlay, Params: map[string]any{"source": "camera1", "speed": 1.0}},
	}}
	var ids []string
	for range 2 {
		e := newReplayEngine(t)
		require.NoError(t, e.api.macroStore.Save(m))
		require.Equal(t, http.StatusAccepted, e.post(t, "/api/macros/replay/run", `{"runId":"`+runID+`"}`))
		ids = append(ids, e.exportedID(t))
	}
	require.Len(t, ids[0], 32)
	require.Equal(t, ids[0], ids[1])
	require.NotEqual(t, runID, ids[0])

	target := &apiMacroTarget{runID: runID}
	require.NotEqual(t, target.nextReplayClipID(), target.nextReplayClipID())
	require.Empty(t, (&apiMacroTarget{}).nextReplayClipID())
}
