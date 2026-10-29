package metrics

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// An SRT input's key carries the publisher's stream id, which can be a
// stream key; its label must not, and must stay the same for the same
// source so a series continues.
func TestSourceLabel_HidesSRTStreamIDs(t *testing.T) {
	label := SourceLabel("srt:live-secret-key-123")
	require.True(t, strings.HasPrefix(label, "srt:"))
	require.NotContains(t, label, "secret")
	require.Len(t, label, len("srt:")+12)
	require.Equal(t, label, SourceLabel("srt:live-secret-key-123"))
	require.NotEqual(t, label, SourceLabel("srt:other"))
}

func TestSourceLabel_KeepsOtherKeys(t *testing.T) {
	for _, key := range []string{"mxl:cam1", "clip:1", "source:black", "cam1", "replay"} {
		require.Equal(t, key, SourceLabel(key))
	}
}

func TestMethodLabel(t *testing.T) {
	require.Equal(t, "GET", MethodLabel("GET"))
	require.Equal(t, "PATCH", MethodLabel("PATCH"))
	require.Equal(t, "other", MethodLabel("FOO"))
	require.Equal(t, "other", MethodLabel("get"))
	require.Equal(t, "other", MethodLabel(""))
}
