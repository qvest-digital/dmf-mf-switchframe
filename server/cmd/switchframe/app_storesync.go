package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

// What an operator writes to disk converges between the engines of a pair
// through a peer.StoreSyncer. Left out, as each engine's own: the SRT
// sources (copied from the booking at every start), the MXL NMOS receiver
// state (each engine is its own NMOS node), the as-run logs, the playout
// cache, the command queue, the command sequence, the leadership epoch and
// the API token. Clips are left out where a clip library is attached, which
// shares them already, and so are replays and recordings, which each engine
// cuts from its own program.

const (
	storeSyncLedgerFile = "peer_sync.json"
	storeSyncTmpDir     = ".peer-sync"

	// storeSyncInterval is how often the stores are compared with the
	// peer's.
	storeSyncInterval = 5 * time.Second
)

// storeSyncStartupWait is how long an engine that has just started takes
// its peer's stores before it serves; a variable for tests.
var storeSyncStartupWait = 2 * time.Minute

// initStoreSync sets up the syncer of the engine's stores with its peer's.
// Nothing without a peer.
func (a *App) initStoreSync() {
	if a.peerPoller == nil || a.cfg.StateDir == "" {
		return
	}
	s := peer.NewStoreSyncer(peer.StoreSyncConfig{
		LedgerPath: a.statePath(storeSyncLedgerFile),
		TmpDir:     a.statePath(storeSyncTmpDir),
		PeerURL:    a.cfg.PeerEngineURL,
		Token:      a.cfg.APIToken,
		Client:     a.peerPoller.HTTPClient(),
	})
	const small = 16 << 20
	if a.presetStore != nil {
		s.Register("presets", presetSyncStore(a.presetStore), peer.StoreOptions{MaxItemBytes: small})
	}
	if a.macroStore != nil {
		s.Register("macros", macroSyncStore(a.macroStore), peer.StoreOptions{MaxItemBytes: small})
	}
	if a.operatorStore != nil {
		s.Register("operators", operatorSyncStore(a.operatorStore), peer.StoreOptions{MaxItemBytes: small})
	}
	if a.scte35Rules != nil {
		s.Register("scte35-rules", scte35SyncStore(a.scte35Rules, a.statePath("scte35_rules.json")), peer.StoreOptions{MaxItemBytes: small})
	}
	if a.stingerStore != nil {
		s.Register("stingers", &stingerSyncStore{store: a.stingerStore, dir: a.statePath("stingers"), stage: s.TmpDir()},
			peer.StoreOptions{MaxItemBytes: 600 << 20})
	}
	if a.wipeMapStore != nil {
		s.Register("wipe-patterns", wipeSyncStore(a.wipeMapStore, a.statePath("wipe_patterns")), peer.StoreOptions{MaxItemBytes: 64 << 20})
	}
	if a.dvePresetStore != nil {
		s.Register("dve-presets", &dirSyncStore{dir: a.statePath("dve_presets"), match: hasSuffix(".json")},
			peer.StoreOptions{MaxItemBytes: small})
	}
	if a.stmapStore != nil {
		s.Register("stmaps", a.stmapSyncStore(), peer.StoreOptions{MaxItemBytes: 256 << 20})
	}
	if a.colorGradeCtrl != nil {
		s.Register("luts", &dirSyncStore{dir: a.statePath("luts"), match: hasSuffix(".cube", ".json")},
			peer.StoreOptions{MaxItemBytes: 64 << 20})
	}
	if a.playoutPlaylistDir != "" {
		s.Register("playout-playlists", &dirSyncStore{dir: a.playoutPlaylistDir, match: hasSuffix(".json")},
			peer.StoreOptions{MaxItemBytes: small})
	}
	if a.clipStore != nil && a.clipStore.LibraryDir() == "" {
		s.Register("clips", newClipSyncStore(a.clipStore, a.cfg.ClipStorageMax),
			peer.StoreOptions{MaxItemBytes: a.cfg.ClipStorageMax + (1 << 20), ItemHashes: true})
	}
	a.storeSyncer = s
}

