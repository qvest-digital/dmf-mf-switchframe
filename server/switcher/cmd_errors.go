package switcher

import "sync"

type commandError struct {
	Seq   uint64 `json:"seq"`
	Error string `json:"error"`
}

type recentErrors struct {
	mu   sync.Mutex
	buf  []commandError
	cap  int
	next int
	full bool
}

func newRecentErrors(capacity int) *recentErrors {
	return &recentErrors{buf: make([]commandError, capacity), cap: capacity}
}

func (r *recentErrors) record(seq uint64, errStr string) {
	if errStr == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = commandError{Seq: seq, Error: errStr}
	r.next = (r.next + 1) % r.cap
	if r.next == 0 {
		r.full = true
	}
}

func (r *recentErrors) snapshot() []commandError {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.full {
		out := make([]commandError, 0, r.cap)
		out = append(out, r.buf[r.next:]...)
		out = append(out, r.buf[:r.next]...)
		return out
	}
	return append([]commandError(nil), r.buf[:r.next]...)
}
