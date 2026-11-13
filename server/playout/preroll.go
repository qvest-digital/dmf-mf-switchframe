package playout

import (
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// prerollRetry is how long a preparation that found its asset not cached
// yet stands before it is tried again: the prefetcher may have fetched it
// by then.
const prerollRetry = time.Second

// Where an item's iterator came from when it went on air.
const (
	prerollReady  = "ready"  // prepared before the cut
	prerollWaited = "waited" // being prepared at the cut; the tick waited for it
	prerollMissed = "missed" // not prepared; opened at the cut
)

// preroll opens the clips a channel airs next ahead of time, off its tick
// goroutine: demux, decoder, seek to the in-point and the first decoded
// frame. Opening an item on the tick that cuts to it stalls the output for
// all of that, so the cut shows the previous picture again and the audio
// stops until the tick returns.
//
// One worker prepares the items one at a time, in the order want lists
// them, the cursor's first: each holds a demuxed file and a decoder, and
// decoding runs beside the channels on air.
type preroll struct {
	provider AssetProvider
	channel  int
	// maxBytes bounds the bytes of the asset files prepared after the
	// cursor's item; zero leaves them unbounded.
	maxBytes int64

	// sizes caches assetSize per asset URI: want runs every tick. A size
	// not known yet is looked up again after prerollRetry. Only want uses
	// it, and the channel calls want under its own lock.
	sizes map[string]sizeEntry

	mu      sync.Mutex
	entries map[string]*prerollEntry // by item ID
	queue   []*prerollEntry          // entries in the order want asked for them
	working bool                     // the worker runs
}

type sizeEntry struct {
	bytes int64
	at    time.Time
}

type prerollEntry struct {
	item PlaylistItem
	done chan struct{} // closed under preroll.mu once the fields below are set

	started   bool // under preroll.mu: the worker took it up
	waited    bool // under preroll.mu: a cut waited for it
	cancelled bool // under preroll.mu: no longer wanted, the preparation closes what it opened

	src     FrameIterator
	fps     float64
	total   int
	err     error
	readyAt time.Time
}

func newPreroll(channel int, provider AssetProvider, maxBytes int64) *preroll {
	return &preroll{
		provider: provider,
		channel:  channel,
		maxBytes: maxBytes,
		entries:  make(map[string]*prerollEntry),
		sizes:    make(map[string]sizeEntry),
	}
}

// size returns the size of uri's file, known or -1, looking it up at most
// once per prerollRetry while it is not known.
func (p *preroll) size(sizer assetSizer, uri string, now time.Time) int64 {
	if e, ok := p.sizes[uri]; ok && (e.bytes >= 0 || now.Sub(e.at) < prerollRetry) {
		return e.bytes
	}
	n := sizer.assetSize(uri)
	p.sizes[uri] = sizeEntry{bytes: n, at: now}
	return n
}

// assetSizer is an AssetProvider that knows the size of an asset's file
// before opening it, or -1.
type assetSizer interface {
	assetSize(assetURI string) int64
}

// sameMedia reports whether a and b play the same frames.
func sameMedia(a, b *PlaylistItem) bool {
	return a.ID == b.ID && a.AssetURI == b.AssetURI && a.InPointMs == b.InPointMs &&
		a.OutPointMs == b.OutPointMs && a.Loop == b.Loop
}

func (e *prerollEntry) isDone() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// want prepares cur, the item at the cursor, and the items in next while
// their files fit maxBytes, in that order, and releases every preparation
// not among them.
func (p *preroll) want(cur, next []PlaylistItem) {
	items := cur
	if sizer, ok := p.provider.(assetSizer); ok && p.maxBytes > 0 {
		var bytes int64
		now := time.Now()
		for _, it := range next {
			if n := p.size(sizer, it.AssetURI, now); n > 0 {
				bytes += n
			}
			if bytes > p.maxBytes {
				break
			}
			items = append(items, it)
		}
	} else {
		items = append(items, next...)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	queue := make([]*prerollEntry, 0, len(items))
	for i := range items {
		e := p.entries[items[i].ID]
		if e != nil {
			// A preparation that found the asset not cached yet is
			// redone once it has stood for prerollRetry.
			retry := e.isDone() && notCachedYet(e.err) && now.Sub(e.readyAt) >= prerollRetry
			if !sameMedia(&e.item, &items[i]) || retry {
				p.dropLocked(e)
				e = nil
			}
		}
		if e == nil {
			e = &prerollEntry{item: items[i], done: make(chan struct{})}
			p.entries[e.item.ID] = e
		}
		queue = append(queue, e)
	}
	for _, e := range p.entries {
		if !slices.Contains(queue, e) {
			p.dropLocked(e)
		}
	}
	p.queue = queue
	if !p.working && slices.ContainsFunc(queue, func(e *prerollEntry) bool { return !e.started }) {
		p.working = true
		go p.work()
	}
}

// removeLocked takes e out of the preroll. Caller holds p.mu.
func (p *preroll) removeLocked(e *prerollEntry) {
	if p.entries[e.item.ID] == e {
		delete(p.entries, e.item.ID)
	}
	p.queue = slices.DeleteFunc(p.queue, func(q *prerollEntry) bool { return q == e })
}

// dropLocked releases e. Caller holds p.mu.
func (p *preroll) dropLocked(e *prerollEntry) {
	p.removeLocked(e)
	e.cancelled = true
	if e.isDone() && e.src != nil {
		e.src.Close()
		e.src = nil
	}
}

// work prepares the queued entries in order until none is left.
func (p *preroll) work() {
	for {
		p.mu.Lock()
		i := slices.IndexFunc(p.queue, func(e *prerollEntry) bool { return !e.started })
		if i < 0 {
			p.working = false
			p.mu.Unlock()
			return
		}
		e := p.queue[i]
		e.started = true
		p.mu.Unlock()
		p.prepare(e)
	}
}

func (p *preroll) prepare(e *prerollEntry) {
	start := time.Now()
	src, fps, total, err := openClip(p.provider, &e.item)
	if err == nil {
		first, ferr := src.Next()
		src = primeIterator(src, first, ferr)
	}

	p.mu.Lock()
	e.fps, e.total, e.err, e.readyAt = fps, total, err, time.Now()
	if e.cancelled && src != nil {
		src.Close()
		src = nil
	}
	e.src = src
	close(e.done)
	cancelled := e.cancelled
	p.mu.Unlock()

	switch {
	case cancelled:
	case notCachedYet(err):
		slog.Debug("playout: pre-roll waits for the asset", "channel", p.channel, "item", e.item.ID, "error", err)
	case err != nil:
		slog.Warn("playout: pre-roll failed", "channel", p.channel, "item", e.item.ID, "error", err)
	default:
		slog.Info("playout: item pre-rolled", "channel", p.channel, "item", e.item.ID,
			"ms", time.Since(start).Milliseconds())
	}
}

// await waits for a preparation of item that is running, so the tick that
// cuts to item takes it without waiting under the channel's lock.
func (p *preroll) await(item *PlaylistItem) {
	p.mu.Lock()
	e := p.entries[item.ID]
	running := e != nil && e.started && !e.isDone() && sameMedia(&e.item, item)
	if running {
		e.waited = true
	}
	p.mu.Unlock()
	if running {
		<-e.done
	}
}

// take hands over the preparation of item and reports how ready it was. It
// returns nil when item was not prepared, and cancels a preparation that
// has not started, which would take as long as opening item at once. A
// running preparation is waited for; a failed one comes back with its
// error.
func (p *preroll) take(item *PlaylistItem) (e *prerollEntry, result string) {
	p.mu.Lock()
	e = p.entries[item.ID]
	if e == nil || !sameMedia(&e.item, item) {
		p.mu.Unlock()
		return nil, prerollMissed
	}
	p.removeLocked(e)
	if !e.started {
		e.cancelled = true
		p.mu.Unlock()
		return nil, prerollMissed
	}
	waited := e.waited
	p.mu.Unlock()

	result = prerollReady
	if waited || !e.isDone() {
		result = prerollWaited
		<-e.done
	}
	return e, result
}

// notCachedYet reports whether err is an asset the cache does not hold yet,
// which opens once the prefetcher has fetched it.
func notCachedYet(err error) bool {
	return errors.Is(err, ErrAssetNotReady) || errors.Is(err, ErrAssetNotFound)
}

// ready counts the preparations that finished and can go on air.
func (p *preroll) ready() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.entries {
		if e.isDone() && e.src != nil {
			n++
		}
	}
	return n
}

// openClip opens item's asset, trimmed and looped as the item says, and
// moves it to the in-point. Without the seek the trim decodes and drops
// every frame before the in-point.
func openClip(provider AssetProvider, item *PlaylistItem) (FrameIterator, float64, int, error) {
	iter, err := provider.Open(item.AssetURI)
	if err != nil {
		return nil, 0, 0, err
	}
	src := clipFrameSource(iter, item)
	if t, ok := src.(*TrimIterator); ok && item.InPointMs > 0 {
		if err := t.Seek(0); err != nil {
			slog.Debug("playout: in-point seek failed, decoding up to it", "item", item.ID, "error", err)
		}
	}
	return src, src.EstimateFPS(), src.TotalFrames(), nil
}

// primeIterator returns src with first, the frame its preparation decoded,
// put back in front of it. It loops only when src does.
func primeIterator(src FrameIterator, first *DecodedFrame, err error) FrameIterator {
	p := &primedIterator{FrameIterator: src, first: first, err: err, primed: true}
	if _, ok := src.(LoopingIterator); ok {
		return &primedLoopingIterator{p}
	}
	return p
}

// primedIterator returns the frame its preparation decoded before going on
// to the iterator it wraps.
type primedIterator struct {
	FrameIterator
	first  *DecodedFrame
	err    error
	primed bool
}

func (p *primedIterator) Next() (*DecodedFrame, error) {
	if p.primed {
		f, err := p.first, p.err
		p.first, p.err, p.primed = nil, nil, false
		return f, err
	}
	return p.FrameIterator.Next()
}

// Seek moves the wrapped iterator, dropping the decoded frame.
func (p *primedIterator) Seek(frame int) error {
	s, ok := p.FrameIterator.(interface{ Seek(int) error })
	if !ok {
		return errors.New("playout: primed seek: inner iterator does not support seeking")
	}
	p.first, p.err, p.primed = nil, nil, false
	return s.Seek(frame)
}

// primedLoopingIterator is a primedIterator over a LoopingIterator.
type primedLoopingIterator struct{ *primedIterator }

// SetLoop passes loop on to the wrapped iterator.
func (p *primedLoopingIterator) SetLoop(loop bool) {
	p.FrameIterator.(LoopingIterator).SetLoop(loop)
}

// Rewind starts the wrapped iterator over.
func (p *primedLoopingIterator) Rewind() error {
	p.first, p.err, p.primed = nil, nil, false
	return p.FrameIterator.(LoopingIterator).Rewind()
}

// startDelayBuckets span one tick at the fastest pipeline rate to the
// seconds a long clip took to open on the tick.
var startDelayBuckets = []float64{0.001, 0.005, 0.01, 0.02, 0.04, 0.1, 0.25, 0.5, 1, 2.5, 5}

// startStats counts how clip items went on air. The manager owns it, so
// the counts survive a channel being recreated.
type startStats struct {
	starts *prometheus.CounterVec
	delay  *prometheus.HistogramVec
}

func newStartStats() *startStats {
	return &startStats{
		starts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "switchframe_playout_item_starts_total",
			Help: "Clip items the channel put on air, by whether they were pre-rolled: ready (prepared before the cut), waited (still being prepared at the cut) or missed (opened at the cut).",
		}, []string{"channel", "preroll"}),
		delay: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "switchframe_playout_item_start_delay_seconds",
			Help:    "Time from the tick that cut or transitioned to a clip item to its first video frame (media=video) and its first audio (media=audio).",
			Buckets: startDelayBuckets,
		}, []string{"channel", "media"}),
	}
}