// storeSyncAtStart takes the peer's stores before the engine serves, when
// the peer answers: an engine that comes back holds what was written while
// it was away before an operator sees it. It waits storeSyncStartupWait at
// most, the first scan of the stores included; a round still running then
// completes in the background.
func (a *App) storeSyncAtStart(ctx context.Context) {
	if a.storeSyncer == nil {
		return
	}
	done := make(chan error, 1)
	go func() {
		// The first listing of the stores comes before any operator
		// write can: what it finds changed since the last run is the
		// stores reading their files anew, as after an upgrade.
		a.storeSyncer.Scan()
		if _, err := a.peerPoller.Fetch(ctx); err != nil {
			slog.Info("peer sync: peer not reached at start; its stores follow once it answers", "error", err)
			done <- nil
			return
		}
		_, err := a.storeSyncer.SyncOnce(a.bgCtx)
		done <- err
	}()
	wait := time.NewTimer(storeSyncStartupWait)
	defer wait.Stop()
	select {
	case err := <-done:
		if err != nil {
			slog.Warn("peer sync: stores not taken from the peer at start; they follow at the next round",
				"error", err)
		}
	case <-wait.C:
		slog.Warn("peer sync: taking the peer's stores takes longer than the start waits; serving meanwhile",
			"waited", storeSyncStartupWait)
	case <-ctx.Done():
	}
}

func hasSuffix(suffixes ...string) func(string) bool {
	return func(name string) bool {
		for _, s := range suffixes {
			if strings.HasSuffix(name, s) {
				return true
			}
		}
		return false
	}
}

// fileModTime returns path's modification time, zero where it is absent.
func fileModTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// jsonSyncStore is a store of small JSON values, all kept in one file. Its
// items carry no date of their own: the file's modification time dates the
// last change of any of them, so an item the ledger does not hold yet is
// undated.
type jsonSyncStore struct {
	items func() map[string]any
	put   func(key string, data []byte) error
	del   func(key string) error

	// decode decodes an item into the store's type, as put does.
	decode func(data []byte) (any, error)
}

// decodeAs decodes JSON into a T, as a store of Ts takes it.
func decodeAs[T any](data []byte) (any, error) {
	var v T
	err := json.Unmarshal(data, &v)
	return v, err
}

// Canonical hashes an item as the store holds it once taken: decoded into
// its type, which drops what the type lacks and fills in what it adds, and
// encoded as List encodes it.
func (j *jsonSyncStore) Canonical(_ string, data []byte) (string, error) {
	v, err := j.decode(data)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(out)
	return hex.EncodeToString(sum[:]), nil
}

func (j *jsonSyncStore) List() ([]peer.SyncItem, error) {
	var out []peer.SyncItem
	for key, v := range j.items() {
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:])
		out = append(out, peer.SyncItem{Key: key, Fingerprint: h, Hash: h, Size: int64(len(data))})
	}
	return out, nil
}

