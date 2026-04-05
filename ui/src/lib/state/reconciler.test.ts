import { describe, it, expect } from 'vitest';
import { createReconciler } from './reconciler.svelte';
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

describe('createReconciler', () => {
	it('picks engine with higher lastCommandSeq as authoritative', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
		r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 3 }));
		expect(r.state.programSource).toBe('cam1');
		expect(r.health.authoritative).toBe('A');
	});

	it('detects source divergence', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
		r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
		const divs = r.health.divergences;
		expect(divs.length).toBeGreaterThan(0);
		const progDiv = divs.find((d) => d.field === 'programSource');
		expect(progDiv).toBeDefined();
		expect(progDiv?.engineA).toBe('cam1');
		expect(progDiv?.engineB).toBe('cam2');
	});

	it('reports nominal when engines agree', () => {
		const r = createReconciler();
		const s = makeState({ programSource: 'cam1', lastCommandSeq: 5 });
		r.ingest('A', s);
		r.ingest('B', { ...s });
		expect(r.health.status).toBe('nominal');
		expect(r.health.divergences).toHaveLength(0);
	});

	it('reports single-engine when only one engine present', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ lastCommandSeq: 3 }));
		expect(r.health.status).toBe('single-engine');
	});

	it('reports disconnected when no engines present', () => {
		const r = createReconciler();
		expect(r.health.status).toBe('disconnected');
	});

	it('tie-breaks to engine A when lastCommandSeq is equal', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
		r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
		// A and B have same seq — A wins the tie-break
		expect(r.health.authoritative).toBe('A');
		// State should reflect A's programSource
		expect(r.state.programSource).toBe('cam1');
	});

	it('detects multiple divergences', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ programSource: 'cam1', ftbActive: false, lastCommandSeq: 5 }));
		r.ingest('B', makeState({ programSource: 'cam2', ftbActive: true, lastCommandSeq: 5 }));
		const divs = r.health.divergences;
		const fields = divs.map((d) => d.field);
		expect(fields).toContain('programSource');
		expect(fields).toContain('ftbActive');
		expect(divs.length).toBeGreaterThanOrEqual(2);
	});
});
