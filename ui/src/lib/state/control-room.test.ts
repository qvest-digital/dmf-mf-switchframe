import { describe, it, expect } from 'vitest';
import { createControlRoomStore } from './control-room.svelte';
import type { ControlRoomState } from '$lib/api/types';

describe('control-room store', () => {
	it('initializes with empty state', () => {
		const store = createControlRoomStore();
		expect(store.state.programSource).toBe('');
		expect(store.state.seq).toBe(0);
		expect(store.state.sources).toEqual({});
	});

	it('applies state update', () => {
		const store = createControlRoomStore();
		const update: ControlRoomState = {
			programSource: 'cam1',
			previewSource: 'cam2',
			transitionType: 'cut',
			transitionDurationMs: 0,
			transitionPosition: 0,
			inTransition: false,

			tallyState: { cam1: 'program', cam2: 'preview' },
			sources: {
				cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy', lastFrameTime: 0 },
				cam2: { key: 'cam2', status: 'healthy', lastFrameTime: 0 },
			},
			seq: 1,
			timestamp: Date.now(),
		};
		store.applyUpdate(update);
		expect(store.state.programSource).toBe('cam1');
		expect(store.state.sources.cam1.label).toBe('Camera 1');
	});

	it('rejects stale updates (lower seq)', () => {
		const store = createControlRoomStore();
		store.applyUpdate({
			seq: 5,
			programSource: 'cam1',
			previewSource: '',
			transitionType: 'cut',
			transitionDurationMs: 0,
			transitionPosition: 0,
			inTransition: false,

			tallyState: {},
			sources: {},
			timestamp: 0,
		});
		store.applyUpdate({
			seq: 3,
			programSource: 'stale',
			previewSource: '',
			transitionType: 'cut',
			transitionDurationMs: 0,
			transitionPosition: 0,
			inTransition: false,

			tallyState: {},
			sources: {},
			timestamp: 0,
		});
		expect(store.state.programSource).not.toBe('stale');
		expect(store.state.programSource).toBe('cam1');
	});

	it('rejects duplicate seq (equal seq)', () => {
		const store = createControlRoomStore();
		store.applyUpdate({
			seq: 5,
			programSource: 'cam1',
			previewSource: '',
			transitionType: 'cut',
			transitionDurationMs: 0,
			transitionPosition: 0,
			inTransition: false,

			tallyState: {},
			sources: {},
			timestamp: 0,
		});
		store.applyUpdate({
			seq: 5,
			programSource: 'duplicate',
			previewSource: '',
			transitionType: 'cut',
			transitionDurationMs: 0,
			transitionPosition: 0,
			inTransition: false,

			tallyState: {},
			sources: {},
			timestamp: 0,
		});
		expect(store.state.programSource).toBe('cam1');
	});

	it('provides derived source list', () => {
		const store = createControlRoomStore();
		store.applyUpdate({
			programSource: '',
			previewSource: '',
			transitionType: 'cut',
			transitionDurationMs: 0,
			transitionPosition: 0,
			inTransition: false,

			tallyState: {},
			sources: {
				cam2: { key: 'cam2', status: 'healthy', lastFrameTime: 0 },
				cam1: { key: 'cam1', status: 'healthy', lastFrameTime: 0 },
			},
			seq: 1,
			timestamp: 0,
		});
		expect(store.sourceKeys).toEqual(['cam1', 'cam2']);
	});

	it('parses MoQ binary data', () => {
		const store = createControlRoomStore();
		const update: ControlRoomState = {
			programSource: 'cam1',
			previewSource: '',
			transitionType: 'cut',
			transitionDurationMs: 0,
			transitionPosition: 0,
			inTransition: false,

			tallyState: { cam1: 'program' },
			sources: {
				cam1: { key: 'cam1', status: 'healthy', lastFrameTime: 0 },
			},
			seq: 1,
			timestamp: Date.now(),
		};
		const data = new TextEncoder().encode(JSON.stringify(update));
		store.applyFromMoQ(data);
		expect(store.state.programSource).toBe('cam1');
		expect(store.state.seq).toBe(1);
	});

	it('ignores malformed MoQ data', () => {
		const store = createControlRoomStore();
		const badData = new TextEncoder().encode('not valid json{{{');
		store.applyFromMoQ(badData);
		expect(store.state.seq).toBe(0);
		expect(store.state.programSource).toBe('');
	});
});
