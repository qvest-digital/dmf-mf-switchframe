package clip

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zsiec/switchframe/server/internal/createonly"
)

// A clip library is a directory of clip media shared by every Store that
// attaches it, typically a mount of an object store. Clips found there are
// listed without being copied, and clips stored locally are copied in once
// they are final, so the library outlives the process that added them.
//
// An object-store mount accepts only some file operations: create a new
// file, write it front to back, close it, read and delete. Rename, append,
// overwrite and writing after an fsync are refused or, depending on the
// store, silently lose data. Every write to the library therefore goes
// through package createonly, and nothing renames or reopens a file there.
//
// Next to each media file sits <media>.json with the clip's metadata, so a
// later scan does not have to read the whole media file to probe it.
//
// A copy cut short still becomes a file: when the process dies, the kernel
// closes the descriptor and the store completes the upload with what was
// written. So Publish writes a pending marker before the media and deletes
// it after: <media>.pending.<owner>, owner being the engine label (plain
// <media>.pending without one). Engines sharing a library copy the same
// clip at the same time, as the UI uploads to all of them, so a marker is
// only ever deleted, and the media beside it replaced, by its own engine,
// or once it is older than peerMarkerTimeout. A scan does not list media
// with any marker beside it unless its sidecar shows the copy finished.

const (
	librarySidecarExt = ".json"
	libraryPendingExt = ".pending"

	// How long another engine's marker counts as a copy in progress. The
	// largest upload copies in minutes; an older marker was left by an
	// engine that died with it.
	peerMarkerTimeout = time.Hour

	// How often a Publish that waits for another engine's copy is tried
	// again before PublishLocal at the next start takes over.
	publishRetries = 30
)

// copyIntoLibrary is the one way media enters the library; a variable so a
// test can observe it.
var copyIntoLibrary = createonly.Copy

// ErrPublishPending reports that another engine is copying the clip into
// the library. Nothing was changed there; the clip stays local.
var ErrPublishPending = errors.New("clip: another engine is copying this clip into the library")

// pendingMarker is a marker beside a library media file.
type pendingMarker struct {
	path    string
	owner   string
	modTime time.Time
}

// markerOf splits a library file name into the media it marks and the
// marker's owner, or reports that it is no marker.
func markerOf(name string) (media, owner string, ok bool) {
	i := strings.LastIndex(name, libraryPendingExt)
	if i <= 0 {
		return "", "", false
	}
	rest := name[i+len(libraryPendingExt):]
	switch {
	case rest == "":
		return name[:i], "", true
	case strings.HasPrefix(rest, ".") && validOwner(rest[1:]) && rest != ".":
		return name[:i], rest[1:], true
	}
	return "", "", false
}

func validOwner(owner string) bool {
	for _, r := range owner {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// markerPath is this store's marker for the media at path.
func (s *Store) markerPath(media string) string {
	if s.libOwner == "" {
		return media + libraryPendingExt
	}
	return media + libraryPendingExt + "." + s.libOwner
}

// markersBeside lists the pending markers of the media at path.
func markersBeside(media string) ([]pendingMarker, error) {
	entries, err := os.ReadDir(filepath.Dir(media))
	if err != nil {
		return nil, err
	}
	base := filepath.Base(media)
	var out []pendingMarker
	for _, e := range entries {
		m, owner, ok := markerOf(e.Name())
		if !ok || m != base || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, pendingMarker{path: filepath.Join(filepath.Dir(media), e.Name()), owner: owner, modTime: info.ModTime()})
	}
	return out, nil
}

// classifyMarkers sorts markers into this store's own, other engines'
// still in progress, and other engines' left by an engine that died.
func (s *Store) classifyMarkers(markers []pendingMarker, now time.Time) (own bool, live, dead []pendingMarker) {
	for _, m := range markers {
		switch {
		case m.owner == s.libOwner:
			own = true
		case now.Sub(m.modTime) < peerMarkerTimeout:
			live = append(live, m)
		default:
			dead = append(dead, m)
		}
	}
	return own, live, dead
}

// libraryClipID derives a clip ID from a path relative to the library, so
// that every Store scanning the same library assigns a file the same ID.
// A file the library itself named <id>.<ext> keeps that ID.
func libraryClipID(rel string) string {
	base := filepath.Base(rel)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if filepath.Dir(rel) == "." && ValidID(stem) {
		return stem
	}
	sum := sha256.Sum256([]byte("switchframe-clip-library\x00" + filepath.ToSlash(rel)))
	return hex.EncodeToString(sum[:16])
}

// isLibraryMedia reports whether a library file is media a scan lists:
// a container the clip player demuxes natively.
func isLibraryMedia(name string) bool {
	return supportedExtensions[strings.ToLower(filepath.Ext(name))]
}

// writeLibrarySidecar stores c's metadata next to the media at mediaPath,
// replacing an earlier sidecar by deleting it first: the library cannot
// overwrite a file in place.
func writeLibrarySidecar(mediaPath string, c *Clip) error {
	meta := *c
	meta.ID = ""
	meta.Filename = ""
	meta.LibraryPath = ""
	data, err := json.MarshalIndent(&meta, "", "  ")
	if err != nil {
		return err
	}
	path := mediaPath + librarySidecarExt
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove old sidecar: %w", err)
	}
	return createonly.Write(path, strings.NewReader(string(data)))
}

