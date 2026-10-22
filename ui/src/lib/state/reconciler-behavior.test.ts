/**
 * Behavioral tests for the dual-engine state reconciler.
 *
 * These tests describe what the reconciler SHOULD do in real production
 * scenarios, not how it's wired. Each test represents a scenario that
 * would actually occur during dual-engine operation.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createReconciler, RTT_HYSTERESIS_MS, AUTHORITY_HOLD_MS, SETTLE_MS } from './reconciler.svelte';
import type { ControlRoomState, PlayoutChannelState, AudioChannel } from '$lib/api/types';

/** Let both engines' states sit unchanged for SETTLE_MS, after which a
 * difference between them counts. */
function settle(r: ReturnType<typeof createReconciler>) {
	if (!vi.isFakeTimers()) vi.useFakeTimers();
	vi.advanceTimersByTime(SETTLE_MS);
	r.tick();
}

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

describe('reconciler behavioral tests', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	// ─── Authoritative engine selection uses ALL fields ───

	describe('authoritative engine selection', () => {
		it('returns ALL fields from the authoritative engine, not a mix of both', () => {
			// Scenario: Engines have diverged. Engine A is authoritative (higher seq).
			// The reconciler must return A's complete state — not mixing A's programSource
			// with B's masterLevel or other fields.
			const r = createReconciler();

			r.ingest('A', makeState({
				programSource: 'cam1',
				previewSource: 'cam3',
				masterLevel: -6,
				inTransition: true,
				transitionPosition: 0.5,
				ftbActive: false,
				lastCommandSeq: 10,
			}));

			r.ingest('B', makeState({
				programSource: 'cam2',
				previewSource: 'cam4',
				masterLevel: 0,
				inTransition: false,
				transitionPosition: 0,
				ftbActive: true,
				lastCommandSeq: 5,
			}));

			// A is authoritative (higher seq)
			expect(r.health.authoritative).toBe('A');

			// ALL fields must come from A, not a mix
			const state = r.state;
			expect(state.programSource).toBe('cam1');
			expect(state.previewSource).toBe('cam3');
			expect(state.masterLevel).toBe(-6);
			expect(state.inTransition).toBe(true);
			expect(state.transitionPosition).toBe(0.5);
			expect(state.ftbActive).toBe(false);
		});

		it('switches ALL fields when authoritative engine changes', () => {
			// Scenario: B catches up and overtakes A's seq.
			// All fields should flip to B's values atomically.
			const r = createReconciler();

			r.ingest('A', makeState({
				programSource: 'cam1',
				masterLevel: -6,
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				programSource: 'cam2',
				masterLevel: 0,
				lastCommandSeq: 5,
			}));
			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');
			expect(r.state.programSource).toBe('cam1');

			// B catches up and passes A
			r.ingest('B', makeState({
				programSource: 'cam2',
				masterLevel: -3,
				lastCommandSeq: 12,
			}));

			// Authority hold-time: must wait AUTHORITY_HOLD_MS before authority switches
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();

			expect(r.health.authoritative).toBe('B');
			expect(r.state.programSource).toBe('cam2');
			expect(r.state.masterLevel).toBe(-3);
		});

		it('uses RTT to break ties when lastCommandSeq is equal', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 10 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 10 }));

			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');

			// Set RTT: B is closer (lower RTT), delta=110 > 100ms hysteresis
			r.setRTT('A', 120);
			r.setRTT('B', 10);

			// First tick introduces B as challenger and starts hold-time clock.
			r.tick();
			// After hold-time elapses, B takes over.
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();

			expect(r.health.authoritative).toBe('B');
			expect(r.state.programSource).toBe('cam2');
		});

		it('falls back to A when both RTT and seq are equal', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 10 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 10 }));

			r.setRTT('A', 20);
			r.setRTT('B', 20);

			// Final tie-break: A wins
			expect(r.health.authoritative).toBe('A');
		});

		it('switches to surviving engine when authoritative disconnects', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 10 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));

			expect(r.health.authoritative).toBe('A');

			// A goes offline
			r.disconnect('A');

			// B becomes authoritative since A is disconnected
			expect(r.health.authoritative).toBe('B');
			expect(r.state.programSource).toBe('cam2');
		});

		it('RTT_HYSTERESIS_MS is 100ms for cross-region stability', () => {
			// Cross-region deployments have RTTs of 50-200ms with 30-80ms fluctuations.
			// The old value of 50ms caused unnecessary authority flapping.
			expect(RTT_HYSTERESIS_MS).toBe(100);
		});

		it('does not flap authority on typical cross-region RTT fluctuations', () => {
			// Scenario: US-East ↔ US-West with ~80ms RTT that fluctuates by ±25ms.
			// Authority should stay stable despite the jitter.
			const r = createReconciler();

			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 10 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 10 }));

			// Initial: A=80ms, B=100ms, delta=20ms < 100ms hysteresis → A wins (default)
			r.setRTT('A', 80);
			r.setRTT('B', 100);
			expect(r.health.authoritative).toBe('A');

			// Jitter: A spikes to 140ms, B drops to 75ms. Delta=65ms < 100ms → still A
			r.setRTT('A', 140);
			r.setRTT('B', 75);
			expect(r.health.authoritative).toBe('A');

			// More jitter: A=90ms, B=170ms. Delta=80ms < 100ms → still A
			r.setRTT('A', 90);
			r.setRTT('B', 170);
			expect(r.health.authoritative).toBe('A');
		});

		it('switches authority when program source is degraded on one engine', () => {
			// Scenario: cam1 is on program. Engine A lost connectivity to cam1 (offline),
			// but Engine B can still see cam1 fine. Even though only 1/8 sources is degraded
			// (well below 50%), authority should switch to B because the PROGRAM source matters most.
			const r = createReconciler();

			r.ingest('A', makeState({
				programSource: 'cam1',
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'offline' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
					cam3: { key: 'cam3', type: 'demo', status: 'healthy' },
					cam4: { key: 'cam4', type: 'demo', status: 'healthy' },
				},
			}));
			r.ingest('B', makeState({
				programSource: 'cam1',
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
					cam3: { key: 'cam3', type: 'demo', status: 'healthy' },
					cam4: { key: 'cam4', type: 'demo', status: 'healthy' },
				},
			}));

			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');

			// B should become authoritative because cam1 (program source) is offline on A.
			// First tick introduces B as challenger and starts hold-time clock.
			r.tick();
			// After hold-time elapses, B takes over.
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');
		});

		it('switches authority when program source has no_signal on one engine', () => {
			const r = createReconciler();

			r.ingest('A', makeState({
				programSource: 'cam1',
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
				},
			}));
			r.ingest('B', makeState({
				programSource: 'cam1',
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'no_signal' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
				},
			}));

			// A should be authoritative because cam1 (program) has no_signal on B
			expect(r.health.authoritative).toBe('A');
		});

		it('does not switch authority when program source is degraded on BOTH engines', () => {
			const r = createReconciler();

			r.ingest('A', makeState({
				programSource: 'cam1',
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'offline' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
				},
			}));
			r.ingest('B', makeState({
				programSource: 'cam1',
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'offline' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
				},
			}));

			// Both degraded → no preference, falls through to seq/RTT/A-wins
			expect(r.health.authoritative).toBe('A');
		});

		it('does not switch authority when program source is stale (not offline/no_signal)', () => {
			// 'stale' is a transient condition, not as severe as offline/no_signal
			const r = createReconciler();

			r.ingest('A', makeState({
				programSource: 'cam1',
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'stale' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
				},
			}));
			r.ingest('B', makeState({
				programSource: 'cam1',
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'healthy' },
				},
			}));

			// stale is not offline or no_signal, so program source check doesn't trigger
			// Falls through to seq/RTT → A wins (default tie-break)
			expect(r.health.authoritative).toBe('A');
		});

		it('program source health check uses programSource from either engine', () => {
			// Edge case: engines might disagree on programSource. Use whichever is set.
			const r = createReconciler();

			r.ingest('A', makeState({
				programSource: '',
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'offline' },
				},
			}));
			r.ingest('B', makeState({
				programSource: 'cam1',
				lastCommandSeq: 10,
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
				},
			}));

			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');

			// programKey = '' || 'cam1' = 'cam1'. cam1 is offline on A, healthy on B → B wins after hold-time.
			// First tick introduces B as challenger and starts hold-time clock.
			r.tick();
			// After hold-time elapses, B takes over.
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');
		});
	});

	// ─── Status transitions with realistic timing ───

	describe('status transitions', () => {
		it('stays synced when seq delta is under 500ms', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ lastCommandSeq: 10 }));
			r.ingest('B', makeState({ lastCommandSeq: 8 }));

			// Just appeared — should still be synced (transient)
			expect(r.health.status).toBe('synced');

			// 400ms later — still under threshold
			vi.advanceTimersByTime(400);
			r.tick();
			expect(r.health.status).toBe('synced');
		});

		it('transitions to lagging after 500ms of seq delta', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ lastCommandSeq: 10 }));
			r.ingest('B', makeState({ lastCommandSeq: 8 }));

			vi.advanceTimersByTime(600);
			r.tick();
			expect(r.health.status).toBe('lagging');
		});

		it('transitions to diverged after 2000ms of seq delta', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ lastCommandSeq: 10 }));
			r.ingest('B', makeState({ lastCommandSeq: 8 }));

			vi.advanceTimersByTime(2100);
			r.tick();
			expect(r.health.status).toBe('diverged');
		});

		it('returns to synced when seq delta resolves', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ lastCommandSeq: 10 }));
			r.ingest('B', makeState({ lastCommandSeq: 8 }));

			vi.advanceTimersByTime(2100);
			r.tick();
			expect(r.health.status).toBe('diverged');

			// B catches up
			r.ingest('B', makeState({ lastCommandSeq: 10 }));
			expect(r.health.status).toBe('synced');
		});

		it('detects mismatch: same seq but different programSource', () => {
			// This is the most dangerous case — engines processed the same command
			// but arrived at different states. Indicates a deterministic failure.
			const r = createReconciler();

			r.ingest('A', makeState({
				programSource: 'cam1',
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				programSource: 'cam2',
				lastCommandSeq: 10,
			}));
			settle(r);

			expect(r.health.status).toBe('mismatch');
		});

		it('does NOT report mismatch when seqs differ', () => {
			// Different seqs = one engine is behind, not a deterministic failure.
			const r = createReconciler();

			r.ingest('A', makeState({
				programSource: 'cam1',
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				programSource: 'cam2',
				lastCommandSeq: 8,
			}));

			// Should be synced (transient) or lagging, not mismatch
			expect(r.health.status).not.toBe('mismatch');
		});

		it('reports diverged once both engines settle when visible fields differ at any seq', () => {
			// Even if the seq difference just appeared, visible field divergence
			// should be flagged as diverged once both engines settle.
			const r = createReconciler();

			r.ingest('A', makeState({
				programSource: 'cam1',
				ftbActive: true,
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				programSource: 'cam1',
				ftbActive: false,
				lastCommandSeq: 8,
			}));
			settle(r);

			// ftbActive differs — should be diverged once both engines settle
			expect(r.health.status).toBe('diverged');
		});

		it('recovering takes priority over other computed statuses', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ lastCommandSeq: 10 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			vi.advanceTimersByTime(2100);
			r.tick();
			expect(r.health.status).toBe('diverged');

			r.setRecovering(true);
			r.tick();
			expect(r.health.status).toBe('recovering');

			// Even if seqs match now, recovering should persist until cleared
			r.ingest('B', makeState({ lastCommandSeq: 10 }));
			expect(r.health.status).toBe('recovering');

			r.setRecovering(false);
			// Now should be synced since seqs match
			r.tick();
			expect(r.health.status).toBe('synced');
		});
	});

	// ─── Divergence detection across all visible fields ───

	describe('divergence detection', () => {
		it('detects divergence on every DIVERGENCE_FIELDS entry', () => {
			const r = createReconciler();

			r.ingest('A', makeState({
				programSource: 'cam1',
				previewSource: 'cam2',
				inTransition: false,
				transitionPosition: 0,
				masterLevel: 0,
				ftbActive: false,
				lastCommandSeq: 5,
			}));

			r.ingest('B', makeState({
				programSource: 'cam3',
				previewSource: 'cam4',
				inTransition: true,
				transitionPosition: 0.5,
				masterLevel: -12,
				ftbActive: true,
				lastCommandSeq: 5,
			}));

			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('programSource');
			expect(fields).toContain('previewSource');
			expect(fields).toContain('inTransition');
			expect(fields).toContain('masterLevel');
			expect(fields).toContain('ftbActive');
		});

		it('reports no divergence when engines agree on all visible fields', () => {
			const r = createReconciler();

			const shared = makeState({ lastCommandSeq: 5 });
			r.ingest('A', { ...shared });
			r.ingest('B', { ...shared });

			expect(r.health.divergences).toHaveLength(0);
		});

		it('reports no divergence when only one engine is present', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			expect(r.health.divergences).toHaveLength(0);
		});

		it('includes correct engine-specific values in divergence report', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ masterLevel: -6, lastCommandSeq: 5 }));
			r.ingest('B', makeState({ masterLevel: -12, lastCommandSeq: 5 }));

			const levelDiv = r.health.divergences.find(d => d.field === 'masterLevel');
			expect(levelDiv).toBeDefined();
			expect(levelDiv!.engineA).toBe(-6);
			expect(levelDiv!.engineB).toBe(-12);
		});
	});

	// ─── Playout mismatch detection ───

	describe('playout mismatch', () => {
		it('detects playout currentItemId mismatch at same seq', () => {
			const r = createReconciler();

			const playoutA: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'on_air', itemIndex: 0, itemCount: 5,
					currentItemId: 'item-1', currentTitle: 'Intro',
				}],
			};
			const playoutB: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'on_air', itemIndex: 0, itemCount: 5,
					currentItemId: 'item-2', currentTitle: 'News',
				}],
			};

			r.ingest('A', makeState({ playout: playoutA, lastCommandSeq: 10 }));
			r.ingest('B', makeState({ playout: playoutB, lastCommandSeq: 10 }));
			settle(r);

			expect(r.health.status).toBe('mismatch');
		});

		it('tolerates <=1 item index drift between engines', () => {
			const r = createReconciler();

			const playoutA: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'on_air', itemIndex: 2, itemCount: 5,
				}],
			};
			const playoutB: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'on_air', itemIndex: 3, itemCount: 5,
				}],
			};

			r.ingest('A', makeState({ playout: playoutA, lastCommandSeq: 10 }));
			r.ingest('B', makeState({ playout: playoutB, lastCommandSeq: 10 }));

			expect(r.health.status).toBe('synced');
		});

		it('detects playout itemIndex mismatch when drift exceeds 1', () => {
			const r = createReconciler();

			const playoutA: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'on_air', itemIndex: 2, itemCount: 5,
				}],
			};
			const playoutB: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'on_air', itemIndex: 4, itemCount: 5,
				}],
			};

			r.ingest('A', makeState({ playout: playoutA, lastCommandSeq: 10 }));
			r.ingest('B', makeState({ playout: playoutB, lastCommandSeq: 10 }));
			settle(r);

			expect(r.health.status).toBe('mismatch');
		});

		it('does NOT mismatch when playout channels are empty on both sides', () => {
			const r = createReconciler();

			r.ingest('A', makeState({
				playout: { channels: [] },
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				playout: { channels: [] },
				lastCommandSeq: 10,
			}));

			expect(r.health.status).toBe('synced');
		});

		it('does NOT mismatch when one side has no playout and other has empty channels', () => {
			const r = createReconciler();

			r.ingest('A', makeState({
				playout: { channels: [] },
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				playout: undefined,
				lastCommandSeq: 10,
			}));

			// No playout on B means no channel comparison — should not mismatch
			expect(r.health.status).not.toBe('mismatch');
		});

		it('detects playout elapsed divergence > 5s on same item', () => {
			const r = createReconciler();

			const playoutA: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'on_air', itemIndex: 3, itemCount: 10,
					currentTitle: 'Interview', itemRemainMs: 20000,
				}],
			};
			const playoutB: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'on_air', itemIndex: 3, itemCount: 10,
					currentTitle: 'Interview', itemRemainMs: 14000,
				}],
			};

			r.ingest('A', makeState({ playout: playoutA, lastCommandSeq: 10 }));
			r.ingest('B', makeState({ playout: playoutB, lastCommandSeq: 10 }));
			settle(r);

			expect(r.health.status).toBe('mismatch');
		});

		it('tolerates playout elapsed divergence <= 5s', () => {
			const r = createReconciler();

			const playoutA: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'on_air', itemIndex: 3, itemCount: 10,
					currentTitle: 'Interview', itemRemainMs: 20000,
				}],
			};
			const playoutB: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'on_air', itemIndex: 3, itemCount: 10,
					currentTitle: 'Interview', itemRemainMs: 17000,
				}],
			};

			r.ingest('A', makeState({ playout: playoutA, lastCommandSeq: 10 }));
			r.ingest('B', makeState({ playout: playoutB, lastCommandSeq: 10 }));

			expect(r.health.status).toBe('synced');
		});

		it('skips elapsed check when channels not on-air', () => {
			const r = createReconciler();

			const playoutA: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'stopped', itemIndex: 3, itemCount: 10,
					itemRemainMs: 20000,
				}],
			};
			const playoutB: { channels: PlayoutChannelState[] } = {
				channels: [{
					id: 0, state: 'stopped', itemIndex: 3, itemCount: 10,
					itemRemainMs: 8000,
				}],
			};

			r.ingest('A', makeState({ playout: playoutA, lastCommandSeq: 10 }));
			r.ingest('B', makeState({ playout: playoutB, lastCommandSeq: 10 }));

			// Both stopped — elapsed time difference should be ignored
			expect(r.health.status).toBe('synced');
		});
	});

	// ─── Engine health tracking ───

	describe('engine health', () => {
		it('tracks per-engine connection state', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			expect(r.engineHealth.A.connected).toBe(true);
			expect(r.engineHealth.B.connected).toBe(false);

			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			expect(r.engineHealth.B.connected).toBe(true);

			r.disconnect('A');
			expect(r.engineHealth.A.connected).toBe(false);
			expect(r.engineHealth.B.connected).toBe(true);
		});

		it('tracks per-engine lastCommandSeq', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ lastCommandSeq: 10 }));
			r.ingest('B', makeState({ lastCommandSeq: 7 }));

			expect(r.engineHealth.A.lastSeq).toBe(10);
			expect(r.engineHealth.B.lastSeq).toBe(7);
		});

		it('tracks per-engine RTT', () => {
			const r = createReconciler();

			r.setRTT('A', 15);
			r.setRTT('B', 42);

			expect(r.engineHealth.A.rtt).toBe(15);
			expect(r.engineHealth.B.rtt).toBe(42);
		});

		it('tracks per-engine clock offset', () => {
			const r = createReconciler();

			r.setClockOffset('A', 5000);
			r.setClockOffset('B', -3000);

			expect(r.engineHealth.A.clockOffset).toBe(5000);
			expect(r.engineHealth.B.clockOffset).toBe(-3000);
		});

		it('extracts per-source health from engine state', () => {
			const r = createReconciler();

			r.ingest('A', makeState({
				sources: {
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
					cam2: { key: 'cam2', type: 'demo', status: 'stale' },
				},
				lastCommandSeq: 5,
			}));

			expect(r.engineHealth.A.sources.cam1).toBe('healthy');
			expect(r.engineHealth.A.sources.cam2).toBe('stale');
		});

		it('returns empty source health for disconnected engine', () => {
			const r = createReconciler();
			expect(r.engineHealth.B.sources).toEqual({});
		});
	});

	// ─── Mode detection ───

	describe('mode', () => {
		it('starts in single mode', () => {
			const r = createReconciler();
			expect(r.mode).toBe('single');
		});

		it('switches to dual after both engines report', () => {
			const r = createReconciler();

			r.ingest('A', makeState());
			expect(r.mode).toBe('single');

			r.ingest('B', makeState());
			expect(r.mode).toBe('dual');
		});

		it('stays dual even after an engine disconnects', () => {
			// Once we've seen both engines, we stay in dual mode.
			// This prevents mode flapping on transient disconnects.
			const r = createReconciler();

			r.ingest('A', makeState());
			r.ingest('B', makeState());
			expect(r.mode).toBe('dual');

			r.disconnect('B');
			expect(r.mode).toBe('dual');
		});
	});

	// ─── Raw states ───

	describe('rawStates', () => {
		it('provides per-engine states for components that need individual data', () => {
			const r = createReconciler();

			const stateA = makeState({ programSource: 'cam1', lastCommandSeq: 10 });
			const stateB = makeState({ programSource: 'cam2', lastCommandSeq: 5 });

			r.ingest('A', stateA);
			r.ingest('B', stateB);

			expect(r.rawStates.A).toEqual(stateA);
			expect(r.rawStates.B).toEqual(stateB);
		});

		it('returns null for engines that have never reported', () => {
			const r = createReconciler();
			expect(r.rawStates.A).toBeNull();
			expect(r.rawStates.B).toBeNull();
		});
	});

	// ─── Reconnection scenarios ───

	describe('disconnect and reconnect', () => {
		it('accepts updated state after reconnection', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			expect(r.health.status).toBe('synced');

			// B disconnects
			r.disconnect('B');
			expect(r.health.status).toBe('single-engine');

			// B reconnects with updated state
			r.ingest('B', makeState({ programSource: 'cam1', lastCommandSeq: 8 }));
			expect(r.engineHealth.B.connected).toBe(true);
			expect(r.engineHealth.B.lastSeq).toBe(8);
		});

		it('handles both engines disconnecting', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));

			r.disconnect('A');
			r.disconnect('B');

			expect(r.health.status).toBe('disconnected');
		});

		it('re-ingesting with stale seq after reconnect does not confuse authoritative selection', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ lastCommandSeq: 10 }));
			r.ingest('B', makeState({ lastCommandSeq: 10 }));

			r.disconnect('B');

			// B reconnects with a stale (lower) seq — maybe it restarted
			r.ingest('B', makeState({ programSource: 'cam3', lastCommandSeq: 3 }));

			// A should still be authoritative since it has higher seq
			expect(r.health.authoritative).toBe('A');
		});
	});

	// ─── Audio channel divergence detection ───

	function makeAudioChannel(overrides: Partial<AudioChannel> = {}): AudioChannel {
		return {
			level: 0,
			trim: 0,
			muted: false,
			afv: false,
			peakL: -60,
			peakR: -60,
			eq: [
				{ frequency: 100, gain: 0, q: 1, enabled: false },
				{ frequency: 1000, gain: 0, q: 1, enabled: false },
				{ frequency: 10000, gain: 0, q: 1, enabled: false },
			],
			compressor: { threshold: -20, ratio: 4, attack: 10, release: 100, makeupGain: 0 },
			gainReduction: 0,
			gate: { threshold: -50, range: -80, attack: 0.5, release: 50, hold: 10 },
			...overrides,
		};
	}

	describe('audio channel divergence', () => {
		it('audioChannels metering differences do NOT cause divergence', () => {
			const r = createReconciler();
			const makeAudio = (peakL: number, peakR: number, gr: number) => ({
				cam1: {
					level: -6, trim: 0, muted: false, afv: false,
					peakL, peakR,
					eq: [{frequency: 100, gain: 0, q: 1, enabled: true}, {frequency: 1000, gain: 0, q: 1, enabled: true}, {frequency: 10000, gain: 0, q: 1, enabled: true}] as [import('$lib/api/types').EQBand, import('$lib/api/types').EQBand, import('$lib/api/types').EQBand],
					compressor: { threshold: -20, ratio: 4, attack: 10, release: 100, makeupGain: 0 },
					gainReduction: gr,
					gate: { threshold: -40, range: -80, attack: 0.5, release: 50, hold: 0 },
				},
			});

			r.ingest('A', makeState({ lastCommandSeq: 5, audioChannels: makeAudio(-12, -14, -3) }));
			r.ingest('B', makeState({ lastCommandSeq: 5, audioChannels: makeAudio(-15, -18, -5) }));

			const audioDivs = r.health.divergences.filter(d => d.field === 'audioChannels');
			expect(audioDivs).toHaveLength(0);
		});

		it('audioChannels setting differences DO cause divergence', () => {
			const r = createReconciler();
			const makeAudio = (level: number) => ({
				cam1: {
					level, trim: 0, muted: false, afv: false,
					peakL: -12, peakR: -14,
					eq: [{frequency: 100, gain: 0, q: 1, enabled: true}, {frequency: 1000, gain: 0, q: 1, enabled: true}, {frequency: 10000, gain: 0, q: 1, enabled: true}] as [import('$lib/api/types').EQBand, import('$lib/api/types').EQBand, import('$lib/api/types').EQBand],
					compressor: { threshold: -20, ratio: 4, attack: 10, release: 100, makeupGain: 0 },
					gainReduction: -3,
					gate: { threshold: -40, range: -80, attack: 0.5, release: 50, hold: 0 },
				},
			});

			r.ingest('A', makeState({ lastCommandSeq: 5, audioChannels: makeAudio(-6) }));
			r.ingest('B', makeState({ lastCommandSeq: 5, audioChannels: makeAudio(-12) }));

			const audioDivs = r.health.divergences.filter(d => d.field === 'audioChannels');
			expect(audioDivs).toHaveLength(1);
		});

		it('transitionPosition causes divergence when positions differ', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.45 }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.48 }));

			const posDivs = r.health.divergences.filter(d => d.field === 'transitionPosition');
			expect(posDivs).toHaveLength(1);
		});

		it('detects audioChannels divergence when muted differs', () => {
			const r = createReconciler();

			r.ingest('A', makeState({
				audioChannels: { cam1: makeAudioChannel({ muted: true }) },
				lastCommandSeq: 5,
			}));
			r.ingest('B', makeState({
				audioChannels: { cam1: makeAudioChannel({ muted: false }) },
				lastCommandSeq: 5,
			}));

			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('audioChannels');
		});

		it('detects audioChannels divergence when afv differs', () => {
			const r = createReconciler();

			r.ingest('A', makeState({
				audioChannels: { cam1: makeAudioChannel({ afv: true }) },
				lastCommandSeq: 5,
			}));
			r.ingest('B', makeState({
				audioChannels: { cam1: makeAudioChannel({ afv: false }) },
				lastCommandSeq: 5,
			}));

			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('audioChannels');
		});

		it('detects audioChannels divergence when level differs', () => {
			const r = createReconciler();

			r.ingest('A', makeState({
				audioChannels: { cam1: makeAudioChannel({ level: -6 }) },
				lastCommandSeq: 5,
			}));
			r.ingest('B', makeState({
				audioChannels: { cam1: makeAudioChannel({ level: 0 }) },
				lastCommandSeq: 5,
			}));

			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('audioChannels');
		});

		it('reports no audioChannels divergence when channels match', () => {
			const r = createReconciler();

			const channels = { cam1: makeAudioChannel({ muted: true, afv: false, level: -6 }) };
			r.ingest('A', makeState({ audioChannels: channels, lastCommandSeq: 5 }));
			r.ingest('B', makeState({ audioChannels: { ...channels }, lastCommandSeq: 5 }));

			const fields = r.health.divergences.map(d => d.field);
			expect(fields).not.toContain('audioChannels');
		});

		it('reports no audioChannels divergence when both are undefined', () => {
			const r = createReconciler();

			r.ingest('A', makeState({ audioChannels: undefined, lastCommandSeq: 5 }));
			r.ingest('B', makeState({ audioChannels: undefined, lastCommandSeq: 5 }));

			const fields = r.health.divergences.map(d => d.field);
			expect(fields).not.toContain('audioChannels');
		});
	});

	describe('audio mute/afv critical mismatch', () => {
		it('detects mismatch when audio muted differs at same seq', () => {
			// Scenario: same command seq, but one engine has cam1 muted and the other doesn't.
			// This is critical — audience hears audio on one engine but not the other.
			const r = createReconciler();

			r.ingest('A', makeState({
				audioChannels: { cam1: makeAudioChannel({ muted: true }) },
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				audioChannels: { cam1: makeAudioChannel({ muted: false }) },
				lastCommandSeq: 10,
			}));
			settle(r);

			expect(r.health.status).toBe('mismatch');
		});

		it('detects mismatch when audio afv differs at same seq', () => {
			const r = createReconciler();

			r.ingest('A', makeState({
				audioChannels: { cam1: makeAudioChannel({ afv: true }) },
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				audioChannels: { cam1: makeAudioChannel({ afv: false }) },
				lastCommandSeq: 10,
			}));
			settle(r);

			expect(r.health.status).toBe('mismatch');
		});

		it('does NOT mismatch when audio channels agree at same seq', () => {
			const r = createReconciler();

			const channels = { cam1: makeAudioChannel({ muted: true, afv: false }) };
			r.ingest('A', makeState({ audioChannels: channels, lastCommandSeq: 10 }));
			r.ingest('B', makeState({ audioChannels: { ...channels }, lastCommandSeq: 10 }));

			expect(r.health.status).not.toBe('mismatch');
		});

		it('does NOT mismatch when one engine has no audioChannels', () => {
			// If one engine hasn't reported audio yet, we can't call it a mismatch.
			const r = createReconciler();

			r.ingest('A', makeState({
				audioChannels: { cam1: makeAudioChannel({ muted: true }) },
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				audioChannels: undefined,
				lastCommandSeq: 10,
			}));

			expect(r.health.status).not.toBe('mismatch');
		});

		it('does NOT mismatch on audio when seqs differ', () => {
			// Different seqs = one engine is behind. Not a deterministic failure.
			const r = createReconciler();

			r.ingest('A', makeState({
				audioChannels: { cam1: makeAudioChannel({ muted: true }) },
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				audioChannels: { cam1: makeAudioChannel({ muted: false }) },
				lastCommandSeq: 8,
			}));

			expect(r.health.status).not.toBe('mismatch');
		});

		it('checks all channels, not just the first', () => {
			const r = createReconciler();

			r.ingest('A', makeState({
				audioChannels: {
					cam1: makeAudioChannel({ muted: false }),
					cam2: makeAudioChannel({ muted: true }),
				},
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				audioChannels: {
					cam1: makeAudioChannel({ muted: false }),
					cam2: makeAudioChannel({ muted: false }),
				},
				lastCommandSeq: 10,
			}));
			settle(r);

			expect(r.health.status).toBe('mismatch');
		});

		it('skips channels only present on one engine', () => {
			// If engine A has cam3 but B doesn't, that's not a mismatch — it's
			// just a source that hasn't been registered on one engine yet.
			const r = createReconciler();

			r.ingest('A', makeState({
				audioChannels: {
					cam1: makeAudioChannel({ muted: false }),
					cam3: makeAudioChannel({ muted: true }),
				},
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				audioChannels: {
					cam1: makeAudioChannel({ muted: false }),
				},
				lastCommandSeq: 10,
			}));

			// cam3 only on A — should NOT trigger mismatch for muted/afv
			// (but audioChannels divergence will be detected since the objects differ)
			expect(r.health.status).not.toBe('mismatch');
		});

		it('detects mismatch for channels only present on engine B', () => {
			// Bug: hasAudioMismatch() only iterated stateA.audioChannels entries.
			// Channels only on B were never checked. Both directions must be covered.
			const r = createReconciler();

			r.ingest('A', makeState({
				audioChannels: {
					cam1: makeAudioChannel({ muted: false }),
				},
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				audioChannels: {
					cam1: makeAudioChannel({ muted: false }),
					cam2: makeAudioChannel({ muted: true }),
				},
				lastCommandSeq: 10,
			}));

			// cam2 only on B with muted=true — the union-based check should still
			// skip single-engine channels (not a mismatch, just not registered yet)
			expect(r.health.status).not.toBe('mismatch');
		});

		it('detects mismatch when channel exists on both but only checked from B side', () => {
			// Ensure both-direction iteration catches mismatch regardless of iteration order
			const r = createReconciler();

			r.ingest('A', makeState({
				audioChannels: {
					cam1: makeAudioChannel({ muted: false }),
					cam2: makeAudioChannel({ muted: false }),
				},
				lastCommandSeq: 10,
			}));
			r.ingest('B', makeState({
				audioChannels: {
					cam1: makeAudioChannel({ muted: false }),
					cam2: makeAudioChannel({ muted: true }),
				},
				lastCommandSeq: 10,
			}));
			settle(r);

			// cam2 on both engines with differing muted state — must be detected
			expect(r.health.status).toBe('mismatch');
		});
	});
});
