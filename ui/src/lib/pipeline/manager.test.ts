import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { PipelineManager } from './manager';
import type { MediaPipeline } from '$lib/transport/media-pipeline';

// Mock createMediaPipeline so Engine B tests don't need real WebTransport.
vi.mock('$lib/transport/media-pipeline', async (importOriginal) => {
	const original = await importOriginal<typeof import('$lib/transport/media-pipeline')>();
	return {
		...original,
		createMediaPipeline: vi.fn(() => createMockPipeline()),
	};
});
import { createMediaPipeline } from '$lib/transport/media-pipeline';

/** Create a mock MediaPipeline with all methods stubbed. */
function createMockPipeline() {
	return {
		addSource: vi.fn(),
		removeSource: vi.fn(),
		connectSource: vi.fn(),
		disconnectSource: vi.fn(),
		getAudioDecoder: vi.fn().mockReturnValue(null),
		attachCanvas: vi.fn().mockReturnValue(true),
		detachCanvas: vi.fn(),
		destroy: vi.fn(),
		feedVideoFrame: vi.fn(),
		feedAudioFrame: vi.fn(),
		setSourceMuted: vi.fn(),
		setSourceGain: vi.fn(),
		resumeAllAudio: vi.fn().mockResolvedValue(undefined),
		getAllDiagnostics: vi.fn().mockResolvedValue({}),
		resetRendererSync: vi.fn(),
		hasSource: vi.fn().mockReturnValue(false),
		getSourceTransport: vi.fn().mockReturnValue(null),
		startStallDetection: vi.fn(),
		stopStallDetection: vi.fn(),
		getLastFrameTime: vi.fn().mockReturnValue(0),
	} satisfies MediaPipeline;
}

/** Create a minimal sources record for testing. */
function makeSources(...keys: string[]): Record<string, { key: string; type: string; status: string }> {
	const sources: Record<string, { key: string; type: string; status: string }> = {};
	for (const key of keys) {
		sources[key] = { key, type: 'demo', status: 'healthy' };
	}
	return sources;
}

/** Create a mock canvas element. */
function createCanvas(): HTMLCanvasElement {
	return document.createElement('canvas');
}