// readLibrarySidecar returns the metadata stored next to mediaPath, or an
// error when there is none or it does not describe a file of size bytes.
func readLibrarySidecar(mediaPath string, size int64) (*Clip, error) {
	data, err := os.ReadFile(mediaPath + librarySidecarExt)
	if err != nil {
		return nil, err
	}
	var c Clip
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.ByteSize != size {
		return nil, fmt.Errorf("sidecar describes %d bytes, file has %d", c.ByteSize, size)
	}
	return &c, nil
}

// removeLibraryMedia deletes a library clip's media and sidecar. Files
// already gone, for instance removed by another Store sharing the library,
// are not an error.
func removeLibraryMedia(mediaPath string) error {
	for _, p := range []string{mediaPath, mediaPath + librarySidecarExt} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// LibraryScan reports what ScanLibrary changed.
type LibraryScan struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	// Skipped lists library paths with no clip: not playable as stored, or
	// unreadable.
	Skipped []string `json:"skipped"`
}

type libraryCandidate struct {
	id, rel string
	info    fs.FileInfo
	markers []pendingMarker
}

// ScanLibrary lists every playable media file in the attached library as a
// clip, and drops clips whose library file is gone. A file without a valid
// sidecar is probed, and the sidecar written for the next scan. The store is
// not locked while files are probed.
func (s *Store) ScanLibrary() (LibraryScan, error) {
	var res LibraryScan
	if s.libDir == "" {
		return res, nil
	}

	s.mu.RLock()
	known := make(map[string]bool, len(s.clips))
	for id := range s.clips {
		known[id] = true
	}
	s.mu.RUnlock()

	var found []libraryCandidate
	present := map[string]bool{}
	markers := map[string][]pendingMarker{}
	err := filepath.WalkDir(s.libDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") && path != s.libDir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if media, owner, ok := markerOf(d.Name()); ok {
			if info, err := d.Info(); err == nil {
				key := filepath.Join(filepath.Dir(path), media)
				markers[key] = append(markers[key], pendingMarker{path: path, owner: owner, modTime: info.ModTime()})
			}
			return nil
		}
		if !isLibraryMedia(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(s.libDir, path)
		if err != nil {
			return err
		}
		id := libraryClipID(rel)
		present[rel] = true
		if known[id] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			res.Skipped = append(res.Skipped, rel)
			return nil
		}
		found = append(found, libraryCandidate{id: id, rel: rel, info: info})
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("scan clip library: %w", err)
	}

	var add []*Clip
	now := time.Now()
	for _, f := range found {
		f.markers = markers[filepath.Join(s.libDir, f.rel)]
		if len(f.markers) > 0 && !s.finishedDespiteMarkers(f, now) {
			res.Skipped = append(res.Skipped, f.rel)
			continue
		}
		c, err := s.libraryClip(f)
		if err != nil {
			res.Skipped = append(res.Skipped, f.rel)
			continue
		}
		add = append(add, c)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range add {
		if _, exists := s.clips[c.ID]; exists {
			continue
		}
		s.clips[c.ID] = c
		res.Added++
	}
	for id, c := range s.clips {
		if c.LibraryPath != "" && !present[c.LibraryPath] {
			delete(s.clips, id)
			res.Removed++
		}
	}
	if res.Added > 0 || res.Removed > 0 {
		if err := s.save(); err != nil {
			return res, fmt.Errorf("save clips: %w", err)
		}
	}
	return res, nil
}

