package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zsiec/switchframe/server/mxl"
)

// openCost is how long an open of a missing flow takes, as an mxlfs
// lookup waits for the flow.
const openCost = 2 * time.Second

// missingFlows opens nothing: each open takes openCost and fails. inOpen
// counts the opens under way.
type missingFlows struct{ inOpen atomic.Int32 }

func (m *missingFlows) open() error {
	m.inOpen.Add(1)
	defer m.inOpen.Add(-1)
	time.Sleep(openCost)
	return errors.New("flow not found")
}

func (m *missingFlows) video(string) (mxl.DiscreteReader, error) { return nil, m.open() }
func (m *missingFlows) audio(string) (mxl.ContinuousReader, error) {
	return nil, m.open()
}

// deletedFlow is a video handle on a flow its writer deleted: every read
// reports it invalid, so the reader loop reopens the flow itself.
type deletedFlow struct{}

func (deletedFlow) ReadGrain(uint64, uint64) ([]byte, mxl.GrainInfo, error) {
	return nil, mxl.GrainInfo{}, mxl.ErrFlowInvalid
}
func (deletedFlow) ConfigInfo() mxl.FlowConfig {
	return mxl.FlowConfig{Format: mxl.DataFormatVideo, GrainRate: mxl.Rational{Numerator: 50, Denominator: 1}}
}
func (deletedFlow) HeadIndex() (uint64, error) { return 1, nil }
func (deletedFlow) Close() error               { return nil }

// Teardown of MXL inputs whose flows are missing, or deleted and not back,
// waits for the opens under way on them, and an open of a missing flow
// takes seconds. Every source is shut down before any is waited for, so
// the teardown takes about one open however many sources there are,
// rather than one open per source while the sources not yet stopped
// start new ones. Frame rates do not enter: the time is the opens'.
func TestCloseSubsystems_StopsMXLSourcesTogether(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		flows := &missingFlows{}
		quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
		a := &App{}
		for i := range 3 {
			src := mxl.NewSource(mxl.SourceConfig{FlowName: fmt.Sprintf("mxl:cam%d", i), Width: 16, Height: 16, Logger: quiet})
			src.Start(context.Background(),
				mxl.NewReopeningReader("v", deletedFlow{}, flows.video, quiet),
				mxl.NewReopeningAudioReader("a", nil, flows.audio, quiet),
				mxl.NewReopeningReader("d", nil, flows.video, quiet))
			if i == 2 {
				a.demoMXLSources = append(a.demoMXLSources, src)
			} else {
				a.mxlSources = append(a.mxlSources, src)
			}
			// Out of step, so an open is under way on some source at
			// any time.
			time.Sleep(time.Second)
		}
		time.Sleep(1500 * time.Millisecond)
		if flows.inOpen.Load() == 0 {
			t.Fatal("no open under way at the teardown")
		}

		start := time.Now()
		a.closeSubsystems()
		if took := time.Since(start); took > openCost {
			t.Errorf("teardown took %v, want at most one open, %v", took, openCost)
		}
		if n := flows.inOpen.Load(); n != 0 {
			t.Errorf("%d opens still under way after teardown", n)
		}
	})
}
