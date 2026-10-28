package clip

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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
// written. So Publish writes <media>.pending before the media and deletes it
// after; a scan does not list media with a marker beside it, and the next
// Publish of that clip replaces the media.

const (
	librarySidecarExt = ".json"
	libraryPendingExt = ".pending"
)

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
		if d.IsDir() || !isLibraryMedia(d.Name()) {
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
	for _, f := range found {
		if _, err := os.Stat(filepath.Join(s.libDir, f.rel) + libraryPendingExt); err == nil {
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

// Publish copies a locally stored clip into the attached library and then
// serves it from there, removing the local file. A library file of the same
// name already present, as when another Store sharing the library published
// the same clip ID, is taken as this clip's, unless a pending marker from an
// earlier Publish says it is incomplete. Without a library, or for a clip
// already in it, Publish does nothing. A clip that cannot be copied stays
// local and playable here.
func (s *Store) Publish(id string) error {
	if s.libDir == "" {
		return nil
	}
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
	marker := dst + libraryPendingExt

	// Not empty: the store completes the upload of an empty file only
	// later, and the marker has to be there before the media is.
	err := createonly.Write(marker, strings.NewReader(id+"\n"))
	unfinished := errors.Is(err, fs.ErrExist)
	if err != nil && !unfinished {
		return fmt.Errorf("mark clip pending in library: %w", err)
	}

	err = createonly.Copy(dst, src)
	switch {
	case errors.Is(err, fs.ErrExist) && unfinished:
		// Left by a Publish that did not finish, this one's or the other
		// engine's: what is there may be cut short. The store refuses the
		// delete while that upload is still being completed.
		if err = os.Remove(dst); err == nil {
			err = createonly.Copy(dst, src)
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
	if merr := os.Remove(marker); merr != nil && !errors.Is(merr, fs.ErrNotExist) {
		slog.Warn("clip: pending marker left in library; the clip is not listed by later scans until it is published again", "clip", id, "error", merr)
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
			slog.Warn("clip: not copied into the clip library", "clip", id, "error", err)
			left++
			continue
		}
		published++
	}
	return published, left
}
