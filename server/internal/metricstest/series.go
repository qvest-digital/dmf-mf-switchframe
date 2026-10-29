// Package metricstest reads a Prometheus collector's series for tests.
package metricstest

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Series gathers c and returns each family's series by name, keyed by
// their label values joined with "/" in label-name order. It fails t when
// the collector does not pass the registry's own consistency checks.
func Series(t testing.TB, c prometheus.Collector) map[string]map[string]float64 {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if problems, err := testutil.CollectAndLint(c); err != nil || len(problems) > 0 {
		t.Fatalf("lint: %v %v", err, problems)
	}
	out := map[string]map[string]float64{}
	for _, f := range families {
		series := map[string]float64{}
		for _, m := range f.GetMetric() {
			key := ""
			for i, l := range m.GetLabel() {
				if i > 0 {
					key += "/"
				}
				key += l.GetValue()
			}
			switch {
			case m.Counter != nil:
				series[key] = m.GetCounter().GetValue()
			case m.Gauge != nil:
				series[key] = m.GetGauge().GetValue()
			}
		}
		out[f.GetName()] = series
	}
	return out
}
