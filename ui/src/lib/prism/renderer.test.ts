import { describe, it, expect, vi, beforeEach } from 'vitest';
import { PrismRenderer } from './renderer';
import { VideoRenderBuffer } from './video-render-buffer';

// Mock VideoFrame — jsdom doesn't support WebCodecs
class MockVideoFrame {
	readonly timestamp: number;
	readonly duration: number;
	readonly displayWidth: number;
	readonly displayHeight: number;
	closed = false;

	constructor(timestamp: number, duration = 33333) {
		this.timestamp = timestamp;
		this.duration = duration;
		this.displayWidth = 320;
		this.displayHeight = 240;
	}

	close() {
		this.closed = true;
	}
}

// Mock canvas
function createMockCanvas(): HTMLCanvasElement {
	return {
		width: 0,
		height: 0,
		getContext: vi.fn().mockReturnValue({
			drawImage: vi.fn(),
		}),
	} as unknown as HTMLCanvasElement;
}

describe('PrismRenderer', () => {
	let canvas: HTMLCanvasElement;
	let buffer: VideoRenderBuffer;

	beforeEach(() => {
		canvas = createMockCanvas();
		buffer = new VideoRenderBuffer();
	});

	describe('look-ahead tolerance for video-ahead-of-audio', () => {
		it('draws frame when video is 50ms ahead of audio', () => {
			// 50ms ahead — within the 100ms look-ahead tolerance
			const audioClock = { getPlaybackPTS: () => 1_000_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			buffer.addFrame(new MockVideoFrame(1_050_000) as unknown as VideoFrame);

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
			expect(diag.framesSkipped).toBe(0);
		});

		it('draws frame when video is 90ms ahead of audio', () => {
			// 90ms ahead — within the 100ms tolerance
			const audioClock = { getPlaybackPTS: () => 1_000_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			buffer.addFrame(new MockVideoFrame(1_090_000) as unknown as VideoFrame);

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
			expect(diag.framesSkipped).toBe(0);
		});

		it('skips frame when video is 150ms+ ahead of audio', () => {
			// 150ms exceeds the 100ms look-ahead tolerance — should skip.
			// Server-side PTS alignment (SeedPTSFromVideo + frame counter)
			// keeps offsets well under 100ms during normal operation.
			const audioClock = { getPlaybackPTS: () => 1_000_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			buffer.addFrame(new MockVideoFrame(1_150_000) as unknown as VideoFrame);

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(0);
			expect(diag.framesSkipped).toBe(1);
		});

		it('recovers from PTS discontinuity when video is 2+ seconds ahead', () => {
			// Large PTS gap triggers discontinuity recovery — re-anchors clock
			// and draws the frame instead of freezing after source cuts.
			const audioClock = { getPlaybackPTS: () => 1_000_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			buffer.addFrame(new MockVideoFrame(3_500_000) as unknown as VideoFrame);

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
		});
	});

	describe('audio-driven frame selection', () => {
		it('draws frame matching audio PTS', () => {
			const audioClock = { getPlaybackPTS: () => 1_000_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			// Frame at exactly the audio PTS
			buffer.addFrame(new MockVideoFrame(1_000_000) as unknown as VideoFrame);

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
		});

		it('draws frame just before audio PTS', () => {
			const audioClock = { getPlaybackPTS: () => 1_100_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			buffer.addFrame(new MockVideoFrame(1_000_000) as unknown as VideoFrame);

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
		});
	});

	describe('freerun mode (no audio)', () => {
		it('draws frames without audio clock', () => {
			const audioClock = { getPlaybackPTS: () => -1 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			buffer.addFrame(new MockVideoFrame(1_000_000) as unknown as VideoFrame);

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
			expect(diag.clockMode).toBe('freerun');
		});

		it('counts empty buffer hits when buffer is empty', () => {
			const audioClock = { getPlaybackPTS: () => -1 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			// No frames in buffer
			renderer.renderOnce();
			renderer.renderOnce();
			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.emptyBufferHits).toBe(3);
			expect(diag.framesDrawn).toBe(0);
		});
	});

	describe('resetSync', () => {
		it('clears AV sync tracking so new source starts fresh', () => {
			const audioClock = { getPlaybackPTS: () => 1_000_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			// Draw a frame to establish sync tracking
			buffer.addFrame(new MockVideoFrame(1_000_000) as unknown as VideoFrame);
			renderer.renderOnce();

			let diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
			expect(diag.currentVideoPTS).toBe(1_000_000);
			expect(diag.currentAudioPTS).toBe(1_000_000);

			// Reset sync — simulates program source change
			renderer.resetSync();

			diag = renderer.getDiagnostics();
			expect(diag.currentVideoPTS).toBe(-1);
			expect(diag.currentAudioPTS).toBe(-1);
			expect(diag.avSyncMs).toBe(0);
			expect(diag.avSyncMin).toBe(0);
			expect(diag.avSyncMax).toBe(0);
			expect(diag.avSyncAvg).toBe(0);
			// framesDrawn should NOT be reset (cumulative diagnostic)
			expect(diag.framesDrawn).toBe(1);
		});

		it('allows new source PTS to be tracked without old source interference', () => {
			let audioPTS = 1_000_000;
			const audioClock = { getPlaybackPTS: () => audioPTS };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			// Draw from old source at PTS 1s
			buffer.addFrame(new MockVideoFrame(1_000_000) as unknown as VideoFrame);
			renderer.renderOnce();

			// Simulate transition: reset sync, then new source at PTS 5s
			renderer.resetSync();
			audioPTS = 5_000_000;

			buffer.addFrame(new MockVideoFrame(5_000_000) as unknown as VideoFrame);
			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(2);
			expect(diag.currentVideoPTS).toBe(5_000_000);
			expect(diag.currentAudioPTS).toBe(5_000_000);
			// AV sync should reflect only the new source (0ms delta)
			expect(diag.avSyncMs).toBe(0);
		});
	});

	describe('queue-pressure desync recovery', () => {
		it('recovers from 800ms offset via PTS discontinuity detection', () => {
			// 800ms > 500ms threshold triggers PTS discontinuity recovery —
			// re-anchors clock and draws instead of freezing.
			const audioClock = { getPlaybackPTS: () => 1_000_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			for (let i = 0; i < 8; i++) {
				buffer.addFrame(new MockVideoFrame(1_800_000 + i * 16_667) as unknown as VideoFrame);
			}

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
		});

		it('draws frames when video is within 100ms of audio (normal operation)', () => {
			// With server-side PTS alignment (SeedPTSFromVideo + frame counter),
			// video PTS should be within ~50ms of audio. Renderer draws normally.
			// Feed frames one-at-a-time to avoid triggering live-edge skip
			// (which correctly fires when >2 frames queue up at once).
			let audioPTS = 10_000_000;
			const audioClock = { getPlaybackPTS: () => audioPTS };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			const PTS_OFFSET = 50_000; // 50ms video-ahead (typical)

			// Simulate steady-state: add one frame, render, advance audio
			let totalDrawn = 0;
			for (let tick = 0; tick < 8; tick++) {
				const framePTS = audioPTS + PTS_OFFSET;
				buffer.addFrame(new MockVideoFrame(framePTS) as unknown as VideoFrame);
				renderer.renderOnce();
				audioPTS += 33_333; // advance audio by one frame interval
				const diag = renderer.getDiagnostics();
				totalDrawn = diag.framesDrawn;
			}

			// Should draw frames as audio catches up to video PTS
			expect(totalDrawn).toBeGreaterThanOrEqual(5);
		});
	});

	describe('live-edge skip', () => {
		it('skips to newest frame when buffer exceeds target depth', () => {
			const buffer = new VideoRenderBuffer();
			const closeFns: ReturnType<typeof vi.fn>[] = [];
			// Add 5 frames — exceeds target depth of 2
			for (let i = 0; i < 5; i++) {
				const close = vi.fn();
				closeFns.push(close);
				buffer.addFrame({
					timestamp: i * 33333,
					duration: 33333,
					displayWidth: 320,
					displayHeight: 240,
					close,
				} as unknown as VideoFrame);
			}

			const audioClock = { getPlaybackPTS: () => -1 };
			const canvas = createMockCanvas();
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			renderer.renderOnce();

			// Frames 0-3 should be closed (skipped), frame 4 displayed
			expect(closeFns[0]).toHaveBeenCalled();
			expect(closeFns[1]).toHaveBeenCalled();
			expect(closeFns[2]).toHaveBeenCalled();
			expect(closeFns[3]).toHaveBeenCalled();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
			expect(diag.liveEdgeSkips).toBeGreaterThan(0);
		});
	});

	describe('setTimeout fallback', () => {
		it('starts with fallback disabled', () => {
			const audioClock = { getPlaybackPTS: () => -1 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			const diag = renderer.getDiagnostics();
			expect(diag.useSetTimeoutFallback).toBe(false);
		});

		it('switches to setTimeout after 3 consecutive slow rAF intervals', () => {
			const audioClock = { getPlaybackPTS: () => -1 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			// Simulate 4 render ticks with 60ms gaps (> 50ms threshold).
			// First tick establishes _diagLastRafTime, ticks 2-4 each register
			// as slow (interval > 50ms), reaching the threshold of 3.
			const baseTime = 1000;
			const mockNow = vi.spyOn(performance, 'now');

			// Keep buffer populated so ticks don't exit early
			for (let i = 0; i < 10; i++) {
				buffer.addFrame(new MockVideoFrame(i * 33333) as unknown as VideoFrame);
			}

			for (let i = 0; i < 4; i++) {
				mockNow.mockReturnValue(baseTime + i * 60);
				renderer.renderOnce();
			}

			const diag = renderer.getDiagnostics();
			expect(diag.useSetTimeoutFallback).toBe(true);

			mockNow.mockRestore();
		});

		it('switches back to rAF after 5 consecutive normal intervals', () => {
			const audioClock = { getPlaybackPTS: () => -1 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			const mockNow = vi.spyOn(performance, 'now');

			// Keep buffer populated
			for (let i = 0; i < 20; i++) {
				buffer.addFrame(new MockVideoFrame(i * 33333) as unknown as VideoFrame);
			}

			// First: trigger the fallback with 4 slow ticks (60ms gaps)
			const baseTime = 1000;
			for (let i = 0; i < 4; i++) {
				mockNow.mockReturnValue(baseTime + i * 60);
				renderer.renderOnce();
			}
			expect(renderer.getDiagnostics().useSetTimeoutFallback).toBe(true);

			// Now simulate 6 fast ticks (16ms gaps) — need 5 consecutive
			// normal intervals to switch back. Tick 0 establishes the new
			// _diagLastRafTime, ticks 1-5 each register as normal.
			const fastBase = baseTime + 4 * 60;
			for (let i = 0; i < 6; i++) {
				mockNow.mockReturnValue(fastBase + i * 16);
				renderer.renderOnce();
			}

			expect(renderer.getDiagnostics().useSetTimeoutFallback).toBe(false);

			mockNow.mockRestore();
		});

		it('does not switch on isolated slow frames', () => {
			const audioClock = { getPlaybackPTS: () => -1 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			const mockNow = vi.spyOn(performance, 'now');

			for (let i = 0; i < 10; i++) {
				buffer.addFrame(new MockVideoFrame(i * 33333) as unknown as VideoFrame);
			}

			// Alternate: slow, fast, slow, fast — never 3 consecutive slow
			const times = [1000, 1060, 1076, 1136, 1152, 1212, 1228];
			for (const t of times) {
				mockNow.mockReturnValue(t);
				renderer.renderOnce();
			}

			expect(renderer.getDiagnostics().useSetTimeoutFallback).toBe(false);

			mockNow.mockRestore();
		});
	});

	describe('audio stall detection', () => {
		it('switches to stall-freerun when audio stalls for > 200ms', () => {
			let audioPTS = 1_000_000;
			const audioClock = { getPlaybackPTS: () => audioPTS };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			// Add frame and draw once to establish audio tracking
			buffer.addFrame(new MockVideoFrame(1_000_000) as unknown as VideoFrame);
			renderer.renderOnce();

			// Stall audio (same PTS) and advance wall clock by 250ms
			vi.spyOn(performance, 'now')
				.mockReturnValueOnce(performance.now() + 250);

			// Add frame for the stall-freerun to draw
			buffer.addFrame(new MockVideoFrame(1_033_000) as unknown as VideoFrame);
			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.clockMode).toBe('audio-stall-freerun');
		});
	});
});
