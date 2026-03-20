import { describe, it, expect } from 'vitest';
import { CompressedFrameQueue } from './compressed-frame-queue';

describe('CompressedFrameQueue', () => {
	it('push and drain returns correct frames up to target + lookahead', () => {
		const queue = new CompressedFrameQueue(1_000_000); // 1s max duration

		queue.push(new Uint8Array([1]), 100_000, false);
		queue.push(new Uint8Array([2]), 200_000, false);
		queue.push(new Uint8Array([3]), 300_000, false);
		queue.push(new Uint8Array([4]), 400_000, false);

		// Drain up to 250_000 with no lookahead
		const frames = queue.drain(250_000, 0);
		expect(frames).toHaveLength(2);
		expect(frames[0].timestamp).toBe(100_000);
		expect(frames[0].data).toEqual(new Uint8Array([1]));
		expect(frames[1].timestamp).toBe(200_000);
		expect(frames[1].data).toEqual(new Uint8Array([2]));

		// Remaining frames still in queue
		expect(queue.size()).toBe(2);

		// Drain with lookahead of 50_000 should include the 300_000 frame
		const frames2 = queue.drain(250_000, 50_000);
		expect(frames2).toHaveLength(1);
		expect(frames2[0].timestamp).toBe(300_000);

		expect(queue.size()).toBe(1);
	});

	it('drain returns empty when no frames qualify', () => {
		const queue = new CompressedFrameQueue(1_000_000);

		queue.push(new Uint8Array([1]), 500_000, false);
		queue.push(new Uint8Array([2]), 600_000, false);

		const frames = queue.drain(100_000, 0);
		expect(frames).toHaveLength(0);
		expect(queue.size()).toBe(2);
	});

	it('evicts oldest when queue exceeds max duration', () => {
		const queue = new CompressedFrameQueue(500_000); // 500ms max duration

		queue.push(new Uint8Array([1]), 100_000, false);
		queue.push(new Uint8Array([2]), 200_000, false);
		queue.push(new Uint8Array([3]), 300_000, false);
		// This push makes span = 600_000 - 100_000 = 500_000, which is within bounds
		queue.push(new Uint8Array([4]), 600_000, false);
		expect(queue.size()).toBe(4);

		// This push makes span = 700_000 - 100_000 = 600_000, exceeding 500_000
		// Should evict until span <= 500_000
		queue.push(new Uint8Array([5]), 700_000, false);
		// Evicts 100_000 (span becomes 700_000 - 200_000 = 500_000, within bounds)
		expect(queue.size()).toBe(4);
		expect(queue.oldestPTS()).toBe(200_000);
		expect(queue.newestPTS()).toBe(700_000);
	});

	it('flush clears all frames', () => {
		const queue = new CompressedFrameQueue(1_000_000);

		queue.push(new Uint8Array([1]), 100_000, false);
		queue.push(new Uint8Array([2]), 200_000, false);
		queue.push(new Uint8Array([3]), 300_000, false);

		expect(queue.size()).toBe(3);
		queue.flush();
		expect(queue.size()).toBe(0);
		expect(queue.oldestPTS()).toBe(-1);
		expect(queue.newestPTS()).toBe(-1);
	});

	it('preserves description for keyframes', () => {
		const queue = new CompressedFrameQueue(1_000_000);
		const avcC = new Uint8Array([0x01, 0x64, 0x00, 0x1f]);

		queue.push(new Uint8Array([1]), 100_000, true, avcC);
		queue.push(new Uint8Array([2]), 200_000, false);

		const frames = queue.drain(300_000, 0);
		expect(frames).toHaveLength(2);

		expect(frames[0].isKeyframe).toBe(true);
		expect(frames[0].description).toEqual(avcC);

		expect(frames[1].isKeyframe).toBe(false);
		expect(frames[1].description).toBeUndefined();
	});

	it('newestPTS and oldestPTS are correct', () => {
		const queue = new CompressedFrameQueue(1_000_000);

		expect(queue.oldestPTS()).toBe(-1);
		expect(queue.newestPTS()).toBe(-1);

		queue.push(new Uint8Array([1]), 300_000, false);
		expect(queue.oldestPTS()).toBe(300_000);
		expect(queue.newestPTS()).toBe(300_000);

		queue.push(new Uint8Array([2]), 100_000, false);
		expect(queue.oldestPTS()).toBe(100_000);
		expect(queue.newestPTS()).toBe(300_000);

		queue.push(new Uint8Array([3]), 500_000, false);
		expect(queue.oldestPTS()).toBe(100_000);
		expect(queue.newestPTS()).toBe(500_000);
	});

	it('drain is idempotent on empty queue', () => {
		const queue = new CompressedFrameQueue(1_000_000);

		const frames1 = queue.drain(100_000, 0);
		expect(frames1).toHaveLength(0);

		const frames2 = queue.drain(100_000, 0);
		expect(frames2).toHaveLength(0);

		expect(queue.size()).toBe(0);
	});

	it('drain returns frames in PTS order even if pushed out of order', () => {
		const queue = new CompressedFrameQueue(1_000_000);

		queue.push(new Uint8Array([3]), 300_000, false);
		queue.push(new Uint8Array([1]), 100_000, false);
		queue.push(new Uint8Array([2]), 200_000, false);

		const frames = queue.drain(400_000, 0);
		expect(frames).toHaveLength(3);
		expect(frames[0].timestamp).toBe(100_000);
		expect(frames[1].timestamp).toBe(200_000);
		expect(frames[2].timestamp).toBe(300_000);
	});

	it('handles exact boundary for drain target', () => {
		const queue = new CompressedFrameQueue(1_000_000);

		queue.push(new Uint8Array([1]), 100_000, false);
		queue.push(new Uint8Array([2]), 200_000, false);

		// Target exactly equals a frame's PTS -- should include it
		const frames = queue.drain(100_000, 0);
		expect(frames).toHaveLength(1);
		expect(frames[0].timestamp).toBe(100_000);
	});
});
