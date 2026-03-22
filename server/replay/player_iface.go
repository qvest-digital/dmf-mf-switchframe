package replay

import "context"

// replayPlayerIface abstracts the CPU and GPU replay player implementations.
// Both replayPlayer and gpuReplayPlayer satisfy this interface so the Manager
// can switch between them transparently.
type replayPlayerIface interface {
	Start(ctx context.Context)
	Stop()
	Wait()
	Progress() float64
	Pause()
	Resume()
	Seek(position float64)
	SetSpeed(speed float64)
	SetShutterAngle(angle float64)
}

// Compile-time interface checks.
var _ replayPlayerIface = (*replayPlayer)(nil)
var _ replayPlayerIface = (*gpuReplayPlayer)(nil)