func (j *jsonSyncStore) Open(key string) (io.ReadCloser, error) {
	v, ok := j.items()[key]
	if !ok {
		return nil, fs.ErrNotExist
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (j *jsonSyncStore) Put(key string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return j.put(key, data)
}

func (j *jsonSyncStore) Delete(key string) error {
	return j.del(key)
}

// presetSyncStore keys presets by name: an engine names the ID of a
// preset it creates itself, so the two engines' copies of a preset
// created through both carry different IDs. Taking the peer's presets of a
// name takes their IDs too.
func presetSyncStore(s *preset.Store) *jsonSyncStore {
	return &jsonSyncStore{
		decode: decodeAs[[]preset.Preset],
		items: func() map[string]any {
			byName := map[string][]preset.Preset{}
			for _, p := range s.List() {
				byName[p.Name] = append(byName[p.Name], p)
			}
			out := make(map[string]any, len(byName))
			for name, ps := range byName {
				out[name] = ps
			}
			return out
		},
		put: func(key string, data []byte) error {
			var ps []preset.Preset
			if err := json.Unmarshal(data, &ps); err != nil {
				return err
			}
			return s.ReplaceByName(key, ps)
		},
		del: func(key string) error { return s.ReplaceByName(key, nil) },
	}
}

func macroSyncStore(s *macro.Store) *jsonSyncStore {
	return &jsonSyncStore{
		decode: decodeAs[macro.Macro],
		items: func() map[string]any {
			out := map[string]any{}
			for _, m := range s.List() {
				out[m.Name] = m
			}
			return out
		},
		put: func(key string, data []byte) error {
			var m macro.Macro
			if err := json.Unmarshal(data, &m); err != nil {
				return err
			}
			if m.Name != key {
				return fmt.Errorf("macro %q under key %q", m.Name, key)
			}
			return s.Save(m)
		},
		del: func(key string) error {
			if err := s.Delete(key); err != nil && !errors.Is(err, macro.ErrNotFound) {
				return err
			}
			return nil
		},
	}
}

// operatorSyncStore keys operators by ID, token included, so an operator
// registered on one engine only is taken by the other too. Operators of an
// identity provider are left out: each engine adds them as their sign-ins
// reach it.
func operatorSyncStore(s *operator.Store) *jsonSyncStore {
	return &jsonSyncStore{
		decode: decodeAs[operator.Operator],
		items: func() map[string]any {
			out := map[string]any{}
			for _, op := range s.List() {
				if !strings.HasPrefix(op.ID, operator.ExternalIDPrefix) {
					out[op.ID] = op
				}
			}
			return out
		},
		put: func(key string, data []byte) error {
			var op operator.Operator
			if err := json.Unmarshal(data, &op); err != nil {
				return err
			}
			if op.ID != key || strings.HasPrefix(op.ID, operator.ExternalIDPrefix) {
				return fmt.Errorf("operator %q under key %q", op.ID, key)
			}
			return s.Put(op)
		},
		del: func(key string) error {
			if err := s.Delete(key); err != nil && !errors.Is(err, operator.ErrNotFound) {
				return err
			}
			return nil
		},
	}
}

// scte35RulesItem is the one item of the SCTE-35 rules store: the rules
// are evaluated in order, under one default action, so they converge as a
// whole.
const scte35RulesItem = "rules"

type scte35RulesContent struct {
	Rules         []scte35.Rule     `json:"rules"`
	DefaultAction scte35.RuleAction `json:"defaultAction"`
}

// scte35SyncStore lists the rules only once they were saved: the defaults
// of an engine that never had any are no write of their own.
func scte35SyncStore(s *scte35.RulesStore, file string) *jsonSyncStore {
	return &jsonSyncStore{
		decode: decodeAs[scte35RulesContent],
		items: func() map[string]any {
			if fileModTime(file).IsZero() {
				return nil
			}
			// As a Put stores them: decoding fills in defaults.
			rules := s.List()
			if data, err := json.Marshal(rules); err == nil {
				_ = json.Unmarshal(data, &rules)
			}
			return map[string]any{scte35RulesItem: scte35RulesContent{Rules: rules, DefaultAction: s.DefaultAction()}}
		},
		put: func(key string, data []byte) error {
			if key != scte35RulesItem {
				return fmt.Errorf("no SCTE-35 rules item %q", key)
			}
			var c scte35RulesContent
			if err := json.Unmarshal(data, &c); err != nil {
				return err
			}
			return s.Replace(c.Rules, c.DefaultAction)
		},
		del: func(string) error { return nil },
	}
}

// dirSyncStore is a store of files in one directory, one item each.
type dirSyncStore struct {
	dir   string
	match func(name string) bool

	// put, when set, takes up a file just written; del, when set,
	// removes one in the store's own way.
	put func(name string) error
	del func(name string) error
}

// syncTmpSuffix marks a file being written by a pull.
const syncTmpSuffix = ".sync-tmp"

func (d *dirSyncStore) valid(name string) error {
	if name == "" || filepath.Base(name) != name || strings.HasPrefix(name, ".") || !d.match(name) {
		return fmt.Errorf("no item %q in this store", name)
	}
	return nil
}

func (d *dirSyncStore) List() ([]peer.SyncItem, error) {
	entries, err := os.ReadDir(d.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []peer.SyncItem
	for _, e := range entries {
		if !e.Type().IsRegular() || d.valid(e.Name()) != nil {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			out = append(out, peer.SyncItem{Key: e.Name(), Unreadable: true})
			continue
		}
		out = append(out, peer.SyncItem{
			Key:         e.Name(),
			Fingerprint: fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano()),
			ModTime:     fi.ModTime(),
		})
	}
	return out, nil
}

func (d *dirSyncStore) Open(key string) (io.ReadCloser, error) {
	if err := d.valid(key); err != nil {
		return nil, err
	}
	return os.Open(filepath.Join(d.dir, key))
}

func (d *dirSyncStore) Put(key string, r io.Reader) error {
	if err := d.valid(key); err != nil {
		return err
	}
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(d.dir, key)
	tmp := filepath.Join(d.dir, "."+key+syncTmpSuffix)
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if d.put != nil {
		return d.put(key)
	}
	return nil
}

func (d *dirSyncStore) Delete(key string) error {
	if err := d.valid(key); err != nil {
		return err
	}
	if d.del != nil {
		if err := d.del(key); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(d.dir, key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// wipeSyncStore syncs a custom wipe pattern's map (<id>.bin) and its
// metadata (<id>.json) as two items, and registers the pattern once both
// are in place.
func wipeSyncStore(s *wipemap.WipeMapStore, dir string) *dirSyncStore {
	idOf := func(name string) string {
		return strings.TrimSuffix(strings.TrimSuffix(name, ".bin"), ".json")
	}
	return &dirSyncStore{
		dir:   dir,
		match: hasSuffix(".bin", ".json"),
		put:   func(name string) error { return registerCustomWipe(s, dir, idOf(name)) },
		del: func(name string) error {
			if err := s.DeleteCustom(idOf(name)); err != nil && !errors.Is(err, wipemap.ErrPatternNotFound) {
				return err
			}
			return nil
		},
	}
}

// registerCustomWipe registers the custom wipe pattern id stored in dir,
// once its map and its metadata are both there.
func registerCustomWipe(s *wipemap.WipeMapStore, dir, id string) error {
	data, err := os.ReadFile(filepath.Join(dir, id+".bin"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	metaJSON, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var meta struct {
		Name          string
		Width, Height int
	}
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return err
	}
	return s.UploadCustom(id, meta.Name, data, meta.Width, meta.Height)
}

// loadCustomWipes registers the custom wipe patterns stored in dir, as
// they were uploaded or taken from the peer engine before a restart.
func loadCustomWipes(s *wipemap.WipeMapStore, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".bin")
		if !ok || e.IsDir() {
			continue
		}
		if err := registerCustomWipe(s, dir, id); err != nil {
			slog.Warn("custom wipe pattern not loaded", "id", id, "error", err)
		}
	}
}

const (
	stmapStaticExt   = ".stmap"
	stmapAnimatedExt = ".stmap-anim.json"
)

// stmapSyncStore syncs the ST map files and loads each one it takes into
// the registry, as a start does.
func (a *App) stmapSyncStore() *dirSyncStore {
	nameOf := func(file string) (string, bool) {
		if n, ok := strings.CutSuffix(file, stmapAnimatedExt); ok {
			return n, true
		}
		return strings.TrimSuffix(file, stmapStaticExt), false
	}
	return &dirSyncStore{
		dir:   a.statePath("stmaps"),
		match: hasSuffix(stmapStaticExt, stmapAnimatedExt),
		put: func(file string) error {
			name, animated := nameOf(file)
			return a.loadSTMap(name, animated)
		},
		del: func(file string) error {
			name, _ := nameOf(file)
			if err := a.stmapRegistry.Delete(name); err != nil && !errors.Is(err, stmap.ErrNotFound) {
				return err
			}
			return nil
		},
	}
}

// loadSTMap loads the stored ST map name into the registry, regenerating
// an animated one from its parameters.
func (a *App) loadSTMap(name string, animated bool) error {
	if !animated {
		m, err := a.stmapStore.LoadStatic(name)
		if err != nil {
			return err
		}
		return a.stmapRegistry.Store(m)
	}
	meta, err := a.stmapStore.LoadAnimatedMeta(name)
	if err != nil {
		return err
	}
	anim, err := stmap.GenerateAnimated(meta.Generator, meta.Params, meta.Width, meta.Height, meta.FrameCount)
	if err != nil {
		return err
	}
	anim.Name = name
	// Use mmap-cached LUT build when cache dir is configured.
	if a.cfg.CacheDir != "" {
		cacheDir := filepath.Join(a.cfg.CacheDir, "stmap",
			name+"_"+stmap.CacheKey(meta.Generator, meta.Params, meta.Width, meta.Height, meta.FrameCount))
		anim.BuildProcessorsCached(cacheDir)
	} else {
		anim.BuildProcessors()
	}
	return a.stmapRegistry.StoreAnimated(anim)
}

// writeTar writes files, a name and its path each, to w as a tar stream
// that depends on their names and content only.
func writeTar(w io.Writer, names []string, path func(name string) string) error {
	tw := tar.NewWriter(w)
	for _, name := range names {
		f, err := os.Open(path(name))
		if err != nil {
			return err
		}
		fi, err := f.Stat()
		if err == nil {
			err = tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeReg,
				Name:     name,
				Mode:     0o644,
				Size:     fi.Size(),
				ModTime:  time.Unix(0, 0),
				Format:   tar.FormatUSTAR,
			})
		}
		if err == nil {
			_, err = io.Copy(tw, f)
		}
		_ = f.Close()
		if err != nil {
			return err
		}
	}
	return tw.Close()
}

// tarReadCloser streams what write writes.
func tarReadCloser(write func(io.Writer) error) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(write(pw)) }()
	return pr
}

