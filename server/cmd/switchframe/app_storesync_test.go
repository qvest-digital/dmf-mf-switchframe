package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/colorgrade"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

const syncTestToken = "pair-token"

// syncTestEngine is an engine of a pair with every store it syncs, on its
// own state directory, reachable by its peer while up.
type syncTestEngine struct {
	t   *testing.T
	dir string
	app *App
	srv *httptest.Server
	up  atomic.Bool

	// maxStingers and clipMax, when set, bound the stores the next start
	// opens.
	maxStingers int
	clipMax     int64
}

func newSyncTestEngine(t *testing.T) *syncTestEngine {
	e := &syncTestEngine{t: t, dir: t.TempDir()}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !e.up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/api/peer/health" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+syncTestToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/sync/stores":
			e.app.storeSyncer.ServeManifest(w, r)
		case "/api/sync/stores/item":
			e.app.storeSyncer.ServeItem(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(e.srv.Close)
	return e
}

// start opens the engine's stores from its state directory, as a process
// start does, with its peer at peerURL.
func (e *syncTestEngine) start(peerURL string) {
	t := e.t
	clipMax := e.clipMax
	if clipMax == 0 {
		clipMax = 1 << 30
	}
	a := &App{cfg: AppConfig{StateDir: e.dir, PeerEngineURL: peerURL, APIToken: syncTestToken, ClipStorageMax: clipMax}}
	var err error
	a.presetStore, err = preset.NewStore(a.statePath("presets.json"))
	require.NoError(t, err)
	a.macroStore, err = macro.NewStore(a.statePath("macros.json"))
	require.NoError(t, err)
	a.operatorStore, err = operator.NewStore(a.statePath("operators.json"))
	require.NoError(t, err)
	a.scte35Rules, err = scte35.LoadRulesStore(a.statePath("scte35_rules.json"))
	require.NoError(t, err)
	a.stingerStore, err = stinger.NewStore(a.statePath("stingers"), e.maxStingers, "")
	require.NoError(t, err)
	a.wipeMapStore, err = wipemap.NewWipeMapStore(a.statePath("wipe_patterns"))
	require.NoError(t, err)
	a.dvePresetStore, err = dve.NewPresetStore(a.statePath("dve_presets"))
	require.NoError(t, err)
	a.stmapStore, err = stmap.NewStore(a.statePath("stmaps"))
	require.NoError(t, err)
	a.stmapRegistry = stmap.NewRegistry()
	cg, err := colorgrade.NewStore(a.statePath("luts"))
	require.NoError(t, err)
	a.colorGradeCtrl = newColorGradeController(cg)
	a.playoutPlaylistDir = a.statePath("playout-playlists")
	require.NoError(t, os.MkdirAll(a.playoutPlaylistDir, 0o755))
	a.clipStore, err = clip.NewStore(a.statePath("clips"), a.cfg.ClipStorageMax)
	require.NoError(t, err)
	a.peerPoller = peer.NewHealthPoller(peerURL)
	a.bgCtx, a.bgCancel = context.WithCancel(context.Background())
	e.t.Cleanup(a.bgCancel)
	a.initStoreSync()
	require.NotNil(t, a.storeSyncer)
	e.app = a
	e.up.Store(true)
}

func (e *syncTestEngine) sync() peer.SyncResult {
	e.t.Helper()
	res, err := e.app.storeSyncer.SyncOnce(context.Background())
	require.NoError(e.t, err)
	return res
}

// manifest is the engine's items and their hashes, store by store.
func (e *syncTestEngine) manifest() map[string]map[string]string {
	out := map[string]map[string]string{}
	for store, items := range e.app.storeSyncer.Manifest().Stores {
		out[store] = map[string]string{}
		for key, it := range items {
			if !it.Deleted {
				out[store][key] = it.Hash
			}
		}
	}
	return out
}

func syncTestPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.NRGBA{R: shade, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// fill writes one item into every store an operator writes to.
func (e *syncTestEngine) fill() {
	t, a := e.t, e.app
	_, err := a.presetStore.Create("Opening", preset.ControlRoomSnapshot{ProgramSource: "cam1"})
	require.NoError(t, err)
	require.NoError(t, a.macroStore.Save(macro.Macro{Name: "Go", Steps: []macro.Step{{Action: macro.ActionCut, Params: map[string]any{"source": "cam1"}}}}))
	_, err = a.operatorStore.RegisterWithToken("Ada", operator.RoleDirector, "op-ada-0001", strings.Repeat("ab", 32))
	require.NoError(t, err)
	_, err = a.scte35Rules.Create(scte35.Rule{Name: "pass all", Enabled: true, Action: scte35.ActionPass})
	require.NoError(t, err)

	stingerDir := a.statePath("stingers", "swoosh")
	require.NoError(t, os.MkdirAll(stingerDir, 0o755))
	for i, shade := range []uint8{10, 20} {
		require.NoError(t, os.WriteFile(filepath.Join(stingerDir, "frame_000"+string(rune('1'+i))+".png"), syncTestPNG(t, shade), 0o644))
	}
	require.NoError(t, a.stingerStore.LoadFromDir("swoosh"))

	require.NoError(t, a.wipeMapStore.UploadCustom("star", "Star", []byte{0, 64, 128, 255}, 2, 2))
	require.NoError(t, a.dvePresetStore.Save(&dve.Preset{ID: "two-box", Name: "Two box"}))
	require.NoError(t, a.stmapStore.SaveStatic(&stmap.STMap{Name: "lens", Width: 2, Height: 2,
		S: []float32{0, 1, 0, 1}, T: []float32{0, 0, 1, 1}}))
	require.NoError(t, os.WriteFile(a.statePath("luts", "warm.cube"), []byte("LUT_3D_SIZE 2\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(a.playoutPlaylistDir, "morning.json"), []byte(`{"name":"morning","items":[]}`), 0o644))

	id := strings.Repeat("c", 32)
	require.NoError(t, os.WriteFile(a.statePath("clips", id+".ts"), bytes.Repeat([]byte{0x47}, 188*4), 0o644))
	require.NoError(t, a.clipStore.AddWithID(id, &clip.Clip{Name: "intro", Filename: id + ".ts",
		Source: clip.SourceUpload, ByteSize: 188 * 4}))
}

func syncTestPair(t *testing.T) (a, b *syncTestEngine) {
	a, b = newSyncTestEngine(t), newSyncTestEngine(t)
	a.start(b.srv.URL)
	b.start(a.srv.URL)
	return a, b
}

// An engine that starts empty takes every store's items from its peer,
// each exactly as the peer holds it, and leaves the peer's alone.
func TestStoreSync_EmptyEngineTakesThePeersStores(t *testing.T) {
	a, b := syncTestPair(t)
	a.fill()
	want := a.manifest()
	for _, store := range []string{"presets", "macros", "operators", "scte35-rules", "stingers", "wipe-patterns",
		"dve-presets", "stmaps", "luts", "playout-playlists", "clips"} {
		require.NotEmpty(t, want[store], "store %s holds nothing to sync", store)
	}

	res := a.sync()
	require.Zero(t, res.Pulled+res.Deleted, "an empty peer changed the full engine")
	require.Equal(t, want, a.manifest())

	b.sync()
	require.Equal(t, want, b.manifest())

	// What the stores serve, not only their files.
	_, ok := b.app.stingerStore.Get("swoosh")
	require.True(t, ok, "stinger not loaded")
	_, ok = b.app.stmapRegistry.Get("lens")
	require.True(t, ok, "ST map not loaded into the registry")
	_, err := b.app.wipeMapStore.GetPattern("star")
	require.NoError(t, err, "wipe pattern not registered")
	op, err := b.app.operatorStore.GetByToken(strings.Repeat("ab", 32))
	require.NoError(t, err, "operator token not taken")
	require.Equal(t, "Ada", op.Name)
	c, err := b.app.clipStore.Get(strings.Repeat("c", 32))
	require.NoError(t, err)
	require.Equal(t, "intro", c.Name)
	require.Equal(t, []string{"Opening"}, presetNames(b.app.presetStore))
	pa, _ := a.app.presetStore.GetByName("Opening")
	pb, _ := b.app.presetStore.GetByName("Opening")
	require.Equal(t, pa.ID, pb.ID, "preset taken under another ID")

	// Converged: further rounds move nothing.
	for i := 0; i < 2; i++ {
		ra, rb := a.sync(), b.sync()
		require.Zero(t, ra.Pulled+ra.Deleted+ra.Diverged)
		require.Zero(t, rb.Pulled+rb.Deleted+rb.Diverged)
	}
}

// A write and a delete made while the peer was down reach it when it
// comes back on its own disk, and its older copies come back to neither.
func TestStoreSync_WritesWhilePeerDownConverge(t *testing.T) {
	a, b := syncTestPair(t)
	a.fill()
	b.sync()
	a.sync()

	// b goes down.
	b.up.Store(false)
	_, err := a.app.presetStore.Create("Closing", preset.ControlRoomSnapshot{ProgramSource: "cam2"})
	require.NoError(t, err)
	require.NoError(t, a.app.macroStore.Delete("Go"))
	require.NoError(t, a.app.stingerStore.Delete("swoosh"))
	a.app.storeSyncer.Scan()

	// b restarts on its own state directory and takes a's stores before
	// it serves.
	b.start(a.srv.URL)
	require.Equal(t, []string{"Opening"}, presetNames(b.app.presetStore))
	b.app.storeSyncAtStart(context.Background())
	require.Equal(t, []string{"Closing", "Opening"}, presetNames(b.app.presetStore))
	require.Empty(t, b.app.macroStore.List())
	_, ok := b.app.stingerStore.Get("swoosh")
	require.False(t, ok, "deleted stinger kept")
	_, err = os.Stat(b.app.statePath("stingers", "swoosh"))
	require.True(t, os.IsNotExist(err), "deleted stinger left on disk")

	a.sync()
	require.Empty(t, a.app.macroStore.List(), "deleted macro came back")
	_, ok = a.app.stingerStore.Get("swoosh")
	require.False(t, ok, "deleted stinger came back")
	require.Equal(t, a.manifest(), b.manifest())
}

func presetNames(s *preset.Store) []string {
	var names []string
	for _, p := range s.List() {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names
}

// A clip each engine cut from its own program, a replay or a recording,
// carries its own ID on each and is not copied: the peer holds its own.
func TestStoreSync_LeavesEachEnginesOwnClips(t *testing.T) {
	a, b := syncTestPair(t)
	id := strings.Repeat("d", 32)
	require.NoError(t, os.WriteFile(a.app.statePath("clips", id+".ts"), []byte{0x47}, 0o644))
	require.NoError(t, a.app.clipStore.AddWithID(id, &clip.Clip{Name: "replay", Filename: id + ".ts",
		Source: clip.SourceReplay, ByteSize: 1}))
	b.sync()
	require.Empty(t, b.app.clipStore.List())
}

// The SCTE-35 rules of an engine that never saved any are no write: they
// do not replace the peer's.
func TestStoreSync_DefaultSCTE35RulesDoNotReplaceThePeers(t *testing.T) {
	a, b := syncTestPair(t)
	_, err := a.app.scte35Rules.Create(scte35.Rule{Name: "replace", Enabled: true, Action: scte35.ActionReplace})
	require.NoError(t, err)
	a.sync()
	require.Len(t, a.app.scte35Rules.List(), 1)
	b.sync()
	require.Len(t, b.app.scte35Rules.List(), 1)
	raw, err := json.Marshal(b.app.scte35Rules.List())
	require.NoError(t, err)
	require.Contains(t, string(raw), `"replace"`)
}

// An engine whose peer does not answer at start goes on without its stores
// rather than waiting on it.
func TestStoreSync_StartWithPeerDown(t *testing.T) {
	a, b := syncTestPair(t)
	b.fill()
	b.up.Store(false)
	_, err := a.app.presetStore.Create("Local", preset.ControlRoomSnapshot{})
	require.NoError(t, err)
	a.app.storeSyncAtStart(context.Background())
	require.Equal(t, []string{"Local"}, presetNames(a.app.presetStore))
	ok, failed, _ := a.app.storeSyncer.Rounds()
	require.Zero(t, ok+failed, "a round ran against a peer that does not answer")
	// The stores were listed all the same, before any write after the
	// start: a change found then is the stores reading their files anew.
	ledger, err := os.ReadFile(a.app.statePath(storeSyncLedgerFile))
	require.NoError(t, err, "the stores were not listed at the start")
	require.Contains(t, string(ledger), `"Local"`)
}

// Custom wipe patterns, uploaded or taken from the peer engine, are
// registered again after a restart: their files alone make no pattern.
func TestLoadCustomWipes(t *testing.T) {
	dir := t.TempDir()
	s, err := wipemap.NewWipeMapStore(dir)
	require.NoError(t, err)
	require.NoError(t, s.UploadCustom("diamond", "Diamond", []byte{1, 2, 3, 4, 5, 6}, 3, 2))

	restarted, err := wipemap.NewWipeMapStore(dir)
	require.NoError(t, err)
	restarted.RegisterBuiltins()
	loadCustomWipes(restarted, dir)
	p, err := restarted.GetPattern("diamond")
	require.NoError(t, err)
	require.Equal(t, "Diamond", p.Name)
	m, err := restarted.GetMap("diamond", 3, 2)
	require.NoError(t, err)
	require.Equal(t, []byte{1, 2, 3, 4, 5, 6}, m)
}

func addTestStinger(t *testing.T, a *App, name string, shade uint8) {
	t.Helper()
	dir := a.statePath("stingers", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "frame_0001.png"), syncTestPNG(t, shade), 0o644))
	require.NoError(t, a.stingerStore.LoadFromDir(name))
}

// A stinger an engine holds on disk but did not load, for want of room or
// because its files fail to read, is not taken for deleted: its peer keeps
// its copy.
func TestStoreSync_UnloadedItemIsNotDeletedOnThePeer(t *testing.T) {
	a, b := syncTestPair(t)
	addTestStinger(t, a.app, "alpha", 10)
	addTestStinger(t, a.app, "beta", 20)
	b.sync()
	a.sync()

	b.maxStingers = 1
	b.start(a.srv.URL)
	require.Len(t, b.app.stingerStore.List(), 1)
	require.NoError(t, os.Chmod(b.app.statePath("stingers", "beta"), 0))
	t.Cleanup(func() { _ = os.Chmod(b.app.statePath("stingers", "beta"), 0o755) })
	b.app.storeSyncer.Scan()

	res := a.sync()
	require.Zero(t, res.Deleted, "an item the peer failed to load was deleted")
	require.ElementsMatch(t, []string{"alpha", "beta"}, a.app.stingerStore.List())
}

// A clip whose media an engine cannot read now is not taken for deleted.
func TestStoreSync_UnreadableClipIsNotDeletedOnThePeer(t *testing.T) {
	a, b := syncTestPair(t)
	a.fill()
	b.sync()
	a.sync()
	clips := b.app.statePath("clips")
	require.NoError(t, os.Chmod(clips, 0))
	t.Cleanup(func() { _ = os.Chmod(clips, 0o755) })
	b.app.storeSyncer.Scan()
	require.Zero(t, a.sync().Deleted, "a clip the peer could not read was deleted")
	_, err := a.app.clipStore.Get(strings.Repeat("c", 32))
	require.NoError(t, err)
}

// A clip the clip storage has no room for is not transferred, and not
// tried again until its backoff ends.
func TestStoreSync_ClipWithoutRoomIsNotTransferred(t *testing.T) {
	a, b := syncTestPair(t)
	id := strings.Repeat("e", 32)
	media := bytes.Repeat([]byte{0x47}, 188*100)
	require.NoError(t, os.WriteFile(a.app.statePath("clips", id+".ts"), media, 0o644))
	require.NoError(t, a.app.clipStore.AddWithID(id, &clip.Clip{Name: "big", Filename: id + ".ts",
		Source: clip.SourceUpload, ByteSize: int64(len(media))}))

	b.clipMax = 1000
	b.start(a.srv.URL)
	var reads atomic.Int32
	inner := a.srv.Config.Handler
	a.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/stores/item" {
			reads.Add(1)
		}
		inner.ServeHTTP(w, r)
	})
	res := b.sync()
	require.Equal(t, 1, res.Failed)
	require.Zero(t, reads.Load(), "a clip without room was transferred")
	require.Empty(t, b.app.clipStore.List())
	b.sync()
	require.Zero(t, reads.Load())
}

// Clip media that does not match the hash sent with it is not stored, and
// what a transfer a restart cut short left is removed at start.
func TestStoreSync_ClipMediaHashAndLeftovers(t *testing.T) {
	a, b := syncTestPair(t)
	a.fill()
	id := strings.Repeat("c", 32)
	src := newClipSyncStore(a.app.clipStore, 1<<30)
	rc, err := src.Open(id)
	require.NoError(t, err)
	var item bytes.Buffer
	_, err = item.ReadFrom(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())

	forged := bytes.Replace(item.Bytes(), bytes.Repeat([]byte{0x47}, 188), bytes.Repeat([]byte{0x48}, 188), 1)
	require.NotEqual(t, item.Bytes(), forged)
	dst := newClipSyncStore(b.app.clipStore, 1<<30)
	require.ErrorContains(t, dst.Put(id, bytes.NewReader(forged)), "does not match its hash")
	require.Empty(t, b.app.clipStore.List())
	require.NoError(t, dst.Put(id, bytes.NewReader(item.Bytes())))
	_, err = b.app.clipStore.Get(id)
	require.NoError(t, err)

	left := filepath.Join(b.app.statePath("clips"), ".sync-clip-123")
	require.NoError(t, os.WriteFile(left, []byte("partial"), 0o644))
	newClipSyncStore(b.app.clipStore, 1<<30)
	_, err = os.Stat(left)
	require.True(t, os.IsNotExist(err), "a cut-short transfer left behind")
}

// A first round that takes longer than the start waits does not hold the
// engine back from serving.
func TestStoreSync_StartWaitIsBounded(t *testing.T) {
	a, b := syncTestPair(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	inner := a.srv.Config.Handler
	a.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/stores" {
			<-release
		}
		inner.ServeHTTP(w, r)
	})
	defer func(d time.Duration) { storeSyncStartupWait = d }(storeSyncStartupWait)
	storeSyncStartupWait = 100 * time.Millisecond
	start := time.Now()
	b.app.storeSyncAtStart(context.Background())
	require.Less(t, time.Since(start), 5*time.Second)
}

