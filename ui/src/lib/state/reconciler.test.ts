import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createAutoReconcile } from './auto-reconcile';
import { createReconciler, clipSlotCatchingUp, fieldEquals, STALE_THRESHOLD_MS, PONG_FRESH_MS, STATE_UPDATE_STALE_MS, AUTHORITY_HOLD_MS, sortedStringify, SETTLE_MS, SETTLE_MAX_MS, leastAdvancedUpload } from './reconciler.svelte';
import type { ControlRoomState, ClipPlayerState } from '$lib/api/types';

/** Let both engines' states sit unchanged for SETTLE_MS, after which a
 * difference between them counts. */
function settle(r: ReturnType<typeof createReconciler>) {
	if (!vi.isFakeTimers()) vi.useFakeTimers();
	vi.advanceTimersByTime(SETTLE_MS);
	r.tick();
}

afterEach(() => {
	vi.useRealTimers();
});


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

	// Engine B's state names engine A as its peer. Handed to the UI as is,
	// it would point every consumer of engineBUrl, the warm-standby and
	// failover pipelines among them, at engine A while B is authoritative.
	it('reports the URL engine B is connected at while engine B is authoritative', () => {
		vi.useFakeTimers();
		const r = createReconciler();
		r.setEngineBUrl('https://engine-b');
		r.setRTT('A', 180);
		r.setRTT('B', 30);
		r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5, engineBUrl: 'https://engine-b' }));
		r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5, engineBUrl: 'https://engine-a' }));
		r.tick();
		vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
		r.tick();
		expect(r.health.authoritative).toBe('B');
		expect(r.state.programSource).toBe('cam2');
		expect(r.state.engineBUrl).toBe('https://engine-b');
		vi.useRealTimers();
	});

	// With engine A down at load, B is found by the control plane or DNS
	// and A never reports an engineBUrl.
	it('reports the URL engine B is connected at when engine A never sent one', () => {
		const r = createReconciler();
		r.setEngineBUrl('https://engine-b');
		r.ingest('B', makeState({ lastCommandSeq: 5, engineBUrl: 'https://engine-a' }));
		expect(r.health.authoritative).toBe('B');
		expect(r.state.engineBUrl).toBe('https://engine-b');
	});

	// Engine B found through the control plane or re-provisioned: A still
	// names another peer. A differing URL per authority would rebuild the
	// warm-standby pipeline on every authority change.
	it('reports the URL engine B is connected at while engine A is authoritative', () => {
		const r = createReconciler();
		r.setEngineBUrl('https://engine-b');
		r.ingest('A', makeState({ lastCommandSeq: 5, engineBUrl: 'https://engine-b-old' }));
		r.ingest('B', makeState({ lastCommandSeq: 5, engineBUrl: 'https://engine-a' }));
		expect(r.health.authoritative).toBe('A');
		expect(r.state.engineBUrl).toBe('https://engine-b');
	});

	// A command reaches the two engines at different moments; without
	// command seqs both report the same seq meanwhile. Reporting that as a
	// mismatch starts a full state resync on every cut.
	it('does not report a mismatch while a command reaches the second engine', () => {
		vi.useFakeTimers();
		const r = createReconciler();
		r.ingest('A', makeState({ previewSource: 'cam1', lastCommandSeq: 0 }));
		r.ingest('B', makeState({ previewSource: 'cam1', lastCommandSeq: 0 }));
		vi.advanceTimersByTime(SETTLE_MS);
		r.tick();
		expect(r.health.status).toBe('synced');

		r.ingest('A', makeState({ previewSource: 'cam2', lastCommandSeq: 0 }));
		expect(r.health.status).toBe('synced');
		vi.advanceTimersByTime(60);
		r.tick();
		expect(r.health.status).toBe('synced');
		r.ingest('B', makeState({ previewSource: 'cam2', lastCommandSeq: 0 }));
		vi.advanceTimersByTime(SETTLE_MS);
		r.tick();
		expect(r.health.status).toBe('synced');
	});

	// Dragging a fader sends level after level; each engine trails the other
	// by the command in flight for as long as the drag lasts.
	it('does not report a divergence while a fader is being dragged', () => {
		vi.useFakeTimers();
		const r = createReconciler();
		const at = (level: number) => makeState({
			audioChannels: { cam1: { level, trim: 0, muted: false, afv: false } as never },
		});
		r.ingest('A', at(0));
		r.ingest('B', at(0));
		for (let level = 1; level <= 30; level++) {
			r.ingest('A', at(level));
			vi.advanceTimersByTime(50);
			r.tick();
			expect(r.health.status).toBe('synced');
			r.ingest('B', at(level));
			vi.advanceTimersByTime(50);
		}
		vi.advanceTimersByTime(SETTLE_MS);
		r.tick();
		expect(r.health.status).toBe('synced');
	});

	// The limiter's meters change on every state update while program audio
	// plays. Counted as a change, they would hold every difference back
	// for as long as the audio runs.
	it('reports a mismatch while the limiter meters keep moving', () => {
		vi.useFakeTimers();
		const r = createReconciler();
		const at = (previewSource: string, truePeak: number) => makeState({
			previewSource, lastCommandSeq: 0,
			limiter: { threshold: -1, ceiling: -0.5, gainReduction: truePeak / 10, truePeak },
		});
		for (let i = 0; i < 20; i++) {
			r.ingest('A', at('cam2', -20 + (i % 7)));
			r.ingest('B', at('cam1', -21 + (i % 5)));
			vi.advanceTimersByTime(100);
			r.tick();
		}
		expect(r.health.status).toBe('mismatch');
	});

	// The meters themselves differ between engines that are in step.
	it('does not report the limiter meters as a divergence', () => {
		vi.useFakeTimers();
		const r = createReconciler();
		r.ingest('A', makeState({ limiter: { threshold: -1, ceiling: -0.5, gainReduction: 2, truePeak: -3 } }));
		r.ingest('B', makeState({ limiter: { threshold: -1, ceiling: -0.5, gainReduction: 0, truePeak: -144 } }));
		vi.advanceTimersByTime(SETTLE_MS);
		r.tick();
		expect(r.health.divergences).toEqual([]);
		expect(r.health.status).toBe('synced');
	});

	// A field that changes on every update and is compared would still hold
	// a difference back; the cap bounds that.
	it('reports a lasting difference within SETTLE_MAX_MS while the engines keep changing', () => {
		vi.useFakeTimers();
		const r = createReconciler();
		for (let i = 0; i <= SETTLE_MAX_MS / 100; i++) {
			r.ingest('A', makeState({ previewSource: 'cam2', transitionPosition: i / 100 }));
			r.ingest('B', makeState({ previewSource: 'cam1', transitionPosition: i / 100 }));
			vi.advanceTimersByTime(100);
			r.tick();
		}
		expect(r.health.status).toBe('mismatch');
	});

	// A drag longer than SETTLE_MAX_MS keeps one engine a level behind the
	// other the whole time; it must not count as a lasting difference.
	it('does not report a mismatch through a fader drag longer than SETTLE_MAX_MS', () => {
		vi.useFakeTimers();
		const r = createReconciler();
		const at = (level: number, muted = false) => makeState({
			audioChannels: { cam1: { level, trim: 0, muted, afv: false } as never },
		});
		r.ingest('A', at(0));
		r.ingest('B', at(0));
		// Engine A is always one level ahead: the two never report the same
		// level at any one moment.
		for (let level = 2; level <= (SETTLE_MAX_MS / 100) * 2; level++) {
			r.ingest('A', at(level));
			vi.advanceTimersByTime(50);
			r.tick();
			expect(r.health.status).toBe('synced');
			r.ingest('B', at(level - 1));
			vi.advanceTimersByTime(50);
			r.tick();
			expect(r.health.status).toBe('synced');
		}
	});

	// Each command of a drag carries its own seq. An engine further away
	// applies every one of them a little later, so the two seqs differ at
	// every moment of the drag although neither engine misses a command.
	it('does not report lagging while the engine behind keeps applying commands', () => {
		vi.useFakeTimers();
		const r = createReconciler();
		const at = (level: number) => makeState({
			lastCommandSeq: level,
			audioChannels: { cam1: { level, trim: 0, muted: false, afv: false } as never },
		});
		r.ingest('A', at(0));
		r.ingest('B', at(0));
		const seen = new Set<string>();
		for (let level = 1; level <= 40; level++) {
			r.ingest('A', at(level));
			vi.advanceTimersByTime(60);
			r.tick();
			seen.add(r.health.status);
			r.ingest('B', at(level - 1));
			vi.advanceTimersByTime(60);
			r.tick();
			seen.add(r.health.status);
		}
		expect([...seen]).toEqual(['synced']);
	});

	it('reports lagging once the engine behind stops applying commands', () => {
		vi.useFakeTimers();
		const r = createReconciler();
		r.ingest('A', makeState({ lastCommandSeq: 1 }));
		r.ingest('B', makeState({ lastCommandSeq: 1 }));
		for (let seq = 2; seq <= 4; seq++) {
			r.ingest('A', makeState({ lastCommandSeq: seq }));
			r.ingest('B', makeState({ lastCommandSeq: seq - 1 }));
			vi.advanceTimersByTime(100);
		}
		// Engine A keeps going; engine B stays at 3.
		for (let seq = 5; seq <= 12; seq++) {
			r.ingest('A', makeState({ lastCommandSeq: seq }));
			vi.advanceTimersByTime(100);
			r.tick();
		}
		expect(r.health.status).not.toBe('synced');
	});

	// The page hides the audio drift warning while the engines settle: a
	// fader dragged on both shows a level difference at nearly every moment.
	it('reports settling while a drag changes the engines and not once they hold still', () => {
		vi.useFakeTimers();
		const r = createReconciler();
		const at = (level: number) => makeState({
			lastCommandSeq: level,
			audioChannels: { cam1: { level, trim: 0, muted: false, afv: false } as never },
		});
		r.ingest('A', at(0));
		r.ingest('B', at(0));
		for (let level = 1; level <= 40; level++) {
			r.ingest('A', at(level));
			vi.advanceTimersByTime(60);
			r.tick();
			expect(r.settling).toBe(true);
			r.ingest('B', at(level - 1));
			vi.advanceTimersByTime(60);
			r.tick();
			expect(r.settling).toBe(true);
		}
		// Engine B never gets the last level: a real drift, reported.
		vi.advanceTimersByTime(SETTLE_MS);
		r.tick();
		expect(r.settling).toBe(false);
		expect(r.health.status).not.toBe('synced');
	});

	// SCTE-35 start and log times come from each engine's clock.
	it('does not report SCTE-35 event timestamps as a divergence', () => {
		vi.useFakeTimers();
		const ev = (startedAt: number) => ({ eventId: 7, commandType: 'splice_insert', isOut: true, elapsedMs: 0, autoReturn: false, held: false, spliceTimePts: 0, startedAt });
		const st = (t: number) => ({ enabled: true, activeEvents: { '7': ev(t) }, eventLog: [{ eventId: 7, commandType: 'splice_insert', isOut: true, autoReturn: false, timestamp: t, status: 'injected' }], heartbeatOk: true, config: {} as never });
		const r = createReconciler();
		r.ingest('A', makeState({ scte35: st(1000) as never }));
		r.ingest('B', makeState({ scte35: st(1003) as never }));
		vi.advanceTimersByTime(SETTLE_MS);
		r.tick();
		expect(r.health.divergences).toEqual([]);
	});

	it('reports a difference that outlasts the settle time', () => {
		vi.useFakeTimers();
		const r = createReconciler();
		r.ingest('A', makeState({ previewSource: 'cam2', lastCommandSeq: 0 }));
		r.ingest('B', makeState({ previewSource: 'cam1', lastCommandSeq: 0 }));
		expect(r.health.status).toBe('synced');
		vi.advanceTimersByTime(SETTLE_MS);
		r.tick();
		expect(r.health.status).toBe('mismatch');
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
			vi.useFakeTimers();
			const r = createReconciler();
			r.setRTT('A', 150);
			r.setRTT('B', 20);
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');

			// After hold-time, B wins by lower RTT
			r.tick(); // introduce B as challenger
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');
			expect(r.state.programSource).toBe('cam2');
			vi.useRealTimers();
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
			// Delta = 60ms < 100ms hysteresis → A wins by default
			r.setRTT('A', 80);
			r.setRTT('B', 140);
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('A');

			// Flip RTTs: now A=140, B=80 (delta still only 60ms) → still A
			r.setRTT('A', 140);
			r.setRTT('B', 80);
			expect(r.health.authoritative).toBe('A');
		});

		it('flips authority when RTT difference exceeds hysteresis', () => {
			vi.useFakeTimers();
			const r = createReconciler();
			// Delta = 150ms > 100ms hysteresis → B wins (lower RTT)
			r.setRTT('A', 180);
			r.setRTT('B', 30);
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');

			// After hold-time, B wins by lower RTT
			r.tick(); // introduce B as challenger
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');
			vi.useRealTimers();
		});

		it('returns to A when RTTs converge within hysteresis (after hold-time)', () => {
			vi.useFakeTimers();
			const r = createReconciler();
			// Start: A=180, B=30 → delta=150 > 100ms → B would win by RTT
			r.setRTT('A', 180);
			r.setRTT('B', 30);
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');

			// Let B become authoritative via hold-time
			r.tick(); // introduce B as challenger
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');

			// RTTs converge: A=80, B=40 → delta=40 < 100ms → A wins (default)
			// But hold-time prevents immediate switch
			r.setRTT('A', 80);
			r.setRTT('B', 40);
			r.tick(); // introduce A as challenger
			expect(r.health.authoritative).toBe('B'); // still held to B

			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('A'); // hold-time elapsed, switches to A
			vi.useRealTimers();
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

		it('reports diverged once both engines settle on field mismatch at different seqs', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 3 }));
			settle(r);
			// Field mismatch + different seq = diverged once both engines settle
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
			settle(r);
			r.tick();
			expect(r.health.status).toBe('mismatch');
		});

		it('reports mismatch when playout title differs at same itemIndex', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				playout: { channels: [{ id: 0, state: 'on_air', currentTitle: 'Interview', itemIndex: 0, itemCount: 1 }] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				playout: { channels: [{ id: 0, state: 'on_air', currentTitle: 'Bumper', itemIndex: 0, itemCount: 1 }] },
			}));
			settle(r);
			r.tick();
			expect(r.health.status).toBe('mismatch');
		});

		it('tolerates <=1 item index drift in playout (natural scheduler drift)', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 2, itemCount: 15, currentTitle: 'Interview' }] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 3, itemCount: 15, currentTitle: 'Bumper' }] },
			}));
			expect(r.health.status).toBe('synced');
		});

		it('reports mismatch when playout index drift exceeds 1', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 2, itemCount: 15 }] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 5, itemCount: 15 }] },
			}));
			settle(r);
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

		it('reports mismatch when playout elapsed time diverges > 5s on same item', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 2, itemCount: 15, currentTitle: 'Interview', itemRemainMs: 25000 }] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 2, itemCount: 15, currentTitle: 'Interview', itemRemainMs: 19000 }] },
			}));
			settle(r);
			r.tick();
			expect(r.health.status).toBe('mismatch');
		});

		it('tolerates playout elapsed time divergence <= 5s', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 2, itemCount: 15, currentTitle: 'Interview', itemRemainMs: 25000 }] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'on_air', itemIndex: 2, itemCount: 15, currentTitle: 'Interview', itemRemainMs: 22000 }] },
			}));
			r.tick();
			expect(r.health.status).toBe('synced');
		});

		it('skips playout elapsed check when channels not on-air', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'stopped', itemIndex: 2, itemCount: 15, itemRemainMs: 25000 }] },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 10,
				playout: { channels: [{ id: 0, state: 'stopped', itemIndex: 2, itemCount: 15, itemRemainMs: 8000 }] },
			}));
			r.tick();
			expect(r.health.status).toBe('synced');
		});

		it('does not report mismatch when fields agree at same seq', () => {
			const r = createReconciler();
			const s = makeState({ lastCommandSeq: 5 });
			r.ingest('A', s);
			r.ingest('B', { ...s });
			r.tick();
			expect(r.health.status).toBe('synced');
		});

		it('reports mismatch when recording.active differs at same seq', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				recording: { active: true, filename: 'rec1.ts' },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				recording: { active: false },
			}));
			settle(r);
			r.tick();
			expect(r.health.status).toBe('mismatch');
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
			vi.useFakeTimers();
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 3 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 10 }));
			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');

			// Let B become authoritative via hold-time
			r.tick(); // introduce B as challenger
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');

			r.disconnect('B');
			expect(r.health.authoritative).toBe('A');
			expect(r.state.programSource).toBe('cam1');
			vi.useRealTimers();
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

		it('last ingest wins for authoritative when seq advances (after hold-time)', () => {
			vi.useFakeTimers();
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 1 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 2 }));
			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');

			// Let B become authoritative via hold-time (B has higher seq)
			r.tick(); // introduce B as challenger
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');
			expect(r.state.programSource).toBe('cam2');

			// A now has higher seq — but hold-time prevents immediate switch
			r.ingest('A', makeState({ programSource: 'cam3', lastCommandSeq: 3 }));
			expect(r.health.authoritative).toBe('B'); // still held to B

			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('A'); // hold-time elapsed
			expect(r.state.programSource).toBe('cam3');
			vi.useRealTimers();
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
			vi.useFakeTimers();
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
			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');

			// After hold-time, B wins by health
			r.tick(); // introduce B as challenger
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');
			vi.useRealTimers();
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
			vi.useFakeTimers();
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
			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');

			// After hold-time, B wins by higher seq
			r.tick(); // introduce B as challenger
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');
			vi.useRealTimers();
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
			settle(r);
			r.tick();
			expect(r.health.status).toBe('mismatch');
		});

		it('reports mismatch when previewSource differs at same seq', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ previewSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ previewSource: 'cam3', lastCommandSeq: 5 }));
			settle(r);
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

	describe('destination divergence', () => {
		it('detects divergence when active destination count differs', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				destinations: [
					{ id: 'd1', type: 'srt-caller', state: 'running' },
					{ id: 'd2', type: 'srt-listener', state: 'running' },
				],
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				destinations: [
					{ id: 'd1', type: 'srt-caller', state: 'running' },
					{ id: 'd2', type: 'srt-listener', state: 'stopped' },
				],
			}));
			const divs = r.health.divergences;
			const destDiv = divs.find(d => d.field === 'destinations');
			expect(destDiv).toBeDefined();
			expect(destDiv?.engineA).toBe('2 active');
			expect(destDiv?.engineB).toBe('1 active');
		});

		it('no destination divergence when active counts match', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				destinations: [
					{ id: 'd1', type: 'srt-caller', state: 'running' },
				],
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				destinations: [
					{ id: 'd1', type: 'srt-caller', state: 'connected' },
				],
			}));
			const divs = r.health.divergences;
			const destDiv = divs.find(d => d.field === 'destinations');
			expect(destDiv).toBeUndefined();
		});

		it('no destination divergence when both undefined', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			const divs = r.health.divergences;
			const destDiv = divs.find(d => d.field === 'destinations');
			expect(destDiv).toBeUndefined();
		});

		it('detects divergence when one engine has destinations and other has none', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				destinations: [
					{ id: 'd1', type: 'srt-caller', state: 'running' },
				],
			}));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			const divs = r.health.divergences;
			const destDiv = divs.find(d => d.field === 'destinations');
			expect(destDiv).toBeDefined();
			expect(destDiv?.engineA).toBe('1 active');
			expect(destDiv?.engineB).toBe('0 active');
		});
	});

	describe('srtOutput divergence', () => {
		it('detects divergence when srtOutput.active differs', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				srtOutput: { active: true, mode: 'listener', port: 9000 },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				srtOutput: { active: false },
			}));
			const divs = r.health.divergences;
			const srtDiv = divs.find(d => d.field === 'srtOutput');
			expect(srtDiv).toBeDefined();
			expect((srtDiv?.engineA as any)?.active).toBe(true);
			expect((srtDiv?.engineB as any)?.active).toBe(false);
		});

		it('no srtOutput divergence when both active', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				srtOutput: { active: true, mode: 'listener', port: 9000 },
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				srtOutput: { active: true, mode: 'caller', address: '1.2.3.4', port: 9001 },
			}));
			const divs = r.health.divergences;
			const srtDiv = divs.find(d => d.field === 'srtOutput');
			expect(srtDiv).toBeUndefined();
		});

		it('no srtOutput divergence when both undefined', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			const divs = r.health.divergences;
			const srtDiv = divs.find(d => d.field === 'srtOutput');
			expect(srtDiv).toBeUndefined();
		});

		it('detects divergence when one engine has srtOutput and other does not', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				srtOutput: { active: true, mode: 'listener', port: 9000 },
			}));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			const divs = r.health.divergences;
			const srtDiv = divs.find(d => d.field === 'srtOutput');
			expect(srtDiv).toBeDefined();
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

		it('detects SCTE-35 state divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, scte35: { active: true } as any }));
			r.ingest('B', makeState({ lastCommandSeq: 5, scte35: { active: false } as any }));
			r.tick();
			expect(r.health.divergences.some(d => d.field === 'scte35')).toBe(true);
		});

		it('detects macro divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, macro: { running: true, macroId: 'm1' } as any }));
			r.ingest('B', makeState({ lastCommandSeq: 5, macro: undefined as any }));
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('macro');
		});

		it('detects colorGrade divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, colorGrade: { preset: 'warm' } as any }));
			r.ingest('B', makeState({ lastCommandSeq: 5, colorGrade: { preset: 'cool' } as any }));
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('colorGrade');
		});

		it('detects stmap divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, stmap: { active: true } as any }));
			r.ingest('B', makeState({ lastCommandSeq: 5, stmap: { active: false } as any }));
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('stmap');
		});

		it('detects DVE layout divergence (full object)', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				layout: { activePreset: 'pip', slots: [{ id: 0, sourceKey: 'cam1' }] } as any,
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				layout: { activePreset: 'pip', slots: [{ id: 0, sourceKey: 'cam2' }] } as any,
			}));
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('layout');
		});

		it('detects limiter divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, limiter: { threshold: -1, enabled: true } as any }));
			r.ingest('B', makeState({ lastCommandSeq: 5, limiter: { threshold: -3, enabled: true } as any }));
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('limiter');
		});

		it('detects pipelineFormat divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, pipelineFormat: { width: 1920, height: 1080 } as any }));
			r.ingest('B', makeState({ lastCommandSeq: 5, pipelineFormat: { width: 1280, height: 720 } as any }));
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('pipelineFormat');
		});

		it('detects cbr divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, cbr: { enabled: true, bitrate: 5000000 } as any }));
			r.ingest('B', makeState({ lastCommandSeq: 5, cbr: { enabled: false, bitrate: 5000000 } as any }));
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('cbr');
		});

		it('detects aiSegmentation divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, aiSegmentation: { cam1: { enabled: true } } as any }));
			r.ingest('B', makeState({ lastCommandSeq: 5, aiSegmentation: { cam1: { enabled: false } } as any }));
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('aiSegmentation');
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

		it('divergence detection includes audioChannels settings (excluding metering)', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				audioChannels: { cam1: { level: -6, muted: false, afv: false } } as any,
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				audioChannels: { cam1: { level: -12, muted: true, afv: true } } as any,
			}));
			settle(r);
			// audioChannels settings differ, so divergence should be detected
			const fields = r.health.divergences.map(d => d.field);
			expect(fields).toContain('audioChannels');
			// muted/afv differ at same seq → critical mismatch (stronger than diverged)
			expect(r.health.status).toBe('mismatch');
		});

		it('transitionPosition difference IS detected as divergence', () => {
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

	// ── Command dispatch error warnings ──

	describe('command dispatch error warnings', () => {
		it('surfaces lastExecutedErr from engine A as a warning', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, lastExecutedErr: 'source not found' }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			expect(r.health.warnings).toContain('engine_a_cmd_error: source not found');
		});

		it('surfaces lastExecutedErr from engine B as a warning', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5, lastExecutedErr: 'transition in progress' }));
			expect(r.health.warnings).toContain('engine_b_cmd_error: transition in progress');
		});

		it('surfaces errors from both engines and flags divergence', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, lastExecutedErr: 'error A' }));
			r.ingest('B', makeState({ lastCommandSeq: 5, lastExecutedErr: 'error B' }));
			expect(r.health.warnings).toContain('engine_a_cmd_error: error A');
			expect(r.health.warnings).toContain('engine_b_cmd_error: error B');
			expect(r.health.warnings).toContain('cmd_error_divergence');
		});

		it('does not flag cmd_error_divergence when both engines have the same error', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, lastExecutedErr: 'same error' }));
			r.ingest('B', makeState({ lastCommandSeq: 5, lastExecutedErr: 'same error' }));
			expect(r.health.warnings).toContain('engine_a_cmd_error: same error');
			expect(r.health.warnings).toContain('engine_b_cmd_error: same error');
			expect(r.health.warnings).not.toContain('cmd_error_divergence');
		});

		it('clears error warnings when state updates without errors', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, lastExecutedErr: 'temp error' }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			expect(r.health.warnings).toContain('engine_a_cmd_error: temp error');
			// Error clears on next successful command
			r.ingest('A', makeState({ lastCommandSeq: 6 }));
			r.ingest('B', makeState({ lastCommandSeq: 6 }));
			const cmdWarnings = r.health.warnings.filter(w => w.includes('cmd_error'));
			expect(cmdWarnings).toHaveLength(0);
		});

		it('does not surface errors from disconnected engines', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, lastExecutedErr: 'error A' }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			expect(r.health.warnings).toContain('engine_a_cmd_error: error A');
			r.disconnect('A');
			const cmdWarnings = r.health.warnings.filter(w => w.includes('cmd_error'));
			expect(cmdWarnings).toHaveLength(0);
		});
	});

	// ── Late command warnings ──

	describe('late command warnings', () => {
		it('surfaces late commands from engine A as a warning', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, lateCommands: 3 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			expect(r.health.warnings).toContain('3 commands arrived late on Engine A');
		});

		it('surfaces late commands from engine B as a warning', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5, lateCommands: 7 }));
			expect(r.health.warnings).toContain('7 commands arrived late on Engine B');
		});

		it('surfaces late commands from both engines', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, lateCommands: 2 }));
			r.ingest('B', makeState({ lastCommandSeq: 5, lateCommands: 4 }));
			expect(r.health.warnings).toContain('2 commands arrived late on Engine A');
			expect(r.health.warnings).toContain('4 commands arrived late on Engine B');
		});

		it('does not warn when lateCommands is zero', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, lateCommands: 0 }));
			r.ingest('B', makeState({ lastCommandSeq: 5, lateCommands: 0 }));
			const lateWarnings = r.health.warnings.filter(w => w.includes('late'));
			expect(lateWarnings).toHaveLength(0);
		});

		it('does not surface late commands from disconnected engines', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, lateCommands: 3 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			expect(r.health.warnings).toContain('3 commands arrived late on Engine A');
			r.disconnect('A');
			const lateWarnings = r.health.warnings.filter(w => w.includes('late'));
			expect(lateWarnings).toHaveLength(0);
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

		it('detects divergence when layer counts differ', () => {
			const r = createReconciler();
			// A has 3 layers, B has 1 layer — layer count mismatch is a divergence
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				graphics: {
					layers: [
						{ id: 1, active: true, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080 },
						{ id: 2, active: true, zOrder: 1, x: 0, y: 0, width: 1920, height: 1080 },
						{ id: 3, active: false, zOrder: 2, x: 0, y: 0, width: 1920, height: 1080 },
					],
				},
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				graphics: {
					layers: [{ id: 1, active: true, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080 }],
				},
			}));
			const divs = r.health.divergences;
			const graphicsDiv = divs.find((d) => d.field === 'graphics.layers');
			expect(graphicsDiv).toBeDefined();
			expect(graphicsDiv?.engineA).toBe('3 layers');
			expect(graphicsDiv?.engineB).toBe('1 layers');
		});

		it('detects divergence when A has layers but B has none', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				graphics: {
					layers: [
						{ id: 1, active: true, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080 },
					],
				},
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				graphics: { layers: [] },
			}));
			const divs = r.health.divergences;
			const graphicsDiv = divs.find((d) => d.field === 'graphics.layers');
			expect(graphicsDiv).toBeDefined();
			expect(graphicsDiv?.engineA).toBe('1 layers');
			expect(graphicsDiv?.engineB).toBe('0 layers');
		});

		it('still detects per-layer divergence when counts match', () => {
			const r = createReconciler();
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
					layers: [
						{ id: 1, active: true, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080 },
						{ id: 2, active: false, zOrder: 1, x: 0, y: 0, width: 1920, height: 1080 },
					],
				},
			}));
			const divs = r.health.divergences;
			const graphicsDiv = divs.find((d) => d.field === 'graphics.layers');
			expect(graphicsDiv).toBeDefined();
			expect((graphicsDiv?.engineA as any)?.active).toBe(true);
			expect((graphicsDiv?.engineB as any)?.active).toBe(false);
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

	describe('pong-based freshness', () => {
		beforeEach(() => {
			vi.useFakeTimers();
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		it('does not mark engine stale when pongs are fresh and state updates are within STATE_UPDATE_STALE_MS', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.setTransportAlive('A', true);
			r.setTransportAlive('B', true);

			// Simulate pongs arriving every 5s with state updates within threshold
			vi.advanceTimersByTime(5_000);
			r.recordPong('A');
			r.recordPong('B');

			vi.advanceTimersByTime(5_000); // 10s total, within STATE_UPDATE_STALE_MS (12s)
			r.recordPong('A');
			r.recordPong('B');

			r.tick();
			expect(r.health.status).toBe('synced');
			expect(r.health.staleEngines).toHaveLength(0);
		});

		it('marks engine stale when pongs stop arriving', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.setTransportAlive('A', true);
			r.setTransportAlive('B', true);

			// One pong received
			r.recordPong('A');
			r.recordPong('B');

			// Advance past pong freshness window (30s) without new pongs
			vi.advanceTimersByTime(PONG_FRESH_MS + 1000);
			r.tick();

			// Pongs expired, falls back to STALE_TRANSPORT_ALIVE_MS which is also exceeded
			expect(r.health.status).not.toBe('synced');
		});

		it('keeps engine alive with periodic pongs and state updates over long idle period', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.setTransportAlive('A', true);
			r.setTransportAlive('B', true);

			// Simulate 2 minutes of operation with pongs every 10s and state updates every 15s
			for (let i = 0; i < 12; i++) {
				vi.advanceTimersByTime(10_000);
				r.recordPong('A');
				r.recordPong('B');
				// Re-ingest state updates periodically to keep them fresh (< 20s)
				if (i % 2 === 0) {
					r.ingest('A', makeState({ lastCommandSeq: 5 }));
					r.ingest('B', makeState({ lastCommandSeq: 5 }));
				}
				r.tick();
				expect(r.health.status).toBe('synced');
				expect(r.health.staleEngines).toHaveLength(0);
			}
		});

		it('marks only the engine without pongs as stale', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			r.setTransportAlive('A', true);
			r.setTransportAlive('B', true);

			// Only A receives pongs AND state updates, B stops both
			vi.advanceTimersByTime(10_000);
			r.recordPong('A');
			r.ingest('A', makeState({ lastCommandSeq: 5 })); // keep A's state fresh

			vi.advanceTimersByTime(10_000);
			r.recordPong('A');
			r.ingest('A', makeState({ lastCommandSeq: 5 })); // keep A's state fresh

			// 20s since B's last state update. B has no pongs, A has fresh pongs + state.
			// B should fall back to STALE_TRANSPORT_ALIVE_MS (15s) and be marked stale.
			r.tick();
			expect(r.health.status).toBe('single-engine');
			expect(r.health.staleEngines).not.toContain('A');
		});

		it('PONG_FRESH_MS is 30000', () => {
			expect(PONG_FRESH_MS).toBe(30_000);
		});

		it('marks engine stale when state updates stop despite fresh pongs', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 1 }));
			r.ingest('B', makeState({ lastCommandSeq: 1 }));
			r.setTransportAlive('A', true);
			r.setTransportAlive('B', true);
			r.recordPong('A');
			r.recordPong('B');

			// Advance time: B's state update is old but pongs are fresh
			vi.advanceTimersByTime(STATE_UPDATE_STALE_MS + 1);
			r.recordPong('A');
			r.recordPong('B');
			// Only A sends a state update
			r.ingest('A', makeState({ lastCommandSeq: 2 }));
			r.tick();

			// B should be stale because no state updates for 20s
			expect(r.health.staleEngines).toContain('B');
			expect(r.health.staleEngines).not.toContain('A');
			expect(r.health.status).toBe('single-engine');
		});

		it('does not mark engine stale when both pongs and state updates are fresh', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 1 }));
			r.ingest('B', makeState({ lastCommandSeq: 1 }));
			r.setTransportAlive('A', true);
			r.setTransportAlive('B', true);
			r.recordPong('A');
			r.recordPong('B');

			// Advance just under the state update stale threshold
			vi.advanceTimersByTime(STATE_UPDATE_STALE_MS - 1000);
			r.recordPong('A');
			r.recordPong('B');
			r.tick();

			// Both should still be alive
			expect(r.health.staleEngines).toHaveLength(0);
			expect(r.health.status).toBe('synced');
		});

		it('STATE_UPDATE_STALE_MS is 12000', () => {
			expect(STATE_UPDATE_STALE_MS).toBe(12_000);
		});

		it('pong freshness requires transport to be alive', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));

			// Transport is NOT alive, but pongs somehow recorded
			r.setTransportAlive('A', false);
			r.setTransportAlive('B', false);
			r.recordPong('A');
			r.recordPong('B');

			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 1000);
			r.tick();

			// Should still be disconnected because transport is not alive
			expect(r.health.status).toBe('disconnected');
		});
	});

	// ── Transition lock ──

	describe('transition authority lock', () => {
		it('authority stays locked during transition even if B has better RTT', () => {
			const r = createReconciler();
			// A is authoritative (default winner at equal seq, no RTT set)
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: false }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: false }));
			expect(r.health.authoritative).toBe('A');

			// Transition starts on A (current authoritative)
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.7 }));

			// Now B gets much better RTT (would normally win)
			r.setRTT('A', 200);
			r.setRTT('B', 50);
			r.tick();

			// Authority should stay locked to A since A was auth before transition
			expect(r.health.authoritative).toBe('A');
		});

		it('lock is released when transition ends (authority switches after hold-time)', () => {
			vi.useFakeTimers();
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: false }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: false }));
			expect(r.health.authoritative).toBe('A');

			// Transition starts
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.5 }));
			r.setRTT('A', 200);
			r.setRTT('B', 50);

			// Locked to A
			expect(r.health.authoritative).toBe('A');

			// Transition ends — B is the candidate but hold-time applies
			r.ingest('A', makeState({ lastCommandSeq: 6, inTransition: false }));
			r.ingest('B', makeState({ lastCommandSeq: 6, inTransition: false }));
			r.tick();
			expect(r.health.authoritative).toBe('A'); // still held to A

			// After hold-time, B takes over
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');
			vi.useRealTimers();
		});

		it('lock falls back if locked engine becomes unreachable', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: false }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: false }));
			expect(r.health.authoritative).toBe('A');

			// Transition starts, locked to A
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));

			// A disconnects
			r.disconnect('A');
			r.tick();

			// Lock should not prevent fallback to B
			expect(r.health.authoritative).toBe('B');
		});

		it('does not lock when transition is already in progress at first ingest', () => {
			const r = createReconciler();
			// Both engines report already in-transition on first ingest
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.5 }));

			// B has clearly better RTT — should still go to B since lock was set
			// to A (the authoritative at the time the transition was detected)
			r.setRTT('A', 300);
			r.setRTT('B', 50);
			r.tick();

			// Lock was set to A (authoritative when first transition was detected),
			// so A stays authoritative despite B having better RTT
			expect(r.health.authoritative).toBe('A');
		});

		it('clears orphaned transition lock when locked engine goes stale', () => {
			vi.useFakeTimers();
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: false }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: false }));
			expect(r.health.authoritative).toBe('A');

			// Transition starts, locked to A
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));

			// Set B as clearly better RTT — currently locked to A
			r.setRTT('A', 300);
			r.setRTT('B', 30);
			expect(r.health.authoritative).toBe('A');

			// A goes stale (no updates for >5s), B keeps sending
			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 100);
			r.ingest('B', makeState({ lastCommandSeq: 6, inTransition: false }));
			r.tick();

			// Lock should be cleared because A is stale, B is now authoritative
			expect(r.health.authoritative).toBe('B');
			vi.useRealTimers();
		});

		it('clears orphaned transition lock when locked engine disconnects', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: false }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: false }));
			expect(r.health.authoritative).toBe('A');

			// Transition starts, locked to A
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));
			r.ingest('B', makeState({ lastCommandSeq: 5, inTransition: true, transitionPosition: 0.3 }));

			// Set B as clearly better RTT — currently locked to A
			r.setRTT('A', 300);
			r.setRTT('B', 30);
			expect(r.health.authoritative).toBe('A');

			// A disconnects
			r.disconnect('A');

			// B sends update ending transition
			r.ingest('B', makeState({ lastCommandSeq: 6, inTransition: false }));
			r.tick();

			// Lock should be cleared because A disconnected, B is authoritative
			expect(r.health.authoritative).toBe('B');
		});

		it('transition lock uses fresh timestamp, not stale lastTickTime', () => {
			vi.useFakeTimers();
			const r = createReconciler();

			// Both engines connected, B is clearly more qualified (higher seq)
			r.ingest('A', makeState({ lastCommandSeq: 5, inTransition: false }));
			r.ingest('B', makeState({ lastCommandSeq: 10, inTransition: false }));
			// First dual-engine evaluation always picks A for stability
			expect(r.health.authoritative).toBe('A');

			// Let B become authoritative via hold-time
			r.tick(); // introduce B as challenger
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');

			// Advance time well past stale threshold so lastTickTime is old.
			// DO NOT call tick() — this leaves lastTickTime stale.
			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 1000);

			// Engine A sends a fresh update at the new time (Date.now() is current).
			// But lastTickTime is 6+ seconds ago, so the $derived `authoritative`
			// would consider A stale (lastSeenA was set before the time advance).
			// Then B sends transition start.
			r.ingest('A', makeState({ lastCommandSeq: 10, inTransition: false }));
			// Now A's lastSeenA is fresh (just set by ingest).

			// B starts transition — lock should use Date.now() to evaluate authority,
			// seeing both engines as fresh (A was just ingested, B is being ingested now).
			r.ingest('B', makeState({ lastCommandSeq: 10, inTransition: true, transitionPosition: 0.1 }));

			// With both engines fresh at equal seq (no RTT set), A wins ties.
			// If the lock used the stale lastTickTime ($derived `authoritative`),
			// it might have computed authority wrong (e.g., thinking A was stale).
			// The inline Date.now() computation should correctly determine both are fresh.
			//
			// Now trigger tick to update lastTickTime and check final authority.
			// The lock was set correctly at ingest time, so it should stick.
			r.tick();

			// At equal seq with no RTT, A wins. The transition lock should have
			// been set to A (both engines effective, A wins tie-break).
			expect(r.health.authoritative).toBe('A');

			vi.useRealTimers();
		});
	});

	// ── Authority hold-time ──

	describe('authority hold-time', () => {
		beforeEach(() => {
			vi.useFakeTimers();
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		it('AUTHORITY_HOLD_MS is 2000', () => {
			expect(AUTHORITY_HOLD_MS).toBe(2000);
		});

		it('does not flip authority within hold-time when both engines are effective', () => {
			const r = createReconciler();
			// Establish A as authority (first dual-engine eval always picks A)
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('A');

			// B gets higher seq — candidate switches to B
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 10 }));
			// Authority should NOT flip yet (hold-time)
			expect(r.health.authoritative).toBe('A');

			// Advance 1000ms — still within 2000ms hold-time
			vi.advanceTimersByTime(1000);
			r.tick();
			expect(r.health.authoritative).toBe('A');

			// Advance to 1999ms — still within hold-time
			vi.advanceTimersByTime(999);
			r.tick();
			expect(r.health.authoritative).toBe('A');
		});

		it('flips authority after hold-time elapses', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('A');

			// B gets higher seq
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 10 }));
			expect(r.health.authoritative).toBe('A'); // held

			// Advance past hold-time (2000ms)
			vi.advanceTimersByTime(2000);
			r.tick();
			expect(r.health.authoritative).toBe('B'); // switched
		});

		it('resets hold-time if candidate flaps back before hold-time elapses', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('A');

			// B gets higher seq — candidate switches to B
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 10 }));
			expect(r.health.authoritative).toBe('A');

			// Advance 1000ms
			vi.advanceTimersByTime(1000);
			r.tick();

			// A catches up — candidate flaps back to A (same seq, A wins ties)
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 10 }));
			expect(r.health.authoritative).toBe('A');

			// B gets higher seq again — hold-time resets
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 15 }));
			expect(r.health.authoritative).toBe('A');

			// Only 1000ms — not enough
			vi.advanceTimersByTime(1000);
			r.tick();
			expect(r.health.authoritative).toBe('A');

			// Full 2000ms from the RESET point
			vi.advanceTimersByTime(1000);
			r.tick();
			expect(r.health.authoritative).toBe('B');
		});

		it('switches immediately when current authority is stale (no hold-time)', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('A');

			// A goes stale (no updates for >5s)
			vi.advanceTimersByTime(STALE_THRESHOLD_MS + 100);
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 6 }));
			r.tick();

			// Should switch to B immediately — no hold-time because A is stale (not effective)
			expect(r.health.authoritative).toBe('B');
		});

		it('switches immediately when current authority disconnects (no hold-time)', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 10 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('A');

			// A disconnects — immediate fallback to B
			r.disconnect('A');
			expect(r.health.authoritative).toBe('B');
		});

		it('first dual-engine eval always picks A for stability', () => {
			const r = createReconciler();
			// B has clearly better RTT — but first eval always picks A
			r.setRTT('A', 200);
			r.setRTT('B', 30);
			r.ingest('A', makeState({ programSource: 'cam1', lastCommandSeq: 5 }));
			r.ingest('B', makeState({ programSource: 'cam2', lastCommandSeq: 5 }));
			expect(r.health.authoritative).toBe('A');

			// First tick introduces B as challenger and starts hold-time clock
			r.tick();
			// After hold-time, B wins by RTT
			vi.advanceTimersByTime(AUTHORITY_HOLD_MS);
			r.tick();
			expect(r.health.authoritative).toBe('B');
		});
	});

	// ── Enhanced clip player divergence ──

	describe('enhanced clip player divergence', () => {
		it('detects divergence when clip names differ at same slot', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				clipPlayers: [
					{ id: 0, state: 'playing', clipName: 'intro.mp4' },
					{ id: 1, state: 'empty', clipName: '' },
				],
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				clipPlayers: [
					{ id: 0, state: 'playing', clipName: 'outro.mp4' },
					{ id: 1, state: 'empty', clipName: '' },
				],
			}));
			const divs = r.health.divergences;
			const clipDiv = divs.find(d => d.field === 'clipPlayers');
			expect(clipDiv).toBeDefined();
			expect(clipDiv?.engineA).toBe('intro.mp4:playing');
			expect(clipDiv?.engineB).toBe('outro.mp4:playing');
		});

		it('detects divergence when clip states differ at same slot', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				clipPlayers: [
					{ id: 0, state: 'playing', clipName: 'clip1.ts' },
				],
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				clipPlayers: [
					{ id: 0, state: 'paused', clipName: 'clip1.ts' },
				],
			}));
			const divs = r.health.divergences;
			const clipDiv = divs.find(d => d.field === 'clipPlayers');
			expect(clipDiv).toBeDefined();
			expect(clipDiv?.engineA).toBe('clip1.ts:playing');
			expect(clipDiv?.engineB).toBe('clip1.ts:paused');
		});

		it('detects divergence when slot counts differ', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				clipPlayers: [
					{ id: 0, state: 'playing', clipName: 'clip1.ts' },
					{ id: 1, state: 'empty', clipName: '' },
				],
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				clipPlayers: [
					{ id: 0, state: 'playing', clipName: 'clip1.ts' },
				],
			}));
			const divs = r.health.divergences;
			const clipDiv = divs.find(d => d.field === 'clipPlayers');
			expect(clipDiv).toBeDefined();
			expect(clipDiv?.engineA).toBe('2 slots');
			expect(clipDiv?.engineB).toBe('1 slots');
		});

		it('no clip divergence when all slots match', () => {
			const r = createReconciler();
			const clips: ClipPlayerState[] = [
				{ id: 0, state: 'playing', clipName: 'clip1.ts' },
				{ id: 1, state: 'empty', clipName: '' },
			];
			r.ingest('A', makeState({ lastCommandSeq: 5, clipPlayers: clips }));
			r.ingest('B', makeState({ lastCommandSeq: 5, clipPlayers: [...clips] }));
			const divs = r.health.divergences;
			const clipDiv = divs.find(d => d.field === 'clipPlayers');
			expect(clipDiv).toBeUndefined();
		});

		it('no clip divergence when both undefined', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			const divs = r.health.divergences;
			const clipDiv = divs.find(d => d.field === 'clipPlayers');
			expect(clipDiv).toBeUndefined();
		});
	});

	// ── Comms divergence ──

	describe('comms divergence', () => {
		it('detects divergence when comms.active differs', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				comms: { active: true, participants: [] } as any,
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				comms: { active: false, participants: [] } as any,
			}));
			const divs = r.health.divergences;
			const commsDiv = divs.find(d => d.field === 'comms.active');
			expect(commsDiv).toBeDefined();
			expect(commsDiv?.engineA).toBe(true);
			expect(commsDiv?.engineB).toBe(false);
		});

		it('no comms divergence when both active', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				comms: { active: true, participants: [{ name: 'op1', audioLevel: 0.5 }] } as any,
			}));
			r.ingest('B', makeState({
				lastCommandSeq: 5,
				comms: { active: true, participants: [{ name: 'op1', audioLevel: 0.8 }] } as any,
			}));
			const divs = r.health.divergences;
			const commsDiv = divs.find(d => d.field === 'comms.active');
			expect(commsDiv).toBeUndefined();
		});

		it('no comms divergence when both undefined', () => {
			const r = createReconciler();
			r.ingest('A', makeState({ lastCommandSeq: 5 }));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			const divs = r.health.divergences;
			const commsDiv = divs.find(d => d.field === 'comms.active');
			expect(commsDiv).toBeUndefined();
		});

		it('detects divergence when one engine has comms and other does not', () => {
			const r = createReconciler();
			r.ingest('A', makeState({
				lastCommandSeq: 5,
				comms: { active: true, participants: [] } as any,
			}));
			r.ingest('B', makeState({ lastCommandSeq: 5 }));
			const divs = r.health.divergences;
			const commsDiv = divs.find(d => d.field === 'comms.active');
			expect(commsDiv).toBeDefined();
			expect(commsDiv?.engineA).toBe(true);
			expect(commsDiv?.engineB).toBeUndefined();
		});
	});
});

