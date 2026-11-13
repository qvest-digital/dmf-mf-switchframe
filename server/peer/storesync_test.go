package peer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

// memStore is a SyncStore in memory.
type memStore struct {
	mu    sync.Mutex
	items map[string][]byte
	ver   map[string]int
	mod   map[string]time.Time
	puts  int

	// normalize, when set, is what Put stores of what it is given.
	normalize func([]byte) []byte
	// unreadable lists items held but failing to read.
	unreadable map[string]bool
	// refuse, when set, refuses every item for want of room.
	refuse error
	admits int
	opens  map[string]int
	// afterPut, when set, runs at the first listing after a put, as a
	// write here landing right after the store took an item.
	afterPut func(m *memStore)
	putDone  bool
	// itemHashes lists items with a hash of their own, as clips carry, not
	// that of what Open returns.
	itemHashes bool
	// undated lists no modification times, as a store of JSON values.
	undated bool
}

// Canonical hashes what Put would hold of data.
func (m *memStore) Canonical(_ string, data []byte) (string, error) {
	m.mu.Lock()
	normalize := m.normalize
	m.mu.Unlock()
	if normalize != nil {
		data = normalize(data)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (m *memStore) Admit(string, int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.admits++
	return m.refuse
}

func newMemStore() *memStore {
	return &memStore{items: map[string][]byte{}, ver: map[string]int{}, mod: map[string]time.Time{}}
}

func (m *memStore) set(key, value string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = []byte(value)
	m.ver[key]++
	m.mod[key] = at
}

func (m *memStore) remove(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
}

func (m *memStore) get(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.items[key]
	return string(v), ok
}

func (m *memStore) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.items {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m *memStore) putCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.puts
}

func (m *memStore) List() ([]SyncItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putDone && m.afterPut != nil {
		m.putDone = false
		hook := m.afterPut
		m.afterPut = nil
		hook(m)
	}
	var out []SyncItem
	for k := range m.items {
		it := SyncItem{Key: k, Fingerprint: fmt.Sprint(m.ver[k]), ModTime: m.mod[k], Unreadable: m.unreadable[k]}
		if m.undated {
			it.ModTime = time.Time{}
		}
		if m.itemHashes {
			sum := sha256.Sum256(append([]byte("meta:"), m.items[k]...))
			it.Hash, it.Size = hex.EncodeToString(sum[:]), int64(len(m.items[k]))
		}
		out = append(out, it)
	}
	return out, nil
}

func (m *memStore) Open(key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.opens == nil {
		m.opens = map[string]int{}
	}
	m.opens[key]++
	v, ok := m.items[key]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), v...))), nil
}

func (m *memStore) Put(key string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.normalize != nil {
		data = m.normalize(data)
	}
	m.items[key] = data
	m.ver[key]++
	m.mod[key] = time.Now()
	m.puts++
	m.putDone = true
	return nil
}

func (m *memStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	return nil
}

// syncEngine is one engine of a pair as the store sync sees it: its store,
// its ledger on disk and its API.
type syncEngine struct {
	t      *testing.T
	dir    string
	store  *memStore
	syncer *StoreSyncer
	srv    *httptest.Server
	peer   *syncEngine
	clock  *time.Time
}