func newTestStingerSyncStore(t *testing.T, maxClips int) (*stingerSyncStore, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "stingers")
	st, err := stinger.NewStore(dir, maxClips, "")
	require.NoError(t, err)
	return &stingerSyncStore{store: st, dir: dir, stage: filepath.Join(t.TempDir(), "stage")}, dir
}

func writeStingerDir(t *testing.T, dir, name string, files map[string][]byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0o755))
	for f, data := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name, f), data, 0o644))
	}
}

func listedKeys(t *testing.T, s peer.SyncStore) []string {
	t.Helper()
	items, err := s.List()
	require.NoError(t, err)
	var keys []string
	for _, it := range items {
		if !it.Unreadable {
			keys = append(keys, it.Key)
		}
	}
	sort.Strings(keys)
	return keys
}

// A directory without a frame, as an upload cut short leaves, is no
// stinger; one with frames is, loaded or not, and deleting it removes its
// files though the store never loaded it.
func TestStingerSyncStore_ListAndDeleteFromDisk(t *testing.T) {
	s, dir := newTestStingerSyncStore(t, 0)
	writeStingerDir(t, dir, "partial", map[string][]byte{"audio.wav": []byte("RIFF")})
	writeStingerDir(t, dir, "empty", nil)
	writeStingerDir(t, dir, "unloaded", map[string][]byte{"frame_0001.png": syncTestPNG(t, 30)})
	require.Equal(t, []string{"unloaded"}, listedKeys(t, s))
	// Held, but no stinger: reported unreadable, so a directory emptied
	// for a moment never counts as a deleted stinger.
	items, err := s.List()
	require.NoError(t, err)
	unreadable := map[string]bool{}
	for _, it := range items {
		unreadable[it.Key] = it.Unreadable
	}
	require.Equal(t, map[string]bool{"partial": true, "empty": true, "unloaded": false}, unreadable)
	_, loaded := s.store.Get("unloaded")
	require.False(t, loaded)

	require.NoError(t, s.Delete("unloaded"))
	_, err = os.Stat(filepath.Join(dir, "unloaded"))
	require.True(t, os.IsNotExist(err), "a stinger the store never loaded kept its files")
	require.Empty(t, listedKeys(t, s))
}