// Each engine stores an uploaded clip on its own; the clip can be loaded only
// once the slowest engine has it, so the merged state shows that engine's
// progress rather than the authoritative engine's, which may already be done.
describe('merged clip upload progress', () => {
	it('shows the engine still processing when the authoritative one is done', () => {
		const r = createReconciler();
		r.ingest('A', makeState());
		r.ingest('B', makeState({ clipUpload: { stage: 'transcoding', percent: 40, filename: 'bunny.mov' } }));

		expect(r.health.authoritative).toBe('A');
		expect(r.state.clipUpload).toEqual({ stage: 'transcoding', percent: 40, filename: 'bunny.mov' });
	});

	it('clears once every engine has finished', () => {
		const r = createReconciler();
		r.ingest('A', makeState());
		r.ingest('B', makeState({ clipUpload: { stage: 'validating', percent: 0 } }));
		r.ingest('B', makeState());

		expect(r.state.clipUpload).toBeUndefined();
	});

	it('ignores a disconnected engine', () => {
		const r = createReconciler();
		r.ingest('A', makeState());
		r.ingest('B', makeState({ clipUpload: { stage: 'analyzing', percent: 0 } }));
		r.disconnect('B');

		expect(r.state.clipUpload).toBeUndefined();
	});
});

// Clip lists refresh when clipCount moves. A clip counts once every engine
// holds it, so the merged count is the lowest: it moves when the last engine
// stores the clip, not when the authoritative one does.
describe('merged clip count', () => {
	it('is the lowest count among connected engines', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ clipCount: 3 }));
		r.ingest('B', makeState({ clipCount: 2 }));

		expect(r.health.authoritative).toBe('A');
		expect(r.state.clipCount).toBe(2);

		r.ingest('B', makeState({ clipCount: 3 }));
		expect(r.state.clipCount).toBe(3);
	});

	it('ignores a disconnected engine', () => {
		const r = createReconciler();
		r.ingest('A', makeState({ clipCount: 3 }));
		r.ingest('B', makeState({ clipCount: 1 }));
		r.disconnect('B');

		expect(r.state.clipCount).toBe(3);
	});
});

