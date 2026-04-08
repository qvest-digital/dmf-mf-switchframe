import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createReconciler, fieldEquals, STALE_THRESHOLD_MS, sortedStringify } from './reconciler.svelte';
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
	// ── Existing tests (updated 'nominal' → 'synced') ──

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

	it('reports synced when engines agree', () => {
		const r = createReconciler();
		const s = makeState({ programSource: 'cam1', lastCommandSeq: 5 });
		r.ingest('A', s);
		r.ingest('B', { ...s });
		expect(r.health.status).toBe('synced');
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

	it('tie-breaks to engine A when lastCommandSeq is equal (default, no RTT)', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
		r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
		expect(r.health.authoritative).toBe('A');
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

	// ── RTT-based tie-breaking ──

	describe('RTT-based tie-breaking', () => {
		it('prefers engine B when B has lower RTT at equal seq', () => {
			const r = createReconciler();
			r.setRTT('A', 50);
			r.setRTT('B', 20);
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('B');
			expect(r.state.programSource).toBe('cam2');
		});

		it('still prefers higher seq over lower RTT', () => {
			const r = createReconciler();
			r.setRTT('A', 100);
			r.setRTT('B', 10);
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 10 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('A');
		});

		it('falls back to A when RTTs are equal', () => {
			const r = createReconciler();
			r.setRTT('A', 30);
			r.setRTT('B', 30);
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('A');
		});

		it('does not flip authority on small RTT changes at equal seq (within hysteresis)', () => {
			const r = createReconciler();
			// Delta = 5ms < 10ms hysteresis → A wins by default
			r.setRTT('A', 30);
			r.setRTT('B', 35);
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('A');

			// Flip RTTs: now A=35, B=30 (delta still only 5ms) → still A
			r.setRTT('A', 35);
			r.setRTT('B', 30);
			expect(r.health.authoritative).toBe('A');
		});

		it('flips authority when RTT difference exceeds hysteresis', () => {
			const r = createReconciler();
			// Delta = 20ms > 10ms hysteresis → B wins (lower RTT)
			r.setRTT('A', 50);
			r.setRTT('B', 30);
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('B');
		});

		it('returns to A when RTTs converge within hysteresis', () => {
			const r = createReconciler();
			// Start: A=50, B=30 → delta=20 > 10ms → B is authoritative
			r.setRTT('A', 50);
			r.setRTT('B', 30);
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('B');

			// RTTs converge: A=31, B=30 → delta=1 < 10ms → A wins (default)
			r.setRTT('A', 31);
			r.setRTT('B', 30);
			expect(r.health.authoritative).toBe('A');
		});
	});

	// ── Time-aware status detection ──

	describe('time-aware sync status', () => {
		beforeEach(() => {
			vi.useFakeTimers();
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		it('stays synced when seq delta is transient (< 500ms)', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 3 }));
			// Seq differs but less than 500ms has passed
			vi.advanceTimersByTime(200);
			r.tick();
			expect(r.health.status).toBe('synced');
		});

		it('transitions to lagging after 500ms of seq delta', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 3 }));
			vi.advanceTimersByTime(600);
			r.tick();
			expect(r.health.status).toBe('lagging');
		});

		it('transitions to diverged after 2s of seq delta', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 3 }));
			vi.advanceTimersByTime(2100);
			r.tick();
			expect(r.health.status).toBe('diverged');
		});

		it('returns to synced when seqs align', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 3 }));
			vi.advanceTimersByTime(600);
			r.tick();
			expect(r.health.status).toBe('lagging');

			// B catches up
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.tick();
			expect(r.health.status).toBe('synced');
		});

		it('reports diverged immediately on field mismatch at different seqs', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 3 }));
			// Field mismatch + different seq = diverged immediately
			r.tick();
			expect(r.health.status).toBe('diverged');
		});
	});

	// ── Mismatch detection ──

	describe('mismatch detection', () => {
		it('reports mismatch when programSource differs at same lastCommandSeq', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			r.tick();
			expect(r.health.status).toBe('mismatch');
		});

		it('reports mismatch when playout currentItemId differs at same seq', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				playout: { channels: [{ id: 0, state: 'on_air', currentItemId: 'item-1', itemIndex: 0, itemCount: 1 }] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				playout: { channels: [{ id: 0, state: 'on_air', currentItemId: 'item-2', itemIndex: 0, itemCount: 1 }] },
			}));
			r.tick();
			expect(r.health.status).toBe('mismatch');
		});

		it('reports mismatch when playout current items differ at same seq', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 2, itemCount: 15, currentTitle: 'Interview' }] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 3, itemCount: 15, currentTitle: 'Bumper' }] },
			}));
			expect(r.health.status).toBe('mismatch');
		});

		it('does not report mismatch when playout items match', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 2, itemCount: 15, currentTitle: 'Interview' }] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 2, itemCount: 15, currentTitle: 'Interview' }] },
			}));
			expect(r.health.status).toBe('synced');
		});

		it('ignores playout mismatch when one engine has no playout', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 2, itemCount: 15 }] },
			}));
			r.ingest('B', makeState({ lastCommandSeq: 10 }));
			// One engine has no playout — not a mismatch
			expect(r.health.status).not.toBe('mismatch');
		});

		it('does not report mismatch when fields agree at same seq', () => {
			const r = createReconciler();
			const s = makeState({ lastCommandSeq: 5 });
			r.ingest('A', s);
			r.ingest('B', { ...s });
			r.tick();
			expect(r.health.status).toBe('synced');
		});
	});

	// ── Recovering status ──

	describe('recovering status', () => {
		it('reports recovering when setRecovering(true)', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.setRecovering(true);
			r.tick();
			expect(r.health.status).toBe('recovering');
		});

		it('disconnected trumps recovering when both engines offline', () => {
			const r = createReconciler();
			r.ingest('A', makeState());
			r.disconnect('A');
			r.setRecovering(true);
			r.tick();
			expect(r.health.status).toBe('disconnected');
		});

		it('clears recovering on setRecovering(false)', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.setRecovering(true);
			r.tick();
			expect(r.health.status).toBe('recovering');
			r.setRecovering(false);
			r.tick();
			expect(r.health.status).toBe('synced');
		});
	});

	// ── Engine health store ──

	describe('engine health', () => {
		it('exposes per-engine health details', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, seq: 10, timestamp: 2000 }));
			const h = r.engineHealth;
			expect(h.A.connected).toBe(true);
			expect(h.A.lastSeq).toBe(5);
			expect(h.B.connected).toBe(false);
		});

		it('tracks RTT per engine', () => {
			const r = createReconciler();
			r.setRTT('A', 25);
			r.setRTT('B', 50);
			expect(r.engineHealth.A.rtt).toBe(25);
			expect(r.engineHealth.B.rtt).toBe(50);
		});

		it('tracks clock offset per engine', () => {
			const r = createReconciler();
			r.setClockOffset('A', 150);
			r.setClockOffset('B', -200);
			expect(r.engineHealth.A.clockOffset).toBe(150);
			expect(r.engineHealth.B.clockOffset).toBe(-200);
		});

		it('disconnect marks engine as disconnected without clearing last state', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 3 }));
			r.disconnect('B');
			expect(r.engineHealth.B.connected).toBe(false);
			expect(r.engineHealth.B.lastSeq).toBe(3);
			// State should still be reconciled from A
			expect(r.health.status).toBe('single-engine');
		});

		it('tracks source health per engine', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'stale' },
				},
			}));
			const h = r.engineHealth;
			expect(h.A.sources['cam1']).toBe('healthy');
			expect(h.A.sources['cam2']).toBe('stale');
		});

		it('tracks uptime from first ingest', () => {
			vi.useFakeTimers();
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 1 }));
			vi.advanceTimersByTime(5000);
			// tick() updates lastTickTime which drives the uptime computation
			r.tick();
			expect(r.engineHealth.A.uptime).toBeGreaterThanOrEqual(5000);
			vi.useRealTimers();
		});
	});

	// ── Mode tracking ──

	describe('mode tracking', () => {
		it('reports single mode initially', () => {
			const r = createReconciler();
			expect(r.mode).toBe('single');
		});

		it('reports single mode when only one engine ingested', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 3 }));
			expect(r.mode).toBe('single');
		});

		it('reports dual mode once both engines ingested', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 3 }));
			r.ingest('B', makeState({ lastCommandSeq: 3 }));
			expect(r.mode).toBe('dual');
		});

		it('stays dual even after one engine disconnects', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 3 }));
			r.ingest('B', makeState({ lastCommandSeq: 3 }));
			r.disconnect('B');
			expect(r.mode).toBe('dual');
		});
	});

	// ── Disconnect and reconnect ──

	describe('disconnect and reconnect', () => {
		it('authoritative falls back to B when A disconnects', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 10 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('A');

			r.disconnect('A');
			expect(r.health.authoritative).toBe('B');
			expect(r.state.programSource).toBe('cam2');
		});

		it('authoritative falls back to A when B disconnects', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 3 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 10 }));
			expect(r.health.authoritative).toBe('B');

			r.disconnect('B');
			expect(r.health.authoritative).toBe('A');
			expect(r.state.programSource).toBe('cam1');
		});

		it('re-ingest after disconnect restores connected status', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			expect(r.health.status).toBe('synced');

			r.disconnect('B');
			expect(r.health.status).toBe('single-engine');

			// B reconnects with new state
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			expect(r.health.status).toBe('synced');
			expect(r.engineHealth.B.connected).toBe(true);
		});

		it('re-ingest with stale seq after reconnect shows correct authoritative', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 10 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 10 }));
			r.disconnect('B');

			// B comes back with an older seq (it missed some commands)
			r.ingest('B', makeState({ programSource: 'cam3', lastCommandSeq: 7 }));
			expect(r.health.authoritative).toBe('A');
			expect(r.state.programSource).toBe('cam1');
		});

		it('disconnect both engines results in disconnected status', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.disconnect('A');
			r.disconnect('B');
			expect(r.health.status).toBe('disconnected');
		});
	});

	// ── Rapid ingestion ──

	describe('rapid ingestion', () => {
		it('handles many rapid updates from one engine without crash', () => {
			const r = createReconciler();
			r.ingest('B', makeState({ lastCommandSeq: 1 }));
			for (let i = 1; i <= 100; i++) {
				r.ingest('A', makeState({ programSource: `cam${i % 4}`, lastCommandSeq: i }));
			}
			expect(r.state.programSource).toBe('cam0');
			expect(r.health.authoritative).toBe('A');
		});

		it('last ingest wins for authoritative when seq advances', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 1 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 2 }));
			expect(r.health.authoritative).toBe('B');
			expect(r.state.programSource).toBe('cam2');

			r.ingest('A', makeState({ programSource: 'cam3', lastCommandSeq: 3 }));
			expect(r.health.authoritative).toBe('A');
			expect(r.state.programSource).toBe('cam3');
		});
	});

	// ── rawStates getter ──

	describe('rawStates', () => {
		it('exposes per-engine raw state', () => {
			const r = createReconciler();
			const stA = makeState({ programSource: 'cam1', lastCommandSeq: 5 });
			const stB = makeState({ programSource: 'cam2', lastCommandSeq: 3 });
			r.ingest('A', stA);
			r.ingest('B', stB);
			expect(r.rawStates.A).toStrictEqual(stA);
			expect(r.rawStates.B).toStrictEqual(stB);
		});

		it('returns null for engines that have not reported', () => {
			const r = createReconciler();
			expect(r.rawStates.A).toBeNull();
			expect(r.rawStates.B).toBeNull();
		});

		it('returns null for A if only B has reported', () => {
			const r = createReconciler();
			r.ingest('B', makeState({ lastCommandSeq: 1 }));
			expect(r.rawStates.A).toBeNull();
			expect(r.rawStates.B).not.toBeNull();
		});
	});

	// ── Source health in authority selection ──

	describe('source health in authority selection', () => {
		it('authority prefers healthy engine when one has >50% degraded sources', () => {
			const r = createReconciler();
			// Engine A: 1 healthy, 2 offline => 33% healthy (<50%)
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'offline' },
					cam3: { key: 'cam3', type: 'demo', status: 'no_signal' },
				},
			}));
			// Engine B: 3 healthy => 100% healthy
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
					cam3: { key: 'cam3', type: 'demo', status: 'healthy' },
				},
			}));
			expect(r.health.authoritative).toBe('B');
		});

		it('authority prefers A when B has >50% degraded', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
				},
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'offline' },
					cam2: { key: 'cam2', type: 'demo', status: 'offline' },
				},
			}));
			expect(r.health.authoritative).toBe('A');
		});

		it('falls back to seq when both engines have similar health', () => {
			const r = createReconciler();
			// Both healthy but B has higher seq
			r.ingest('A', makeState({
				lastCommandSeq: 3,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
				},
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 7,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
				},
			}));
			expect(r.health.authoritative).toBe('B');
		});

		it('falls back to seq when both engines are equally degraded', () => {
			const r = createReconciler();
			// Both >50% degraded — health tie, fall through to seq
			r.ingest('A', makeState({
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'offline' },
					cam2: { key: 'cam2', type: 'demo', status: 'offline' },
					cam3: { key: 'cam3', type: 'demo', status: 'healthy' },
				},
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'offline' },
					cam2: { key: 'cam2', type: 'demo', status: 'offline' },
					cam3: { key: 'cam3', type: 'demo', status: 'healthy' },
				},
			}));
			// Both degraded so health doesn't break it — seq wins (A=10 > B=5)
			expect(r.health.authoritative).toBe('A');
		});

		it('ignores health when neither engine is majorly degraded', () => {
			const r = createReconciler();
			// A: 67% healthy, B: 100% healthy. Neither <50%, so seq wins.
			r.ingest('A', makeState({
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'offline' },
					cam3: { key: 'cam3', type: 'demo', status: 'healthy' },
				},
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
					cam3: { key: 'cam3', type: 'demo', status: 'healthy' },
				},
			}));
			expect(r.health.authoritative).toBe('A');
		});

		it('treats stale sources as healthy for authority', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'stale' },
					cam2: { key: 'cam2', type: 'demo', status: 'stale' },
				},
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
				},
			}));
			// Both >50% healthy, falls through to A wins tie
			expect(r.health.authoritative).toBe('A');
		});

		it('handles engines with no sources gracefully', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, sources: {} }));
			r.ingest('B', makeState({ lastCommandSeq: 5, sources: {} }));
			// No sources = not degraded, falls through to A wins tie
			expect(r.health.authoritative).toBe('A');
		});
	});

	// ── Expanded MISMATCH_FIELDS ──

	describe('expanded mismatch fields', () => {
		it('reports mismatch when ftbActive differs at same seq', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ ftbActive: false, lastCommandSeq: 5 }));
			r.ingest('B', makeState({ ftbActive: true, lastCommandSeq: 5 }));
			r.tick();
			expect(r.health.status).toBe('mismatch');
		});

		it('reports mismatch when previewSource differs at same seq', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ previewSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ previewSource: 'cam3', lastCommandSeq: 5 }));
			r.tick();
			expect(r.health.status).toBe('mismatch');
		});

		it('does not report mismatch when ftbActive and previewSource agree', () => {
			const r = createReconciler();
			const s = makeState({ ftbActive: true, previewSource: 'cam2', lastCommandSeq: 5 });
			r.ingest('A', s);
			r.ingest('B', { ...s });
			r.tick();
			expect(r.health.status).not.toBe('mismatch');
		});
	});

	// ── Recording divergence ──

	describe('recording divergence', () => {
		it('detects divergence when recording.active differs', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				recording: { active: true, filename: 'rec1.ts' },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				recording: { active: false },
			}));
			const divs = r.health.divergences;
			const recDiv = divs.find(d => d.field === 'recording');
			expect(recDiv).toBeDefined();
			expect((recDiv?.engineA as any)?.active).toBe(true);
			expect((recDiv?.engineB as any)?.active).toBe(false);
		});

		it('no recording divergence when both active', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				recording: { active: true, filename: 'rec1.ts' },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				recording: { active: true, filename: 'rec2.ts' },
			}));
			const divs = r.health.divergences;
			const recDiv = divs.find(d => d.field === 'recording');
			expect(recDiv).toBeUndefined();
		});

		it('no recording divergence when both undefined', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			const divs = r.health.divergences;
			const recDiv = divs.find(d => d.field === 'recording');
			expect(recDiv).toBeUndefined();
		});
	});

	// ── fieldEquals ──

	describe('fieldEquals', () => {
		it('returns true for identical primitives', () => {
			expect(fieldEquals(1, 1)).toBe(true);
			expect(fieldEquals('a', 'a')).toBe(true);
			expect(fieldEquals(true, true)).toBe(true);
		});

		it('returns false for different primitives', () => {
			expect(fieldEquals(1, 2)).toBe(false);
			expect(fieldEquals('a', 'b')).toBe(false);
			expect(fieldEquals(true, false)).toBe(false);
		});

		it('handles null and undefined', () => {
			expect(fieldEquals(null, null)).toBe(true);
			expect(fieldEquals(undefined, undefined)).toBe(true);
			expect(fieldEquals(null, undefined)).toBe(true);
			expect(fieldEquals(undefined, null)).toBe(true);
			expect(fieldEquals(null, 0)).toBe(false);
			expect(fieldEquals(undefined, '')).toBe(false);
		});

		it('compares nested objects via JSON', () => {
			expect(fieldEquals({ a: 1, b: 2 }, { a: 1, b: 2 })).toBe(true);
			expect(fieldEquals({ a: 1 }, { a: 2 })).toBe(false);
		});

		it('treats objects with different key ordering as equal', () => {
			expect(fieldEquals({ a: 1, b: 2 }, { b: 2, a: 1 })).toBe(true);
		});

		it('treats deeply nested objects with different key ordering as equal', () => {
			const objA = { outer: { x: 1, y: 2 }, z: 3 };
			const objB = { z: 3, outer: { y: 2, x: 1 } };
			expect(fieldEquals(objA, objB)).toBe(true);
		});

		it('treats objects with different values as not equal regardless of key order', () => {
			expect(fieldEquals({ a: 1, b: 2 }, { b: 3, a: 1 })).toBe(false);
		});

		it('handles nested arrays inside objects with different key ordering', () => {
			const objA = { items: [1, 2, 3], name: 'test' };
			const objB = { name: 'test', items: [1, 2, 3] };
			expect(fieldEquals(objA, objB)).toBe(true);
		});

		it('does not sort arrays (order matters)', () => {
			expect(fieldEquals([2, 1], [1, 2])).toBe(false);
		});

		it('compares arrays correctly', () => {
			expect(fieldEquals([1, 2], [1, 2])).toBe(true);
			expect(fieldEquals([1, 2], [1, 3])).toBe(false);
			expect(fieldEquals([1, 2], [1, 2, 3])).toBe(false);
		});

		it('returns false for different types', () => {
			expect(fieldEquals(1, '1')).toBe(false);
			expect(fieldEquals(true, 1)).toBe(false);
		});

		it('treats objects with different key order as equal', () => {
			expect(fieldEquals({ b: 2, a: 1 }, { a: 1, b: 2 })).toBe(true);
		});

		it('treats nested objects with different key order as equal', () => {
			expect(fieldEquals({ x: { b: 2, a: 1 } }, { x: { a: 1, b: 2 } })).toBe(true);
		});
	});

	// ── Expanded DIVERGENCE_FIELDS ──

	describe('expanded divergence fields', () => {
		it('detects transitionType divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ transitionType: 'cut', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ transitionType: 'dissolve', lastCommandSeq: 5 }));
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('transitionType');
		});

		it('detects transitionDurationMs divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ transitionDurationMs: 500, lastCommandSeq: 5 }));
			r.ingest('B', makeState({ transitionDurationMs: 1000, lastCommandSeq: 5 }));
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('transitionDurationMs');
		});
	});

	// ── Edge cases in state ──

	describe('edge cases', () => {
		it('returns EMPTY_STATE when no engines have reported', () => {
			const r = createReconciler();
			expect(r.state.programSource).toBe('');
			expect(r.state.previewSource).toBe('');
			expect(r.state.seq).toBe(0);
		});

		it('handles state with empty sources object', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ sources: {}, lastCommandSeq: 1 }));
			expect(r.engineHealth.A.sources).toEqual({});
		});

		it('divergence detection ignores audioChannels (not in DIVERGENCE_FIELDS)', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				audioChannels: [{ source: 'cam1', level: -6, muted: false, afv: false }] as any,
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				audioChannels: [{ source: 'cam1', level: -12, muted: true, afv: true }] as any,
			}));
			// audioChannels is not in DIVERGENCE_FIELDS, so should be synced
			expect(r.health.divergences).toHaveLength(0);
			expect(r.health.status).toBe('synced');
		});

		it('transitionPosition difference is detected as divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ transitionPosition: 0.5, lastCommandSeq: 5 }));
			r.ingest('B', makeState({ transitionPosition: 0.0, lastCommandSeq: 5 }));
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('transitionPosition');
		});
	});

	// ── Clock divergence warnings ──

	describe('clock divergence warnings', () => {
		it('health.warnings is empty by default', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			expect(r.health.warnings).toEqual([]);
		});

		it('includes clock_diverged warning when setClockDiverged(true)', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.setClockDiverged(true);
			expect(r.health.warnings).toContain('clock_diverged');
		});

		it('clears clock_diverged warning when setClockDiverged(false)', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.setClockDiverged(true);
			expect(r.health.warnings).toContain('clock_diverged');
			r.setClockDiverged(false);
			expect(r.health.warnings).not.toContain('clock_diverged');
			expect(r.health.warnings).toEqual([]);
		});

		it('warnings do not affect sync status', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.setClockDiverged(true);
			r.tick();
			// Status should still be synced despite clock warning
			expect(r.health.status).toBe('synced');
			expect(r.health.warnings).toContain('clock_diverged');
		});
	});

	// ── requestCheck (partial failure fast-path) ──

	describe('requestCheck', () => {
		it('immediately transitions lagging to diverged when seq delta exists', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 3 }));
			r.tick();
			// Normally would be 'synced' or 'lagging' because not enough time passed
			const statusBefore = r.health.status;
			expect(statusBefore === 'synced' || statusBefore === 'lagging').toBe(true);

			// requestCheck forces immediate re-evaluation past the threshold
			r.requestCheck();
			expect(r.health.status).toBe('diverged');
		});

		it('does nothing when engines are already synced', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.tick();
			expect(r.health.status).toBe('synced');

			r.requestCheck();
			expect(r.health.status).toBe('synced');
		});

		it('does nothing when only one engine is connected', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.tick();
			expect(r.health.status).toBe('single-engine');

			r.requestCheck();
			expect(r.health.status).toBe('single-engine');
		});
	});

	// ── Stale engine detection ──

	describe('stale engine detection', () => {
		beforeEach(() => {
			vi.useFakeTimers();
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		it('reports single-engine B authoritative when A is stale for 5s', () => {
			const r = createReconciler();
			const s = makeState({ lastCommandSeq: 5 });
			r.ingest('A', { ...s });
			r.ingest('B', { ...s });
			expect(r.health.status).toBe('synced');

			// Advance past stale threshold, only B sends updates
			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 100);
			r.ingest('B', makeState({ lastCommandSeq: 6 }));
			r.tick();

			expect(r.health.status).toBe('single-engine');
			expect(r.health.authoritative).toBe('B');
			expect(r.health.staleEngines).toContain('A');
			expect(r.health.staleEngines).not.toContain('B');
		});

		it('reports single-engine A authoritative when B is stale for 5s', () => {
			const r = createReconciler();
			const s = makeState({ lastCommandSeq: 5 });
			r.ingest('A', { ...s });
			r.ingest('B', { ...s });
			expect(r.health.status).toBe('synced');

			// Advance past stale threshold, only A sends updates
			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 100);
			r.ingest('A', makeState({ lastCommandSeq: 6 }));
			r.tick();

			expect(r.health.status).toBe('single-engine');
			expect(r.health.authoritative).toBe('A');
			expect(r.health.staleEngines).toContain('B');
			expect(r.health.staleEngines).not.toContain('A');
		});

		it('reports disconnected when both engines are stale', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			expect(r.health.status).toBe('synced');

			// Neither engine sends updates for >5s
			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 100);
			r.tick();

			expect(r.health.status).toBe('disconnected');
			expect(r.health.staleEngines).toContain('A');
			expect(r.health.staleEngines).toContain('B');
		});

		it('reports normal status when neither engine is stale', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.tick();

			expect(r.health.status).toBe('synced');
			expect(r.health.staleEngines).toHaveLength(0);
		});

		it('recovers from stale when engine resumes sending updates', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));

			// A goes stale
			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 100);
			r.ingest('B', makeState({ lastCommandSeq: 6 }));
			r.tick();
			expect(r.health.status).toBe('single-engine');
			expect(r.health.staleEngines).toContain('A');

			// A comes back
			r.ingest('A', makeState({ lastCommandSeq: 6 }));
			r.tick();
			expect(r.health.status).toBe('synced');
			expect(r.health.staleEngines).toHaveLength(0);
		});

		it('stale engine not counted in staleEngines if already disconnected', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));

			r.disconnect('A');
			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 100);
			r.ingest('B', makeState({ lastCommandSeq: 6 }));
			r.tick();

			// A is disconnected, not stale (stale requires connected=true)
			expect(r.health.staleEngines).not.toContain('A');
			expect(r.health.status).toBe('single-engine');
		});

		it('stale detection uses 5000ms threshold', () => {
			expect(STALE_THRESHOLD_MS).toBe(5000);

			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));

			// At 4900ms (just under threshold) — should still be synced
			vi.advanceTimersByTime(4900);
			r.tick();
			expect(r.health.status).toBe('synced');
			expect(r.health.staleEngines).toHaveLength(0);

			// At 5100ms (just over threshold) — both stale → disconnected
			vi.advanceTimersByTime(200);
			r.tick();
			expect(r.health.status).toBe('disconnected');
			expect(r.health.staleEngines).toHaveLength(2);
		});
	});

	// ── Graphics layer divergence ──

	describe('graphics layer divergence', () => {
		it('detects graphics layer html5Status divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				graphics: {
					layers: [{ id: 1, active: true, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080, html5Status: 'playing' }],
				},
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				graphics: {
					layers: [{ id: 1, active: true, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080, html5Status: 'error' }],
				},
			}));
			const divs = r.health.divergences;
			const graphicsDiv = divs.find((d) => d.field === 'graphics.layers');
			expect(graphicsDiv).toBeDefined();
		});

		it('detects graphics layer active state divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				graphics: {
					layers: [{ id: 1, active: true, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080 }],
				},
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				graphics: {
					layers: [{ id: 1, active: false, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080 }],
				},
			}));
			const divs = r.health.divergences;
			const graphicsDiv = divs.find((d) => d.field === 'graphics.layers');
			expect(graphicsDiv).toBeDefined();
			expect((graphicsDiv?.engineA as any)?.active).toBe(true);
			expect((graphicsDiv?.engineB as any)?.active).toBe(false);
		});

		it('does not report divergence when graphics layers match', () => {
			const r = createReconciler();
			const layer = { id: 1, active: true, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080, html5Status: 'playing' as const };
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				graphics: { layers: [layer] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				graphics: { layers: [{ ...layer }] },
			}));
			const divs = r.health.divergences;
			const graphicsDiv = divs.find((d) => d.field === 'graphics.layers');
			expect(graphicsDiv).toBeUndefined();
		});

		it('does not report divergence when both engines have no graphics state', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			const divs = r.health.divergences;
			const graphicsDiv = divs.find((d) => d.field === 'graphics.layers');
			expect(graphicsDiv).toBeUndefined();
		});

		it('skips layer pairs where one engine has no matching layer index', () => {
			const r = createReconciler();
			// A has 2 layers, B has 1 layer — index 1 has no counterpart in B
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				graphics: {
					layers: [
						{ id: 1, active: true, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080 },
						{ id: 2, active: true, zOrder: 1, x: 0, y: 0, width: 1920, height: 1080 },
					],
				},
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				graphics: {
					layers: [{ id: 1, active: true, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080 }],
				},
			}));
			// Extra layer in A but B has no counterpart — should not report divergence
			const divs = r.health.divergences;
			const graphicsDiv = divs.find((d) => d.field === 'graphics.layers');
			expect(graphicsDiv).toBeUndefined();
		});
	});

	// ── Layout activePreset divergence ──

	describe('layout activePreset divergence', () => {
		it('detects layout activePreset divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				layout: { activePreset: 'pip', slots: [] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				layout: { activePreset: 'side-by-side', slots: [] },
			}));
			const divs = r.health.divergences;
			const layoutDiv = divs.find((d) => d.field === 'layout.activePreset');
			expect(layoutDiv).toBeDefined();
			expect(layoutDiv?.engineA).toBe('pip');
			expect(layoutDiv?.engineB).toBe('side-by-side');
		});

		it('does not report layout divergence when activePreset matches', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				layout: { activePreset: 'pip', slots: [] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				layout: { activePreset: 'pip', slots: [] },
			}));
			const divs = r.health.divergences;
			const layoutDiv = divs.find((d) => d.field === 'layout.activePreset');
			expect(layoutDiv).toBeUndefined();
		});

		it('does not report layout divergence when both engines have no layout state', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			const divs = r.health.divergences;
			const layoutDiv = divs.find((d) => d.field === 'layout.activePreset');
			expect(layoutDiv).toBeUndefined();
		});

		it('detects divergence when one engine has layout and other does not', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				layout: { activePreset: 'pip', slots: [] },
			}));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			const divs = r.health.divergences;
			const layoutDiv = divs.find((d) => d.field === 'layout.activePreset');
			expect(layoutDiv).toBeDefined();
			expect(layoutDiv?.engineA).toBe('pip');
			expect(layoutDiv?.engineB).toBeUndefined();
		});
	});

	// ── Transport-alive awareness ──

	describe('transport-alive stale detection', () => {
		beforeEach(() => {
			vi.useFakeTimers();
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		it('does not report stale when transport is alive but idle', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));

			// Mark both transports alive (WebTransport connected)
			r.setTransportAlive('A', true);
			r.setTransportAlive('B', true);

			// Advance past the stale threshold with no new state updates
			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 1000);
			r.tick();

			// Should NOT be stale while transport is alive
			expect(r.health.status).toBe('synced');
			expect(r.health.staleEngines).toHaveLength(0);
		});

		it('reports stale when transport is dead and no updates', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));

			// Transport is NOT alive (polling or disconnected)
			r.setTransportAlive('A', false);
			r.setTransportAlive('B', false);

			// Advance past the stale threshold
			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 1000);
			r.tick();

			// Both stale → disconnected
			expect(r.health.status).toBe('disconnected');
		});

		it('reports single-engine when only one transport goes stale', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));

			// Only B's transport dies
			r.setTransportAlive('A', true);
			r.setTransportAlive('B', false);

			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 1000);
			r.tick();

			// B is stale, A is alive → single-engine
			expect(r.health.status).toBe('single-engine');
			expect(r.health.staleEngines).toContain('B');
			expect(r.health.staleEngines).not.toContain('A');
		});

		it('transitions from synced to stale when transport dies', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.setTransportAlive('A', true);
			r.setTransportAlive('B', true);

			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 1000);
			r.tick();
			expect(r.health.status).toBe('synced');

			// Transport dies
			r.setTransportAlive('B', false);
			r.tick();

			expect(r.health.staleEngines).toContain('B');
		});
	});
});