// finishedDespiteMarkers reports whether media with pending markers beside
// it is complete anyway: Publish writes the sidecar only once the media is,
// so a sidecar of the media's size means the engine died before deleting
// its marker. Markers this store owns, unless its Publish of the clip is
// running, and other engines' dead ones are then deleted, so a complete
// clip does not stay hidden behind them.
func (s *Store) finishedDespiteMarkers(f libraryCandidate, now time.Time) bool {
	path := filepath.Join(s.libDir, f.rel)
	if _, err := readLibrarySidecar(path, f.info.Size()); err != nil {
		return false
	}
	s.mu.RLock()
	publishing := s.publishing[f.id] != nil
	s.mu.RUnlock()
	for _, m := range f.markers {
		mine := m.owner == s.libOwner && !publishing
		if mine || (m.owner != s.libOwner && now.Sub(m.modTime) >= peerMarkerTimeout) {
			if err := os.Remove(m.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				slog.Info("clip: stale pending marker not deleted", "path", m.path, "error", err)
			}
		}
	}
	return true
}

// libraryClip builds the clip for a library file from its sidecar, or by
// probing the file and then writing the sidecar.
func (s *Store) libraryClip(f libraryCandidate) (*Clip, error) {
	path := filepath.Join(s.libDir, f.rel)
	c, err := readLibrarySidecar(path, f.info.Size())
	if err != nil {
		probe, perr := s.probe(path)
		if perr != nil {
			return nil, perr
		}
		base := filepath.Base(f.rel)
		c = &Clip{
			Name:       strings.TrimSuffix(base, filepath.Ext(base)),
			Source:     SourceUpload,
			Codec:      probe.Codec,
			AudioCodec: probe.AudioCodec,
			Width:      probe.Width,
			Height:     probe.Height,
			FPSNum:     probe.FPSNum,
			FPSDen:     probe.FPSDen,
			DurationMs: probe.DurationMs,
			SampleRate: probe.SampleRate,
			Channels:   probe.Channels,
			ByteSize:   f.info.Size(),
			CreatedAt:  f.info.ModTime(),
		}
		// Best effort: a read-only library is still listed, it is only
		// probed again by the next scan.
		if err := writeLibrarySidecar(path, c); err != nil {
			slog.Info("clip: library sidecar not written, probed again on the next scan", "path", f.rel, "error", err)
		}
	}
	c.ID = f.id
	c.Filename = ""
	c.LibraryPath = f.rel
	return c, nil
}

// beginPublish waits for a running Publish of id in this store and marks
// one as running; the returned function ends it.
func (s *Store) beginPublish(id string) func() {
	s.mu.Lock()
	for {
		running := s.publishing[id]
		if running == nil {
			break
		}
		s.mu.Unlock()
		<-running
		s.mu.Lock()
	}
	done := make(chan struct{})
	s.publishing[id] = done
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.publishing, id)
		s.mu.Unlock()
		close(done)
	}
}

