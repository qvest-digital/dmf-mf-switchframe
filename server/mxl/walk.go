package mxl

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// mxlfsMagic is the f_type statfs reports for an mxlfs.
const mxlfsMagic = 0x6d786c66

// WalkFirst wraps open so that, on an mxlfs domain, the flow's directory is
// looked up before open is called, and open is not called for a flow the
// lookup does not find. On any other domain it returns open.
//
// libmxl opens every reader of an instance under one lock and holds it
// across the lookup of the flow. On an mxlfs mounted with wait=, as mxl-k8s
// mounts it, a lookup of a flow that is not there waits until the flow
// appears or the wait runs out, and reports the flow as wanted to the node
// agent, which brings it here: a mirror, or the output of a redundancy set,
// which nothing lists until then. Made inside libmxl, that wait holds the
// lock, so inputs retrying flows nobody writes keep an open of a live flow
// queued behind them past any timeout. Made here first, it blocks only the
// caller, the flow is reported wanted all the same, and the open that
// follows finds it.
//
// On another domain a lookup fails at once, and what brings a missing flow
// there acts on libmxl's own open, so the open is left to libmxl.
func WalkFirst[T any](domain string, open func(string) (T, error)) func(string) (T, error) {
	var st unix.Statfs_t
	if unix.Statfs(domain, &st) != nil || st.Type != mxlfsMagic {
		return open
	}
	return walkFirst(domain, func(dir string) error {
		_, err := os.Stat(dir)
		return err
	}, open)
}

// walkFirst calls open for a flow whose directory lookup finds it.
func walkFirst[T any](domain string, lookup func(dir string) error, open func(string) (T, error)) func(string) (T, error) {
	return func(flowID string) (T, error) {
		// libmxl names a flow's directory after its id in lower case.
		dir := filepath.Join(domain, strings.ToLower(flowID)+".mxl-flow")
		if err := lookup(dir); errors.Is(err, fs.ErrNotExist) {
			var zero T
			return zero, fmt.Errorf("mxl: flow %s: %w", flowID, err)
		}
		return open(flowID)
	}
}
