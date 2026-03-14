package atomicutil

import "sync/atomic"

// UpdateMax atomically updates field to val if val > current.
// Uses a CAS loop for lock-free thread safety.
func UpdateMax(field *atomic.Int64, val int64) {
	for {
		cur := field.Load()
		if val <= cur {
			return
		}
		if field.CompareAndSwap(cur, val) {
			return
		}
	}
}
