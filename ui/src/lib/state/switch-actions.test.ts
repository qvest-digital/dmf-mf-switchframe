import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('$lib/api/switch-api', () => ({
	cut: vi.fn().mockResolvedValue({}),
	startTransition: vi.fn().mockResolvedValue({}),
	apiCall: vi.fn(),
}));

import { cutToPreview, autoTransitionToPreview } from './switch-actions';
import { createControlRoomStore } from './control-room.svelte';
import { cut, startTransition } from '$lib/api/switch-api';
import type { ControlRoomState } from '$lib/api/types';

const mockedCut = vi.mocked(cut);
const mockedStartTransition = vi.mocked(startTransition);

function makeState(overrides: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 0,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		masterLevel: 0,
		programPeak: [0, 0],
		tallyState: {},
		sources: {},
		seq: 1,
		lastCommandSeq: 0,
		timestamp: 1000,
		...overrides,
	} as ControlRoomState;
}

describe('switch actions', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	describe('cutToPreview', () => {
		it('cuts the server-confirmed preview when no optimistic preview is pending', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({ previewSource: 'cam2' }));

			cutToPreview(store);

			expect(mockedCut).toHaveBeenCalledWith('cam2');
		});

		it('cuts the optimistic preview when the operator just pressed a preview key', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({ previewSource: 'cam2' }));

			// Operator presses '3' (optimistic preview) then Cut before the
			// server round trip confirms the new preview. The preview monitor
			// already shows cam3 (effectiveState) — Cut must target cam3, not
			// the stale server-confirmed cam2.
			store.optimisticPreview('cam3');
			cutToPreview(store);

			expect(mockedCut).toHaveBeenCalledWith('cam3');
			// The optimistic program update must match what was actually cut.
			expect(store.effectiveState.programSource).toBe('cam3');
		});

		it('does nothing when there is no preview source', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({ previewSource: '' }));

			cutToPreview(store);

			expect(mockedCut).not.toHaveBeenCalled();
		});
	});

	describe('autoTransitionToPreview', () => {
		it('transitions the server-confirmed preview when no optimistic preview is pending', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({ previewSource: 'cam2' }));

			autoTransitionToPreview(store, 'mix', 1000);

			expect(mockedStartTransition).toHaveBeenCalledWith('cam2', 'mix', 1000);
		});

		it('transitions the optimistic preview when the operator just pressed a preview key', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({ previewSource: 'cam2' }));

			store.optimisticPreview('cam3');
			autoTransitionToPreview(store, 'wipe', 500);

			expect(mockedStartTransition).toHaveBeenCalledWith('cam3', 'wipe', 500);
		});

		it('does not transition while a transition is already in progress', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({ inTransition: true }));

			autoTransitionToPreview(store, 'mix', 1000);

			expect(mockedStartTransition).not.toHaveBeenCalled();
		});

		it('does not transition while FTB is active', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({ ftbActive: true }));

			autoTransitionToPreview(store, 'mix', 1000);

			expect(mockedStartTransition).not.toHaveBeenCalled();
		});

		it('does nothing when there is no preview source', () => {
			const store = createControlRoomStore();
			store.applyUpdate(makeState({ previewSource: '' }));

			autoTransitionToPreview(store, 'mix', 1000);

			expect(mockedStartTransition).not.toHaveBeenCalled();
		});
	});
});
