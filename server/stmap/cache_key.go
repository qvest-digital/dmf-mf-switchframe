package stmap

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
)

// CacheKey produces a 16-character hex string uniquely identifying a generated
// ST map configuration. Deterministic: same inputs always produce the same key.
func CacheKey(name string, params map[string]float64, w, h, frameCount int) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s:%d:%d:%d", name, w, h, frameCount)
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(hash, ":%s=%.10g", k, params[k])
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

// DirComplete returns true if a cache directory has all expected LUT frame files.
func DirComplete(dir string, frameCount int) bool {
	for i := 0; i < frameCount; i++ {
		path := fmt.Sprintf("%s/frame_%03d.lut", dir, i)
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}
