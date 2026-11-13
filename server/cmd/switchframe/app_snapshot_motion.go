package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
)

var _ internal.SnapshotGraphicsMotionSyncer = (*App)(nil)

const (
	// maxSequenceBytes is the largest sequence ZIP an upload takes.
	maxSequenceBytes = 256 << 20

	// sequenceReadTimeout bounds reading one sequence ZIP from the peer.
	sequenceReadTimeout = 2 * time.Minute
)

// graphicsMotionSnapshots captures the tickers and sequences the layers run.
func (a *App) graphicsMotionSnapshots() ([]internal.TickerSnapshot, []internal.SequenceSnapshot) {
	var tickers []internal.TickerSnapshot
	if a.tickerEngine != nil {
		for id, cfg := range a.tickerEngine.Configs() {
			tickers = append(tickers, internal.TickerSnapshot{
				LayerID: id, Text: cfg.Text, FontSize: cfg.FontSize, Speed: cfg.Speed,
				Bold: cfg.Bold, Loop: cfg.Loop, Height: cfg.Height,
			})
		}
		sort.Slice(tickers, func(i, j int) bool { return tickers[i].LayerID < tickers[j].LayerID })
	}
	var sequences []internal.SequenceSnapshot
	if a.sequenceEngine != nil {
		for id, src := range a.sequenceEngine.Sources() {
			sequences = append(sequences, internal.SequenceSnapshot{
				LayerID: id, Hash: src.Hash, Size: src.Size, SourceFPS: src.SourceFPS, Speed: src.Speed,
			})
		}
		sort.Slice(sequences, func(i, j int) bool { return sequences[i].LayerID < sequences[j].LayerID })
	}
	return tickers, sequences
}

