package switcher

// TallyStatus represents the tally light state for a source.
type TallyStatus string

const (
	TallyProgram  TallyStatus = "program"
	TallyPreview  TallyStatus = "preview"
	TallyKeyFill  TallyStatus = "key_fill"
	TallyKeyAlpha TallyStatus = "key_alpha"
	TallyIdle     TallyStatus = "idle"
)

// SourceHealthStatus represents the health/connectivity state of a video source.
type SourceHealthStatus string

const (
	SourceHealthy  SourceHealthStatus = "healthy"
	SourceStale    SourceHealthStatus = "stale"
	SourceNoSignal SourceHealthStatus = "no_signal"
	SourceOffline  SourceHealthStatus = "offline"
)