// A clip load reads the whole file before the slot changes: 14s for an
// 8-minute 1080p clip, and an engine can finish seconds after the other.
// While it does, it reports the clip as loading. Treating that as divergence
// pushed a snapshot that queued a second load of the same file behind the
// first and showed the "Resyncing" banner for the length of both.
describe('clip load on one engine slower than the other', () => {
	const slot = (p: Partial<ClipPlayerState>): ClipPlayerState => ({ id: 1, state: 'empty', ...p });
	const players = (first: ClipPlayerState): ClipPlayerState[] => [first, slot({ id: 2 }), slot({ id: 3 }), slot({ id: 4 })];

	it('neither shows a banner nor pushes a snapshot while the slower engine loads', async () => {
		vi.useFakeTimers();
		const r = createReconciler();
		const pushSnapshot = vi.fn().mockResolvedValue({ warnings: [] });
		const ar = createAutoReconcile({
			getSnapshot: vi.fn().mockResolvedValue({}),
			pushSnapshot,
		});
		const statuses = new Set<string>();
		const loadedA = makeState({ clipPlayers: players(slot({ clipId: 'x', clipName: 'bunny', state: 'loaded' })) });
		const loadingB = makeState({ clipPlayers: players(slot({ loadingClipId: 'x', loadingClipName: 'bunny' })) });

		r.ingest('A', makeState({ clipPlayers: players(slot({})) }));
		r.ingest('B', makeState({ clipPlayers: players(slot({})) }));
		// Both start loading; A finishes, B needs another six seconds.
		r.ingest('A', loadedA);
		r.ingest('B', loadingB);
		for (let t = 0; t < 6000; t += 200) {
			await vi.advanceTimersByTimeAsync(200);
			r.ingest('A', loadedA);
			r.ingest('B', loadingB);
			r.tick();
			statuses.add(r.health.status);
			ar.onSyncStatusChange(r.health.status, r.health.authoritative);
		}
		r.ingest('B', makeState({ clipPlayers: players(slot({ clipId: 'x', clipName: 'bunny', state: 'loaded' })) }));
		r.tick();
		statuses.add(r.health.status);
		await vi.advanceTimersByTimeAsync(3000);

		expect([...statuses]).toEqual(['synced']);
		expect(r.health.divergences).toEqual([]);
		expect(pushSnapshot).not.toHaveBeenCalled();
	});

	it('still reports a slot loading a different clip than the other engine holds', () => {
		expect(clipSlotCatchingUp(
			slot({ clipName: 'bunny', state: 'loaded' }),
			slot({ loadingClipName: 'other' }),
		)).toBe(false);
		expect(clipSlotCatchingUp(
			slot({ clipName: 'bunny', state: 'loaded' }),
			slot({ clipName: 'other', state: 'loaded' }),
		)).toBe(false);
		expect(clipSlotCatchingUp(
			slot({ loadingClipName: 'bunny' }),
			slot({ loadingClipName: 'bunny' }),
		)).toBe(true);
	});
});