func newSyncEngine(t *testing.T, clock *time.Time) *syncEngine {
	e := &syncEngine{t: t, dir: t.TempDir(), store: newMemStore(), clock: clock}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/sync/stores":
			e.syncer.ServeManifest(w, r)
		case "/api/sync/stores/item":
			e.syncer.ServeItem(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(e.srv.Close)
	return e
}

// start starts the engine's syncer on its ledger, as a process start does.
func (e *syncEngine) start(maxItem int64) {
	e.startWith(StoreOptions{MaxItemBytes: maxItem})
}

func (e *syncEngine) startWith(opts StoreOptions) {
	e.syncer = NewStoreSyncer(StoreSyncConfig{
		LedgerPath: filepath.Join(e.dir, "ledger.json"),
		TmpDir:     filepath.Join(e.dir, "tmp"),
		PeerURL:    e.peer.srv.URL,
		Token:      "secret",
	})
	e.syncer.now = func() time.Time { return *e.clock }
	e.syncer.Register("things", e.store, opts)
}

func (e *syncEngine) sync() SyncResult {
	e.t.Helper()
	res, err := e.syncer.SyncOnce(context.Background())
	if err != nil {
		e.t.Fatalf("SyncOnce: %v", err)
	}
	return res
}

func newSyncPair(t *testing.T) (a, b *syncEngine, clock *time.Time) {
	now := time.Unix(1_700_000_000, 0)
	clock = &now
	a, b = newSyncEngine(t, clock), newSyncEngine(t, clock)
	a.peer, b.peer = b, a
	a.start(1 << 20)
	b.start(1 << 20)
	return a, b, clock
}

func (c *syncEngine) tick(clock *time.Time, d time.Duration) { *clock = clock.Add(d) }

func assertItems(t *testing.T, e *syncEngine, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	for _, k := range e.store.keys() {
		v, _ := e.store.get(k)
		got[k] = v
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("store holds %v, want %v", got, want)
	}
}

// An item written while the peer was down reaches it once it is back, and
// what it held from before is not copied over the newer write.
func TestStoreSyncWriteWhilePeerDownConverges(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("intro", "v1", *clock)
	b.sync()
	a.sync()
	assertItems(t, b, map[string]string{"intro": "v1"})

	// b goes down; a takes a new item and a change to an old one.
	b.srv.Config.Handler = http.NotFoundHandler()
	b.tick(clock, time.Minute)
	a.store.set("outro", "v1", *clock)
	a.store.set("intro", "v2", *clock)
	a.syncer.Scan()

	// b comes back on its ledger and takes both before it serves.
	b.tick(clock, time.Minute)
	b.start(1 << 20)
	b.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sync/stores":
			b.syncer.ServeManifest(w, r)
		case "/api/sync/stores/item":
			b.syncer.ServeItem(w, r)
		}
	})
	if res := b.sync(); res.Pulled != 2 {
		t.Fatalf("restarted engine pulled %d items, want 2", res.Pulled)
	}
	assertItems(t, b, map[string]string{"intro": "v2", "outro": "v1"})

	// a takes nothing back from b.
	puts := a.store.putCount()
	a.sync()
	assertItems(t, a, map[string]string{"intro": "v2", "outro": "v1"})
	if a.store.putCount() != puts {
		t.Fatalf("engine that kept running took %d items back", a.store.putCount()-puts)
	}
}

// A delete reaches the peer, also one that was down when it happened, and
// the peer's older copy does not bring the item back.
func TestStoreSyncDeletePropagates(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("lower-third", "v1", *clock)
	a.store.set("bug", "v1", *clock)
	b.sync()
	a.sync()

	b.tick(clock, time.Minute)
	a.store.remove("lower-third")
	a.syncer.Scan()

	// b restarts on its ledger, which still holds the item.
	b.tick(clock, time.Minute)
	b.start(1 << 20)
	a.sync()
	assertItems(t, a, map[string]string{"bug": "v1"})
	if res := b.sync(); res.Deleted != 1 {
		t.Fatalf("peer deleted %d items, want 1", res.Deleted)
	}
	assertItems(t, b, map[string]string{"bug": "v1"})
	a.sync()
	b.sync()
	assertItems(t, a, map[string]string{"bug": "v1"})
	assertItems(t, b, map[string]string{"bug": "v1"})
}

// A freshly started, empty engine takes its peer's items and deletes none
// of them.
func TestStoreSyncEmptyEngineNeverWipesPeer(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("one", "1", *clock)
	a.store.set("two", "2", *clock)
	a.syncer.Scan()

	b.tick(clock, time.Hour)
	if res := a.sync(); res.Deleted != 0 || res.Pulled != 0 {
		t.Fatalf("engine took %+v from an empty peer", res)
	}
	assertItems(t, a, map[string]string{"one": "1", "two": "2"})
	if res := b.sync(); res.Pulled != 2 {
		t.Fatalf("empty engine pulled %d items, want 2", res.Pulled)
	}
	assertItems(t, b, map[string]string{"one": "1", "two": "2"})
}

// The later of two writes of an item wins on both engines.
func TestStoreSyncLaterWriteWins(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("show", "base", *clock)
	b.sync()

	b.tick(clock, time.Second)
	a.store.set("show", "from-a", *clock)
	a.syncer.Scan()
	b.tick(clock, time.Second)
	b.store.set("show", "from-b", *clock)
	b.syncer.Scan()

	a.sync()
	b.sync()
	assertItems(t, a, map[string]string{"show": "from-b"})
	assertItems(t, b, map[string]string{"show": "from-b"})
}

