import { describe, it, expect, vi } from 'vitest';
import { createControlRoomStore } from './control-room.svelte';
import type { ControlRoomState } from '$lib/api/types';

function makeState(overrides: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		programSource: '',
		previewSource: '',
		transitionType: 'cut',
		transitionDurationMs: 0,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		audioChannels: undefined,
		masterLevel: 0,
		programPeak: [0, 0],
		tallyState: {},
		sources: {},
		seq: 0,
		timestamp: 0,
		...overrides,
	};
}

describe('control-room store', () => {
	it('initializes with empty state', () => {
		const store = createControlRoomStore();
		expect(store.state.programSource).toBe('');
		expect(store.state.seq).toBe(0);
		expect(store.state.sources).toEqual({});
	});

	it('applies state update', () => {
		const store = createControlRoomStore();
		const update = makeState({
			programSource: 'cam1',
			previewSource: 'cam2',
			tallyState: { cam1: 'program', cam2: 'preview' },
			sources: {
				cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' },
				cam2: { key: 'cam2', status: 'healthy' },
			},
			seq: 1,
			timestamp: Date.now(),
		});
		store.applyUpdate(update);
		expect(store.state.programSource).toBe('cam1');
		expect(store.state.sources.cam1.label).toBe('Camera 1');
	});

	it('rejects stale updates (lower seq)', () => {
		const store = createControlRoomStore();
		store.applyUpdate(makeState({ seq: 5, programSource: 'cam1' }));
		store.applyUpdate(makeState({ seq: 3, programSource: 'stale' }));
		expect(store.state.programSource).not.toBe('stale');
		expect(store.state.programSource).toBe('cam1');
	});

	it('rejects duplicate seq (equal seq)', () => {
		const store = createControlRoomStore();
		store.applyUpdate(makeState({ seq: 5, programSource: 'cam1' }));
		store.applyUpdate(makeState({ seq: 5, programSource: 'duplicate' }));
		expect(store.state.programSource).toBe('cam1');
	});

	it('provides derived source list', () => {
		const store = createControlRoomStore();
		store.applyUpdate(makeState({
			sources: {
				cam2: { key: 'cam2', status: 'healthy' },
				cam1: { key: 'cam1', status: 'healthy' },
			},
			seq: 1,
		}));
		expect(store.sourceKeys).toEqual(['cam1', 'cam2']);
	});

	it('parses MoQ binary data', () => {
		const store = createControlRoomStore();
		const update = makeState({
			programSource: 'cam1',
			tallyState: { cam1: 'program' },
			sources: {
				cam1: { key: 'cam1', status: 'healthy' },
			},
			seq: 1,
			timestamp: Date.now(),
		});
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

	describe('lastServerUpdate tracking', () => {
		it('initializes lastServerUpdate to current time', () => {
			const before = Date.now();
			const store = createControlRoomStore();
			const after = Date.now();
			expect(store.lastServerUpdate).toBeGreaterThanOrEqual(before);
			expect(store.lastServerUpdate).toBeLessThanOrEqual(after);
		});

		it('updates lastServerUpdate on applyUpdate', () => {
			vi.useFakeTimers();
			const now = Date.now();
			vi.setSystemTime(now);

			const store = createControlRoomStore();

			vi.setSystemTime(now + 5000);
			store.applyUpdate(makeState({ seq: 1, programSource: 'cam1' }));

			expect(store.lastServerUpdate).toBe(now + 5000);
			vi.useRealTimers();
		});

		it('updates lastServerUpdate on applyFromMoQ', () => {
			vi.useFakeTimers();
			const now = Date.now();
			vi.setSystemTime(now);

			const store = createControlRoomStore();

			vi.setSystemTime(now + 3000);
			const data = new TextEncoder().encode(
				JSON.stringify(makeState({ seq: 1, programSource: 'cam1' }))
			);
			store.applyFromMoQ(data);

			expect(store.lastServerUpdate).toBe(now + 3000);
			vi.useRealTimers();
		});

		it('updates lastServerUpdate even on stale seq (server is alive)', () => {
			vi.useFakeTimers();
			const now = Date.now();
			vi.setSystemTime(now);

			const store = createControlRoomStore();

			vi.setSystemTime(now + 1000);
			store.applyUpdate(makeState({ seq: 5, programSource: 'cam1' }));

			vi.setSystemTime(now + 2000);
			store.applyUpdate(makeState({ seq: 3, programSource: 'stale' }));

			// Heartbeat updated even though state was not applied
			expect(store.lastServerUpdate).toBe(now + 2000);
			// But state itself should not change
			expect(store.state.programSource).toBe('cam1');
			vi.useRealTimers();
		});

		it('does not update lastServerUpdate on malformed MoQ data', () => {
			vi.useFakeTimers();
			const now = Date.now();
			vi.setSystemTime(now);

			const store = createControlRoomStore();
			const initial = store.lastServerUpdate;

			vi.setSystemTime(now + 5000);
			store.applyFromMoQ(new TextEncoder().encode('not valid json{{{'));

			expect(store.lastServerUpdate).toBe(initial);
			vi.useRealTimers();
		});
	});

	describe('optimistic updates', () => {
		it('optimisticCut immediately reflects in effectiveState', () => {
			const store = createControlRoomStore();
			// Set up initial server state: cam1 on program, cam2 on preview
			store.applyUpdate(makeState({
				programSource: 'cam1',
				previewSource: 'cam2',
				tallyState: { cam1: 'program', cam2: 'preview' },
				sources: {
					cam1: { key: 'cam1', status: 'healthy' },
					cam2: { key: 'cam2', status: 'healthy' },
				},
				seq: 1,
			}));

			// Optimistically cut to cam2
			store.optimisticCut('cam2');

			// effectiveState should show cam2 on program immediately
			expect(store.effectiveState.programSource).toBe('cam2');
			expect(store.effectiveState.tallyState.cam2).toBe('program');
			// Old program source becomes preview
			expect(store.effectiveState.tallyState.cam1).toBe('preview');

			// Underlying server state is unchanged
			expect(store.state.programSource).toBe('cam1');
		});

		it('optimisticPreview immediately reflects in effectiveState', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({
				programSource: 'cam1',
				previewSource: 'cam2',
				tallyState: { cam1: 'program', cam2: 'preview' },
				sources: {
					cam1: { key: 'cam1', status: 'healthy' },
					cam2: { key: 'cam2', status: 'healthy' },
					cam3: { key: 'cam3', status: 'healthy' },
				},
				seq: 1,
			}));

			// Optimistically set preview to cam3
			store.optimisticPreview('cam3');

			expect(store.effectiveState.previewSource).toBe('cam3');
			expect(store.effectiveState.tallyState.cam3).toBe('preview');
			// Program unchanged
			expect(store.effectiveState.programSource).toBe('cam1');
			// Underlying server state unchanged
			expect(store.state.previewSource).toBe('cam2');
		});

		it('clears pendingAction when server confirms matching program state', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({
				programSource: 'cam1',
				previewSource: 'cam2',
				tallyState: { cam1: 'program', cam2: 'preview' },
				sources: {
					cam1: { key: 'cam1', status: 'healthy' },
					cam2: { key: 'cam2', status: 'healthy' },
				},
				seq: 1,
			}));

			store.optimisticCut('cam2');
			expect(store.effectiveState.programSource).toBe('cam2');

			// Server confirms the cut
			store.applyUpdate(makeState({
				programSource: 'cam2',
				previewSource: 'cam1',
				tallyState: { cam2: 'program', cam1: 'preview' },
				sources: {
					cam1: { key: 'cam1', status: 'healthy' },
					cam2: { key: 'cam2', status: 'healthy' },
				},
				seq: 2,
			}));

			// effectiveState should now just be server state (no overlay)
			expect(store.effectiveState.programSource).toBe('cam2');
			expect(store.state.programSource).toBe('cam2');
		});

		it('clears pendingAction when server confirms matching preview state', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({
				programSource: 'cam1',
				previewSource: 'cam2',
				tallyState: { cam1: 'program', cam2: 'preview' },
				sources: {
					cam1: { key: 'cam1', status: 'healthy' },
					cam2: { key: 'cam2', status: 'healthy' },
					cam3: { key: 'cam3', status: 'healthy' },
				},
				seq: 1,
			}));

			store.optimisticPreview('cam3');
			expect(store.effectiveState.previewSource).toBe('cam3');

			// Server confirms the preview change
			store.applyUpdate(makeState({
				programSource: 'cam1',
				previewSource: 'cam3',
				tallyState: { cam1: 'program', cam3: 'preview' },
				sources: {
					cam1: { key: 'cam1', status: 'healthy' },
					cam2: { key: 'cam2', status: 'healthy' },
					cam3: { key: 'cam3', status: 'healthy' },
				},
				seq: 2,
			}));

			expect(store.effectiveState.previewSource).toBe('cam3');
			expect(store.state.previewSource).toBe('cam3');
		});

		it('effectiveState falls back to server state when no pending', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({
				programSource: 'cam1',
				previewSource: 'cam2',
				tallyState: { cam1: 'program', cam2: 'preview' },
				seq: 1,
			}));

			// No optimistic action pending
			expect(store.effectiveState.programSource).toBe('cam1');
			expect(store.effectiveState.previewSource).toBe('cam2');
			// Should be the same object reference as state
			expect(store.effectiveState).toBe(store.state);
		});

		it('second optimistic action replaces the first', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({
				programSource: 'cam1',
				previewSource: 'cam2',
				tallyState: { cam1: 'program', cam2: 'preview' },
				sources: {
					cam1: { key: 'cam1', status: 'healthy' },
					cam2: { key: 'cam2', status: 'healthy' },
					cam3: { key: 'cam3', status: 'healthy' },
				},
				seq: 1,
			}));

			// First: optimistic preview to cam3
			store.optimisticPreview('cam3');
			expect(store.effectiveState.previewSource).toBe('cam3');

			// Second: optimistic cut to cam2 — should replace the preview pending
			store.optimisticCut('cam2');
			expect(store.effectiveState.programSource).toBe('cam2');
			// The preview pending was overwritten, so preview should be server state
			expect(store.effectiveState.previewSource).toBe('cam2');
		});

		it('pending action expires after 2 seconds', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({
				programSource: 'cam1',
				previewSource: 'cam2',
				tallyState: { cam1: 'program', cam2: 'preview' },
				sources: {
					cam1: { key: 'cam1', status: 'healthy' },
					cam2: { key: 'cam2', status: 'healthy' },
				},
				seq: 1,
			}));

			// Use fake timers to control Date.now()
			vi.useFakeTimers();
			const now = Date.now();
			vi.setSystemTime(now);

			store.optimisticCut('cam2');
			expect(store.effectiveState.programSource).toBe('cam2');

			// Advance time past the 2s timeout
			vi.setSystemTime(now + 2001);

			// The pending action should have expired
			expect(store.effectiveState.programSource).toBe('cam1');

			vi.useRealTimers();
		});
	});
});