describe('PipelineManager', () => {
	let pipeline: ReturnType<typeof createMockPipeline>;
	let manager: PipelineManager;
	let sourceKeys: string[];

	beforeEach(() => {
		pipeline = createMockPipeline();
		sourceKeys = [];
		manager = new PipelineManager(pipeline, () => sourceKeys);
		// Stub getElementById for tile canvases (still uses DOM lookup)
		vi.spyOn(document, 'getElementById').mockImplementation((id: string) => {
			const canvas = document.createElement('canvas');
			canvas.id = id;
			return canvas;
		});
	});

	afterEach(() => {
		manager.destroy();
		vi.restoreAllMocks();
	});

	describe('syncSources', () => {
		it('should add new sources to the pipeline', async () => {
			await manager.syncSources(makeSources('cam1', 'cam2'));

			expect(pipeline.addSource).toHaveBeenCalledWith('cam1');
			expect(pipeline.addSource).toHaveBeenCalledWith('cam2');
			expect(pipeline.connectSource).toHaveBeenCalledWith('cam1');
			expect(pipeline.connectSource).toHaveBeenCalledWith('cam2');
		});

		it('should not re-add sources that are already connected', async () => {
			await manager.syncSources(makeSources('cam1'));
			pipeline.addSource.mockClear();
			pipeline.connectSource.mockClear();

			await manager.syncSources(makeSources('cam1'));

			expect(pipeline.addSource).not.toHaveBeenCalled();
			expect(pipeline.connectSource).not.toHaveBeenCalled();
		});

		it('should remove stale sources no longer in state', async () => {
			await manager.syncSources(makeSources('cam1', 'cam2'));
			pipeline.removeSource.mockClear();

			// cam2 is gone
			await manager.syncSources(makeSources('cam1'));

			expect(pipeline.removeSource).toHaveBeenCalledWith('cam2');
			expect(pipeline.removeSource).not.toHaveBeenCalledWith('cam1');
		});

		it('should attach tile canvases for new sources', async () => {
			await manager.syncSources(makeSources('cam1'));

			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam1',
				'tile-cam1',
				expect.any(HTMLCanvasElement),
			);
		});

		it('should not re-attach tile canvases that are already attached', async () => {
			await manager.syncSources(makeSources('cam1'));
			pipeline.attachCanvas.mockClear();

			await manager.syncSources(makeSources('cam1'));

			// tile-cam1 should not be re-attached
			const tileCalls = pipeline.attachCanvas.mock.calls.filter(
				(c: unknown[]) => c[1] === 'tile-cam1',
			);
			expect(tileCalls).toHaveLength(0);
		});

		it('should clean up canvas tracking when source is removed', async () => {
			await manager.syncSources(makeSources('cam1'));
			pipeline.attachCanvas.mockClear();

			// Remove cam1
			await manager.syncSources(makeSources());

			expect(pipeline.removeSource).toHaveBeenCalledWith('cam1');

			// Re-add cam1 — should re-attach canvas since tracking was cleared
			pipeline.addSource.mockClear();
			await manager.syncSources(makeSources('cam1'));

			expect(pipeline.addSource).toHaveBeenCalledWith('cam1');
			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam1',
				'tile-cam1',
				expect.any(HTMLCanvasElement),
			);
		});

		it('should handle canvas not found in DOM gracefully', async () => {
			vi.spyOn(document, 'getElementById').mockReturnValue(null);

			await manager.syncSources(makeSources('cam1'));

			// Source is added but canvas is not attached
			expect(pipeline.addSource).toHaveBeenCalledWith('cam1');
			expect(pipeline.attachCanvas).not.toHaveBeenCalled();
		});
	});

	describe('syncProgramPreviewCanvases', () => {
		it('should attach program canvas to program-preview source when provided', () => {
			// UI renders exclusively from program-preview; full-quality
			// `program` is never subscribed from the browser.
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'program-preview',
				'program',
				pgmCanvas,
			);
		});

		it('never subscribes to or attaches the full-quality `program` source', () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			// The UI must not touch the full-quality `program` MoQ source in
			// any way — no addSource, no connectSource, no attachCanvas.
			const programSourceCalls = pipeline.attachCanvas.mock.calls.filter(
				(c: unknown[]) => c[0] === 'program',
			);
			expect(programSourceCalls).toHaveLength(0);
			expect(pipeline.addSource).not.toHaveBeenCalledWith('program');
			expect(pipeline.connectSource).not.toHaveBeenCalledWith('program');
		});

		it('should attach preview canvas for the preview source', () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam1',
				'preview',
				pvwCanvas,
			);
		});

		it('should not re-attach program canvas if already attached', () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);
			pipeline.attachCanvas.mockClear();

			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			const programCalls = pipeline.attachCanvas.mock.calls.filter(
				(c: unknown[]) => c[0] === 'program-preview' && c[1] === 'program',
			);
			expect(programCalls).toHaveLength(0);
		});

		it('should detach old preview and attach new on preview source change', () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);
			pipeline.detachCanvas.mockClear();
			pipeline.attachCanvas.mockClear();

			manager.syncProgramPreviewCanvases('cam2', pgmCanvas, pvwCanvas);

			// Detach old preview source
			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam1', 'preview');
			// Attach new preview source
			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam2',
				'preview',
				pvwCanvas,
			);
		});

		it('should handle empty preview source', () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);
			pipeline.detachCanvas.mockClear();
			pipeline.attachCanvas.mockClear();

			manager.syncProgramPreviewCanvases('', pgmCanvas, pvwCanvas);

			// Should detach old preview
			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam1', 'preview');
			// Should not attach with empty source
			const previewAttach = pipeline.attachCanvas.mock.calls.filter(
				(c: unknown[]) => c[1] === 'preview',
			);
			expect(previewAttach).toHaveLength(0);
		});

		it('should not attach when canvas elements are not provided', () => {
			manager.syncProgramPreviewCanvases('cam1');

			// No canvas provided, so nothing should be attached
			expect(pipeline.attachCanvas).not.toHaveBeenCalled();
		});

		it('should not attach when canvas elements are null', () => {
			manager.syncProgramPreviewCanvases('cam1', null, null);

			// Null canvases, so nothing should be attached
			expect(pipeline.attachCanvas).not.toHaveBeenCalled();
		});

		it('should handle program canvas provided without preview canvas', () => {
			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, null);

			// Program-preview should be attached to the program canvas slot.
			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'program-preview',
				'program',
				pgmCanvas,
			);
			// Preview should not be attached (no canvas)
			const previewCalls = pipeline.attachCanvas.mock.calls.filter(
				(c: unknown[]) => c[1] === 'preview',
			);
			expect(previewCalls).toHaveLength(0);
		});

		it('should handle preview canvas provided without program canvas', () => {
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', null, pvwCanvas);

			// Program should not be attached (no canvas)
			const programCalls = pipeline.attachCanvas.mock.calls.filter(
				(c: unknown[]) => c[1] === 'program',
			);
			expect(programCalls).toHaveLength(0);
			// Preview should be attached
			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam1',
				'preview',
				pvwCanvas,
			);
		});

		it('should detach old source and attach new source when switching preview', () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();

			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			// Switch to cam2 — should detach cam1 and attach cam2
			manager.syncProgramPreviewCanvases('cam2', pgmCanvas, pvwCanvas);

			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam1', 'preview');
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam2', 'preview', pvwCanvas);
		});

		it('should re-attach preview when canvas element changes (layout switch)', () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas1 = createCanvas();

			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas1);
			pipeline.attachCanvas.mockClear();
			pipeline.detachCanvas.mockClear();

			// Layout switch creates a new DOM canvas element for the same source
			const pvwCanvas2 = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas2);

			// Should detach old canvas and re-attach with new element
			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam1', 'preview');
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam1', 'preview', pvwCanvas2);
		});

		it('should retry preview attachment when source not yet in pipeline', () => {
			// Simulate the race condition: syncProgramPreviewCanvases is called
			// before syncSources adds the preview source to the pipeline.
			// attachCanvas returns false (source doesn't exist yet).
			pipeline.attachCanvas.mockReturnValue(false);

			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam2', pgmCanvas, pvwCanvas);

			// attachCanvas was attempted but failed
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam2', 'preview', pvwCanvas);

			// Now simulate syncSources adding cam2, then calling sync again.
			// attachCanvas succeeds this time.
			pipeline.attachCanvas.mockReturnValue(true);
			pipeline.attachCanvas.mockClear();

			manager.syncProgramPreviewCanvases('cam2', pgmCanvas, pvwCanvas);

			// Should retry the attachment (not skip due to stale tracking)
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam2', 'preview', pvwCanvas);
		});

		it('should retry pending preview via syncSources after failed attachment', async () => {
			// Simulate: syncProgramPreviewCanvases fails because source not in pipeline
			pipeline.attachCanvas.mockReturnValue(false);

			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			// Preview attachment failed, stored as pending
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam1', 'preview', pvwCanvas);

			// Now syncSources adds the source — should retry pending preview
			pipeline.attachCanvas.mockReturnValue(true);
			pipeline.attachCanvas.mockClear();

			await manager.syncSources(makeSources('cam1'));

			// syncSources should retry the pending preview attachment
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam1', 'preview', pvwCanvas);
		});

		it('should not record source key when called without canvas elements', () => {
			// Simulate: tick callback runs before SimpleMode's $effect provides canvas refs
			manager.syncProgramPreviewCanvases('cam1', null, null);

			// Now call with actual canvas refs — should trigger attachment
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			// Should attach because tracking was null, not 'cam1'
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam1', 'preview', pvwCanvas);
		});
	});

	describe('onLayoutChange', () => {
		it('should detach all tile canvases', async () => {
			await manager.syncSources(makeSources('cam1', 'cam2'));
			pipeline.detachCanvas.mockClear();

			manager.onLayoutChange();

			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam1', 'tile-cam1');
			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam2', 'tile-cam2');
		});

		it('should detach program canvas', async () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);
			pipeline.detachCanvas.mockClear();

			manager.onLayoutChange();

			expect(pipeline.detachCanvas).toHaveBeenCalledWith('program-preview', 'program');
		});

		it('should detach preview canvas', async () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);
			pipeline.detachCanvas.mockClear();

			manager.onLayoutChange();

			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam1', 'preview');
		});

		it('should reset canvas tracking so re-sync re-attaches', async () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			await manager.syncSources(makeSources('cam1'));
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);
			pipeline.attachCanvas.mockClear();

			manager.onLayoutChange();

			// Re-sync should re-attach everything (with new canvas refs)
			const newPgm = createCanvas();
			const newPvw = createCanvas();
			await manager.syncSources(makeSources('cam1'));
			manager.syncProgramPreviewCanvases('cam1', newPgm, newPvw);

			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam1',
				'tile-cam1',
				expect.any(HTMLCanvasElement),
			);
			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'program-preview',
				'program',
				newPgm,
			);
			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam1',
				'preview',
				newPvw,
			);
		});
	});

	describe('metering', () => {
		let rafCallbacks: FrameRequestCallback[];

		beforeEach(() => {
			rafCallbacks = [];
			vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
				rafCallbacks.push(cb);
				return rafCallbacks.length;
			});
			vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
		});

		it('should start metering loop with requestAnimationFrame', () => {
			sourceKeys = ['cam1'];
			manager.startMetering();

			expect(window.requestAnimationFrame).toHaveBeenCalled();
		});

		it('should stop metering loop', () => {
			manager.startMetering();
			manager.stopMetering();

			expect(window.cancelAnimationFrame).toHaveBeenCalled();
		});

		it('should return empty levels before metering starts', () => {
			const levels = manager.getLevels();
			expect(levels.sourceLevels).toEqual({});
			expect(levels.programLevels).toEqual({ peakL: 0, peakR: 0 });
		});

		it('should sample audio decoder levels on each frame', () => {
			sourceKeys = ['cam1'];
			const mockDecoder = {
				getLevels: vi.fn().mockReturnValue({ peak: [0.5, 0.7], rms: [0, 0], peakHold: [0, 0], channels: 2 }),
			};
			pipeline.getAudioDecoder.mockImplementation((key: string) => {
				if (key === 'cam1') return mockDecoder;
				return null;
			});

			manager.startMetering();

			// Execute the rAF callback
			expect(rafCallbacks.length).toBeGreaterThan(0);
			rafCallbacks[0](performance.now());

			const levels = manager.getLevels();
			expect(levels.sourceLevels['cam1']).toEqual({ peakL: 0.5, peakR: 0.7 });
		});

		it('should sample program audio decoder levels', () => {
			sourceKeys = [];
			const mockProgramDecoder = {
				getLevels: vi.fn().mockReturnValue({ peak: [0.8, 0.6], rms: [0, 0], peakHold: [0, 0], channels: 2 }),
			};
			pipeline.getAudioDecoder.mockImplementation((key: string) => {
				if (key === 'program-preview') return mockProgramDecoder;
				return null;
			});

			manager.startMetering();

			// Execute rAF callback
			rafCallbacks[0](performance.now());

			const levels = manager.getLevels();
			expect(levels.programLevels).toEqual({ peakL: 0.8, peakR: 0.6 });
		});

		it('should schedule next frame after each callback', () => {
			sourceKeys = [];
			manager.startMetering();

			// Execute first callback
			rafCallbacks[0](performance.now());

			// Should have scheduled another rAF
			expect(rafCallbacks.length).toBe(2);
		});

		it('should not double-start metering', () => {
			manager.startMetering();
			manager.startMetering();

			// Only one rAF should be requested
			expect(rafCallbacks.length).toBe(1);
		});

		it('should invoke onLevelsUpdate callback on each rAF tick', () => {
			const onLevelsUpdate = vi.fn();
			const mgr = new PipelineManager(pipeline, () => ['cam1'], onLevelsUpdate);

			const mockDecoder = {
				getLevels: vi.fn().mockReturnValue({ peak: [0.5, 0.7], rms: [0, 0], peakHold: [0, 0], channels: 2 }),
			};
			pipeline.getAudioDecoder.mockImplementation((key: string) => {
				if (key === 'cam1') return mockDecoder;
				return null;
			});

			mgr.startMetering();
			rafCallbacks[0](performance.now());

			expect(onLevelsUpdate).toHaveBeenCalledWith(
				{ cam1: { peakL: 0.5, peakR: 0.7 } },
				{ peakL: 0, peakR: 0 },
			);
			mgr.destroy();
		});
	});

	describe('destroy', () => {
		it('should stop metering on destroy', () => {
			vi.spyOn(window, 'requestAnimationFrame').mockReturnValue(42);
			vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});

			manager.startMetering();
			manager.destroy();

			expect(window.cancelAnimationFrame).toHaveBeenCalledWith(42);
		});

		it('should reset all tracking state', async () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			await manager.syncSources(makeSources('cam1'));
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			manager.destroy();

			// After destroy, a new sync should treat everything as fresh
			pipeline.addSource.mockClear();
			pipeline.attachCanvas.mockClear();

			await manager.syncSources(makeSources('cam1'));

			expect(pipeline.addSource).toHaveBeenCalledWith('cam1');
		});
	});

	describe('Engine B pipeline', () => {
		const mockedCreateMediaPipeline = vi.mocked(createMediaPipeline);

		beforeEach(() => {
			mockedCreateMediaPipeline.mockClear();
		});

		it('createEngineBPipeline creates secondary pipeline', () => {
			manager.createEngineBPipeline('https://engine-b.example.com');
			expect(manager.hasEngineBPipeline).toBe(true);
			expect(mockedCreateMediaPipeline).toHaveBeenCalledWith({ engineBaseUrl: 'https://engine-b.example.com' });
		});

		it('createEngineBPipeline passes onControlState callback', () => {
			const onControlState = vi.fn();
			manager.createEngineBPipeline('https://engine-b.example.com', { onControlState });
			expect(mockedCreateMediaPipeline).toHaveBeenCalledWith({
				engineBaseUrl: 'https://engine-b.example.com',
				onControlState,
			});
		});

		it('destroyEngineBPipeline cleans up', () => {
			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			manager.destroyEngineBPipeline();
			expect(manager.hasEngineBPipeline).toBe(false);
			expect(bPipeline.destroy).toHaveBeenCalled();
		});

		it('createEngineBPipeline with identical URL is idempotent', () => {
			manager.createEngineBPipeline('https://engine-b.example.com');
			manager.createEngineBPipeline('https://engine-b.example.com');
			expect(manager.hasEngineBPipeline).toBe(true);
			expect(mockedCreateMediaPipeline).toHaveBeenCalledTimes(1);
		});

		it('createEngineBPipeline with new URL rebuilds the pipeline', () => {
			// Engine B re-provisioned at a new URL — the existing pipelineB
			// points at the dead URL and must be torn down and rebuilt.
			manager.createEngineBPipeline('https://engine-b-old.example.com');
			const oldPipeline = mockedCreateMediaPipeline.mock.results[0].value;

			manager.createEngineBPipeline('https://engine-b-new.example.com');

			expect(oldPipeline.destroy).toHaveBeenCalled();
			expect(mockedCreateMediaPipeline).toHaveBeenCalledTimes(2);
			expect(mockedCreateMediaPipeline).toHaveBeenNthCalledWith(2, {
				engineBaseUrl: 'https://engine-b-new.example.com',
			});
			expect(manager.hasEngineBPipeline).toBe(true);
		});

		it('createEngineBPipeline URL replacement mid-failover re-attaches program canvas', async () => {
			// Edge case: Engine B dies while we are actively failed over to
			// it. Both engines briefly unreachable, control plane provisions
			// a replacement B, cpDiscovery surfaces the new URL, addEngine
			// fires createEngineBPipeline with the new URL. The destroyed
			// pipelineB had the program canvas attached — it must be
			// re-attached to the new pipelineB or the operator sees a
			// permanently black program.
			sourceKeys = ['cam1'];
			await manager.syncSources(makeSources('cam1'));

			const programCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', programCanvas, null);

			// Warm standby + failover to B.
			manager.enableWarmStandby('https://engine-b-old.example.com', ['cam1']);
			const oldPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			// Make old pipeline appear ready for failover.
			oldPipeline.getAudioDecoder.mockReturnValue({});
			await manager.failoverToEngineB('https://engine-b-old.example.com', ['cam1']);
			expect(manager.activeEngine).toBe('B');

			// B is re-provisioned at a new URL — cpDiscovery path triggers
			// createEngineBPipeline(newUrl). The rebuild must re-attach the
			// program canvas to the fresh pipelineB.
			manager.createEngineBPipeline('https://engine-b-new.example.com');

			const newPipeline = mockedCreateMediaPipeline.mock.results[1].value;
			// Re-attach program canvas to program-preview (UI never connects to
			// full-quality `program`; that channel is for SRT/record output).
			expect(newPipeline.attachCanvas).toHaveBeenCalledWith('program-preview', 'program', programCanvas);
			// Tile canvases also re-attach from the attachedCanvases set.
			expect(newPipeline.attachCanvas).toHaveBeenCalledWith('cam1', 'tile-cam1', expect.any(HTMLCanvasElement));
		});

		it('enableWarmStandby with new URL rebuilds pipelineB and re-connects sources', () => {
			manager.enableWarmStandby('https://engine-b-old.example.com', ['cam1', 'cam2']);
			const oldPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			expect(oldPipeline.connectSource).toHaveBeenCalledWith('cam1');
			expect(oldPipeline.connectSource).toHaveBeenCalledWith('cam2');

			manager.enableWarmStandby('https://engine-b-new.example.com', ['cam1', 'cam2']);

			expect(oldPipeline.destroy).toHaveBeenCalled();
			const newPipeline = mockedCreateMediaPipeline.mock.results[1].value;
			// Source tiles + program-preview must be connected on the new pipeline.
			expect(newPipeline.addSource).toHaveBeenCalledWith('cam1');
			expect(newPipeline.connectSource).toHaveBeenCalledWith('cam1');
			expect(newPipeline.addSource).toHaveBeenCalledWith('cam2');
			expect(newPipeline.connectSource).toHaveBeenCalledWith('cam2');
			expect(newPipeline.addSource).toHaveBeenCalledWith('program-preview');
		});

		it('connectSourceB adds and connects source on Engine B', () => {
			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			manager.connectSourceB('cam1');
			expect(bPipeline.addSource).toHaveBeenCalledWith('cam1');
			expect(bPipeline.connectSource).toHaveBeenCalledWith('cam1');
		});

		it('connectSourceB is no-op without Engine B pipeline', () => {
			// Should not throw
			manager.connectSourceB('cam1');
			expect(manager.hasEngineBPipeline).toBe(false);
		});

		it('disconnectSourceB removes source from Engine B', () => {
			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			manager.disconnectSourceB('cam1');
			expect(bPipeline.removeSource).toHaveBeenCalledWith('cam1');
		});

		it('disconnectSourceB is no-op without Engine B pipeline', () => {
			manager.disconnectSourceB('cam1');
			expect(manager.hasEngineBPipeline).toBe(false);
		});

		it('attachCanvasB attaches canvas to Engine B source', () => {
			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			const canvas = createCanvas();
			const result = manager.attachCanvasB('cam1', 'preview-b', canvas);
			expect(result).toBe(true);
			expect(bPipeline.attachCanvas).toHaveBeenCalledWith('cam1', 'preview-b', canvas);
		});

		it('attachCanvasB returns false without Engine B pipeline', () => {
			const canvas = createCanvas();
			const result = manager.attachCanvasB('cam1', 'preview-b', canvas);
			expect(result).toBe(false);
		});

		it('detachCanvasB detaches canvas from Engine B source', () => {
			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			manager.detachCanvasB('cam1', 'preview-b');
			expect(bPipeline.detachCanvas).toHaveBeenCalledWith('cam1', 'preview-b');
		});

		it('detachCanvasB is no-op without Engine B pipeline', () => {
			// Should not throw
			manager.detachCanvasB('cam1', 'preview-b');
		});

		it('destroy cleans up Engine B pipeline', () => {
			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			manager.destroy();
			expect(bPipeline.destroy).toHaveBeenCalled();
			expect(manager.hasEngineBPipeline).toBe(false);
		});
	});

	describe('switchProgramToEngineB / switchProgramToEngineA', () => {
		const mockedCreateMediaPipeline = vi.mocked(createMediaPipeline);

		beforeEach(() => {
			mockedCreateMediaPipeline.mockClear();
		});

		it('switchProgramToEngineB moves program canvas to Engine B', () => {
			// Set up: program canvas attached on Engine A
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);
			pipeline.detachCanvas.mockClear();

			// Create Engine B pipeline
			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			manager.connectSourceB('program-preview');

			// Switch to Engine B
			manager.switchProgramToEngineB();

			// Should detach from Engine A's program-preview (UI-visible program feed)
			expect(pipeline.detachCanvas).toHaveBeenCalledWith('program-preview', 'program');
			// Should attach to Engine B's program-preview
			expect(bPipeline.attachCanvas).toHaveBeenCalledWith('program-preview', 'program', pgmCanvas);
		});

		it('switchProgramToEngineA moves program canvas back to Engine A', () => {
			// Set up: program canvas, then switch to B, then back to A
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			manager.connectSourceB('program');
			manager.switchProgramToEngineB();

			pipeline.detachCanvas.mockClear();
			pipeline.attachCanvas.mockClear();
			bPipeline.detachCanvas.mockClear();

			// Switch back to Engine A
			manager.switchProgramToEngineA();

			// Should detach from Engine B's program-preview
			expect(bPipeline.detachCanvas).toHaveBeenCalledWith('program-preview', 'program');
			// Should re-attach to Engine A's program-preview
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('program-preview', 'program', pgmCanvas);
		});

		it('switchProgramToEngineB is no-op without Engine B pipeline', () => {
			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, null);
			pipeline.detachCanvas.mockClear();

			// No Engine B pipeline — should not throw or detach
			manager.switchProgramToEngineB();
			expect(pipeline.detachCanvas).not.toHaveBeenCalled();
		});

		it('switchProgramToEngineA is no-op without Engine B pipeline', () => {
			// Should not throw
			manager.switchProgramToEngineA();
		});

		it('switchProgramToEngineB is no-op without program canvas', () => {
			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			manager.connectSourceB('program-preview');

			// No program canvas was ever set
			manager.switchProgramToEngineB();
			expect(bPipeline.attachCanvas).not.toHaveBeenCalled();
		});

		it('switchProgramToEngineA always attaches program-preview', () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			manager.connectSourceB('program-preview');
			manager.switchProgramToEngineB();

			bPipeline.detachCanvas.mockClear();
			pipeline.attachCanvas.mockClear();

			manager.switchProgramToEngineA();

			expect(bPipeline.detachCanvas).toHaveBeenCalledWith('program-preview', 'program');
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('program-preview', 'program', pgmCanvas);
		});

		// The toggle moves only the program view. Disconnecting Engine A's
		// other sources left preview and tiles on a dead transport, frozen
		// until the operator switched back to A.
		it('switchProgramToEngineB keeps Engine A preview and tiles connected', async () => {
			await manager.syncSources(makeSources('cam1', 'cam2'));
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam2', pgmCanvas, pvwCanvas);
			manager.createEngineBPipeline('https://engine-b.example.com');
			manager.connectSourceB('program-preview');
			pipeline.disconnectSource.mockClear();
			pipeline.detachCanvas.mockClear();

			manager.switchProgramToEngineB();

			expect(pipeline.disconnectSource.mock.calls).toEqual([['program-preview']]);
			expect(pipeline.detachCanvas).not.toHaveBeenCalledWith('cam2', 'preview');

			// Back to A reconnects the program feed; connectSource on the
			// sources that stayed connected is a no-op in the pipeline.
			pipeline.connectSource.mockClear();
			manager.switchProgramToEngineA();
			expect(pipeline.connectSource).toHaveBeenCalledWith('program-preview');
		});

		// The A/B toggle moves only the program canvas: the preview stays
		// on Engine A.
		it('keeps the preview on Engine A while toggled to Engine B', async () => {
			await manager.syncSources(makeSources('cam1', 'cam2'));
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam2', createCanvas(), pvwCanvas);
			manager.createEngineBPipeline('https://engine-b.example.com');
			const b = mockedCreateMediaPipeline.mock.results[0].value;
			manager.connectSourceB('program-preview');
			manager.switchProgramToEngineB();
			pipeline.attachCanvas.mockClear();

			manager.syncProgramPreviewCanvases('cam1', null, pvwCanvas);

			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam1', 'preview', pvwCanvas);
			expect(b.attachCanvas).not.toHaveBeenCalledWith('cam1', 'preview', pvwCanvas);
		});

		it('activeEngine getter reflects current state', () => {
			expect(manager.activeEngine).toBe('A');

			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, null);
			manager.createEngineBPipeline('https://engine-b.example.com');
			manager.connectSourceB('program-preview');

			manager.switchProgramToEngineB();
			expect(manager.activeEngine).toBe('B');

			manager.switchProgramToEngineA();
			expect(manager.activeEngine).toBe('A');
		});
	});

	describe('failoverToEngineB / restoreToEngineA', () => {
		const mockedCreateMediaPipeline = vi.mocked(createMediaPipeline);

		beforeEach(() => {
			vi.useFakeTimers();
			mockedCreateMediaPipeline.mockClear();
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		// A failover moves everything to Engine B, so Engine A's sources
		// are no longer shown and must stop decoding headless.
		it('failoverToEngineB disconnects Engine A sources', async () => {
			await manager.syncSources(makeSources('cam1', 'cam2'));
			manager.syncProgramPreviewCanvases('cam1', createCanvas(), createCanvas());
			const bPipelineMock = createMockPipeline();
			bPipelineMock.getAudioDecoder.mockImplementation((key: string) =>
				key === 'program-preview' ? ({ getLevels: vi.fn().mockReturnValue({ peak: [0, 0] }) } as any) : null);
			mockedCreateMediaPipeline.mockReturnValueOnce(bPipelineMock);
			pipeline.disconnectSource.mockClear();

			const failoverPromise = manager.failoverToEngineB('https://engine-b:8080', ['cam1', 'cam2']);
			await vi.advanceTimersByTimeAsync(250);
			await failoverPromise;

			const disconnected = pipeline.disconnectSource.mock.calls.map((c: unknown[]) => c[0]);
			expect(disconnected).toEqual(expect.arrayContaining(['program-preview', 'cam1', 'cam2']));
		});

		it('failoverToEngineB creates pipeline B and switches activeEngine', async () => {
			// Set up: program canvas on Engine A
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			// Add sources to track attached canvases
			await manager.syncSources(makeSources('cam1', 'cam2'));

			// Make the mock pipeline B return a ready audio decoder for
			// program-preview (the browser-visible program feed).
			const bPipelineMock = createMockPipeline();
			bPipelineMock.getAudioDecoder.mockImplementation((key: string) => {
				if (key === 'program-preview') return { getLevels: vi.fn().mockReturnValue({ peak: [0, 0] }) } as any;
				return null;
			});
			mockedCreateMediaPipeline.mockReturnValueOnce(bPipelineMock);

			expect(manager.activeEngine).toBe('A');

			const failoverPromise = manager.failoverToEngineB('https://engine-b:8080', ['cam1', 'cam2']);
			// Advance timers to let readiness polling resolve
			await vi.advanceTimersByTimeAsync(250);
			await failoverPromise;

			expect(manager.activeEngine).toBe('B');
			expect(manager.hasEngineBPipeline).toBe(true);

			// Verify Engine B has source tiles + program-preview connected.
			// Full-quality `program` is never subscribed from the browser.
			expect(bPipelineMock.addSource).toHaveBeenCalledWith('cam1');
			expect(bPipelineMock.addSource).toHaveBeenCalledWith('cam2');
			expect(bPipelineMock.addSource).toHaveBeenCalledWith('program-preview');
			expect(bPipelineMock.connectSource).toHaveBeenCalledWith('program-preview');
			expect(bPipelineMock.addSource).not.toHaveBeenCalledWith('program');
			expect(bPipelineMock.connectSource).not.toHaveBeenCalledWith('program');
		});

		it('restoreToEngineA switches back and destroys pipeline B', async () => {
			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, null);
			await manager.syncSources(makeSources('cam1'));

			// Failover to B first
			const bPipelineMock = createMockPipeline();
			bPipelineMock.getAudioDecoder.mockReturnValue({ getLevels: vi.fn().mockReturnValue({ peak: [0, 0] }) } as any);
			mockedCreateMediaPipeline.mockReturnValueOnce(bPipelineMock);

			const failoverPromise = manager.failoverToEngineB('https://engine-b:8080', ['cam1']);
			await vi.advanceTimersByTimeAsync(250);
			await failoverPromise;
			expect(manager.activeEngine).toBe('B');

			// Now restore to A
			await manager.restoreToEngineA(['cam1']);

			expect(manager.activeEngine).toBe('A');
			expect(manager.hasEngineBPipeline).toBe(false);
			expect(bPipelineMock.destroy).toHaveBeenCalled();
		});

		it('failoverToEngineB is idempotent when already on B', async () => {
			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, null);

			const bPipelineMock = createMockPipeline();
			bPipelineMock.getAudioDecoder.mockReturnValue({ getLevels: vi.fn().mockReturnValue({ peak: [0, 0] }) } as any);
			mockedCreateMediaPipeline.mockReturnValueOnce(bPipelineMock);

			const p1 = manager.failoverToEngineB('https://engine-b:8080', ['cam1']);
			await vi.advanceTimersByTimeAsync(250);
			await p1;
			expect(manager.activeEngine).toBe('B');

			// Second failover should be a no-op (returns immediately)
			mockedCreateMediaPipeline.mockClear();
			await manager.failoverToEngineB('https://engine-b:8080', ['cam1']);

			expect(manager.activeEngine).toBe('B');
			// Should NOT have created another pipeline
			expect(mockedCreateMediaPipeline).not.toHaveBeenCalled();
		});

		it('failoverToEngineB passes onControlState callback to pipeline creation', async () => {
			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, null);

			const bPipelineMock = createMockPipeline();
			bPipelineMock.getAudioDecoder.mockReturnValue({ getLevels: vi.fn().mockReturnValue({ peak: [0, 0] }) } as any);
			mockedCreateMediaPipeline.mockReturnValueOnce(bPipelineMock);

			const onControlState = vi.fn();
			const failoverPromise = manager.failoverToEngineB('https://engine-b:8080', ['cam1'], { onControlState });
			await vi.advanceTimersByTimeAsync(250);
			await failoverPromise;

			expect(mockedCreateMediaPipeline).toHaveBeenCalledWith({
				engineBaseUrl: 'https://engine-b:8080',
				onControlState,
			});
		});

		it('restoreToEngineA is idempotent when already on A', async () => {
			expect(manager.activeEngine).toBe('A');

			// Should be a no-op
			await manager.restoreToEngineA(['cam1', 'cam2']);

			expect(manager.activeEngine).toBe('A');
		});

		it('failoverToEngineB times out and proceeds when pipeline B is slow', async () => {
			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, null);

			// Pipeline B never becomes ready (getAudioDecoder always returns null)
			const bPipelineMock = createMockPipeline();
			mockedCreateMediaPipeline.mockReturnValueOnce(bPipelineMock);

			const failoverPromise = manager.failoverToEngineB('https://engine-b:8080', ['cam1']);

			// Advance past the 3-second timeout
			await vi.advanceTimersByTimeAsync(3200);
			await failoverPromise;

			// Should still complete (timeout proceeds anyway)
			expect(manager.activeEngine).toBe('B');
		});

		it('failoverToEngineB swaps tile canvases to pipeline B', async () => {
			// Set up pipeline A with tile canvases
			await manager.syncSources(makeSources('cam1'));

			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, null);

			const bPipelineMock = createMockPipeline();
			bPipelineMock.getAudioDecoder.mockReturnValue({ getLevels: vi.fn().mockReturnValue({ peak: [0, 0] }) } as any);
			mockedCreateMediaPipeline.mockReturnValueOnce(bPipelineMock);

			const failoverPromise = manager.failoverToEngineB('https://engine-b:8080', ['cam1']);
			await vi.advanceTimersByTimeAsync(250);
			await failoverPromise;

			// Pipeline A should have tile detached
			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam1', 'tile-cam1');
			// Pipeline B should have tile attached
			expect(bPipelineMock.attachCanvas).toHaveBeenCalledWith(
				'cam1',
				'tile-cam1',
				expect.any(HTMLCanvasElement),
			);
		});

		/** Fail over with sources cam1 and cam2, preview on cam2. */
		async function failOver(pgmCanvas: HTMLCanvasElement, pvwCanvas: HTMLCanvasElement) {
			await manager.syncSources(makeSources('cam1', 'cam2'));
			manager.syncProgramPreviewCanvases('cam2', pgmCanvas, pvwCanvas);
			const b = createMockPipeline();
			b.getAudioDecoder.mockReturnValue({ getLevels: vi.fn().mockReturnValue({ peak: [0, 0] }) } as any);
			mockedCreateMediaPipeline.mockReturnValueOnce(b);
			const done = manager.failoverToEngineB('https://engine-b:8080', ['cam1', 'cam2']);
			await vi.advanceTimersByTimeAsync(250);
			await done;
			pipeline.attachCanvas.mockClear();
			pipeline.detachCanvas.mockClear();
			pipeline.connectSource.mockClear();
			b.attachCanvas.mockClear();
			b.detachCanvas.mockClear();
			return b;
		}

		// After a failover the preview attached on Engine A, whose sources
		// are disconnected, and the Engine B renderer of the old preview
		// source went on drawing onto the preview canvas: the preview showed
		// a source other than the engine's preview.
		it('moves the preview on Engine B after a failover', async () => {
			const pvwCanvas = createCanvas();
			const b = await failOver(createCanvas(), pvwCanvas);

			manager.syncProgramPreviewCanvases('cam1', null, pvwCanvas);

			expect(b.detachCanvas).toHaveBeenCalledWith('cam2', 'preview');
			expect(b.attachCanvas).toHaveBeenCalledWith('cam1', 'preview', pvwCanvas);
			expect(pipeline.attachCanvas).not.toHaveBeenCalled();
		});

		it('attaches a new source tile on Engine B after a failover', async () => {
			const b = await failOver(createCanvas(), createCanvas());
			b.addSource.mockClear();
			b.connectSource.mockClear();

			await manager.syncSources(makeSources('cam1', 'cam2', 'cam3'));

			expect(b.addSource).toHaveBeenCalledWith('cam3');
			expect(b.connectSource).toHaveBeenCalledWith('cam3');
			expect(b.attachCanvas).toHaveBeenCalledWith('cam3', 'tile-cam3', expect.any(HTMLCanvasElement));
			expect(pipeline.attachCanvas).not.toHaveBeenCalled();
			expect(pipeline.connectSource).not.toHaveBeenCalled();
		});

		it('re-attaches a replaced program canvas on Engine B after a failover', async () => {
			const b = await failOver(createCanvas(), createCanvas());
			const newPgm = createCanvas();

			manager.syncProgramPreviewCanvases('cam2', newPgm, null);

			expect(b.detachCanvas).toHaveBeenCalledWith('program-preview', 'program');
			expect(b.attachCanvas).toHaveBeenCalledWith('program-preview', 'program', newPgm);
			expect(pipeline.attachCanvas).not.toHaveBeenCalled();
		});

		it('moves the preview back on Engine A after restoring to it', async () => {
			const pvwCanvas = createCanvas();
			await failOver(createCanvas(), pvwCanvas);
			await manager.restoreToEngineA(['cam1', 'cam2']);
			pipeline.attachCanvas.mockClear();

			manager.syncProgramPreviewCanvases('cam1', null, pvwCanvas);

			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam1', 'preview', pvwCanvas);
		});

		// Without an Engine B pipeline the failover builds one; with one,
		// it needs Engine B's program feed to deliver.
		it('refuses a stall failover only while an Engine B pipeline delivers nothing', () => {
			expect(manager.refusesStallFailover()).toBe(false);
			manager.createEngineBPipeline('https://engine-b:8080');
			const b = mockedCreateMediaPipeline.mock.results[0].value;
			vi.advanceTimersByTime(10_000);

			b.getLastFrameTime.mockReturnValue(0);
			expect(manager.refusesStallFailover()).toBe(true);
			b.getLastFrameTime.mockReturnValue(performance.now() - 1000);
			expect(manager.refusesStallFailover()).toBe(false);
			b.getLastFrameTime.mockReturnValue(performance.now() - 2000);
			expect(manager.refusesStallFailover()).toBe(true);
		});

		// A program stall that Engine B shares is this browser or its
		// network; failing over to B would only move it.
		it('engineBDelivering reports whether Engine B program frames arrive', () => {
			expect(manager.engineBDelivering(1500)).toBe(false);
			manager.createEngineBPipeline('https://engine-b:8080');
			const b = mockedCreateMediaPipeline.mock.results[0].value;
			vi.advanceTimersByTime(10_000);
			const now = performance.now();

			b.getLastFrameTime.mockReturnValue(now - 1000);
			expect(manager.engineBDelivering(1500)).toBe(true);
			b.getLastFrameTime.mockReturnValue(now - 2000);
			expect(manager.engineBDelivering(1500)).toBe(false);
			b.getLastFrameTime.mockReturnValue(0);
			expect(manager.engineBDelivering(1500)).toBe(false);
			expect(b.getLastFrameTime).toHaveBeenCalledWith('program-preview');
		});

		// Destroying Engine B's pipeline while it shows the program, as when
		// Engine B stays offline, left Engine A's sources disconnected and
		// the program canvas on nothing.
		it('moves every canvas back to Engine A when Engine B is destroyed after a failover', async () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			const b = await failOver(pgmCanvas, pvwCanvas);

			manager.destroyEngineBPipeline();

			expect(manager.activeEngine).toBe('A');
			expect(b.destroy).toHaveBeenCalled();
			const connected = pipeline.connectSource.mock.calls.map((c: unknown[]) => c[0]);
			expect(connected).toEqual(expect.arrayContaining(['program-preview', 'cam1', 'cam2']));
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('program-preview', 'program', pgmCanvas);
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam2', 'preview', pvwCanvas);
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam1', 'tile-cam1', expect.any(HTMLCanvasElement));

			// A later preview change lands on Engine A.
			pipeline.attachCanvas.mockClear();
			manager.syncProgramPreviewCanvases('cam1', null, pvwCanvas);
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('cam1', 'preview', pvwCanvas);
		});

		it('moves the program back to Engine A when Engine B is destroyed while toggled to it', async () => {
			await manager.syncSources(makeSources('cam1'));
			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, createCanvas());
			manager.createEngineBPipeline('https://engine-b.example.com');
			manager.connectSourceB('program-preview');
			manager.switchProgramToEngineB();
			pipeline.attachCanvas.mockClear();
			pipeline.connectSource.mockClear();

			manager.destroyEngineBPipeline();

			expect(manager.activeEngine).toBe('A');
			expect(pipeline.connectSource).toHaveBeenCalledWith('program-preview');
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('program-preview', 'program', pgmCanvas);
		});

		it('failoverToEngineB swaps preview canvas to pipeline B', async () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam2', pgmCanvas, pvwCanvas);

			const bPipelineMock = createMockPipeline();
			bPipelineMock.getAudioDecoder.mockReturnValue({ getLevels: vi.fn().mockReturnValue({ peak: [0, 0] }) } as any);
			mockedCreateMediaPipeline.mockReturnValueOnce(bPipelineMock);

			const failoverPromise = manager.failoverToEngineB('https://engine-b:8080', ['cam1', 'cam2']);
			await vi.advanceTimersByTimeAsync(250);
			await failoverPromise;

			// Preview should be detached from A and attached to B
			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam2', 'preview');
			expect(bPipelineMock.attachCanvas).toHaveBeenCalledWith(
				'cam2',
				'preview',
				pvwCanvas,
			);
		});

		it('restoreToEngineA swaps canvases back and re-attaches to A', async () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam2', pgmCanvas, pvwCanvas);
			await manager.syncSources(makeSources('cam1'));

			const bPipelineMock = createMockPipeline();
			bPipelineMock.getAudioDecoder.mockReturnValue({ getLevels: vi.fn().mockReturnValue({ peak: [0, 0] }) } as any);
			mockedCreateMediaPipeline.mockReturnValueOnce(bPipelineMock);

			const failoverPromise = manager.failoverToEngineB('https://engine-b:8080', ['cam1']);
			await vi.advanceTimersByTimeAsync(250);
			await failoverPromise;

			// Clear mocks to track restore-specific calls
			pipeline.attachCanvas.mockClear();
			pipeline.detachCanvas.mockClear();
			bPipelineMock.detachCanvas.mockClear();

			await manager.restoreToEngineA(['cam1']);

			// Tile canvases should move back from B to A
			expect(bPipelineMock.detachCanvas).toHaveBeenCalledWith('cam1', 'tile-cam1');
			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam1',
				'tile-cam1',
				expect.any(HTMLCanvasElement),
			);
		});
	});

	describe('warm standby', () => {
		const mockedCreateMediaPipeline = vi.mocked(createMediaPipeline);

		beforeEach(() => {
			vi.useFakeTimers();
			mockedCreateMediaPipeline.mockClear();
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		it('enableWarmStandby creates Engine B pipeline', () => {
			manager.enableWarmStandby('https://engine-b.example.com', ['cam1', 'cam2']);

			expect(manager.hasEngineBPipeline).toBe(true);
			expect(manager.warmStandbyActive).toBe(true);
			expect(mockedCreateMediaPipeline).toHaveBeenCalledWith({
				engineBaseUrl: 'https://engine-b.example.com',
			});

			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			// Should connect source tiles plus program-preview. The full-quality
			// `program` MoQ source is never subscribed from the browser.
			expect(bPipeline.addSource).toHaveBeenCalledWith('cam1');
			expect(bPipeline.addSource).toHaveBeenCalledWith('cam2');
			expect(bPipeline.addSource).toHaveBeenCalledWith('program-preview');
			expect(bPipeline.connectSource).toHaveBeenCalledWith('cam1');
			expect(bPipeline.connectSource).toHaveBeenCalledWith('cam2');
			expect(bPipeline.connectSource).toHaveBeenCalledWith('program-preview');
			expect(bPipeline.addSource).not.toHaveBeenCalledWith('program');
			expect(bPipeline.connectSource).not.toHaveBeenCalledWith('program');
		});

		it('enableWarmStandby is idempotent (calling twice is safe)', () => {
			manager.enableWarmStandby('https://engine-b.example.com', ['cam1']);
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			bPipeline.addSource.mockClear();
			bPipeline.connectSource.mockClear();

			// Call again with same sources — should not re-create or re-connect
			manager.enableWarmStandby('https://engine-b.example.com', ['cam1']);

			expect(mockedCreateMediaPipeline).toHaveBeenCalledTimes(1);
			expect(bPipeline.addSource).not.toHaveBeenCalled();
			expect(bPipeline.connectSource).not.toHaveBeenCalled();
		});

		it('enableWarmStandby passes onControlState callback', () => {
			const onControlState = vi.fn();
			manager.enableWarmStandby('https://engine-b.example.com', ['cam1'], { onControlState });

			expect(mockedCreateMediaPipeline).toHaveBeenCalledWith({
				engineBaseUrl: 'https://engine-b.example.com',
				onControlState,
			});
		});

		it('failoverToEngineB skips wait when warm standby active', async () => {
			// Set up program canvas on Engine A
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);
			await manager.syncSources(makeSources('cam1', 'cam2'));

			// Enable warm standby first
			manager.enableWarmStandby('https://engine-b.example.com', ['cam1', 'cam2']);
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			bPipeline.addSource.mockClear();
			bPipeline.connectSource.mockClear();

			// Failover should complete instantly (no 3s wait)
			const start = Date.now();
			await manager.failoverToEngineB('https://engine-b.example.com', ['cam1', 'cam2']);
			const elapsed = Date.now() - start;

			expect(manager.activeEngine).toBe('B');
			// Should NOT have re-added sources (warm standby already connected them)
			expect(bPipeline.addSource).not.toHaveBeenCalledWith('cam1');
			expect(bPipeline.addSource).not.toHaveBeenCalledWith('cam2');
			expect(bPipeline.addSource).not.toHaveBeenCalledWith('program');
			// Should be instant (no timer-based wait)
			expect(elapsed).toBeLessThan(100);
		});

		it('failoverToEngineB still waits when no warm standby', async () => {
			// Set up program canvas on Engine A
			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, null);

			// Pipeline B never becomes ready (cold path)
			const bPipelineMock = createMockPipeline();
			mockedCreateMediaPipeline.mockReturnValueOnce(bPipelineMock);

			const failoverPromise = manager.failoverToEngineB('https://engine-b:8080', ['cam1']);

			// Must advance timers past the 3-second timeout (cold path)
			await vi.advanceTimersByTimeAsync(3200);
			await failoverPromise;

			expect(manager.activeEngine).toBe('B');
		});

		it('syncWarmStandby adds new sources to B', () => {
			manager.enableWarmStandby('https://engine-b.example.com', ['cam1']);
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			bPipeline.addSource.mockClear();
			bPipeline.connectSource.mockClear();

			// New source appears
			manager.syncWarmStandby(['cam1', 'cam2']);

			// cam1 already connected (skip), cam2 is new
			expect(bPipeline.addSource).toHaveBeenCalledWith('cam2');
			expect(bPipeline.connectSource).toHaveBeenCalledWith('cam2');
			expect(bPipeline.addSource).not.toHaveBeenCalledWith('cam1');
		});

		it('syncWarmStandby is no-op without warm standby', () => {
			// No warm standby enabled — should not throw or do anything
			manager.syncWarmStandby(['cam1', 'cam2']);
			expect(manager.hasEngineBPipeline).toBe(false);
		});

		it('syncWarmStandby is no-op when active engine is B', async () => {
			// Set up and failover to B
			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, null);
			await manager.syncSources(makeSources('cam1'));

			manager.enableWarmStandby('https://engine-b.example.com', ['cam1']);
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;

			await manager.failoverToEngineB('https://engine-b.example.com', ['cam1']);
			bPipeline.addSource.mockClear();
			bPipeline.connectSource.mockClear();

			// syncWarmStandby should be no-op because active engine is B
			manager.syncWarmStandby(['cam1', 'cam2']);
			expect(bPipeline.addSource).not.toHaveBeenCalled();
		});

		it('destroyEngineBPipeline resets warm standby state', () => {
			manager.enableWarmStandby('https://engine-b.example.com', ['cam1']);
			expect(manager.warmStandbyActive).toBe(true);

			manager.destroyEngineBPipeline();
			expect(manager.warmStandbyActive).toBe(false);
			expect(manager.hasEngineBPipeline).toBe(false);
		});
	});
});