// stingerSyncStore syncs a stinger's directory, its PNG frames and WAV, as
// one item.
type stingerSyncStore struct {
	store *stinger.Store
	dir   string
	stage string
}

func (s *stingerSyncStore) files(name string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, name))
	if err != nil {
		return nil, err
	}
	var out []fs.FileInfo
	for _, e := range entries {
		if !e.Type().IsRegular() || !stingerFile(e.Name()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, fi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func stingerFile(name string) bool {
	lower := strings.ToLower(name)
	return filepath.Base(name) == name && (strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".wav"))
}

// List lists the stingers on disk, loaded or not: one the store failed to
// load at start, or had no room for, is still the operator's.
func (s *stingerSyncStore) List() ([]peer.SyncItem, error) {
	dirs, err := s.store.DirFiles()
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []peer.SyncItem
	for name, all := range dirs {
		var files []fs.FileInfo
		for _, fi := range all {
			if stingerFile(fi.Name()) {
				files = append(files, fi)
			}
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
		if all == nil || !hasStingerFrame(files) {
			// Unreadable, or without a frame, as an upload cut short
			// leaves: no stinger to take, and none deleted either. One
			// never listed before stays absent.
			out = append(out, peer.SyncItem{Key: name, Unreadable: true})
			continue
		}
		h := sha256.New()
		var mt time.Time
		for _, fi := range files {
			_, _ = fmt.Fprintf(h, "%s:%d:%d\n", fi.Name(), fi.Size(), fi.ModTime().UnixNano())
			if fi.ModTime().After(mt) {
				mt = fi.ModTime()
			}
		}
		out = append(out, peer.SyncItem{Key: name, Fingerprint: hex.EncodeToString(h.Sum(nil)), ModTime: mt})
	}
	return out, nil
}

func hasStingerFrame(files []fs.FileInfo) bool {
	for _, fi := range files {
		if strings.HasSuffix(strings.ToLower(fi.Name()), ".png") {
			return true
		}
	}
	return false
}

func validStingerName(name string) error {
	if name == "" || filepath.Base(name) != name || strings.HasPrefix(name, ".") {
		return fmt.Errorf("no stinger %q", name)
	}
	return nil
}

func (s *stingerSyncStore) Open(key string) (io.ReadCloser, error) {
	if err := validStingerName(key); err != nil {
		return nil, err
	}
	files, err := s.files(key)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(files))
	for i, fi := range files {
		names[i] = fi.Name()
	}
	return tarReadCloser(func(w io.Writer) error {
		return writeTar(w, names, func(n string) string { return filepath.Join(s.dir, key, n) })
	}), nil
}

// Admit refuses a stinger the store has no room for.
func (s *stingerSyncStore) Admit(key string, _ int64) error {
	return s.store.Room(key)
}

func (s *stingerSyncStore) Put(key string, r io.Reader) error {
	if err := os.MkdirAll(s.stage, 0o700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(s.stage, "stinger-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || !stingerFile(hdr.Name) {
			return fmt.Errorf("stinger item holds %q", hdr.Name)
		}
		f, err := os.Create(filepath.Join(stage, hdr.Name))
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return s.store.Replace(key, stage)
}

func (s *stingerSyncStore) Delete(key string) error {
	if err := validStingerName(key); err != nil {
		return err
	}
	if err := s.store.Delete(key); err != nil && !errors.Is(err, stinger.ErrNotFound) {
		return err
	}
	// A stinger the store did not load has its files all the same.
	return os.RemoveAll(filepath.Join(s.dir, key))
}

// clipSyncStore syncs the clips an operator uploaded, each its metadata and
// media as one item. A clip is identified by its metadata, without when
// each engine added it, and its media's size: the same upload reaches both
// engines, and hashing gigabytes of media to tell it apart from itself
// would cost more than it finds.
type clipSyncStore struct {
	store    *clip.Store
	dir      string
	maxBytes int64 // the clip storage limit
}

// newClipSyncStore returns the clip store's sync store, removing what a
// transfer a restart cut short left in dir.
func newClipSyncStore(store *clip.Store, maxBytes int64) *clipSyncStore {
	dir := store.Dir()
	if left, err := filepath.Glob(filepath.Join(dir, clipSyncTmpPattern)); err == nil {
		for _, f := range left {
			_ = os.Remove(f)
		}
	}
	return &clipSyncStore{store: store, dir: dir, maxBytes: maxBytes}
}

const (
	clipMetaEntry      = "clip.json"
	clipMediaEntry     = "media"
	clipHashEntry      = "media.sha256"
	clipSyncTmpPattern = ".sync-clip-*"
)

// clipFilename is where a synced clip's media lives: under its ID, as an
// upload leaves it.
func clipFilename(c *clip.Clip) string {
	return c.ID + filepath.Ext(c.Filename)
}

// synced reports whether c is a clip the store syncs.
func (s *clipSyncStore) synced(c *clip.Clip) bool {
	return c.Source == clip.SourceUpload && !c.Ephemeral && c.LibraryPath == "" &&
		c.Filename == clipFilename(c)
}

func (s *clipSyncStore) List() ([]peer.SyncItem, error) {
	var out []peer.SyncItem
	for _, c := range s.store.List() {
		if !s.synced(c) {
			// Not an upload, or one still being stored.
			continue
		}
		fi, err := os.Stat(filepath.Join(s.dir, c.Filename))
		if err != nil {
			// Held, but its media cannot be read now.
			out = append(out, peer.SyncItem{Key: c.ID, Unreadable: true})
			continue
		}
		id := *c
		id.CreatedAt = time.Time{}
		data, err := json.Marshal(struct {
			Clip      clip.Clip `json:"clip"`
			MediaSize int64     `json:"mediaSize"`
		}{id, fi.Size()})
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:])
		out = append(out, peer.SyncItem{
			Key:         c.ID,
			Fingerprint: h + ":" + fmt.Sprint(fi.ModTime().UnixNano()),
			Hash:        h,
			Size:        fi.Size(),
			ModTime:     fi.ModTime(),
		})
	}
	return out, nil
}

