package peer

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The stores an operator writes to (presets, macros, uploaded media and the
// like) converge between the two engines of a pair item by item: each
// engine keeps a ledger of its items, the hash of each one's content and
// when it last changed, and a tombstone for each it deleted. Each engine
// pulls from its peer whatever the peer changed later than it did, and
// deletes what the peer deleted later, so the newer write of an item wins
// on both sides and a delete is never undone by a copy that predates it.
//
// An engine whose ledger does not hold an item has nothing to say about
// it: a freshly started or empty engine lists no items and no tombstones,
// so its peer neither pulls nor deletes anything from it, and it takes all
// of its peer's items.

// SyncItem is one item of a SyncStore.
type SyncItem struct {
	// Key names the item within its store.
	Key string

	// Fingerprint changes whenever the item's content does: a size and
	// modification time, or a hash. Hashing the content is skipped while
	// it stays the same.
	Fingerprint string

	// Hash identifies the item's content, set by a store whose content as
	// Open returns it holds more than the item's identity (a clip's media
	// next to its metadata). Empty: the hash of what Open returns.
	Hash string

	// Size is the size of the item's content as transferred, set by a store
	// that sets Hash; 0 otherwise, the size of what Open returns.
	Size int64

	// ModTime is when the item last changed. It dates a change made while
	// no sync was running. Zero where it is unknown: an item the ledger
	// does not hold yet then loses to any dated write of it on the peer.
	ModTime time.Time

	// Unreadable is set for an item the store holds but cannot read now,
	// as one whose files fail to open. Its ledger entry is kept as it is:
	// only an item a listing positively lacks counts as deleted.
	Unreadable bool
}

// SyncStore is a store whose items converge between the engines.
type SyncStore interface {
	// List returns every item the store holds. An error leaves the
	// store's ledger as it is: no item is taken as deleted.
	List() ([]SyncItem, error)

	// Open returns an item's content.
	Open(key string) (io.ReadCloser, error)

	// Put creates the item, or replaces it, with the content r holds.
	Put(key string, r io.Reader) error

	// Delete removes the item.
	Delete(key string) error
}

// SyncAdmitter is optionally implemented by a SyncStore that can refuse an
// item for want of room before it is transferred: a refused item is not
// read from the peer again until the backoff for it ends.
type SyncAdmitter interface {
	Admit(key string, size int64) error
}

// SyncCanonicalizer is optionally implemented by a SyncStore that holds an
// item in its own form rather than as the bytes it is given, as a store of
// JSON values that decodes them into its type: Canonical returns the hash
// of what the store holds after a Put of data. Two engines whose stores
// hold one version in different forms, as across a change of the type,
// then count it as one version rather than as two later writes.
type SyncCanonicalizer interface {
	Canonical(key string, data []byte) (string, error)
}

// StoreOptions configures a store registered with a StoreSyncer.
type StoreOptions struct {
	// MaxItemBytes bounds an item's content as transferred. A larger item
	// is not pulled.
	MaxItemBytes int64

	// ItemHashes is set for a store whose items carry their own Hash: a
	// transfer is then not checked against it.
	ItemHashes bool
}

// LedgerEntry is what an engine knows of one item.
type LedgerEntry struct {
	Hash    string `json:"hash,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Mod     int64  `json:"mod"` // Unix nanoseconds of the last change
	Deleted bool   `json:"deleted,omitempty"`

	// Alias is the peer's hash of the version this engine took from it,
	// where the store holds it in its own form: both hashes name one
	// version.
	Alias string `json:"alias,omitempty"`

	// FP is the item's fingerprint when it was last hashed; this
	// engine's own, left out of the manifest.
	FP string `json:"fp,omitempty"`
}

// beats reports whether a is a later write of its item than b. Equal times
// are settled the same way on both engines: a delete first, then the
// larger hash.
func (a LedgerEntry) beats(b LedgerEntry) bool {
	if a.Mod != b.Mod {
		return a.Mod > b.Mod
	}
	if a.Deleted != b.Deleted {
		return a.Deleted
	}
	return a.Hash > b.Hash
}

// sameContent reports whether a and b hold the same version of an item.
func (a LedgerEntry) sameContent(b LedgerEntry) bool {
	if a.Deleted || b.Deleted {
		return a.Deleted == b.Deleted
	}
	return a.Hash == b.Hash || (a.Alias != "" && a.Alias == b.Hash) || (b.Alias != "" && b.Alias == a.Hash)
}

// StoreManifest is an engine's ledger as its peer reads it.
type StoreManifest struct {
	Stores map[string]map[string]LedgerEntry `json:"stores"`
}

const (
	syncHashHeader = "X-Sync-Hash"
	syncModHeader  = "X-Sync-Mod"

	// An item that fails to be taken from the peer is tried again after
	// failureBackoff, doubling to maxFailureBackoff while the same version
	// fails; a new version of it on the peer is tried at once.
	failureBackoff    = time.Minute
	maxFailureBackoff = 30 * time.Minute
)

// manifestTimeout bounds reading the peer's manifest, and
// transferStallTimeout an item transfer that moves no byte; variables for
// tests.
var (
	manifestTimeout      = 30 * time.Second
	transferStallTimeout = time.Minute
)

// failedPull is the backoff of an item that failed to be taken.
type failedPull struct {
	hash  string
	mod   int64
	until time.Time
	wait  time.Duration
}

// StoreSyncConfig configures a StoreSyncer.
type StoreSyncConfig struct {
	// LedgerPath is where the ledger persists. Empty keeps it in memory:
	// a restart then takes every item as new, dated by its ModTime.
	LedgerPath string

	// TmpDir holds items while they are transferred. It must be on the
	// stores' filesystem so a store can rename an item into place.
	TmpDir string

	// PeerURL is the peer's API base URL, empty for no peer.
	PeerURL string

	// Token is the bearer token the peer's API takes.
	Token string

	// Client makes the requests to the peer. Nil uses a client without a
	// timeout; every request is bounded by its context.
	Client *http.Client
}

// StoreSyncStats counts what a StoreSyncer did for one store.
type StoreSyncStats struct {
	Store string

	// Pulled and Deleted count items taken from the peer and deleted for
	// it, Bytes the content pulled, Failures the pulls and deletes that
	// failed.
	Pulled, Deleted, Bytes, Failures int64

	// Diverged is how many items differed from the peer's at the last
	// round, whichever engine held the later write.
	Diverged int64
}

type registeredStore struct {
	name string
	s    SyncStore
	opts StoreOptions

	pulled, deleted, bytes, failures, diverged atomic.Int64

	// failed is owned by the round: the items whose pull failed.
	failed map[string]*failedPull

	// scanned is set once the store was first listed after the start.
	scanned bool
}

// StoreSyncer converges the registered stores with the peer's.
type StoreSyncer struct {
	cfg StoreSyncConfig
	now func() time.Time

	// roundMu serializes rounds; mu guards the ledger and every store
	// access that changes it.
	roundMu sync.Mutex
	mu      sync.Mutex
	stores  map[string]*registeredStore
	order   []string
	ledger  map[string]map[string]*LedgerEntry
	dirty   bool

	rounds       atomic.Int64
	roundErrors  atomic.Int64
	lastRoundAt  atomic.Int64 // Unix nanoseconds of the last completed round
	lastRoundErr atomic.Value // string
}

// NewStoreSyncer returns a StoreSyncer, its ledger read from cfg.LedgerPath.
func NewStoreSyncer(cfg StoreSyncConfig) *StoreSyncer {
	if cfg.Client == nil {
		cfg.Client = &http.Client{}
	}
	s := &StoreSyncer{
		cfg:    cfg,
		now:    time.Now,
		stores: map[string]*registeredStore{},
		ledger: map[string]map[string]*LedgerEntry{},
	}
	s.lastRoundErr.Store("")
	if cfg.TmpDir != "" {
		// What a transfer cut short by a restart left behind.
		_ = os.RemoveAll(cfg.TmpDir)
	}
	if cfg.LedgerPath != "" {
		data, err := os.ReadFile(cfg.LedgerPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			slog.Error("peer sync: ledger unreadable; every item counts as new", "path", cfg.LedgerPath, "error", err)
		default:
			if err := json.Unmarshal(data, &s.ledger); err != nil {
				slog.Error("peer sync: ledger unreadable; every item counts as new", "path", cfg.LedgerPath, "error", err)
				s.ledger = map[string]map[string]*LedgerEntry{}
			}
		}
	}
	return s
}

// Register adds a store under name. Call it before the syncer is used.
func (s *StoreSyncer) Register(name string, store SyncStore, opts StoreOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stores[name] = &registeredStore{name: name, s: store, opts: opts, failed: map[string]*failedPull{}}
	s.order = append(s.order, name)
	if s.ledger[name] == nil {
		s.ledger[name] = map[string]*LedgerEntry{}
	}
}

// Stats returns what the syncer did per store, in registration order.
func (s *StoreSyncer) Stats() []StoreSyncStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StoreSyncStats, 0, len(s.order))
	for _, name := range s.order {
		rs := s.stores[name]
		out = append(out, StoreSyncStats{
			Store:    name,
			Pulled:   rs.pulled.Load(),
			Deleted:  rs.deleted.Load(),
			Bytes:    rs.bytes.Load(),
			Failures: rs.failures.Load(),
			Diverged: rs.diverged.Load(),
		})
	}
	return out
}

// Rounds returns how many rounds completed and how many failed, and when
// the last one completed.
func (s *StoreSyncer) Rounds() (ok, failed int64, last time.Time) {
	if n := s.lastRoundAt.Load(); n != 0 {
		last = time.Unix(0, n)
	}
	return s.rounds.Load(), s.roundErrors.Load(), last
}

// Scan brings the ledger up to date with the stores.
func (s *StoreSyncer) Scan() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanLocked()
}

func (s *StoreSyncer) scanLocked() {
	for _, name := range s.order {
		if err := s.scanStoreLocked(s.stores[name]); err != nil {
			slog.Warn("peer sync: store not listed; its ledger stays as it was", "store", name, "error", err)
		}
	}
	s.persistLocked()
}

func (s *StoreSyncer) scanStoreLocked(rs *registeredStore) error {
	items, err := rs.s.List()
	if err != nil {
		return err
	}
	led := s.ledger[rs.name]
	now := s.now().UnixNano()
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		seen[it.Key] = true
		if it.Unreadable {
			slog.Warn("peer sync: item unreadable; its ledger entry stays as it was", "store", rs.name, "key", it.Key)
			continue
		}
		e := led[it.Key]
		if e != nil && !e.Deleted && it.Fingerprint != "" && e.FP == it.Fingerprint {
			continue
		}
		hash, size := it.Hash, it.Size
		if hash == "" {
			hash, size, err = hashContent(rs.s, it.Key)
			if err != nil {
				// Unreadable now is not deleted: the next scan tries again.
				slog.Warn("peer sync: item not hashed", "store", rs.name, "key", it.Key, "error", err)
				continue
			}
		}
		if e != nil && !e.Deleted && e.Hash == hash {
			e.FP, e.Size = it.Fingerprint, size
			s.dirty = true
			continue
		}
		mod := now
		switch {
		case !it.ModTime.IsZero():
			mod = it.ModTime.UnixNano()
		case e == nil:
			// Undated and never seen: older than any dated write, the
			// peer's included; two such versions settle by hash.
			mod = 1
		case !rs.scanned && !e.Deleted && e.Alias != "":
			// The peer's version, taken in this store's own form, found in
			// another form at the first listing after a start: the store's
			// type changed with the engine, as in an upgrade, and encodes
			// the same version anew. No write, so it dates just before the
			// peer's, which this engine takes again in its new form.
			led[it.Key] = &LedgerEntry{Hash: hash, Size: size, Mod: e.Mod - 1, FP: it.Fingerprint}
			s.dirty = true
			slog.Info("peer sync: item held in a new form since the start; the peer's version is taken again",
				"store", rs.name, "key", it.Key)
			continue
		}
		if e != nil && mod <= e.Mod {
			mod = e.Mod + 1
		}
		led[it.Key] = &LedgerEntry{Hash: hash, Size: size, Mod: mod, FP: it.Fingerprint}
		s.dirty = true
	}
	for key, e := range led {
		if e.Deleted || seen[key] {
			continue
		}
		mod := now
		if mod <= e.Mod {
			mod = e.Mod + 1
		}
		led[key] = &LedgerEntry{Mod: mod, Deleted: true}
		s.dirty = true
	}
	rs.scanned = true
	return nil
}

func hashContent(store SyncStore, key string) (string, int64, error) {
	rc, err := store.Open(key)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, rc)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func (s *StoreSyncer) persistLocked() {
	if !s.dirty || s.cfg.LedgerPath == "" {
		return
	}
	data, err := json.Marshal(s.ledger)
	if err != nil {
		slog.Error("peer sync: ledger not saved", "error", err)
		return
	}
	tmp := s.cfg.LedgerPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		slog.Error("peer sync: ledger not saved", "path", s.cfg.LedgerPath, "error", err)
		return
	}
	if err := os.Rename(tmp, s.cfg.LedgerPath); err != nil {
		_ = os.Remove(tmp)
		slog.Error("peer sync: ledger not saved", "path", s.cfg.LedgerPath, "error", err)
		return
	}
	s.dirty = false
}

// Manifest scans the stores and returns the ledger as the peer reads it.
func (s *StoreSyncer) Manifest() StoreManifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanLocked()
	m := StoreManifest{Stores: make(map[string]map[string]LedgerEntry, len(s.ledger))}
	for _, name := range s.order {
		led := s.ledger[name]
		out := make(map[string]LedgerEntry, len(led))
		for key, e := range led {
			c := *e
			c.FP = ""
			out[key] = c
		}
		m.Stores[name] = out
	}
	return m
}

// fromPeer reports whether r carries the API token both engines of a pair
// share, and answers 403 where it does not. The stores hold secrets, the
// operators' tokens among them, that an operator signed in with a token of
// its own is not to read.
func (s *StoreSyncer) fromPeer(w http.ResponseWriter, r *http.Request) bool {
	if s.cfg.Token == "" {
		return true
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.Token)) != 1 {
		http.Error(w, "the stores are served to the peer engine only", http.StatusForbidden)
		return false
	}
	return true
}

// ServeManifest serves GET /api/sync/stores: the engine's manifest, to the
// peer engine only.
func (s *StoreSyncer) ServeManifest(w http.ResponseWriter, r *http.Request) {
	if !s.fromPeer(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.Manifest())
}

// ServeItem serves GET /api/sync/stores/item?store=&key=: one item's
// content, its hash and modification time in the X-Sync-Hash and
// X-Sync-Mod headers, to the peer engine only.
func (s *StoreSyncer) ServeItem(w http.ResponseWriter, r *http.Request) {
	if !s.fromPeer(w, r) {
		return
	}
	name, key := r.URL.Query().Get("store"), r.URL.Query().Get("key")
	s.mu.Lock()
	rs := s.stores[name]
	if rs == nil {
		s.mu.Unlock()
		http.Error(w, "no such store", http.StatusNotFound)
		return
	}
	if err := s.scanStoreLocked(rs); err != nil {
		s.mu.Unlock()
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	s.persistLocked()
	var e LedgerEntry
	held := false
	if l := s.ledger[name][key]; l != nil && !l.Deleted {
		e, held = *l, true
	}
	s.mu.Unlock()
	if !held {
		http.Error(w, "no such item", http.StatusNotFound)
		return
	}
	rc, err := rs.s.Open(key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer func() { _ = rc.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set(syncHashHeader, e.Hash)
	w.Header().Set(syncModHeader, strconv.FormatInt(e.Mod, 10))
	_, _ = io.Copy(w, rc)
}

// errNoStoreSync reports a peer that does not serve store sync, one built
// before it.
var errNoStoreSync = errors.New("the peer does not serve store sync")

// FetchManifest reads the peer's manifest.
func (s *StoreSyncer) FetchManifest(ctx context.Context) (StoreManifest, error) {
	var m StoreManifest
	ctx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()
	resp, err := s.get(ctx, "/api/sync/stores")
	if err != nil {
		return m, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&m); err != nil {
		return m, fmt.Errorf("decode manifest: %w", err)
	}
	return m, nil
}

func (s *StoreSyncer) get(ctx context.Context, path string) (*http.Response, error) {
	if s.cfg.PeerURL == "" {
		return nil, errors.New("no peer configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.PeerURL+path, nil)
	if err != nil {
		return nil, err
	}
	if s.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	}
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp, nil
	case http.StatusNotFound:
		_ = resp.Body.Close()
		return nil, errNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("status %d: the peer does not take this engine's API token, and both engines need the same one", resp.StatusCode)
	default:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
}

var errNotFound = errors.New("not found")

// SyncResult is what one round did.
type SyncResult struct {
	Pulled, Deleted int
	Bytes           int64
	Failed          int

	// Waiting counts the items left for a later round, their backoff
	// after a failure not over yet.
	Waiting int

	// Diverged counts the items that differed from the peer's when the
	// round started.
	Diverged int
}

type syncAction struct {
	rs     *registeredStore
	key    string
	remote LedgerEntry
}

// SyncOnce runs one round: scans the stores, reads the peer's manifest and
// takes from the peer every item it wrote later than this engine did.
func (s *StoreSyncer) SyncOnce(ctx context.Context) (SyncResult, error) {
	s.roundMu.Lock()
	defer s.roundMu.Unlock()

	var res SyncResult
	m, err := s.FetchManifest(ctx)
	if err != nil {
		if errors.Is(err, errNotFound) {
			err = errNoStoreSync
		}
		s.roundErrors.Add(1)
		s.lastRoundErr.Store(err.Error())
		return res, err
	}

	s.mu.Lock()
	s.scanLocked()
	var pulls, deletes []syncAction
	for _, name := range s.order {
		rs := s.stores[name]
		remote, ok := m.Stores[name]
		if !ok {
			// A peer that does not sync this store says nothing about it.
			rs.diverged.Store(0)
			continue
		}
		led := s.ledger[name]
		diverged := 0
		for key, r := range remote {
			l := led[key]
			if l == nil {
				if r.Deleted {
					r.FP = ""
					led[key] = &r
					s.dirty = true
					continue
				}
				diverged++
				pulls = append(pulls, syncAction{rs, key, r})
				continue
			}
			if l.sameContent(r) {
				// Both hold one version; settle on one time for it so
				// the two ledgers end up the same.
				if r.Mod > l.Mod {
					l.Mod = r.Mod
					s.dirty = true
				}
				continue
			}
			diverged++
			if !r.beats(*l) {
				continue
			}
			if r.Deleted {
				deletes = append(deletes, syncAction{rs, key, r})
			} else {
				pulls = append(pulls, syncAction{rs, key, r})
			}
		}
		for key, l := range led {
			if _, ok := remote[key]; !ok && !l.Deleted {
				diverged++
			}
		}
		rs.diverged.Store(int64(diverged))
		res.Diverged += diverged
	}
	for _, d := range deletes {
		if s.deleteLocked(d) {
			res.Deleted++
		} else {
			res.Failed++
		}
	}
	s.persistLocked()
	s.mu.Unlock()

	// Small items first: a round cut short has taken the most items.
	sort.SliceStable(pulls, func(i, j int) bool { return pulls[i].remote.Size < pulls[j].remote.Size })
	for _, p := range pulls {
		if err := ctx.Err(); err != nil {
			break
		}
		if f := p.rs.failed[p.key]; f != nil && f.hash == p.remote.Hash && f.mod == p.remote.Mod && s.now().Before(f.until) {
			res.Waiting++
			continue
		}
		n, err := s.pull(ctx, p)
		switch {
		case errors.Is(err, errSkipped):
			delete(p.rs.failed, p.key)
		case err != nil:
			res.Failed++
			p.rs.failures.Add(1)
			f := p.rs.failed[p.key]
			if f == nil || f.hash != p.remote.Hash || f.mod != p.remote.Mod {
				f = &failedPull{hash: p.remote.Hash, mod: p.remote.Mod}
				p.rs.failed[p.key] = f
			}
			f.wait = min(max(2*f.wait, failureBackoff), maxFailureBackoff)
			f.until = s.now().Add(f.wait)
			slog.Warn("peer sync: item not pulled", "store", p.rs.name, "key", p.key,
				"error", err, "retryIn", f.wait)
		default:
			delete(p.rs.failed, p.key)
			res.Pulled++
			res.Bytes += n
		}
	}

	s.rounds.Add(1)
	s.lastRoundAt.Store(s.now().UnixNano())
	s.lastRoundErr.Store("")
	if res.Pulled > 0 || res.Deleted > 0 || res.Failed > 0 {
		slog.Info("peer sync: stores converged with the peer",
			"pulled", res.Pulled, "deleted", res.Deleted, "bytes", res.Bytes, "failed", res.Failed)
	}
	return res, ctx.Err()
}

// deleteLocked deletes an item the peer deleted later than this engine
// wrote it, and reports whether it is gone.
func (s *StoreSyncer) deleteLocked(d syncAction) bool {
	if err := d.rs.s.Delete(d.key); err != nil && s.holds(d.rs, d.key) {
		d.rs.failures.Add(1)
		slog.Warn("peer sync: item not deleted", "store", d.rs.name, "key", d.key, "error", err)
		return false
	}
	e := d.remote
	e.FP = ""
	s.ledger[d.rs.name][d.key] = &e
	s.dirty = true
	d.rs.deleted.Add(1)
	slog.Info("peer sync: item deleted, as on the peer", "store", d.rs.name, "key", d.key)
	return true
}

// holds reports whether the store lists key.
func (s *StoreSyncer) holds(rs *registeredStore, key string) bool {
	items, err := rs.s.List()
	if err != nil {
		return true
	}
	for _, it := range items {
		if it.Key == key {
			return true
		}
	}
	return false
}

// errSkipped is a pull that found nothing left to do.
var errSkipped = errors.New("skipped")

// pull takes one item from the peer.
func (s *StoreSyncer) pull(ctx context.Context, p syncAction) (int64, error) {
	limit := p.rs.opts.MaxItemBytes
	if limit > 0 && p.remote.Size > limit {
		return 0, fmt.Errorf("%d bytes, more than the %d an item of this store may hold", p.remote.Size, limit)
	}
	if a, ok := p.rs.s.(SyncAdmitter); ok {
		if err := a.Admit(p.key, p.remote.Size); err != nil {
			return 0, fmt.Errorf("no room: %w", err)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, err := s.get(ctx, "/api/sync/stores/item?store="+url.QueryEscape(p.rs.name)+"&key="+url.QueryEscape(p.key))
	if errors.Is(err, errNotFound) {
		// Deleted on the peer since its manifest was read.
		return 0, errSkipped
	}
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	mod, err := strconv.ParseInt(resp.Header.Get(syncModHeader), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad %s header: %w", syncModHeader, err)
	}
	remote := LedgerEntry{Hash: resp.Header.Get(syncHashHeader), Mod: mod}

	if err := os.MkdirAll(s.cfg.TmpDir, 0o700); err != nil {
		return 0, err
	}
	f, err := os.CreateTemp(s.cfg.TmpDir, "item-*")
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	body := io.Reader(newStallReader(resp.Body, transferStallTimeout, cancel))
	if limit > 0 {
		body = io.LimitReader(body, limit+1)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), body)
	if err != nil {
		return 0, fmt.Errorf("transfer: %w", err)
	}
	if limit > 0 && n > limit {
		return 0, fmt.Errorf("more than the %d bytes an item of this store may hold", limit)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if !p.rs.opts.ItemHashes && sum != remote.Hash {
		// Changed on the peer after its ledger was read: the next round
		// takes the new version.
		return 0, fmt.Errorf("content does not match its hash; changed during the transfer")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	// The form the store holds the item in, where that is its own.
	canonical := ""
	if c, ok := p.rs.s.(SyncCanonicalizer); ok {
		data, err := io.ReadAll(f)
		if err == nil {
			canonical, err = c.Canonical(p.key, data)
		}
		if err != nil {
			return 0, fmt.Errorf("store: %w", err)
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
	}

	// The item may have changed here while it was transferred.
	s.mu.Lock()
	err = s.scanStoreLocked(p.rs)
	if l := s.ledger[p.rs.name][p.key]; err == nil && l != nil && (l.sameContent(remote) || !remote.beats(*l)) {
		err = errSkipped
	}
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	// Put runs without the ledger lock: a large item takes a while to
	// store, and the peer reads the manifest meanwhile.
	if err := p.rs.s.Put(p.key, f); err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// The version taken keeps the peer's date, so neither engine takes it
	// for a later write, while the store holds what was transferred, or
	// that in the store's own form: the peer's hash is then kept as an
	// alias of this engine's. Held as anything else, as by a write here
	// right after it was stored, it is a change of this engine's own,
	// dated now, and the peer takes it in turn.
	it, ok, err := s.listedItem(p.rs, p.key)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	if !ok {
		return 0, errors.New("store: the item taken is not listed")
	}
	hash, size := it.Hash, it.Size
	if hash == "" {
		if hash, size, err = hashContent(p.rs.s, p.key); err != nil {
			return 0, fmt.Errorf("store: %w", err)
		}
	}
	stored := sum
	if p.rs.opts.ItemHashes {
		stored = remote.Hash
	}
	e := &LedgerEntry{Hash: hash, Size: size, Mod: remote.Mod, FP: it.Fingerprint}
	switch {
	case hash == stored:
	case canonical != "" && hash == canonical:
		e.Alias = remote.Hash
	default:
		e.Mod = max(s.now().UnixNano(), remote.Mod+1)
		slog.Info("peer sync: item taken from the peer holds other content here; it counts as changed here",
			"store", p.rs.name, "key", p.key)
	}
	s.ledger[p.rs.name][p.key] = e
	s.dirty = true
	s.persistLocked()
	p.rs.pulled.Add(1)
	p.rs.bytes.Add(n)
	slog.Info("peer sync: item pulled from the peer", "store", p.rs.name, "key", p.key, "bytes", n)
	return n, nil
}

// listedItem returns key as the store lists it.
func (s *StoreSyncer) listedItem(rs *registeredStore, key string) (SyncItem, bool, error) {
	items, err := rs.s.List()
	if err != nil {
		return SyncItem{}, false, err
	}
	for _, it := range items {
		if it.Key == key && !it.Unreadable {
			return it, true, nil
		}
	}
	return SyncItem{}, false, nil
}

// stallReader cancels a transfer that moves no byte for timeout.
type stallReader struct {
	r     io.Reader
	timer *time.Timer
	d     time.Duration
}

func newStallReader(r io.Reader, timeout time.Duration, cancel context.CancelFunc) *stallReader {
	return &stallReader{r: r, d: timeout, timer: time.AfterFunc(timeout, cancel)}
}

func (s *stallReader) Read(b []byte) (int, error) {
	n, err := s.r.Read(b)
	if n > 0 {
		s.timer.Reset(s.d)
	}
	if err != nil {
		s.timer.Stop()
	}
	return n, err
}

// Run syncs with the peer every interval until ctx ends. A failed round is
// logged once until a round succeeds again.
func (s *StoreSyncer) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	failing := ""
	for {
		_, err := s.SyncOnce(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil && err.Error() != failing:
			failing = err.Error()
			slog.Warn("peer sync: stores not synced with the peer", "peerURL", s.cfg.PeerURL, "error", err)
		case err == nil && failing != "":
			failing = ""
			slog.Info("peer sync: stores sync with the peer again", "peerURL", s.cfg.PeerURL)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// TmpDir is where a store may stage an item it puts. It is emptied when
// the syncer starts.
func (s *StoreSyncer) TmpDir() string {
	return filepath.Clean(s.cfg.TmpDir)
}