// Once both hold the same items, a round transfers nothing.
func TestStoreSyncNoRepeatedPullWhenEqual(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("x", "1", *clock)
	b.store.set("y", "2", *clock)
	a.sync()
	b.sync()
	putsA, putsB := a.store.putCount(), b.store.putCount()
	for i := 0; i < 3; i++ {
		b.tick(clock, time.Minute)
		if res := a.sync(); res.Pulled+res.Deleted+res.Diverged != 0 {
			t.Fatalf("round %d on a: %+v", i, res)
		}
		if res := b.sync(); res.Pulled+res.Deleted+res.Diverged != 0 {
			t.Fatalf("round %d on b: %+v", i, res)
		}
	}
	if a.store.putCount() != putsA || b.store.putCount() != putsB {
		t.Fatal("rounds over equal stores put items")
	}
}

// An item beyond the store's bound is not pulled.
func TestStoreSyncSkipsOversizedItem(t *testing.T) {
	a, b, clock := newSyncPair(t)
	b.start(4)
	a.store.set("big", "0123456789", *clock)
	res := b.sync()
	if res.Pulled != 0 || res.Failed != 1 {
		t.Fatalf("round: %+v, want one failed pull", res)
	}
	if _, ok := b.store.get("big"); ok {
		t.Fatal("oversized item was stored")
	}
}

// A peer built before store sync is reported, not taken as empty.
func TestStoreSyncPeerWithoutStoreSync(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("x", "1", *clock)
	b.srv.Config.Handler = http.NotFoundHandler()
	if _, err := a.syncer.SyncOnce(context.Background()); err != errNoStoreSync {
		t.Fatalf("SyncOnce against an old peer: %v, want errNoStoreSync", err)
	}
	assertItems(t, a, map[string]string{"x": "1"})
}

// A tombstone older than a write made here does not delete it.
func TestStoreSyncOlderTombstoneKeepsNewerWrite(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("bug", "v1", *clock)
	b.sync()
	a.tick(clock, time.Minute)
	a.store.remove("bug")
	a.syncer.Scan()
	a.tick(clock, time.Minute)
	b.store.set("bug", "v2", *clock)
	b.syncer.Scan()

	if res := b.sync(); res.Deleted != 0 {
		t.Fatal("an older delete removed a newer write")
	}
	a.sync()
	assertItems(t, a, map[string]string{"bug": "v2"})
	assertItems(t, b, map[string]string{"bug": "v2"})
}

// tamper serves e's items with other content than its manifest names.
func (e *syncEngine) tamper(body string) {
	e.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sync/stores":
			e.syncer.ServeManifest(w, r)
		case "/api/sync/stores/item":
			rec := httptest.NewRecorder()
			e.syncer.ServeItem(rec, r)
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			_, _ = w.Write([]byte(body))
		}
	})
}

// Content that does not match the hash the peer's manifest names is not
// stored.
func TestStoreSyncRefusesContentNotMatchingItsHash(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("x", "real", *clock)
	a.tamper("forged")
	res := b.sync()
	if res.Pulled != 0 || res.Failed != 1 {
		t.Fatalf("round %+v, want one failed pull", res)
	}
	assertItems(t, b, map[string]string{})
}

// An item the manifest names larger than the store's bound is not read at
// all; one that streams larger than it is not stored.
func TestStoreSyncSizeBounds(t *testing.T) {
	t.Run("named in the manifest", func(t *testing.T) {
		a, b, clock := newSyncPair(t)
		b.start(4)
		a.store.set("big", "0123456789", *clock)
		reads := 0
		inner := a.srv.Config.Handler
		a.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/sync/stores/item" {
				reads++
			}
			inner.ServeHTTP(w, r)
		})
		if res := b.sync(); res.Failed != 1 || res.Pulled != 0 {
			t.Fatalf("round %+v", res)
		}
		if reads != 0 {
			t.Fatal("an item the manifest names too large was read")
		}
	})
	t.Run("streamed", func(t *testing.T) {
		a, b, clock := newSyncPair(t)
		// Items carrying their own hash, as clips: the bound alone stops
		// the transfer.
		b.startWith(StoreOptions{MaxItemBytes: 4, ItemHashes: true})
		a.store.set("small", "abc", *clock)
		a.tamper("0123456789")
		if res := b.sync(); res.Failed != 1 || res.Pulled != 0 {
			t.Fatalf("round %+v", res)
		}
		assertItems(t, b, map[string]string{})
	})
}