describe('leastAdvancedUpload', () => {
	it('orders by stage, then by percent', () => {
		const transcoding80 = { stage: 'transcoding' as const, percent: 80 };
		const transcoding20 = { stage: 'transcoding' as const, percent: 20 };
		const analyzing = { stage: 'analyzing' as const, percent: 90 };

		expect(leastAdvancedUpload(transcoding80, analyzing)).toBe(analyzing);
		expect(leastAdvancedUpload(transcoding80, transcoding20)).toBe(transcoding20);
		expect(leastAdvancedUpload(undefined, transcoding80)).toBe(transcoding80);
		expect(leastAdvancedUpload(undefined, undefined)).toBeUndefined();
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

// Mutations go to both engines. A transition only one engine refuses (a
// 3D DVE on a pair where one engine has no GPU) would put the engines'
// programs apart, so the panel offers a type only if every engine accepts it.
describe('merged unavailable transitions', () => {
	const cpu = { 'dve-cube': '3D DVE transitions need a GPU engine', 'dve-corner-peel': 'Peel needs a GPU engine' };

	it('is the union over connected engines, whichever is authoritative', () => {
		const r = createReconciler();
		r.ingest('A', makeState({}));
		r.ingest('B', makeState({ unavailableTransitions: cpu }));
		expect(r.health.authoritative).toBe('A');
		expect(r.state.unavailableTransitions).toEqual(cpu);

		r.ingest('A', makeState({ unavailableTransitions: { 'dve-fold': 'x' } }));
		expect(r.state.unavailableTransitions).toEqual({ ...cpu, 'dve-fold': 'x' });
	});

	it('ignores a disconnected engine', () => {
		const r = createReconciler();
		r.ingest('A', makeState({}));
		r.ingest('B', makeState({ unavailableTransitions: cpu }));
		r.disconnect('B');
		expect(r.state.unavailableTransitions).toBeUndefined();
	});
});