// Publish copies a locally stored clip into the attached library and then
// serves it from there, removing the local file. Without a library, or for
// a clip already in it, Publish does nothing.
//
// A library file of the same name, as when another engine sharing the
// library published the same clip ID, is taken as this clip's. It is
// replaced instead only beside this store's own pending marker, left by a
// Publish that did not finish, or beside another engine's dead one. While
// another engine's marker is live Publish touches nothing and returns
// ErrPublishPending. A clip that is not copied stays local and playable.
func (s *Store) Publish(id string) error {
	if s.libDir == "" {
		return nil
	}
	defer s.beginPublish(id)()

	s.mu.RLock()
	c, ok := s.clips[id]
	var cp Clip
	if ok {
		cp = *c
	}
	s.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	if cp.LibraryPath != "" {
		return nil
	}

	src := filepath.Join(s.dir, cp.Filename)
	rel := id + strings.ToLower(filepath.Ext(cp.Filename))
	dst := filepath.Join(s.libDir, rel)
	marker := s.markerPath(dst)

	markers, err := markersBeside(dst)
	if err != nil {
		return fmt.Errorf("list pending markers in library: %w", err)
	}
	unfinished, live, dead := s.classifyMarkers(markers, time.Now())
	if len(live) > 0 {
		return ErrPublishPending
	}
	if !unfinished {
		// Not empty: the store completes the upload of an empty file only
		// later, and the marker has to be there before the media is.
		err := createonly.Write(marker, strings.NewReader(id+"\n"))
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("mark clip pending in library: %w", err)
		}
		unfinished = err != nil
		// Another engine may have marked the clip since the listing.
		if markers, err := markersBeside(dst); err == nil {
			if _, live, _ := s.classifyMarkers(markers, time.Now()); len(live) > 0 {
				if !unfinished {
					_ = os.Remove(marker)
				}
				return ErrPublishPending
			}
		}
	}

	err = copyIntoLibrary(dst, src)
	switch {
	case errors.Is(err, fs.ErrExist) && (unfinished || len(dead) > 0):
		// Left by a Publish that did not finish: what is there may be cut
		// short. The store refuses the delete while that upload is still
		// being completed.
		if err = os.Remove(dst); err == nil {
			err = copyIntoLibrary(dst, src)
		}
	case errors.Is(err, fs.ErrExist):
		err = nil
	}
	if err != nil {
		// The marker stays while anything may be at dst: it is what keeps
		// a cut-short file from being listed.
		if _, serr := os.Stat(dst); errors.Is(serr, fs.ErrNotExist) {
			_ = os.Remove(marker)
		}
		return fmt.Errorf("copy clip into library: %w", err)
	}

	// The media is complete from here on. The sidecar only saves the next
	// scan a probe, so failing to write it costs nothing else.
	if serr := writeLibrarySidecar(dst, &cp); serr != nil {
		slog.Info("clip: library sidecar not written, probed on the next scan", "clip", id, "error", serr)
	}
	for _, m := range append(dead, pendingMarker{path: marker}) {
		if merr := os.Remove(m.path); merr != nil && !errors.Is(merr, fs.ErrNotExist) {
			slog.Warn("clip: pending marker left in library", "path", m.path, "error", merr)
		}
	}

	s.mu.Lock()
	c, ok = s.clips[id]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	backup := *c
	c.Filename = ""
	c.LibraryPath = rel
	if err := s.save(); err != nil {
		*c = backup
		s.mu.Unlock()
		return fmt.Errorf("save clips: %w", err)
	}
	s.mu.Unlock()
	_ = os.Remove(src)
	return nil
}

// PublishSoon publishes id, and while another engine is copying the same
// clip tries again in the background a little later, as that copy ends.
func (s *Store) PublishSoon(id string) error {
	err := s.Publish(id)
	if errors.Is(err, ErrPublishPending) {
		go func() {
			for try := 0; try < publishRetries; try++ {
				time.Sleep(s.retryWait())
				err := s.Publish(id)
				if errors.Is(err, ErrPublishPending) {
					continue
				}
				if err != nil {
					slog.Warn("clip: not copied into the clip library", "clip", id, "error", err)
				}
				return
			}
			slog.Info("clip: another engine still copying it into the clip library; tried again at the next start", "clip", id)
		}()
	}
	return err
}

// defaultRetryWait spreads the retries of engines waiting on each other.
func defaultRetryWait() time.Duration {
	return 5*time.Second + time.Duration(rand.Int64N(int64(10*time.Second)))
}

// PublishLocal publishes every clip still stored only locally, as one whose
// Publish was cut short by a restart. It returns how many it published and
// how many are still local.
func (s *Store) PublishLocal() (published, left int) {
	if s.libDir == "" {
		return 0, 0
	}
	var ids []string
	s.mu.RLock()
	for id, c := range s.clips {
		if c.LibraryPath == "" && c.Filename != "" {
			ids = append(ids, id)
		}
	}
	s.mu.RUnlock()
	for _, id := range ids {
		if err := s.Publish(id); err != nil {
			if !errors.Is(err, ErrPublishPending) {
				slog.Warn("clip: not copied into the clip library", "clip", id, "error", err)
			}
			left++
			continue
		}
		published++
	}
	return published, left
}