// A write dated before the ledger's last date of the item, as a file
// restored with its old modification time, is still a later write.
func TestStoreSyncRewriteDatedEarlierIsNewer(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("x", "v1", *clock)
	b.sync()
	a.sync()
	a.tick(clock, time.Hour)
	a.store.set("x", "v2", clock.Add(-24*time.Hour))
	a.syncer.Scan()
	b.sync()
	assertItems(t, b, map[string]string{"x": "v2"})
}

// The peer's tombstone of an item this engine never held is kept, and the
// two engines' ledgers settle on one date for a version both hold.
func TestStoreSyncLedgersConverge(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("gone", "v1", *clock)
	a.syncer.Scan()
	a.tick(clock, time.Minute)
	a.store.remove("gone")
	a.syncer.Scan()
	a.store.set("same", "v", *clock)
	a.tick(clock, time.Minute)
	b.store.set("same", "v", *clock)
	a.sync()
	b.sync()
	ma, mb := a.syncer.Manifest().Stores["things"], b.syncer.Manifest().Stores["things"]
	if !mb["gone"].Deleted {
		t.Fatal("the peer's tombstone was not kept")
	}
	if ma["same"].Mod != mb["same"].Mod {
		t.Fatalf("one version dated %d and %d", ma["same"].Mod, mb["same"].Mod)
	}
}

// An item that fails to be taken is not read again until its backoff ends,
// and is read at once when the peer holds a new version of it.
func TestStoreSyncFailureBackoff(t *testing.T) {
	a, b, clock := newSyncPair(t)
	b.store.refuse = fmt.Errorf("full")
	a.store.set("clip", "v1", *clock)
	b.sync()
	if b.store.admits != 1 {
		t.Fatalf("admits %d", b.store.admits)
	}
	b.tick(clock, 30*time.Second)
	if res := b.sync(); res.Waiting != 1 || res.Failed != 0 {
		t.Fatalf("round within the backoff: %+v, want the item waiting", res)
	}
	if b.store.admits != 1 {
		t.Fatal("a refused item was tried again within its backoff")
	}
	b.tick(clock, 31*time.Second)
	b.sync()
	if b.store.admits != 2 {
		t.Fatal("a refused item was not tried again after its backoff")
	}
	b.tick(clock, 90*time.Second)
	b.sync()
	if b.store.admits != 2 {
		t.Fatal("the backoff did not double")
	}
	a.store.set("clip", "v2", *clock)
	a.syncer.Scan()
	b.store.refuse = nil
	b.sync()
	assertItems(t, b, map[string]string{"clip": "v2"})
}

// A store that holds what it takes in its own form, as one that rewrites
// it, counts that as the peer's version: neither engine takes it from the
// other again.
func TestStoreSyncNormalizingStore(t *testing.T) {
	a, b, clock := newSyncPair(t)
	b.store.normalize = bytes.ToUpper
	a.store.set("x", "v", *clock)
	b.sync()
	for i := 0; i < 3; i++ {
		b.tick(clock, time.Minute)
		a.sync()
		b.sync()
	}
	if a.store.putCount() != 0 || b.store.putCount() != 1 {
		t.Fatalf("puts: a %d, b %d, want 0 and 1", a.store.putCount(), b.store.putCount())
	}
	assertItems(t, a, map[string]string{"x": "v"})
	assertItems(t, b, map[string]string{"x": "V"})
}

// Engines whose stores hold one item in different forms, as across a
// change of its type in a rolling upgrade where the newer one adds a field
// the older one drops, settle within one exchange each way, and the newer
// engine keeps its field.
func TestStoreSyncMixedVersionStores(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.normalize = func(d []byte) []byte { // newer: adds the field
		if bytes.Contains(d, []byte("|f=")) {
			return d
		}
		return append(append([]byte(nil), d...), "|f="...)
	}
	b.store.normalize = func(d []byte) []byte { // older: drops it
		if i := bytes.Index(d, []byte("|f=")); i >= 0 {
			return append([]byte(nil), d[:i]...)
		}
		return d
	}
	a.store.set("on-a", "x|f=7", *clock)
	b.store.set("on-b", "y", *clock)
	for i := 0; i < 10; i++ {
		b.tick(clock, 5*time.Second)
		a.sync()
		b.sync()
	}
	if a.store.putCount() > 1 || b.store.putCount() > 1 {
		t.Fatalf("puts over 10 rounds: a %d, b %d, want at most 1 each", a.store.putCount(), b.store.putCount())
	}
	assertItems(t, a, map[string]string{"on-a": "x|f=7", "on-b": "y|f="})
	assertItems(t, b, map[string]string{"on-a": "x", "on-b": "y"})
}