describe('sortedStringify', () => {
	it('handles null and undefined', () => {
		expect(sortedStringify(null)).toBe('null');
		expect(sortedStringify(undefined)).toBe('undefined');
	});

	it('handles primitives', () => {
		expect(sortedStringify(42)).toBe('42');
		expect(sortedStringify('hello')).toBe('"hello"');
		expect(sortedStringify(true)).toBe('true');
	});

	it('sorts object keys deterministically', () => {
		const a = sortedStringify({ z: 1, a: 2 });
		const b = sortedStringify({ a: 2, z: 1 });
		expect(a).toBe(b);
		expect(a).toBe('{"a":2,"z":1}');
	});

	it('handles arrays preserving order', () => {
		expect(sortedStringify([3, 1, 2])).toBe('[3,1,2]');
	});

	it('handles nested objects with sorted keys', () => {
		const result = sortedStringify({ b: { d: 1, c: 2 }, a: 3 });
		expect(result).toBe('{"a":3,"b":{"c":2,"d":1}}');
	});

	it('serializes Date objects as ISO strings', () => {
		const date = new Date('2026-04-07T12:00:00.000Z');
		const result = sortedStringify(date);
		expect(result).toBe('"2026-04-07T12:00:00.000Z"');
	});

	it('handles Date inside an object', () => {
		const date = new Date('2026-01-01T00:00:00.000Z');
		const result = sortedStringify({ ts: date, name: 'test' });
		expect(result).toBe('{"name":"test","ts":"2026-01-01T00:00:00.000Z"}');
	});

	it('handles Date inside an array', () => {
		const date = new Date('2026-06-15T08:30:00.000Z');
		const result = sortedStringify([date, 42]);
		expect(result).toBe('["2026-06-15T08:30:00.000Z",42]');
	});
});