// itemStart follows a clip item from the tick that cut to it until its
// first video frame and first audio are out.
type itemStart struct {
	id      string
	cutAt   time.Time
	preroll string
	video   bool
	audio   bool
}

// beginItemStartLocked starts following item, opened on the tick running
// now with the given pre-roll result. Caller holds c.mu.
func (c *Channel) beginItemStartLocked(item *PlaylistItem, result string) {
	cutAt := c.tickAt
	if cutAt.IsZero() {
		cutAt = time.Now()
	}
	c.itemStart = itemStart{id: item.ID, cutAt: cutAt, preroll: result}
	if s := c.config.starts; s != nil {
		s.starts.WithLabelValues(strconv.Itoa(c.id), result).Inc()
	}
}

// noteItemFrame records when the first video frame and the first audio of
// the item being followed came out.
func (c *Channel) noteItemFrame(itemID string, f *DecodedFrame) {
	if f == nil {
		return
	}
	c.mu.Lock()
	s := &c.itemStart
	if s.id == "" || s.id != itemID {
		c.mu.Unlock()
		return
	}
	now := time.Now()
	var video, audio time.Duration = -1, -1
	if !s.video {
		s.video = true
		video = now.Sub(s.cutAt)
	}
	if !s.audio && (len(f.AudioFrames) > 0 || len(f.AudioData) > 0) {
		s.audio = true
		audio = now.Sub(s.cutAt)
	}
	result := s.preroll
	if s.video && s.audio {
		s.id = ""
	}
	c.mu.Unlock()

	ch := strconv.Itoa(c.id)
	stats := c.config.starts
	if video >= 0 {
		if stats != nil {
			stats.delay.WithLabelValues(ch, "video").Observe(video.Seconds())
		}
		if video > c.frameDuration {
			slog.Warn("playout: late item start", "channel", c.id, "item", itemID,
				"preroll", result, "first_frame_ms", video.Milliseconds())
		} else {
			slog.Info("playout: item started", "channel", c.id, "item", itemID,
				"preroll", result, "first_frame_ms", video.Milliseconds())
		}
	}
	if audio >= 0 {
		if stats != nil {
			stats.delay.WithLabelValues(ch, "audio").Observe(audio.Seconds())
		}
		if audio > 2*c.frameDuration {
			slog.Warn("playout: late item audio", "channel", c.id, "item", itemID,
				"preroll", result, "first_audio_ms", audio.Milliseconds())
		}
	}
}
