package switcher

import (
	"sync/atomic"
	"time"
)

type rawSinkNode struct {
	sink *atomic.Pointer[RawVideoSink]
	name string
}

func (n *rawSinkNode) Name() string                          { return n.name }
func (n *rawSinkNode) Configure(format PipelineFormat) error { return nil }
func (n *rawSinkNode) Active() bool                          { return n.sink.Load() != nil }
func (n *rawSinkNode) Err() error                            { return nil }
func (n *rawSinkNode) Latency() time.Duration                { return 50 * time.Microsecond }
func (n *rawSinkNode) Close() error                          { return nil }

func (n *rawSinkNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	if sinkPtr := n.sink.Load(); sinkPtr != nil {
		cp := src.DeepCopy()
		(*sinkPtr)(cp)
	}
	return src
}
