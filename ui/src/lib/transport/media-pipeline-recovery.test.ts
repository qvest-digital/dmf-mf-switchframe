import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { MoQTransportCallbacks } from '$lib/prism/moq-transport';

// One real media pipeline with a scriptable transport per dial. Every
// disconnect class a preview meets ends in one of three ways on the
// wire: the session closes, the dial never delivers a catalog, or the
// session stays open and simply stops carrying frames. A preview must
// come back from each of them without a page reload.

vi.mock('$lib/prism/video-decoder', () => ({
	PrismVideoDecoder: class {
		preload = vi.fn(); configure = vi.fn(); decode = vi.fn(); reset = vi.fn();
		flush = vi.fn(); pause = vi.fn(); resume = vi.fn();
		getDiagnostics = vi.fn().mockResolvedValue({});
	},
}));
vi.mock('$lib/prism/video-render-buffer', () => ({
	VideoRenderBuffer: class { addFrame = vi.fn(); clear = vi.fn(); takeNewestFrame = vi.fn().mockReturnValue({ frame: null }); },
}));
vi.mock('$lib/prism/audio-decoder', () => ({ PrismAudioDecoder: class {} }));
vi.mock('$lib/prism/renderer', () => ({
	PrismRenderer: class { start = vi.fn(); destroy = vi.fn(); resetSync = vi.fn(); getDiagnostics = vi.fn(); },
}));

type Dial = { key: string; cb: MoQTransportCallbacks; close: ReturnType<typeof vi.fn> };
const dials: Dial[] = [];
vi.mock('$lib/prism/moq-transport', () => ({
	MoQTransport: class {
		close = vi.fn();
		cb: MoQTransportCallbacks;
		constructor(public key: string, cb: MoQTransportCallbacks) {
			this.cb = cb;
			dials.push(this as unknown as Dial);
		}
		// Resolves, as a real connect does once its subscriptions are sent;
		// the catalog arrives through onTrackInfo, driven by the test.
		connect = vi.fn().mockResolvedValue(undefined);
		getDiagnostics = vi.fn(); getTransport = vi.fn();
	},
}));

import { createMediaPipeline, RECONNECT_MAX_MS, STALL_THRESHOLD_MS, type MediaPipeline } from './media-pipeline';
import { clearAllStalls, isSourceStalled } from './source-errors.svelte';

const KEY = 'mxl:mxl-1';
const desc = new Uint8Array([1, 0x64, 0, 0x28]);

/** The newest dial delivers its catalog and starts sending frames. */
function up(p: MediaPipeline): Dial {
	const d = dials[dials.length - 1];
	d.cb.onTrackInfo([{ type: 'video', codec: 'avc1.640028', width: 854, height: 480 } as never]);
	p.feedVideoFrame(KEY, new Uint8Array([1]), true, 0, desc);
	return d;
}

/** Feed frames on the current transport for ms milliseconds. */
function frames(p: MediaPipeline, ms: number): void {
	for (let t = 0; t < ms; t += 20) {
		p.feedVideoFrame(KEY, new Uint8Array([1]), false, t * 1000, null);
		vi.advanceTimersByTime(20);
	}
}

describe('preview recovery per disconnect class', () => {
	let p: MediaPipeline;
	beforeEach(() => {
		vi.useFakeTimers();
		dials.length = 0;
		clearAllStalls();
		p = createMediaPipeline();
		p.addSource(KEY);
		p.connectSource(KEY);
		p.startStallDetection();
		up(p);
		frames(p, 1000);
	});
	afterEach(() => {
		p.destroy();
		vi.useRealTimers();
	});

	// Server restart, engine pod replaced, network blip long enough for
	// QUIC to time out: the session closes and the transport reports it.
	it('redials after the session closes (server restart, network blip)', () => {
		dials[0].cb.onClose();
		vi.advanceTimersByTime(1000);
		expect(dials.length).toBe(2);
		up(p);
		frames(p, 500);
		expect(isSourceStalled(KEY)).toBe(false);
	});

	// The engine is gone and every dial hangs in the QUIC handshake.
	it('redials a dial that never delivers its catalog', () => {
		dials[0].cb.onClose();
		vi.advanceTimersByTime(1000);
		expect(dials.length).toBe(2);
		vi.advanceTimersByTime(4000 + 1000);
		expect(dials.length).toBe(3);
	});

	// The session stays open but frames stop: the server's write loop for
	// the track returned on a write error and left the subscription
	// registered, the track ended, or the input was unwired and rewired
	// to another flow under the same key. The stall detector sees it
	// within 1.5 s; nothing redials, so the tile stays frozen until the
	// page is reloaded.
	it('redials a source whose open session stopped carrying frames', () => {
		vi.advanceTimersByTime(STALL_THRESHOLD_MS + 1000);
		expect(isSourceStalled(KEY)).toBe(true);
		vi.advanceTimersByTime(30_000);
		expect(dials[0].close).toHaveBeenCalled();
		expect(dials.length).toBeGreaterThan(1);
	});
});