func (s *clipSyncStore) Open(key string) (io.ReadCloser, error) {
	c, err := s.store.Get(key)
	if err != nil {
		return nil, err
	}
	if !s.synced(c) {
		return nil, fs.ErrNotExist
	}
	meta, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return tarReadCloser(func(w io.Writer) error {
		tw := tar.NewWriter(w)
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: clipMetaEntry, Mode: 0o644,
			Size: int64(len(meta)), Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		if _, err := tw.Write(meta); err != nil {
			return err
		}
		f, err := os.Open(filepath.Join(s.dir, c.Filename))
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: clipMediaEntry, Mode: 0o644,
			Size: fi.Size(), Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		h := sha256.New()
		if _, err := io.Copy(tw, io.TeeReader(f, h)); err != nil {
			return err
		}
		sum := []byte(hex.EncodeToString(h.Sum(nil)))
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: clipHashEntry, Mode: 0o644,
			Size: int64(len(sum)), Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		if _, err := tw.Write(sum); err != nil {
			return err
		}
		return tw.Close()
	}), nil
}

// Admit refuses a clip that would take the clip storage past its limit.
func (s *clipSyncStore) Admit(key string, size int64) error {
	used := s.store.TotalBytes()
	if c, err := s.store.Get(key); err == nil && !c.Ephemeral && c.LibraryPath == "" {
		used -= c.ByteSize
	}
	if used+size > s.maxBytes {
		return fmt.Errorf("%w: %d bytes held, %d more of the %d allowed", clip.ErrStorageFull, used, size, s.maxBytes)
	}
	return nil
}

