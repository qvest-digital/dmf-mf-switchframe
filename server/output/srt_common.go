package output

const (
	defaultSRTLatency = 120 // ms
)

// srtConn abstracts SRT connection operations for testing without cgo.
// Both SRTCaller and SRTListener use this interface.
type srtConn interface {
	Write(data []byte) (int, error)
	Close()
}