// An input wired to nothing on the engine: the catalog never came for
// 30 s, the browser abandoned each dial at its 4 s connect timeout and
// dialled again after a backoff capped at 4 s, one dial every ~8 s for as
// long as the page was open. Once the engine answers the catalog at once,
// the session is up but carries no frames, and its replacement must back
// off just as far.
describe('dial rate of a source that delivers no video', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		dials.length = 0;
		clearAllStalls();
	});
	afterEach(() => vi.useRealTimers());

	/** Dial times over 35 min; answer, when set, delivers each catalog. */
	function dialTimes(answer: boolean): number[] {
		const p = createMediaPipeline();
		p.addSource('mxl:input');
		p.connectSource('mxl:input');
		const times: number[] = [];
		const start = Date.now();
		for (let t = 0; t < 35 * 60_000; t += 100) {
			while (times.length < dials.length) {
				times.push(Date.now() - start);
				if (answer) dials[times.length - 1].cb.onTrackInfo([{ type: 'video', codec: 'avc1.640028', width: 854, height: 480 } as never]);
			}
			vi.advanceTimersByTime(100);
		}
		p.destroy();
		return times;
	}

	for (const [name, answer] of [['whose catalog never arrives', false], ['whose catalog arrives without frames', true]] as const) {
		it(`backs off to RECONNECT_MAX_MS for a source ${name}`, () => {
			const times = dialTimes(answer);
			const gaps = times.slice(1).map((t, i) => t - times[i]);
			const steady = Math.min(...gaps.slice(-10));
			expect(steady, `steady dial gap ${steady} ms`).toBeGreaterThanOrEqual(0.75 * RECONNECT_MAX_MS);
			// Still redialled, in case the session is the fault.
			expect(times.length, `${times.length} dials in 35 min`).toBeGreaterThan(10);
			expect(times.length, `${times.length} dials in 35 min`).toBeLessThan(90);
		});
	}

	// The engine closes the session of an input once it is wired, as its
	// catalog went stale. The page must take that close as the cue to dial
	// at once, not after the backoff the silent input built up.
	it('dials at once when the engine closes a long-silent session', () => {
		const p = createMediaPipeline();
		p.addSource('mxl:input');
		p.connectSource('mxl:input');
		let answered = 0;
		let openedAt = Date.now();
		for (let t = 0; t < 10 * 60_000; t += 100) {
			while (answered < dials.length) {
				dials[answered++].cb.onTrackInfo([{ type: 'video', codec: 'avc1.640028', width: 854, height: 480 } as never]);
				openedAt = Date.now();
			}
			vi.advanceTimersByTime(100);
		}
		// Wait until the current session has been up 10 s, then close it.
		while (Date.now() - openedAt < 10_000 || answered < dials.length) {
			while (answered < dials.length) {
				dials[answered++].cb.onTrackInfo([{ type: 'video', codec: 'avc1.640028', width: 854, height: 480 } as never]);
				openedAt = Date.now();
			}
			vi.advanceTimersByTime(100);
		}
		const before = dials.length;
		dials[before - 1].cb.onClose();
		vi.advanceTimersByTime(1000);
		p.destroy();
		expect(dials.length, 'dials within 1 s of the close').toBe(before + 1);
	});
});
