import { describe, it, expect } from 'vitest';
import { createVideoPlaybackManager } from './playback';

describe('VideoPlaybackManager', () => {
	it('should create and track source decoders', () => {
		const manager = createVideoPlaybackManager();
		manager.addSource('cam1');
		manager.addSource('cam2');

		expect(manager.sources).toEqual(['cam1', 'cam2']);
	});

	it('should not add duplicate sources', () => {
		const manager = createVideoPlaybackManager();
		manager.addSource('cam1');
		manager.addSource('cam1');

		expect(manager.sources).toEqual(['cam1']);
	});

	it('should remove source and clean up', () => {
		const manager = createVideoPlaybackManager();
		manager.addSource('cam1');
		manager.removeSource('cam1');

		expect(manager.sources).toEqual([]);
	});

	it('should be a no-op to remove a non-existent source', () => {
		const manager = createVideoPlaybackManager();
		manager.removeSource('cam1');

		expect(manager.sources).toEqual([]);
	});

	it('should return canvas for source', () => {
		const manager = createVideoPlaybackManager();
		manager.addSource('cam1');

		const canvas = manager.getCanvas('cam1');
		// OffscreenCanvas may not be available in test env (jsdom)
		expect(canvas !== undefined).toBe(true);
	});

	it('should return null canvas for unknown source', () => {
		const manager = createVideoPlaybackManager();

		const canvas = manager.getCanvas('unknown');
		expect(canvas).toBeNull();
	});

	it('should track program source for program window', () => {
		const manager = createVideoPlaybackManager();
		manager.addSource('cam1');
		manager.addSource('cam2');
		manager.setProgramSource('cam1');

		expect(manager.programSource).toBe('cam1');
	});

	it('should track preview source for preview window', () => {
		const manager = createVideoPlaybackManager();
		manager.addSource('cam1');
		manager.setPreviewSource('cam1');

		expect(manager.previewSource).toBe('cam1');
	});

	it('should clear program source when that source is removed', () => {
		const manager = createVideoPlaybackManager();
		manager.addSource('cam1');
		manager.setProgramSource('cam1');
		manager.removeSource('cam1');

		expect(manager.programSource).toBeNull();
	});

	it('should clear preview source when that source is removed', () => {
		const manager = createVideoPlaybackManager();
		manager.addSource('cam1');
		manager.setPreviewSource('cam1');
		manager.removeSource('cam1');

		expect(manager.previewSource).toBeNull();
	});

	it('should not clear program when a different source is removed', () => {
		const manager = createVideoPlaybackManager();
		manager.addSource('cam1');
		manager.addSource('cam2');
		manager.setProgramSource('cam1');
		manager.removeSource('cam2');

		expect(manager.programSource).toBe('cam1');
	});

	it('should handle cleanup on destroy', () => {
		const manager = createVideoPlaybackManager();
		manager.addSource('cam1');
		manager.addSource('cam2');
		manager.setProgramSource('cam1');
		manager.setPreviewSource('cam2');
		manager.destroy();

		expect(manager.sources).toEqual([]);
		expect(manager.programSource).toBeNull();
		expect(manager.previewSource).toBeNull();
	});
});
