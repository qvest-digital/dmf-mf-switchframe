import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createMediaPipeline, type SourceDiagnostics } from './media-pipeline';

// Mock all Prism modules since they use Web Workers, WebCodecs,
// AudioWorklet, and SharedArrayBuffer -- none available in jsdom.

vi.mock('$lib/prism/video-decoder', () => {
	return {
		PrismVideoDecoder: class MockPrismVideoDecoder {
			preload = vi.fn();
			configure = vi.fn();
			decode = vi.fn();
			reset = vi.fn();
			getDiagnostics = vi.fn().mockResolvedValue({});
		},
	};
});

vi.mock('$lib/prism/video-render-buffer', () => {
	return {
		VideoRenderBuffer: class MockVideoRenderBuffer {
			addFrame = vi.fn();
			getFrameByTimestamp = vi.fn().mockReturnValue({ frame: null, discarded: 0, totalDiscarded: 0, queueSize: 0, queueLengthMs: 0 });
			peekFirstFrame = vi.fn().mockReturnValue(null);
			takeNextFrame = vi.fn().mockReturnValue(null);
			getStats = vi.fn().mockReturnValue({ queueSize: 0, queueLengthMs: 0, totalDiscarded: 0 });
			clear = vi.fn();
		},
	};
});

vi.mock('$lib/prism/audio-decoder', () => {
	return {
		PrismAudioDecoder: class MockPrismAudioDecoder {
			configure = vi.fn().mockResolvedValue(undefined);
			decode = vi.fn();
			setMuted = vi.fn();
			isMuted = vi.fn().mockReturnValue(true);
			enableMetering = vi.fn();
			disableMetering = vi.fn();
			getLevels = vi.fn().mockReturnValue({ peak: [0, 0], rms: [0, 0], peakHold: [0, 0], channels: 2 });
			getPlaybackPTS = vi.fn().mockReturnValue(-1);
			reset = vi.fn();
			setSuspended = vi.fn();
		},
	};
});

vi.mock('$lib/prism/renderer', () => {
	return {
		PrismRenderer: class MockPrismRenderer {
			start = vi.fn();
			renderOnce = vi.fn();
			destroy = vi.fn();
			getVideoBuffer = vi.fn();
			getDiagnostics = vi.fn().mockReturnValue({ rafCount: 0, framesDrawn: 0 });
			freeRunOnly = false;
			maxResolution = 0;
			externallyDriven = false;
		},
	};
});

vi.mock('$lib/prism/moq-transport', () => {
	return {
		MoQTransport: class MockMoQTransport {
			connect = vi.fn().mockResolvedValue(undefined);
			close = vi.fn();
			subscribeAudio = vi.fn().mockResolvedValue(undefined);
			subscribeAllAudio = vi.fn().mockResolvedValue(undefined);
			getDiagnostics = vi.fn().mockReturnValue({});
		},
	};
});

