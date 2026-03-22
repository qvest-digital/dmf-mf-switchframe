// Package htmlgfx manages an HTML5 graphics rendering sidecar (CEF-based)
// for broadcast-quality overlay graphics. It communicates with the sidecar
// over Unix domain sockets using a JSON control protocol and a binary frame
// protocol.
package htmlgfx

// Config holds settings for the HTML graphics sidecar process.
type Config struct {
	BinaryPath string // path to the CEF sidecar binary
	PoolSize   int    // number of browser instances (default 4)
	Width      int    // render width in pixels
	Height     int    // render height in pixels
	FPS        int    // target frame rate
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		PoolSize: 4,
		Width:    1920,
		Height:   1080,
		FPS:      30,
	}
}

// BrowserState tracks the current state of a single browser instance
// in the sidecar pool.
type BrowserState struct {
	ID         int            // browser instance index
	LayerID    int            // graphics layer mapping (-1 if unmapped)
	URL        string         // currently loaded URL
	Format     string         // detected format: "ograf", "casparcg", "unknown", ""
	Status     string         // "empty", "loading", "ready", "playing", "stopped", "error"
	Width      int            // current render width
	Height     int            // current render height
	DataSchema map[string]any // OGraf manifest data schema
	LastData   string         // last data sent (for restart recovery)
}

// NewBrowserState creates a BrowserState with default initial values.
func NewBrowserState(id int) BrowserState {
	return BrowserState{
		ID:      id,
		LayerID: -1,
		Status:  "empty",
	}
}
