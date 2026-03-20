import { describe, it, expect, vi } from 'vitest';
import { VideoRenderBuffer } from './video-render-buffer';

function makeFrame(timestamp: number, duration = 33333): VideoFrame {
	return {
		timestamp,
		duration,
		displayWidth: 320,
		displayHeight: 240,
		close: vi.fn(),
	} as unknown as VideoFrame;
}

describe('VideoRenderBuffer', () => {
	describe('takeNewestFrame', () => {
		it('returns null on empty buffer', () => {
			const buf = new VideoRenderBuffer();
			const result = buf.takeNewestFrame();
			expect(result.frame).toBeNull();
			expect(result.discarded).toBe(0);
		});

		it('returns sole frame when buffer has one', () => {
			const buf = new VideoRenderBuffer();
			const f = makeFrame(1000);
			buf.addFrame(f);
			const result = buf.takeNewestFrame();
			expect(result.frame).toBe(f);
			expect(result.discarded).toBe(0);
			expect(result.queueSize).toBe(0);
		});

		it('returns newest and closes all older frames', () => {
			const buf = new VideoRenderBuffer();
			const f1 = makeFrame(1000);
			const f2 = makeFrame(2000);
			const f3 = makeFrame(3000);
			buf.addFrame(f1);
			buf.addFrame(f2);
			buf.addFrame(f3);

			const result = buf.takeNewestFrame();
			expect(result.frame).toBe(f3);
			expect(result.discarded).toBe(2);
			expect(f1.close).toHaveBeenCalled();
			expect(f2.close).toHaveBeenCalled();
			expect(f3.close).not.toHaveBeenCalled();
			expect(result.queueSize).toBe(0);
		});

		it('updates totalDiscarded counter', () => {
			const buf = new VideoRenderBuffer();
			buf.addFrame(makeFrame(1000));
			buf.addFrame(makeFrame(2000));
			buf.addFrame(makeFrame(3000));
			const result = buf.takeNewestFrame();
			expect(result.totalDiscarded).toBe(2);
		});

		it('updates totalLengthMs correctly', () => {
			const buf = new VideoRenderBuffer();
			buf.addFrame(makeFrame(1000, 33333));
			buf.addFrame(makeFrame(2000, 33333));
			buf.addFrame(makeFrame(3000, 33333));
			const result = buf.takeNewestFrame();
			expect(result.queueLengthMs).toBe(0);
		});
	});

	describe('existing addFrame/getFrameByTimestamp', () => {
		it('evicts oldest when full', () => {
			const buf = new VideoRenderBuffer();
			const frames: VideoFrame[] = [];
			// MAX_ELEMENTS is 60; adding 61 triggers eviction of oldest
			for (let i = 0; i < 61; i++) {
				frames.push(makeFrame(i * 1000));
				buf.addFrame(frames[i]);
			}
			expect(frames[0].close).toHaveBeenCalled();
			expect(buf.getStats().queueSize).toBe(60);
		});
	});
});