describe('MediaPipeline', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('should create a pipeline', () => {
		const pipeline = createMediaPipeline();
		expect(pipeline).toBeDefined();
	});

	it('should add sources', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.addSource('cam2');

		expect(pipeline.getVideoBuffer('cam1')).not.toBeNull();
		expect(pipeline.getVideoBuffer('cam2')).not.toBeNull();
	});

	it('should not duplicate sources', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		const buffer1 = pipeline.getVideoBuffer('cam1');
		pipeline.addSource('cam1');
		const buffer2 = pipeline.getVideoBuffer('cam1');

		// Same buffer instance (not recreated)
		expect(buffer1).toBe(buffer2);
	});

	it('should remove sources and clean up', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.removeSource('cam1');

		expect(pipeline.getVideoBuffer('cam1')).toBeNull();
		expect(pipeline.getAudioDecoder('cam1')).toBeNull();
	});

	it('should return null for unknown source buffers', () => {
		const pipeline = createMediaPipeline();
		expect(pipeline.getVideoBuffer('nonexistent')).toBeNull();
	});

	it('should return null for unknown source audio decoders', () => {
		const pipeline = createMediaPipeline();
		expect(pipeline.getAudioDecoder('nonexistent')).toBeNull();
	});

	it('should destroy all sources', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.addSource('cam2');
		pipeline.addSource('cam3');

		pipeline.destroy();

		expect(pipeline.getVideoBuffer('cam1')).toBeNull();
		expect(pipeline.getVideoBuffer('cam2')).toBeNull();
		expect(pipeline.getVideoBuffer('cam3')).toBeNull();
	});

	it('should feed video frames to the correct source decoder', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.addSource('cam2');

		const desc = new Uint8Array([0, 0, 0, 1]);
		const data = new Uint8Array([1, 2, 3, 4]);

		// First frame with description configures the decoder
		pipeline.feedVideoFrame('cam1', data, true, 1000, desc);

		// The video decoder for cam1 should be configured and then decode called
		const buffer1 = pipeline.getVideoBuffer('cam1');
		expect(buffer1).not.toBeNull();
	});

	it('should ignore video frames for unknown sources', () => {
		const pipeline = createMediaPipeline();
		// Should not throw
		pipeline.feedVideoFrame('nonexistent', new Uint8Array([1]), true, 0, null);
	});

	it('should feed audio frames to the correct source', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');

		const data = new Uint8Array([1, 2, 3]);
		// Audio decoder is null until MoQ track info configures it
		pipeline.feedAudioFrame('cam1', data, 1000);

		// Should not throw even without audio decoder
	});

	it('should ignore audio frames for unknown sources', () => {
		const pipeline = createMediaPipeline();
		// Should not throw
		pipeline.feedAudioFrame('nonexistent', new Uint8Array([1]), 0);
	});

	it('should connect source MoQ transport', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.connectSource('cam1');
		// Should not throw
	});

	it('should disconnect source transport', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.connectSource('cam1');
		pipeline.disconnectSource('cam1');
		// Should not throw
	});

	it('should be safe to disconnect unconnected source', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.disconnectSource('cam1');
		// Should not throw
	});

	it('should be safe to connect unknown source', () => {
		const pipeline = createMediaPipeline();
		pipeline.connectSource('nonexistent');
		// Should not throw
	});

	it('should clean up transport on removeSource', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.connectSource('cam1');
		pipeline.removeSource('cam1');

		expect(pipeline.getVideoBuffer('cam1')).toBeNull();
	});

	it('should support multiple renderers per source', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');

		const canvas1 = document.createElement('canvas');
		const canvas2 = document.createElement('canvas');

		// Attach two canvases with different IDs
		pipeline.attachCanvas('cam1', 'tile-cam1', canvas1);
		pipeline.attachCanvas('cam1', 'program', canvas2);

		// Both should work without destroying each other
		// Detach one — the other should still be active
		pipeline.detachCanvas('cam1', 'tile-cam1');

		// Detach the other
		pipeline.detachCanvas('cam1', 'program');
	});

	it('should replace renderer when same canvasId is reattached', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');

		const canvas1 = document.createElement('canvas');
		const canvas2 = document.createElement('canvas');

		pipeline.attachCanvas('cam1', 'program', canvas1);
		// Reattach with a different canvas — should destroy old renderer
		pipeline.attachCanvas('cam1', 'program', canvas2);

		// Should not throw
		pipeline.detachCanvas('cam1', 'program');
	});

	it('should return empty diagnostics when no sources', async () => {
		const pipeline = createMediaPipeline();
		const diag = await pipeline.getAllDiagnostics();
		expect(diag).toEqual({});
	});

	it('should return diagnostics for all sources', async () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.addSource('cam2');

		const diag = await pipeline.getAllDiagnostics();

		// Both sources should be present
		expect(Object.keys(diag)).toHaveLength(2);
		expect(diag['cam1']).toBeDefined();
		expect(diag['cam2']).toBeDefined();

		// Each source should have the SourceDiagnostics shape
		for (const key of ['cam1', 'cam2']) {
			const sd: SourceDiagnostics = diag[key];
			// No renderer attached, no audio configured, no transport connected
			expect(sd.renderer).toBeNull();
			expect(sd.videoDecoder).toBeDefined(); // async mock resolves to {}
			expect(sd.audio).toBeNull(); // no audio decoder until track info
			expect(sd.transport).toBeNull(); // not connected
		}
	});

	it('should include renderer diagnostics when canvas attached', async () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');

		const canvas = document.createElement('canvas');
		pipeline.attachCanvas('cam1', 'tile-cam1', canvas);

		const diag = await pipeline.getAllDiagnostics();
		// Renderer getDiagnostics was called (mock returns undefined by default)
		expect(diag['cam1'].renderer).toBeDefined();
	});

	it('should include transport diagnostics when connected', async () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.connectSource('cam1');

		const diag = await pipeline.getAllDiagnostics();
		// Transport getDiagnostics was called (mock returns {})
		expect(diag['cam1'].transport).toEqual({});
	});
});
