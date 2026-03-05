import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { PipelineManager } from './manager';
import type { MediaPipeline } from '$lib/transport/media-pipeline';

/** Create a mock MediaPipeline with all methods stubbed. */
function createMockPipeline(): MediaPipeline & { [K in keyof MediaPipeline]: ReturnType<typeof vi.fn> } {
	return {
		addSource: vi.fn(),
		removeSource: vi.fn(),
		connectSource: vi.fn(),
		disconnectSource: vi.fn(),
		getVideoBuffer: vi.fn().mockReturnValue(null),
		getAudioDecoder: vi.fn().mockReturnValue(null),
		attachCanvas: vi.fn(),
		detachCanvas: vi.fn(),
		destroy: vi.fn(),
		feedVideoFrame: vi.fn(),
		feedAudioFrame: vi.fn(),
		setSourceMuted: vi.fn(),
		resumeAllAudio: vi.fn().mockResolvedValue(undefined),
		getAllDiagnostics: vi.fn().mockResolvedValue({}),
	};
}

/** Create a minimal sources record for testing. */
function makeSources(...keys: string[]): Record<string, { key: string; status: string }> {
	const sources: Record<string, { key: string; status: string }> = {};
	for (const key of keys) {
		sources[key] = { key, status: 'healthy' };
	}
	return sources;
}

describe('PipelineManager', () => {
	let pipeline: ReturnType<typeof createMockPipeline>;
	let manager: PipelineManager;
	let sourceKeys: string[];

	beforeEach(() => {
		pipeline = createMockPipeline();
		sourceKeys = [];
		manager = new PipelineManager(pipeline, () => sourceKeys);
		// Stub getElementById to return canvas elements
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
		it('should attach program canvas on first call', () => {
			manager.syncProgramPreviewCanvases('cam1');

			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'program',
				'program',
				expect.any(HTMLCanvasElement),
			);
		});

		it('should not re-attach program canvas if already attached', () => {
			manager.syncProgramPreviewCanvases('cam1');
			pipeline.attachCanvas.mockClear();

			manager.syncProgramPreviewCanvases('cam1');

			const programCalls = pipeline.attachCanvas.mock.calls.filter(
				(c: unknown[]) => c[0] === 'program' && c[1] === 'program',
			);
			expect(programCalls).toHaveLength(0);
		});

		it('should attach preview canvas for the preview source', () => {
			manager.syncProgramPreviewCanvases('cam1');

			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam1',
				'preview',
				expect.any(HTMLCanvasElement),
			);
		});

		it('should detach old preview and attach new on preview source change', () => {
			manager.syncProgramPreviewCanvases('cam1');
			pipeline.detachCanvas.mockClear();
			pipeline.attachCanvas.mockClear();

			manager.syncProgramPreviewCanvases('cam2');

			// Detach old preview source
			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam1', 'preview');
			// Attach new preview source
			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam2',
				'preview',
				expect.any(HTMLCanvasElement),
			);
		});

		it('should handle empty preview source', () => {
			manager.syncProgramPreviewCanvases('cam1');
			pipeline.detachCanvas.mockClear();
			pipeline.attachCanvas.mockClear();

			manager.syncProgramPreviewCanvases('');

			// Should detach old preview
			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam1', 'preview');
			// Should not attach with empty source
			const previewAttach = pipeline.attachCanvas.mock.calls.filter(
				(c: unknown[]) => c[1] === 'preview',
			);
			expect(previewAttach).toHaveLength(0);
		});

		it('should handle program canvas not found in DOM', () => {
			vi.spyOn(document, 'getElementById').mockReturnValue(null);

			manager.syncProgramPreviewCanvases('cam1');

			// No canvas found, so nothing should be attached
			expect(pipeline.attachCanvas).not.toHaveBeenCalled();
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
			manager.syncProgramPreviewCanvases('cam1');
			pipeline.detachCanvas.mockClear();

			manager.onLayoutChange();

			expect(pipeline.detachCanvas).toHaveBeenCalledWith('program', 'program');
		});

		it('should detach preview canvas', async () => {
			manager.syncProgramPreviewCanvases('cam1');
			pipeline.detachCanvas.mockClear();

			manager.onLayoutChange();

			expect(pipeline.detachCanvas).toHaveBeenCalledWith('cam1', 'preview');
		});

		it('should reset canvas tracking so re-sync re-attaches', async () => {
			await manager.syncSources(makeSources('cam1'));
			manager.syncProgramPreviewCanvases('cam1');
			pipeline.attachCanvas.mockClear();

			manager.onLayoutChange();

			// Re-sync should re-attach everything
			await manager.syncSources(makeSources('cam1'));
			manager.syncProgramPreviewCanvases('cam1');

			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam1',
				'tile-cam1',
				expect.any(HTMLCanvasElement),
			);
			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'program',
				'program',
				expect.any(HTMLCanvasElement),
			);
			expect(pipeline.attachCanvas).toHaveBeenCalledWith(
				'cam1',
				'preview',
				expect.any(HTMLCanvasElement),
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
			await manager.syncSources(makeSources('cam1'));
			manager.syncProgramPreviewCanvases('cam1');

			manager.destroy();

			// After destroy, a new sync should treat everything as fresh
			pipeline.addSource.mockClear();
			pipeline.attachCanvas.mockClear();

			await manager.syncSources(makeSources('cam1'));

			expect(pipeline.addSource).toHaveBeenCalledWith('cam1');
		});
	});
});
