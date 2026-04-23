package switcher

import (
	"sync/atomic"
	"testing"
)

func TestSourcesMap_CopyOnWrite_Publish(t *testing.T) {
	var sources atomic.Pointer[map[string]*sourceState]
	m := map[string]*sourceState{"cam1": {}}
	sources.Store(&m)

	old := *sources.Load()
	next := make(map[string]*sourceState, len(old)+1)
	for k, v := range old {
		next[k] = v
	}
	next["cam2"] = &sourceState{}
	sources.Store(&next)

	if _, ok := old["cam2"]; ok {
		t.Error("old map mutated after CoW publish")
	}
	got := *sources.Load()
	if _, ok := got["cam1"]; !ok {
		t.Error("cam1 missing from new snapshot")
	}
	if _, ok := got["cam2"]; !ok {
		t.Error("cam2 missing from new snapshot")
	}
}

func TestSwitcher_SourcesAtomic_InitializedNonNil(t *testing.T) {
	sw := New(nil)
	defer sw.Close()

	p := sw.sourcesAtomic.Load()
	if p == nil {
		t.Fatal("sourcesAtomic.Load() returned nil after New()")
	}
	if *p == nil {
		t.Fatal("sourcesAtomic pointer points to nil map")
	}
}