// A write here that lands right after the store took an item from the
// peer is not taken for the peer's version: it counts as this engine's
// own later write, and the peer takes it.
func TestStoreSyncWriteRightAfterPullIsKept(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("show", "from-a", *clock)
	b.store.afterPut = func(m *memStore) {
		m.items["show"] = []byte("operator")
		m.ver["show"]++
	}
	b.sync()
	a.sync()
	assertItems(t, a, map[string]string{"show": "operator"})
	assertItems(t, b, map[string]string{"show": "operator"})
}

// An item an engine holds but cannot read is not taken for deleted, so its
// peer keeps its copy.
func TestStoreSyncUnreadableItemIsNotDeleted(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("x", "v", *clock)
	b.sync()
	a.sync()
	b.store.mu.Lock()
	b.store.unreadable = map[string]bool{"x": true}
	b.store.ver["x"]++
	b.store.opens = nil
	b.store.mu.Unlock()
	b.tick(clock, time.Minute)
	b.syncer.Scan()
	if b.store.opens["x"] != 0 {
		t.Fatal("an unreadable item was read")
	}
	if res := a.sync(); res.Deleted != 0 {
		t.Fatal("the peer's unreadable item was taken for deleted")
	}
	assertItems(t, a, map[string]string{"x": "v"})
}

// An undated item an engine first sees, as one held before it kept a
// ledger, loses to the peer's dated version of it.
func TestStoreSyncUndatedItemLosesToDatedOne(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("show", "kept", *clock)
	a.syncer.Scan()
	b.tick(clock, time.Hour)
	b.store.set("show", "stale", time.Time{})
	b.sync()
	a.sync()
	assertItems(t, a, map[string]string{"show": "kept"})
	assertItems(t, b, map[string]string{"show": "kept"})
}

