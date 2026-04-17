import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
	createReconciler,
	AUTHORITY_HOLD_MS,
	TRANSITION_LOCK_STALE_THRESHOLD_MS,
	TRANSITION_LOCK_MAX_HOLD_MS,
} from './reconciler.svelte';
import type { ControlRoomState } from '$lib/api/types';

function makeState(overrides: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 500,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		audioChannels: undefined,
		masterLevel: 0,
		programPeak: [0, 0],
		tallyState: {},
		sources: {},
		seq: 1,
		lastCommandSeq: 1,
		timestamp: 1000,
		...overrides,
	};
}

describe('transition lock grace window', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('exposes a grace threshold distinct from the normal stale threshold', () => {
		expect(TRANSITION_LOCK_STALE_THRESHOLD_MS).toBe(5000);
		expect(TRANSITION_LOCK_MAX_HOLD_MS).toBe(10000);
	});

	it('holds authority through a 1-second state gap on the locked engine mid-transition', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: false }));
		r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: false }));
		expect(r.health.authoritative).toBe('A');

		r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));
		r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.7 }));

		r.setRTT('A', 300);
		r.setRTT('B', 30);

		vi.advanceTimersByTime(1000);
		r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.9 }));
		r.tick();

		expect(r.health.authoritative).toBe('A');
	});

	it('releases the lock immediately when the locked engine reports transition complete', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: false }));
		r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: false }));

		r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.5 }));
		r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.5 }));
		expect(r.dualEngineStats.currentAuthority).toBe('A');

		r.ingest('A', makeState({ lastCommandSeq: 6, inTransition: false }));
		r.ingest('B', makeState({ lastCommandSeq: 6, inTransition: false }));

		r.setRTT('A', 300);
		r.setRTT('B', 30);

		vi.advanceTimersByTime(AUTHORITY_HOLD_MS + 1);
		r.tick();
		expect(r.health.authoritative).toBe('B');
	});

	it('releases the lock after the max-hold timeout when the locked engine never reports transition end', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: false }));
		r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: false }));

		r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));
		r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));
		expect(r.health.authoritative).toBe('A');

		r.setRTT('A', 400);
		r.setRTT('B', 40);

		for (let t = 500; t <= TRANSITION_LOCK_MAX_HOLD_MS + 2500; t += 500) {
			vi.advanceTimersByTime(500);
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));
			r.tick();
		}

		expect(r.health.authoritative).toBe('B');
	});

	it('keeps the lock across a transient state-gap shorter than the grace window', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: false }));
		r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: false }));

		r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.2 }));
		r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.2 }));

		r.setRTT('A', 400);
		r.setRTT('B', 40);

		for (let i = 0; i < 4; i++) {
			vi.advanceTimersByTime(600);
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.2 + i * 0.1 }));
			r.tick();
			expect(r.health.authoritative).toBe('A');
		}
	});
});
