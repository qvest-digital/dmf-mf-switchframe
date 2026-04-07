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
		getVideoBuffer: vi.fn().mockReturnValue(null),
		getAudioDecoder: vi.fn().mockReturnValue(null),
		attachCanvas: vi.fn().mockReturnValue(true),
		detachCanvas: vi.fn(),
		destroy: vi.fn(),
		feedVideoFrame: vi.fn(),
		feedAudioFrame: vi.fn(),
		setSourceMuted: vi.fn(),
		resumeAllAudio: vi.fn().mockResolvedValue(undefined),
		getAllDiagnostics: vi.fn().mockResolvedValue({}),
		resetRendererSync: vi.fn(),
		hasSource: vi.fn().mockReturnValue(false),
		getSourceTransport: vi.fn().mockReturnValue(null),
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
		it('should attach program canvas when provided', () => {
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'program',
				'program',
				pgmCanvas,
			);
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
				(c: unknown[]) => c[0] === 'program' && c[1] === 'program',
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

			// Program should be attached
			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'program',
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
				(c: unknown[]) => c[0] === 'program' && c[1] === 'program',
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

			expect(pipeline.detachCanvas).toHaveBeenCalledWith('program', 'program');
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
				'program',
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
				if (key === 'program') return mockProgramDecoder;
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

		it('destroyEngineBPipeline cleans up', () => {
			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			manager.destroyEngineBPipeline();
			expect(manager.hasEngineBPipeline).toBe(false);
			expect(bPipeline.destroy).toHaveBeenCalled();
		});

		it('createEngineBPipeline is idempotent', () => {
			manager.createEngineBPipeline('https://engine-b.example.com');
			manager.createEngineBPipeline('https://engine-b.example.com');
			expect(manager.hasEngineBPipeline).toBe(true);
			expect(mockedCreateMediaPipeline).toHaveBeenCalledTimes(1);
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
			manager.connectSourceB('program');

			// Switch to Engine B
			manager.switchProgramToEngineB();

			// Should detach from Engine A's program source
			expect(pipeline.detachCanvas).toHaveBeenCalledWith('program', 'program');
			// Should attach to Engine B's program source
			expect(bPipeline.attachCanvas).toHaveBeenCalledWith('program', 'program', pgmCanvas);
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

			// Should detach from Engine B
			expect(bPipeline.detachCanvas).toHaveBeenCalledWith('program', 'program');
			// Should re-attach to Engine A
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('program', 'program', pgmCanvas);
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
			manager.connectSourceB('program');

			// No program canvas was ever set
			manager.switchProgramToEngineB();
			expect(bPipeline.attachCanvas).not.toHaveBeenCalled();
		});

		it('switchProgramToEngineA restores program-preview if available', () => {
			// Simulate program-preview being ready (getAudioDecoder returns non-null)
			pipeline.getAudioDecoder.mockImplementation((key: string) => {
				if (key === 'program-preview') return { getLevels: vi.fn().mockReturnValue({ peak: [0, 0], rms: [0, 0], peakHold: [0, 0], channels: 2 }) };
				return null;
			});

			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			manager.createEngineBPipeline('https://engine-b.example.com');
			const bPipeline = mockedCreateMediaPipeline.mock.results[0].value;
			manager.connectSourceB('program');
			manager.switchProgramToEngineB();

			bPipeline.detachCanvas.mockClear();
			pipeline.attachCanvas.mockClear();

			// Switch back — should use program-preview since it's available
			manager.switchProgramToEngineA();

			expect(bPipeline.detachCanvas).toHaveBeenCalledWith('program', 'program');
			expect(pipeline.attachCanvas).toHaveBeenCalledWith('program-preview', 'program', pgmCanvas);
		});

		it('activeEngine getter reflects current state', () => {
			expect(manager.activeEngine).toBe('A');

			const pgmCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, null);
			manager.createEngineBPipeline('https://engine-b.example.com');
			manager.connectSourceB('program');

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

		it('failoverToEngineB creates pipeline B and switches activeEngine', async () => {
			// Set up: program canvas on Engine A
			const pgmCanvas = createCanvas();
			const pvwCanvas = createCanvas();
			manager.syncProgramPreviewCanvases('cam1', pgmCanvas, pvwCanvas);

			// Add sources to track attached canvases
			await manager.syncSources(makeSources('cam1', 'cam2'));

			// Make the mock pipeline B return a ready audio decoder for program
			const bPipelineMock = createMockPipeline();
			bPipelineMock.getAudioDecoder.mockImplementation((key: string) => {
				if (key === 'program') return { getLevels: vi.fn().mockReturnValue({ peak: [0, 0] }) } as any;
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

			// Verify Engine B has sources connected
			expect(bPipelineMock.addSource).toHaveBeenCalledWith('cam1');
			expect(bPipelineMock.addSource).toHaveBeenCalledWith('cam2');
			expect(bPipelineMock.addSource).toHaveBeenCalledWith('program');
			expect(bPipelineMock.addSource).toHaveBeenCalledWith('program-preview');
			expect(bPipelineMock.connectSource).toHaveBeenCalledWith('program');
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
});
