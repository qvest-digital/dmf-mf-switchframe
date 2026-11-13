package main

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/graphics/textrender"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

func motionTestApp(t *testing.T, layers ...int) *App {
	t.Helper()
	a := &App{compositor: graphics.NewCompositor()}
	t.Cleanup(a.compositor.Close)
	for _, id := range layers {
		require.NoError(t, a.compositor.AddLayerWithID(id))
	}
	tr, err := textrender.NewRenderer()
	require.NoError(t, err)
	a.tickerEngine = graphics.NewTickerEngine(a.compositor, tr)
	t.Cleanup(a.tickerEngine.Close)
	a.sequenceEngine = graphics.NewSequenceEngine(a.compositor)
	t.Cleanup(a.sequenceEngine.Close)
	return a
}

// The peer's tickers run here as they run there: a ticker whose text
// differs takes the peer's text, a missing one starts, and one the peer
// does not run stops.
func TestSyncTickers(t *testing.T) {
	a := motionTestApp(t, 1, 2, 3)
	base := graphics.TickerConfig{Text: "old news", FontSize: 32, Speed: 100, Loop: true}
	require.NoError(t, a.tickerEngine.Start(1, base))
	require.NoError(t, a.tickerEngine.Start(3, base))

	fresh := internal.TickerSnapshot{LayerID: 1, Text: "breaking", FontSize: 32, Speed: 100, Loop: true}
	added := internal.TickerSnapshot{LayerID: 2, Text: "weather", FontSize: 24, Speed: 60, Bold: true}
	require.NoError(t, a.SyncTickers([]internal.TickerSnapshot{fresh, added}))

	tickers, _ := a.graphicsMotionSnapshots()
	require.Equal(t, []internal.TickerSnapshot{fresh, added}, tickers)
}

func motionTestPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.NRGBA{R: shade, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func motionTestZIP(t *testing.T, shade uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("loop_0001.png")
	require.NoError(t, err)
	_, err = fw.Write(motionTestPNG(t, shade))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// A layer's PNG sequence is read from the peer by its hash and loaded here;
// one the peer does not run is removed; the same one is not read again.
func TestSyncSequences_TakesThePeersSequence(t *testing.T) {
	peerApp := motionTestApp(t, 1)
	data := motionTestZIP(t, 200)
	require.NoError(t, peerApp.sequenceEngine.UploadAsync(1, data, 25))
	api := control.NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()),
		control.WithCompositor(peerApp.compositor), control.WithSequenceEngine(peerApp.sequenceEngine))
	api.SetSnapshotProvider(&snapshotProviderAdapter{app: peerApp})
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/graphics-sequence" {
			reads.Add(1)
		}
		api.Mux().ServeHTTP(w, r)
	}))
	defer srv.Close()

	a := motionTestApp(t, 1, 2)
	a.cfg.PeerEngineURL = srv.URL
	a.peerPoller = peer.NewHealthPoller(srv.URL)
	require.NoError(t, a.sequenceEngine.UploadAsync(2, motionTestZIP(t, 50), 25))

	_, want := peerApp.graphicsMotionSnapshots()
	require.Len(t, want, 1)
	require.NoError(t, a.SyncSequences(context.Background(), want))
	require.Eventually(t, func() bool {
		_, got := a.graphicsMotionSnapshots()
		return len(got) == 1 && got[0].Hash == want[0].Hash && a.sequenceEngine.HasSequence(1)
	}, 5*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, reads.Load())

	require.NoError(t, a.SyncSequences(context.Background(), want))
	require.EqualValues(t, 1, reads.Load(), "a sequence held already was read again")
}

// A peer that by now holds another sequence on the layer is not taken for
// the one the snapshot named.
func TestSyncSequences_RefusesAnotherSequence(t *testing.T) {
	other := motionTestZIP(t, 90)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(other)
	}))
	defer srv.Close()

	a := motionTestApp(t, 1)
	a.cfg.PeerEngineURL = srv.URL
	a.peerPoller = peer.NewHealthPoller(srv.URL)
	err := a.SyncSequences(context.Background(), []internal.SequenceSnapshot{{LayerID: 1, Hash: "named", SourceFPS: 25, Speed: 1}})
	require.ErrorContains(t, err, "another sequence")
	_, got := a.graphicsMotionSnapshots()
	require.Empty(t, got)
}