// SyncTickers runs the snapshot's tickers here: it starts the missing ones,
// restarts the ones whose config differs, and stops the rest.
func (a *App) SyncTickers(tickers []internal.TickerSnapshot) error {
	if a.tickerEngine == nil {
		if len(tickers) > 0 {
			return errors.New("no ticker engine on this engine")
		}
		return nil
	}
	want := make(map[int]graphics.TickerConfig, len(tickers))
	for _, t := range tickers {
		want[t.LayerID] = graphics.TickerConfig{
			Text: t.Text, FontSize: t.FontSize, Speed: t.Speed, Bold: t.Bold, Loop: t.Loop, Height: t.Height,
		}
	}
	var errs []error
	have := a.tickerEngine.Configs()
	for id, cfg := range have {
		w, ok := want[id]
		if ok && w == cfg {
			continue
		}
		if ok && w == withText(cfg, w.Text) {
			if err := a.tickerEngine.UpdateText(id, w.Text); err != nil {
				errs = append(errs, fmt.Errorf("ticker on layer %d: %w", id, err))
			}
			continue
		}
		if err := a.tickerEngine.Stop(id); err != nil && !errors.Is(err, graphics.ErrTickerNotFound) {
			errs = append(errs, fmt.Errorf("stop ticker on layer %d: %w", id, err))
		}
	}
	for id, cfg := range want {
		if c, ok := have[id]; ok && (c == cfg || cfg == withText(c, cfg.Text)) {
			continue
		}
		if err := a.tickerEngine.Start(id, cfg); err != nil {
			errs = append(errs, fmt.Errorf("start ticker on layer %d: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func withText(cfg graphics.TickerConfig, text string) graphics.TickerConfig {
	cfg.Text = text
	return cfg
}

// SyncSequences loads the snapshot's sequences here: one this engine lacks
// is read from the peer by its hash, and one the snapshot lacks is removed.
func (a *App) SyncSequences(ctx context.Context, sequences []internal.SequenceSnapshot) error {
	if a.sequenceEngine == nil {
		if len(sequences) > 0 {
			return errors.New("no sequence engine on this engine")
		}
		return nil
	}
	var errs []error
	have := a.sequenceEngine.Sources()
	want := make(map[int]bool, len(sequences))
	for _, s := range sequences {
		want[s.LayerID] = true
	}
	for id := range have {
		if want[id] {
			continue
		}
		if err := a.sequenceEngine.Delete(id); err != nil && !errors.Is(err, graphics.ErrSequenceNotFound) {
			errs = append(errs, fmt.Errorf("remove sequence on layer %d: %w", id, err))
		}
	}
	for _, s := range sequences {
		if h, ok := have[s.LayerID]; ok && h.Hash == s.Hash {
			if h.Speed != s.Speed && s.Speed > 0 {
				if err := a.sequenceEngine.SetSpeed(s.LayerID, s.Speed); err != nil && !errors.Is(err, graphics.ErrSequenceNotFound) {
					errs = append(errs, fmt.Errorf("sequence speed on layer %d: %w", s.LayerID, err))
				}
			}
			continue
		}
		zip, ok := prefetchedSequence(ctx, s.Hash)
		var err error
		if !ok {
			zip, err = a.fetchPeerSequence(ctx, s)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("sequence on layer %d: %w", s.LayerID, err))
			continue
		}
		// The speed is set by the next apply, once the sequence is
		// loaded: loading runs in the background.
		if err := a.sequenceEngine.UploadAsync(s.LayerID, zip, s.SourceFPS); err != nil {
			errs = append(errs, fmt.Errorf("sequence on layer %d: %w", s.LayerID, err))
		}
	}
	return errors.Join(errs...)
}

// prefetchKey carries the sequence ZIPs read before an apply, by hash.
type prefetchKey struct{}

func prefetchedSequence(ctx context.Context, hash string) ([]byte, bool) {
	m, _ := ctx.Value(prefetchKey{}).(map[string][]byte)
	zip, ok := m[hash]
	return zip, ok
}

// prefetchSequences reads the sequence ZIPs snap names that this engine
// lacks from the peer, before the apply starts, and returns ctx carrying
// them for SyncSequences: a read takes up to sequenceReadTimeout, which
// the apply's window, during which this engine dates no change of its
// own, does not then span. A ZIP not read here is read by the apply, which
// reports what fails.
func (a *App) prefetchSequences(ctx context.Context, snap internal.FullStateSnapshot) context.Context {
	if !snap.GraphicsMotion || a.sequenceEngine == nil || a.peerPoller == nil {
		return ctx
	}
	have := a.sequenceEngine.Sources()
	zips := map[string][]byte{}
	for _, s := range snap.Sequences {
		if h, ok := have[s.LayerID]; ok && h.Hash == s.Hash {
			continue
		}
		if _, ok := zips[s.Hash]; ok {
			continue
		}
		if zip, err := a.fetchPeerSequence(ctx, s); err == nil {
			zips[s.Hash] = zip
		}
	}
	if len(zips) == 0 {
		return ctx
	}
	return context.WithValue(ctx, prefetchKey{}, zips)
}

// fetchPeerSequence reads the ZIP a sequence was uploaded as from the peer
// engine, the source of every snapshot an engine of a pair takes.
func (a *App) fetchPeerSequence(ctx context.Context, s internal.SequenceSnapshot) ([]byte, error) {
	if a.peerPoller == nil {
		return nil, errors.New("no peer engine to read it from")
	}
	if s.Size > maxSequenceBytes {
		return nil, fmt.Errorf("%d bytes, more than a sequence upload takes", s.Size)
	}
	ctx, cancel := context.WithTimeout(ctx, sequenceReadTimeout)
	defer cancel()
	u := a.cfg.PeerEngineURL + "/api/sync/graphics-sequence?layer=" + strconv.Itoa(s.LayerID) +
		"&hash=" + url.QueryEscape(s.Hash)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if a.cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.APIToken)
	}
	resp, err := a.peerPoller.HTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer answered %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSequenceBytes+1))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != s.Hash {
		return nil, errors.New("the peer holds another sequence on the layer by now")
	}
	return data, nil
}
