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
		it('draws frame when video is 400ms ahead of audio', () => {
			// Audio PTS is at 1,000,000 (1 second)
			// Video frame PTS is at 1,400,000 (1.4 seconds) — 400ms ahead
			const audioClock = { getPlaybackPTS: () => 1_000_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			buffer.addFrame(new MockVideoFrame(1_400_000) as unknown as VideoFrame);

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
			expect(diag.framesSkipped).toBe(0);
		});

		it('draws frame when video is 600ms ahead of audio', () => {
			// This is the key test: 600ms offset exceeds the old 500ms tolerance
			// but should work with the new tolerance
			const audioClock = { getPlaybackPTS: () => 1_000_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			buffer.addFrame(new MockVideoFrame(1_600_000) as unknown as VideoFrame);

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
			expect(diag.framesSkipped).toBe(0);
		});

		it('draws frame when video is 999ms ahead of audio', () => {
			// Edge case: almost 1 second ahead — should still work
			const audioClock = { getPlaybackPTS: () => 1_000_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			buffer.addFrame(new MockVideoFrame(1_999_000) as unknown as VideoFrame);

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(1);
			expect(diag.framesSkipped).toBe(0);
		});

		it('skips frame when video is 2+ seconds ahead (likely PTS discontinuity)', () => {
			// Very large offset suggests a PTS discontinuity (source change),
			// not audio pipeline latency — should NOT draw
			const audioClock = { getPlaybackPTS: () => 1_000_000 };
			const renderer = new PrismRenderer(canvas, buffer, audioClock);
			renderer.externallyDriven = true;

			buffer.addFrame(new MockVideoFrame(3_500_000) as unknown as VideoFrame);

			renderer.renderOnce();

			const diag = renderer.getDiagnostics();
			expect(diag.framesDrawn).toBe(0);
			expect(diag.framesSkipped).toBe(1);
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