// A stinger the store has no room for is refused before it transfers; one
// that replaces a stinger held is not.
func TestStingerSyncStore_Admit(t *testing.T) {
	s, dir := newTestStingerSyncStore(t, 1)
	require.NoError(t, s.Admit("first", 1<<20))
	writeStingerDir(t, dir, "first", map[string][]byte{"frame_0001.png": syncTestPNG(t, 30)})
	require.NoError(t, s.store.LoadFromDir("first"))
	require.ErrorIs(t, s.Admit("second", 1), stinger.ErrMaxClipsReached)
	require.NoError(t, s.Admit("first", 1), "replacing a stinger held was refused")
}

// A stinger being replaced is listed all along: seen missing for a moment
// while its directory is swapped, it would be taken for deleted.
func TestStingerSyncStore_ReplaceIsNeverSeenMissing(t *testing.T) {
	s, dir := newTestStingerSyncStore(t, 0)
	writeStingerDir(t, dir, "swoosh", map[string][]byte{"frame_0001.png": syncTestPNG(t, 30)})
	require.NoError(t, s.store.LoadFromDir("swoosh"))
	stop := make(chan struct{})
	missing := make(chan bool, 1)
	go func() {
		defer close(missing)
		for {
			select {
			case <-stop:
				return
			default:
			}
			items, err := s.List()
			if err != nil {
				continue
			}
			found := false
			for _, it := range items {
				found = found || it.Key == "swoosh"
			}
			// What the ledger takes for deleted is only a key missing; an
			// unreadable one keeps its entry. A readable one must also
			// carry frames, as a stinger seen half swapped would not.
			if !found {
				missing <- true
				return
			}
		}
	}()
	for i := 0; i < 300; i++ {
		stage := filepath.Join(t.TempDir(), "next")
		writeStingerDir(t, filepath.Dir(stage), "next", map[string][]byte{"frame_0001.png": syncTestPNG(t, uint8(i))})
		require.NoError(t, s.store.Replace("swoosh", stage))
	}
	close(stop)
	require.False(t, <-missing, "a stinger being replaced was listed as missing")
}

