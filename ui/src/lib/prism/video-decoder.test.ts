import { describe, it, expect, vi, beforeEach } from 'vitest';
import { PrismVideoDecoder } from './video-decoder';

// Mock the Worker constructor — jsdom doesn't support Web Workers.
class MockWorker {
	onmessage: ((e: MessageEvent) => void) | null = null;
	postMessage = vi.fn();
	terminate = vi.fn();
}

vi.stubGlobal('Worker', function(this: MockWorker) {
	Object.assign(this, new MockWorker());
	return this;
} as unknown as typeof Worker);

// Mock VideoRenderBuffer
class MockVideoRenderBuffer {
	private frames: { timestamp: number }[] = [];
	addFrame = vi.fn((frame: { timestamp: number }) => {
		this.frames.push(frame);
		return true;
	});
	clear = vi.fn(() => { this.frames = []; });
	getFrameCount() { return this.frames.length; }
	getFrames() { return this.frames; }
	getStats = vi.fn().mockReturnValue({ queueSize: 0, queueLengthMs: 0, totalDiscarded: 0 });
	getFrameByTimestamp = vi.fn().mockReturnValue({ frame: null, discarded: 0, totalDiscarded: 0, queueSize: 0, queueLengthMs: 0 });
	peekFirstFrame = vi.fn().mockReturnValue(null);
	takeNextFrame = vi.fn().mockReturnValue(null);
}

describe('PrismVideoDecoder', () => {
	let renderBuffer: MockVideoRenderBuffer;

	beforeEach(() => {
		vi.clearAllMocks();
		renderBuffer = new MockVideoRenderBuffer();
	});

	it('should not clear render buffer on reconfigure', () => {
		const decoder = new PrismVideoDecoder(renderBuffer as any);

		// First configure
		decoder.configure('avc1.42C01E', 320, 240);
		expect(renderBuffer.clear).not.toHaveBeenCalled();

		// Simulate frames being added to buffer
		renderBuffer.addFrame({ timestamp: 1000 });
		renderBuffer.addFrame({ timestamp: 2000 });
		renderBuffer.addFrame({ timestamp: 3000 });
		expect(renderBuffer.getFrameCount()).toBe(3);

		// Reconfigure — should NOT clear the buffer
		decoder.configure('avc1.640028', 1920, 1080);
		expect(renderBuffer.clear).not.toHaveBeenCalled();
		expect(renderBuffer.getFrameCount()).toBe(3);
	});

	it('should clear render buffer on reset', () => {
		const decoder = new PrismVideoDecoder(renderBuffer as any);
		decoder.configure('avc1.42C01E', 320, 240);
		renderBuffer.addFrame({ timestamp: 1000 });

		decoder.reset();
		expect(renderBuffer.clear).toHaveBeenCalled();
	});

	it('should reuse worker on reconfigure (no terminate)', () => {
		const decoder = new PrismVideoDecoder(renderBuffer as any);

		decoder.configure('avc1.42C01E', 320, 240);

		// Get reference to the worker created during first configure
		const workerRef = (decoder as any).worker;
		expect(workerRef).not.toBeNull();

		decoder.configure('avc1.640028', 1920, 1080);

		// Worker terminate should NOT have been called
		expect(workerRef.terminate).not.toHaveBeenCalled();
		// Same worker instance should be reused
		expect((decoder as any).worker).toBe(workerRef);
	});

	describe('diagnostics', () => {
		it('should return empty diagnostics when no worker', async () => {
			const decoder = new PrismVideoDecoder(renderBuffer as any);
			const diag = await decoder.getDiagnostics();

			expect(diag.lifetimeInputCount).toBe(0);
			expect(diag.lifetimeOutputCount).toBe(0);
			expect(diag.lifetimeKeyframeCount).toBe(0);
			expect(diag.lifetimeDecodeErrors).toBe(0);
			expect(diag.lifetimeDiscardedDelta).toBe(0);
			expect(diag.lifetimeDiscardedBufferFull).toBe(0);
			expect(diag.lifetimeConfigureCount).toBe(0);
			expect(diag.lifetimeConfigGuardDrops).toBe(0);
		});
	});
});
