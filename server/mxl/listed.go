package mxl

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// OnlyListed wraps open so that a flow the domain directory does not list
// fails at once, without open being called.
//
// libmxl opens every reader of an instance under one lock, and on a domain
// whose filesystem makes a lookup of a missing name wait for it to appear,
// as mxlfs mounted with wait= does, opening a flow that is not there holds
// that lock until the wait runs out. Readers retrying flows nobody writes
// then hold it nearly all the time, and an open of a flow that is there
// queues behind them past any timeout. A directory listing does not wait,
// and a flow it does not list is one the open would not find either. When
// the domain cannot be listed, open is called as before.
func OnlyListed[T any](domain string, open func(string) (T, error)) func(string) (T, error) {
	return func(flowID string) (T, error) {
		if found, err := listed(domain, flowID); err == nil && !found {
			var zero T
			return zero, fmt.Errorf("mxl: flow %s not found in %s", flowID, domain)
		}
		return open(flowID)
	}
}

// listed reports whether domain has a directory entry for flowID, reading
// the directory rather than looking the name up. libmxl names a flow's
// directory after its id in lower case.
func listed(domain, flowID string) (bool, error) {
	d, err := os.Open(domain)
	if err != nil {
		return false, err
	}
	defer func() { _ = d.Close() }()
	want := flowID + ".mxl-flow"
	for {
		names, err := d.Readdirnames(256)
		for _, n := range names {
			if strings.EqualFold(n, want) {
				return true, nil
			}
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}