func (s *clipSyncStore) Put(key string, r io.Reader) error {
	tr := tar.NewReader(r)
	hdr, err := tr.Next()
	if err != nil {
		return err
	}
	if hdr.Name != clipMetaEntry || hdr.Size > 1<<20 {
		return fmt.Errorf("clip item starts with %q", hdr.Name)
	}
	var c clip.Clip
	if err := json.NewDecoder(tr).Decode(&c); err != nil {
		return err
	}
	if c.ID != key || !clip.ValidID(key) || !s.synced(&c) {
		return fmt.Errorf("clip item %q holds no uploaded clip of that ID", key)
	}
	if ext := filepath.Ext(c.Filename); strings.ContainsAny(ext, `/\`) {
		return fmt.Errorf("clip item %q: bad file name %q", key, c.Filename)
	}
	hdr, err = tr.Next()
	if err != nil {
		return err
	}
	if hdr.Name != clipMediaEntry {
		return fmt.Errorf("clip item %q holds %q, not its media", key, hdr.Name)
	}

	tmp, err := os.CreateTemp(s.dir, clipSyncTmpPattern)
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), tr)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != hdr.Size {
		return fmt.Errorf("clip item %q: media cut short", key)
	}
	if hdr, err = tr.Next(); err != nil || hdr.Name != clipHashEntry || hdr.Size > 128 {
		return fmt.Errorf("clip item %q: no hash of its media", key)
	}
	want, err := io.ReadAll(tr)
	if err != nil {
		return err
	}
	if string(want) != hex.EncodeToString(h.Sum(nil)) {
		return fmt.Errorf("clip item %q: media does not match its hash", key)
	}

	path := filepath.Join(s.dir, c.Filename)
	if _, err := s.store.Get(key); err == nil {
		if err := os.Rename(tmpPath, path); err != nil {
			return err
		}
		return s.store.Update(key, func(cl *clip.Clip) {
			created := cl.CreatedAt
			*cl = c
			cl.CreatedAt = created
		})
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err := s.store.AddWithID(key, &c); err != nil {
		_ = os.Remove(path)
		return err
	}
	slog.Info("clip taken from the peer engine", "clip", key, "name", c.Name, "bytes", n)
	return nil
}

func (s *clipSyncStore) Delete(key string) error {
	c, err := s.store.Get(key)
	if errors.Is(err, clip.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !s.synced(c) {
		// A clip of this engine's own under the same ID is no copy of the
		// peer's to delete.
		return nil
	}
	return s.store.Delete(key)
}