// A transfer is cut once it moves no byte for the stall timeout, and not
// while bytes keep arriving, however long it takes in all.
func TestStallReader(t *testing.T) {
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newStallReader(pr, 80*time.Millisecond, cancel)
	done := make(chan []byte)
	go func() {
		data, _ := io.ReadAll(r)
		done <- data
	}()
	for i := 0; i < 6; i++ {
		time.Sleep(40 * time.Millisecond)
		if _, err := pw.Write([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if ctx.Err() != nil {
		t.Fatal("a transfer still moving bytes was cut")
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a stalled transfer was not cut")
	}
	_ = pw.Close()
	if got := <-done; len(got) != 6 {
		t.Fatalf("read %d bytes", len(got))
	}
}

// A transfer that never moves a byte is cut too.
func TestStallReaderNoByte(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newStallReader(pr, 50*time.Millisecond, cancel)
	go func() { _, _ = io.ReadAll(r) }()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a transfer that never moved a byte was not cut")
	}
}

// A peer that does not answer its manifest is given up on after the
// manifest timeout, not waited on for good.
func TestStoreSyncManifestDeadline(t *testing.T) {
	a, b, _ := newSyncPair(t)
	release := make(chan struct{})
	defer close(release)
	a.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer func(d time.Duration) { manifestTimeout = d }(manifestTimeout)
	manifestTimeout = 100 * time.Millisecond
	done := make(chan error, 1)
	go func() {
		_, err := b.syncer.SyncOnce(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a round against a peer that does not answer succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the round waits on a peer that does not answer its manifest")
	}
}

// An item taken from a store whose items carry their own hash, as clips
// do, keeps the peer's date: it is the peer's version, not a later write.
func TestStoreSyncItemHashesKeepThePeersDate(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.itemHashes, b.store.itemHashes = true, true
	a.startWith(StoreOptions{MaxItemBytes: 1 << 20, ItemHashes: true})
	b.startWith(StoreOptions{MaxItemBytes: 1 << 20, ItemHashes: true})
	a.store.set("clip", "media", *clock)
	a.syncer.Scan()
	b.tick(clock, time.Hour)
	if res := b.sync(); res.Pulled != 1 {
		t.Fatalf("round %+v", res)
	}
	ma, mb := a.syncer.Manifest().Stores["things"]["clip"], b.syncer.Manifest().Stores["things"]["clip"]
	if mb.Mod != ma.Mod || mb.Hash != ma.Hash {
		t.Fatalf("taken as %+v, the peer holds %+v", mb, ma)
	}
}

// A pull whose transfer stops moving is cut and counted as failed rather
// than holding up the round.
func TestStoreSyncStalledTransferIsCut(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.set("x", "content", *clock)
	release := make(chan struct{})
	defer close(release)
	a.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/stores" {
			a.syncer.ServeManifest(w, r)
			return
		}
		rec := httptest.NewRecorder()
		a.syncer.ServeItem(rec, r)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		_, _ = w.Write(rec.Body.Bytes()[:3])
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer func(d time.Duration) { transferStallTimeout = d }(transferStallTimeout)
	transferStallTimeout = 100 * time.Millisecond
	done := make(chan SyncResult, 1)
	go func() {
		res, _ := b.syncer.SyncOnce(context.Background())
		done <- res
	}()
	select {
	case res := <-done:
		if res.Failed != 1 || res.Pulled != 0 {
			t.Fatalf("round %+v, want one failed pull", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a stalled transfer held up the round")
	}
}

// addsField and dropsField are a store type with a field and the same type
// before it had it.
func addsField(d []byte) []byte {
	if bytes.Contains(d, []byte("|f=")) {
		return d
	}
	return append(append([]byte(nil), d...), "|f="...)
}

func dropsField(d []byte) []byte {
	if i := bytes.Index(d, []byte("|f=")); i >= 0 {
		return append([]byte(nil), d[:i]...)
	}
	return d
}

// An engine upgraded after its peer holds the peer's version in its new
// form; finding it so at its first listing is no write of its own. It
// takes the peer's version again, the field's value with it, and the peer
// keeps it. A change it finds later is a write.
func TestStoreSyncSecondEngineUpgradeKeepsField(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.normalize, b.store.normalize = addsField, dropsField
	a.store.undated, b.store.undated = true, true // as JSON stores
	a.store.set("show", "x|f=7", time.Time{})
	a.syncer.Scan()
	b.sync()
	a.sync()
	assertItems(t, b, map[string]string{"show": "x"})

	// b is upgraded: its store now encodes the field, at its zero value.
	b.tick(clock, time.Minute)
	b.store.normalize = addsField
	b.store.mu.Lock()
	b.store.items["show"] = []byte("x|f=")
	b.store.ver["show"]++
	b.store.mod["show"] = time.Time{}
	b.store.mu.Unlock()
	b.start(1 << 20)
	b.sync()
	a.sync()
	assertItems(t, a, map[string]string{"show": "x|f=7"})
	assertItems(t, b, map[string]string{"show": "x|f=7"})

	// A write on b after its start is b's own, and reaches a.
	b.tick(clock, time.Minute)
	b.store.mu.Lock()
	b.store.items["show"] = []byte("y|f=8")
	b.store.ver["show"]++
	b.store.mu.Unlock()
	b.syncer.Scan()
	a.sync()
	assertItems(t, a, map[string]string{"show": "y|f=8"})
}

// After the first listing since the start, an item held in another form
// than the peer's version it took is a write here, though the store does
// not date it.
func TestStoreSyncWriteAfterStartOnAliasedItem(t *testing.T) {
	a, b, clock := newSyncPair(t)
	a.store.normalize, b.store.normalize = addsField, dropsField
	a.store.undated, b.store.undated = true, true // as JSON stores
	a.store.set("show", "x|f=7", time.Time{})
	a.syncer.Scan()
	b.sync()
	b.tick(clock, time.Minute)
	b.start(1 << 20) // a restart without an upgrade
	b.syncer.Scan()

	b.tick(clock, time.Minute)
	b.store.mu.Lock()
	b.store.items["show"] = []byte("z")
	b.store.ver["show"]++
	b.store.mu.Unlock()
	b.syncer.Scan()
	a.sync()
	b.sync()
	assertItems(t, b, map[string]string{"show": "z"})
	assertItems(t, a, map[string]string{"show": "z|f="})
}
