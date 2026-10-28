import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createMediaPipeline, type SourceDiagnostics, type MediaPipelineConfig } from './media-pipeline';
import { isSourceStalled, clearAllStalls, clearSourceStall } from './source-errors.svelte';

// Mock all Prism modules since they use Web Workers, WebCodecs,
// AudioWorklet, and SharedArrayBuffer -- none available in jsdom.

const mockVideoDecoderInstances: Array<{ flush: ReturnType<typeof vi.fn>; reset: ReturnType<typeof vi.fn> }> = [];

vi.mock('$lib/prism/video-decoder', () => {
	return {
		PrismVideoDecoder: class MockPrismVideoDecoder {
			preload = vi.fn();
			configure = vi.fn();
			decode = vi.fn();
			reset = vi.fn();
			flush = vi.fn();
			getDiagnostics = vi.fn().mockResolvedValue({});
			constructor() {
				mockVideoDecoderInstances.push(this);
			}
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

const mockAudioDecoderInstances: Array<{ configure: ReturnType<typeof vi.fn> }> = [];

vi.mock('$lib/prism/audio-decoder', () => {
	return {
		PrismAudioDecoder: class MockPrismAudioDecoder {
			constructor() {
				mockAudioDecoderInstances.push(this);
			}
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

const mockTransports: Array<{ key: string; callbacks: { onTrackInfo(tracks: unknown[]): void } }> = [];

vi.mock('$lib/prism/moq-transport', () => {
	return {
		MoQTransport: class MockMoQTransport {
			constructor(public key: string, public callbacks: { onTrackInfo(tracks: unknown[]): void }) {
				mockTransports.push(this);
			}
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
		mockVideoDecoderInstances.length = 0;
	});

	it('should create a pipeline', () => {
		const pipeline = createMediaPipeline();
		expect(pipeline).toBeDefined();
	});

	it('should create a pipeline with config', () => {
		const onControlState = vi.fn();
		const pipeline = createMediaPipeline({ onControlState });
		expect(pipeline).toBeDefined();
	});

	it('should create a pipeline with empty config', () => {
		const pipeline = createMediaPipeline({});
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

	it('should disconnect source transport and flush decoder', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.connectSource('cam1');

		const decoder = mockVideoDecoderInstances[0];
		expect(decoder.flush).not.toHaveBeenCalled();

		pipeline.disconnectSource('cam1');

		expect(decoder.flush).toHaveBeenCalledTimes(1);
	});

	it('should tolerate decoder flush error during disconnect', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.connectSource('cam1');

		const decoder = mockVideoDecoderInstances[0];
		decoder.flush.mockImplementation(() => { throw new Error('decoder closed'); });

		// Should not throw even when flush fails
		pipeline.disconnectSource('cam1');
		expect(decoder.flush).toHaveBeenCalledTimes(1);
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

	// A context per source made one tab open an audio output stream and an
	// audio thread per source and engine.
	it('decodes the audio of every source into one AudioContext', () => {
		const contexts: Array<{ close: ReturnType<typeof vi.fn> }> = [];
		class FakeAudioContext {
			state = 'running';
			suspend = vi.fn().mockResolvedValue(undefined);
			close = vi.fn().mockResolvedValue(undefined);
			constructor() { contexts.push(this); }
		}
		vi.stubGlobal('AudioContext', FakeAudioContext);
		try {
			mockTransports.length = 0;
			mockAudioDecoderInstances.length = 0;
			const pipeline = createMediaPipeline();
			for (const key of ['cam1', 'cam2']) {
				pipeline.addSource(key);
				pipeline.connectSource(key);
			}
			const audio = [{ type: 'audio', codec: 'mp4a.40.2', sampleRate: 48000, channels: 2 }];
			for (const t of mockTransports) t.callbacks.onTrackInfo(audio);

			expect(contexts).toHaveLength(1);
			const ctxArgs = mockAudioDecoderInstances.map((d) => d.configure.mock.calls[0][3]);
			expect(ctxArgs).toEqual([contexts[0], contexts[0]]);
			pipeline.destroy();
			expect(contexts[0].close).toHaveBeenCalledTimes(1);
		} finally {
			vi.unstubAllGlobals();
		}
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
		// Transport getDiagnostics was called (mock returns {} + group skip fields)
		expect(diag['cam1'].transport).toEqual({ groupSkips: 0, latestGroupID: 0 });
	});
});

describe('MediaPipeline stall detection', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		clearAllStalls();
	});

	afterEach(() => {
		vi.useRealTimers();
		clearAllStalls();
	});

	it('should not mark source as stalled when frames arrive regularly', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');

		pipeline.startStallDetection();

		// Feed a frame
		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('cam1', new Uint8Array([1, 2, 3]), true, 1000, desc);

		// Advance 1s (under 1.5s threshold)
		vi.advanceTimersByTime(1000);

		expect(isSourceStalled('cam1')).toBe(false);

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	it('should mark source as stalled after 1.5s without frames', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');

		// Feed initial frame to establish lastFrameTime
		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('cam1', new Uint8Array([1, 2, 3]), true, 1000, desc);

		pipeline.startStallDetection();

		// Advance past stall threshold (1.5s) + one check interval (500ms)
		vi.advanceTimersByTime(2000);

		expect(isSourceStalled('cam1')).toBe(true);

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	it('should clear stall when frame arrives after stall', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');

		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('cam1', new Uint8Array([1, 2, 3]), true, 1000, desc);

		pipeline.startStallDetection();

		// Stall: no frames for 2s (exceeds 1.5s threshold)
		vi.advanceTimersByTime(2000);
		expect(isSourceStalled('cam1')).toBe(true);

		// Resume: feed another frame
		pipeline.feedVideoFrame('cam1', new Uint8Array([4, 5, 6]), false, 2000, null);
		expect(isSourceStalled('cam1')).toBe(false);

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	it('should not report stalls for removed sources', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');

		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('cam1', new Uint8Array([1, 2, 3]), true, 1000, desc);

		pipeline.startStallDetection();

		// Remove source before stall triggers
		pipeline.removeSource('cam1');

		// Advance past threshold
		vi.advanceTimersByTime(4000);

		expect(isSourceStalled('cam1')).toBe(false);

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	// Stall flags are shared by source key across pipelines. During a
	// failover engine A's program-preview is disconnected while engine B's
	// is fed; B's frames clear the shared flag, and A's stale arrival time
	// must not then report a fresh stall that sends video back to A.
	it('should not report stalls for disconnected sources', () => {
		const onSourceStalled = vi.fn();
		const pipeline = createMediaPipeline({ onSourceStalled });
		pipeline.addSource('program-preview');

		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('program-preview', new Uint8Array([1, 2, 3]), true, 1000, desc);

		pipeline.startStallDetection();
		vi.advanceTimersByTime(2000);
		expect(onSourceStalled).toHaveBeenCalledTimes(1);

		pipeline.disconnectSource('program-preview');
		clearSourceStall('program-preview');
		vi.advanceTimersByTime(4000);

		expect(onSourceStalled).toHaveBeenCalledTimes(1);
		expect(isSourceStalled('program-preview')).toBe(false);

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	// After a restore engine A's program-preview is reconnected. If it still
	// delivers nothing, the stall must be reported so video can leave it.
	it('should report a stall when a reconnected source stays silent', () => {
		const onSourceStalled = vi.fn();
		const pipeline = createMediaPipeline({ onSourceStalled });
		pipeline.addSource('program-preview');

		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('program-preview', new Uint8Array([1, 2, 3]), true, 1000, desc);
		pipeline.startStallDetection();

		pipeline.disconnectSource('program-preview');
		vi.advanceTimersByTime(4000);
		expect(onSourceStalled).not.toHaveBeenCalled();

		// The reconnect gets 3 s for its handshakes, then the 1.5 s window.
		pipeline.connectSource('program-preview');
		vi.advanceTimersByTime(4000);
		expect(onSourceStalled).not.toHaveBeenCalled();
		vi.advanceTimersByTime(1000);
		expect(onSourceStalled).toHaveBeenCalledWith('program-preview');

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	it('should not arm stall detection for a source that never delivered a frame', () => {
		const onSourceStalled = vi.fn();
		const pipeline = createMediaPipeline({ onSourceStalled });
		pipeline.addSource('program-preview');
		pipeline.startStallDetection();

		pipeline.connectSource('program-preview');
		pipeline.disconnectSource('program-preview');
		pipeline.connectSource('program-preview');
		vi.advanceTimersByTime(4000);

		expect(onSourceStalled).not.toHaveBeenCalled();

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	it('should stop stall detection on destroy', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');

		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('cam1', new Uint8Array([1, 2, 3]), true, 1000, desc);

		pipeline.startStallDetection();
		pipeline.destroy();

		// Advance timers — should not throw or set stalls
		vi.advanceTimersByTime(10000);
		expect(isSourceStalled('cam1')).toBe(false);
	});

	it('should track multiple sources independently', () => {
		const pipeline = createMediaPipeline();
		pipeline.addSource('cam1');
		pipeline.addSource('cam2');

		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('cam1', new Uint8Array([1]), true, 1000, desc);
		pipeline.feedVideoFrame('cam2', new Uint8Array([2]), true, 1000, desc);

		pipeline.startStallDetection();

		// Advance 1s, feed cam1 only (cam2 at 1s, under 1.5s threshold)
		vi.advanceTimersByTime(1000);
		pipeline.feedVideoFrame('cam1', new Uint8Array([3]), false, 2000, null);

		// Advance another 1s — cam2 total 2s > 1.5s (stalled), cam1 at 1s (ok)
		vi.advanceTimersByTime(1000);

		expect(isSourceStalled('cam1')).toBe(false);
		expect(isSourceStalled('cam2')).toBe(true);

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	it('should be idempotent for startStallDetection', () => {
		const pipeline = createMediaPipeline();
		pipeline.startStallDetection();
		pipeline.startStallDetection(); // should not create second interval
		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	it('should invoke onSourceStalled callback when a source stalls', () => {
		const onSourceStalled = vi.fn();
		const pipeline = createMediaPipeline({ onSourceStalled });
		pipeline.addSource('program');

		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('program', new Uint8Array([1, 2, 3]), true, 1000, desc);

		pipeline.startStallDetection();

		// Advance past stall threshold (1.5s) + check interval (500ms)
		vi.advanceTimersByTime(2000);

		expect(onSourceStalled).toHaveBeenCalledWith('program');

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	it('should invoke onSourceStalled for each stalled source independently', () => {
		const onSourceStalled = vi.fn();
		const pipeline = createMediaPipeline({ onSourceStalled });
		pipeline.addSource('cam1');
		pipeline.addSource('cam2');

		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('cam1', new Uint8Array([1]), true, 1000, desc);
		pipeline.feedVideoFrame('cam2', new Uint8Array([2]), true, 1000, desc);

		pipeline.startStallDetection();

		// Feed cam1 at 1s to keep it alive
		vi.advanceTimersByTime(1000);
		pipeline.feedVideoFrame('cam1', new Uint8Array([3]), false, 2000, null);

		// At 2s total, cam2 has been stale for 2s > 1.5s (stalled), cam1 only 1s (ok)
		vi.advanceTimersByTime(1000);

		expect(onSourceStalled).toHaveBeenCalledWith('cam2');
		expect(onSourceStalled).not.toHaveBeenCalledWith('cam1');

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	it('should invoke onSourceStalled callback only once per stall event', () => {
		const onSourceStalled = vi.fn();
		const pipeline = createMediaPipeline({ onSourceStalled });
		pipeline.addSource('cam1');

		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('cam1', new Uint8Array([1]), true, 1000, desc);

		pipeline.startStallDetection();

		// Stall fires at first check past threshold (1.5s)
		vi.advanceTimersByTime(2000);
		expect(onSourceStalled).toHaveBeenCalledTimes(1);

		// Additional check intervals while still stalled should NOT fire again
		vi.advanceTimersByTime(2000);
		expect(onSourceStalled).toHaveBeenCalledTimes(1);

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	it('should not invoke onSourceStalled callback after source resumes', () => {
		const onSourceStalled = vi.fn();
		const pipeline = createMediaPipeline({ onSourceStalled });
		pipeline.addSource('cam1');

		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('cam1', new Uint8Array([1]), true, 1000, desc);

		pipeline.startStallDetection();

		// Stall (1.5s threshold)
		vi.advanceTimersByTime(2000);
		expect(onSourceStalled).toHaveBeenCalledTimes(1);

		// Resume with a new frame
		pipeline.feedVideoFrame('cam1', new Uint8Array([2]), false, 2000, null);
		onSourceStalled.mockClear();

		// Advance 1s — under 1.5s threshold since last frame
		vi.advanceTimersByTime(1000);
		expect(onSourceStalled).not.toHaveBeenCalled();

		pipeline.stopStallDetection();
		pipeline.destroy();
	});

	it('should fire onSourceStalled again after recovery and re-stall', () => {
		const onSourceStalled = vi.fn();
		const pipeline = createMediaPipeline({ onSourceStalled });
		pipeline.addSource('cam1');

		const desc = new Uint8Array([0, 0, 0, 1]);
		pipeline.feedVideoFrame('cam1', new Uint8Array([1]), true, 1000, desc);

		pipeline.startStallDetection();

		// First stall (1.5s threshold)
		vi.advanceTimersByTime(2000);
		expect(onSourceStalled).toHaveBeenCalledTimes(1);

		// Recover
		pipeline.feedVideoFrame('cam1', new Uint8Array([2]), false, 2000, null);

		// Second stall
		vi.advanceTimersByTime(2000);
		expect(onSourceStalled).toHaveBeenCalledTimes(2);

		pipeline.stopStallDetection();
		pipeline.destroy();
	});
});