// An item from an engine whose type carries a field this one lacks is held
// here without it; Canonical names the form held, so the item counts as the
// peer's version rather than as a later write here.
func TestJSONSyncStores_CanonicalIsTheFormHeld(t *testing.T) {
	a, _ := syncTestPair(t)
	app := a.app
	cases := []struct {
		store *jsonSyncStore
		key   string
		data  string
	}{
		{presetSyncStore(app.presetStore), "Show",
			`[{"id":"p1","name":"Show","programSource":"cam1","newerField":7}]`},
		{macroSyncStore(app.macroStore), "Go",
			`{"name":"Go","steps":[{"action":"cut","params":{"source":"cam1"}}],"newerField":7}`},
		{operatorSyncStore(app.operatorStore), "op-from-peer",
			`{"id":"op-from-peer","name":"Ada","role":"director","token":"` + strings.Repeat("ab", 32) + `","newerField":7}`},
		{scte35SyncStore(app.scte35Rules, app.statePath("scte35_rules.json")), scte35RulesItem,
			`{"rules":[{"id":"r1","name":"pass","enabled":true,"action":"pass","newerField":7}],"defaultAction":"pass"}`},
	}
	for _, tc := range cases {
		canonical, err := tc.store.Canonical(tc.key, []byte(tc.data))
		require.NoError(t, err)
		require.NoError(t, tc.store.Put(tc.key, strings.NewReader(tc.data)))
		items, err := tc.store.List()
		require.NoError(t, err)
		var held string
		for _, it := range items {
			if it.Key == tc.key {
				held = it.Hash
			}
		}
		require.Equal(t, held, canonical, "store of %q", tc.key)
	}
}
