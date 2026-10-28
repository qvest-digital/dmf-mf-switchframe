// Package createonly writes files into a directory that accepts only new
// files written front to back, such as an object-store mount: no rename, no
// append, no overwrite and no write after an fsync. Data written there any
// other way is refused or, on some stores, silently lost.
package createonly

import (
	"io"
	"os"
)

// Write writes r to path as a new file: created exclusively, written
// sequentially and closed, with no fsync before the last write. An existing
// path is left alone and the error matches fs.ErrExist. A partial file is
// removed on a write error.
func Write(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	// Hide ReadFrom so io.Copy writes plainly instead of trying
	// copy_file_range or sendfile against the mount.
	if _, err := io.Copy(struct{ io.Writer }{f}, r); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// Copy copies the file at src to the new file dst with Write.
func Copy(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return Write(dst, in)
}
