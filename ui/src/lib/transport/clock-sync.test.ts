import { describe, it, expect, vi, afterEach } from 'vitest';
import { ClockSync } from './clock-sync';

function buildPongHelper(timestampUS: number): Uint8Array {
	const buf = new Uint8Array(9);
	buf[0] = 0xFF;
	const view = new DataView(buf.buffer);
	const hi = Math.floor(timestampUS / (2 ** 32));
	const lo = timestampUS % (2 ** 32);
	view.setUint32(1, hi);
	view.setUint32(5, lo);
	return buf;
}

describe('ClockSync', () => {
	afterEach(() => {
		vi.restoreAllMocks();
	});

	describe('EWMA RTT smoothing', () => {
		it('bootstraps first RTT sample as initial value', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			expect(sync.getRTT('A')).toBe(10_000);
		});

		it('smooths RTT via EWMA (alpha=0.125)', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			// Second sample: 10_000 * 0.875 + 50_000 * 0.125 = 8750 + 6250 = 15_000
			sync.updateRTT('A', 50_000);
			expect(sync.getRTT('A')).toBe(15_000);
		});

		it('does not jump to latest sample', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			sync.updateRTT('A', 100_000);
			// EWMA should be much less than latest sample
			expect(sync.getRTT('A')).toBeLessThan(100_000);
			expect(sync.getRTT('A')).toBeGreaterThan(10_000);
		});

		it('converges toward stable value over many samples', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			// Feed many samples of 20_000 -- should converge toward 20_000
			for (let i = 0; i < 50; i++) {
				sync.updateRTT('A', 20_000);
			}
			expect(sync.getRTT('A')).toBeGreaterThan(19_900);
			expect(sync.getRTT('A')).toBeLessThanOrEqual(20_000);
		});
	});

	describe('EWMA jitter tracking', () => {
		it('bootstraps jitter from first RTT sample', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			// Initial jitter = rtt * 0.125 = 1250
			// Not directly accessible, but reflected in safetyMargin
			const margin = sync.safetyMargin();
			// margin = rtt(10000) + 2*jitter(1250) + frame(33333) = 45833
			expect(margin).toBe(10_000 + 2 * 1250 + 33_333);
		});

		it('computes jitter from RTT variation via EWMA', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			// Initial jitter = 10_000 * 0.125 = 1250
			// Second sample: jitter_sample = |15_000 - 10_000| = 5_000
			// But wait, jitter should use absolute diff between sample and smoothed RTT,
			// or between consecutive samples. Let's check: the task says
			// "absolute difference between consecutive RTT samples"
			// So jitter_sample = |15_000 - 10_000| = 5_000
			// new_jitter = 1250 * 0.875 + 5_000 * 0.125 = 1093.75 + 625 = 1718.75
			sync.updateRTT('A', 15_000);

			// Third sample: jitter_sample = |10_000 - 15_000| = 5_000
			// new_jitter = 1718.75 * 0.875 + 5_000 * 0.125 = 1503.90625 + 625 = 2128.90625
			sync.updateRTT('A', 10_000);

			// The safety margin should reflect jitter
			const margin = sync.safetyMargin();
			const expectedRTT = sync.getRTT('A');
			// margin > rtt + frame means jitter is contributing
			expect(margin).toBeGreaterThan(expectedRTT + 33_333);
		});
	});

	describe('getRTT', () => {
		it('returns smoothed RTT for tracked engine', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 20_000);
			expect(sync.getRTT('A')).toBe(20_000);
		});

		it('returns 0 for untracked engine', () => {
			const sync = new ClockSync();
			// Cast to bypass EngineId type — tests fallback for unknown keys
			expect(sync.getRTT('unknown' as 'A')).toBe(0);
		});
	});

	describe('getOffset', () => {
		it('returns stored offset for tracked engine', () => {
			const sync = new ClockSync();
			sync.updateOffset('A', 5_000_000);
			expect(sync.getOffset('A')).toBe(5_000_000);
		});

		it('returns 0 for untracked engine', () => {
			const sync = new ClockSync();
			// Cast to bypass EngineId type — tests fallback for unknown keys
			expect(sync.getOffset('unknown' as 'A')).toBe(0);
		});
	});

	describe('safetyMargin', () => {
		it('uses smoothed RTT values', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			sync.updateRTT('B', 40_000);

			const margin = sync.safetyMargin();
			// maxRTT = 40_000, maxJitter from B bootstrap = 40_000 * 0.125 = 5000
			// margin = 40_000 + 2*5000 + 33_333 = 83_333
			expect(margin).toBe(40_000 + 2 * 5_000 + 33_333);
		});

		it('returns default margin when no engines synced', () => {
			const sync = new ClockSync();
			const margin = sync.safetyMargin();
			// Default: 4 frames = 133_332
			expect(margin).toBe(133_332);
		});

		it('reflects jitter from RTT variation', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			sync.updateRTT('A', 15_000); // 5ms jitter
			sync.updateRTT('A', 10_000); // 5ms jitter again

			const margin = sync.safetyMargin();
			// Jitter should increase the safety margin beyond just RTT + frame
			expect(margin).toBeGreaterThan(sync.getRTT('A') + 33_333);
		});
	});

	describe('ptpNow', () => {
		it('computes PTP time from average offset via handlePong', () => {
			vi.useFakeTimers({ now: 1_000_000 });
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A', 'B'], sendPing, 120_000);
			// sentAt for both = performance.now() * 1000 = 0 µs (fake timer starts at 0)

			vi.advanceTimersByTime(5);
			const sentAt = 0; // performance.now() was 0 at ping time
			const rtt = 5_000; // 5ms = 5_000 µs
			// A offset = 5_000_000, B offset = 3_000_000
			sync.handlePong('A', buildPongHelper(sentAt + rtt / 2 + 5_000_000));
			sync.handlePong('B', buildPongHelper(sentAt + rtt / 2 + 3_000_000));

			// Average offset = 4_000_000
			const localUS = Date.now() * 1000;
			const ptp = sync.ptpNow();
			expect(ptp).toBe(Math.round(localUS + 4_000_000));

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('returns 0 when no offsets known', () => {
			const sync = new ClockSync();
			const ptp = sync.ptpNow();
			expect(ptp).toBe(0);
		});
	});

	describe('buildPing / parsePong', () => {
		it('builds ping datagram', () => {
			const sync = new ClockSync();
			const ping = sync.buildPing(42);

			expect(ping.length).toBe(5);
			expect(ping[0]).toBe(0xFE);
			const view = new DataView(ping.buffer, ping.byteOffset, ping.byteLength);
			expect(view.getUint32(1)).toBe(42);
		});

		it('parses pong datagram', () => {
			const sync = new ClockSync();
			const pong = new Uint8Array(9);
			pong[0] = 0xFF;
			const view = new DataView(pong.buffer);
			const hi = Math.floor(1_700_000_000_000_000 / 2 ** 32);
			const lo = 1_700_000_000_000_000 % 2 ** 32;
			view.setUint32(1, hi);
			view.setUint32(5, lo);

			const serverTime = sync.parsePong(pong);
			expect(serverTime).toBe(1_700_000_000_000_000);
		});

		it('throws on too-short pong data', () => {
			const sync = new ClockSync();
			expect(() => sync.parsePong(new Uint8Array(8))).toThrow('expected >= 9 bytes, got 8');
			expect(() => sync.parsePong(new Uint8Array(0))).toThrow('expected >= 9 bytes, got 0');
		});

		it('throws on wrong message type in pong', () => {
			const sync = new ClockSync();
			const bad = new Uint8Array(9);
			bad[0] = 0xFE; // PING type, not PONG
			expect(() => sync.parsePong(bad)).toThrow('expected type 0xff, got 0xfe');
		});
	});

	describe('handlePong', () => {
		function buildPong(timestampUS: number): Uint8Array {
			const buf = new Uint8Array(9);
			buf[0] = 0xFF;
			const view = new DataView(buf.buffer);
			const hi = Math.floor(timestampUS / (2 ** 32));
			const lo = timestampUS % (2 ** 32);
			view.setUint32(1, hi);
			view.setUint32(5, lo);
			return buf;
		}

		it('computes offset correctly from ping/pong round trip', () => {
			vi.useFakeTimers({ now: 1_000_000 }); // Date.now() = 1_000_000 ms
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 60_000);
			// doPingRound records lastPingSentAt for A = nowMicros() = performance.now() * 1000 = 0 µs

			// Advance 10ms to simulate network delay
			vi.advanceTimersByTime(10);

			// sentAt = 0 µs (performance.now() was 0 at ping time)
			// now = performance.now() * 1000 = 10_000 µs (after 10ms advance)
			// rtt = now - sentAt = 10_000 µs
			// If server is 5_000_000 µs ahead, serverTs = sentAt + rtt/2 + 5_000_000
			const sentAt = 0;
			const rtt = 10_000;
			const knownOffset = 5_000_000;
			const serverTs = sentAt + rtt / 2 + knownOffset;

			const pong = buildPong(serverTs);
			sync.handlePong('A', pong);

			// offset = serverTs - (sentAt + rtt/2) = knownOffset = 5_000_000
			expect(sync.getOffset('A')).toBe(knownOffset);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('ignores pong when no pending ping exists', () => {
			const sync = new ClockSync();
			// Never started periodic sync, so no lastPingSentAt recorded
			const pong = buildPong(1_000_000_000_000);
			sync.handlePong('A', pong);

			// Offset should remain 0 (default)
			expect(sync.getOffset('A')).toBe(0);
		});

		it('updates RTT from pong round trip', () => {
			vi.useFakeTimers({ now: 1_000_000 });
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 60_000);

			// Advance 20ms
			vi.advanceTimersByTime(20);

			// sentAt = 0 µs (performance.now() was 0 at ping time)
			// now = performance.now() * 1000 = 20_000 µs
			// rtt = 20_000 µs
			const sentAt = 0;
			const rtt = 20_000; // 20ms in µs
			const serverTs = sentAt + rtt / 2; // zero offset
			const pong = buildPong(serverTs);
			sync.handlePong('A', pong);

			// RTT should be bootstrapped to 20_000 µs
			expect(sync.getRTT('A')).toBe(rtt);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('uses sub-millisecond precision for RTT', () => {
			// Verify that RTT measurement uses performance.now() rather than Date.now().
			// With fake timers, performance.now() starts at 0 and advances with the clock,
			// while Date.now() starts at whatever the fake timer's `now` is set to.
			// A sentAt of 0 (performance.now()-based) differs from 1_000_000_000 (Date.now()-based),
			// confirming performance.now() is in use.
			vi.useFakeTimers({ now: 1_000_000 }); // Date.now() = 1_000_000 ms
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 60_000);

			vi.advanceTimersByTime(10);

			// sentAt was recorded using nowMicros() = Math.round(performance.now() * 1000)
			// With fake timers, performance.now() starts at 0, so sentAt = 0 µs
			// RTT = nowMicros() - sentAt = 10_000 - 0 = 10_000 µs
			// If this were Date.now()-based, sentAt = 1_000_000_000 and RTT would be 10_000 too,
			// but the offset calculation differs (serverTs is built relative to sentAt = 0 here).
			const sentAt = 0;
			const rtt = 10_000;
			const knownOffset = 1_000;
			const serverTs = sentAt + rtt / 2 + knownOffset;
			sync.handlePong('A', buildPong(serverTs));

			expect(sync.getOffset('A')).toBe(knownOffset);
			expect(sync.getRTT('A')).toBe(rtt);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});
	});

	describe('ptpNow staleness filtering', () => {
		it('excludes stale offsets older than 60s', () => {
			vi.useFakeTimers({ now: 1_000_000 }); // Date.now() = 1_000_000 ms
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 120_000);
			// sentAt for A = nowMicros() = performance.now() * 1000 = 0 µs

			// Simulate pong after 5ms
			vi.advanceTimersByTime(5);
			// performance.now() = 5, nowMicros() = 5_000
			// rtt = 5_000 µs, offset we want = 100_000 µs
			const sentAt = 0; // performance.now() was 0 at ping time
			const rtt = 5_000;
			const pongA = buildPongHelper(sentAt + rtt / 2 + 100_000);
			sync.handlePong('A', pongA);

			// Offset is valid, ptpNow should include it
			const ptpBefore = sync.ptpNow();
			const localBefore = Date.now() * 1000;
			expect(ptpBefore).toBe(Math.round(localBefore + 100_000));

			// Advance 61s to make the offset stale
			vi.advanceTimersByTime(61_000);

			// Now ptpNow should return 0 (no valid offsets, safer than stale time)
			const ptpAfter = sync.ptpNow();
			expect(ptpAfter).toBe(0);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('averages only valid (non-stale) offsets', () => {
			vi.useFakeTimers({ now: 1_000_000 }); // Date.now() = 1_000_000 ms
			const sync = new ClockSync();

			// Use updateOffset + offsetAge directly for precise control
			// Set offset for A with current age
			sync.updateOffset('A', 100_000);
			sync.updateOffset('B', 200_000);

			// ptpNow without any offsetAge entries means offsets are unaged
			// (no handlePong was called, so offsetAge is empty -> offsets are excluded)
			const ptpNoAge = sync.ptpNow();
			// With no valid offsetAge, both offsets are excluded -> returns 0
			expect(ptpNoAge).toBe(0);

			// Now use handlePong to set A's offset with proper age tracking
			const sendPing = vi.fn();
			sync.startPeriodicSync(['A', 'B'], sendPing, 120_000);
			// sentAt for both = nowMicros() = performance.now() * 1000 = 0 µs

			vi.advanceTimersByTime(5);
			const sentAt = 0; // performance.now() was 0 at ping time
			const rtt = 5_000;
			// Set offset for A = 100_000
			sync.handlePong('A', buildPongHelper(sentAt + rtt / 2 + 100_000));
			// Set offset for B = 200_000
			sync.handlePong('B', buildPongHelper(sentAt + rtt / 2 + 200_000));

			// Both valid: average = 150_000
			const ptpBoth = sync.ptpNow();
			const localBoth = Date.now() * 1000;
			expect(ptpBoth).toBe(Math.round(localBoth + 150_000));

			// Advance 55s, then refresh only B via a new ping round
			vi.advanceTimersByTime(55_000);

			// Manually trigger a fresh pong for B only
			// We need a fresh lastPingSentAt for B. The periodic sync at 120s hasn't fired.
			// Use addEngine or direct approach: call doPingRound indirectly by restarting sync
			// Simplest: stop and restart to get fresh sentAt for B
			sync.stopPeriodicSync();
			sync.startPeriodicSync(['B'], sendPing, 120_000);
			// New sentAt for B = nowMicros() = performance.now() * 1000
			// At this point performance.now() = 5 + 55_000 = 55_005 ms

			vi.advanceTimersByTime(5);
			// performance.now() before this advance was 55_005, so sentAt = 55_005_000 µs
			const newSentAt = 55_005_000; // performance.now() was 55_005 at new ping time
			const newRtt = 5_000;
			sync.handlePong('B', buildPongHelper(newSentAt + newRtt / 2 + 200_000));

			// Now A's age is ~55s (still valid), B's age is fresh
			// Wait 6 more seconds to push A past 60s
			vi.advanceTimersByTime(6_000);

			// A's age is now ~61s (stale), B's age is ~6s (valid)
			const ptpOnlyB = sync.ptpNow();
			const localOnlyB = Date.now() * 1000;
			const bOffset = sync.getOffset('B');
			expect(ptpOnlyB).toBe(Math.round(localOnlyB + bOffset));

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});
	});

	describe('getOffsetDivergence', () => {
		it('returns 0 with fewer than 2 engines', () => {
			const sync = new ClockSync();
			expect(sync.getOffsetDivergence()).toBe(0);

			sync.updateOffset('A', 5_000_000);
			expect(sync.getOffsetDivergence()).toBe(0);
		});

		it('returns absolute difference between two engine offsets', () => {
			const sync = new ClockSync();
			sync.updateOffset('A', 10_000_000);
			sync.updateOffset('B', 15_000_000);
			expect(sync.getOffsetDivergence()).toBe(5_000_000);
		});

		it('returns absolute difference regardless of sign', () => {
			const sync = new ClockSync();
			sync.updateOffset('A', -5_000_000);
			sync.updateOffset('B', 5_000_000);
			expect(sync.getOffsetDivergence()).toBe(10_000_000);
		});
	});

	describe('isClockDiverged', () => {
		it('returns false when fewer than 2 engines have offsets', () => {
			const sync = new ClockSync();
			expect(sync.isClockDiverged()).toBe(false);

			sync.updateOffset('A', 5_000_000);
			expect(sync.isClockDiverged()).toBe(false);
		});

		it('returns false when divergence is within safety margin', () => {
			const sync = new ClockSync();
			// Set RTTs so safetyMargin is computed from real data
			sync.updateRTT('A', 10_000);
			sync.updateRTT('B', 10_000);

			// safetyMargin = maxRTT(10000) + 2*maxJitter(1250) + frame(33333) = 45833
			// Set offsets with small divergence (1000 µs) -- well within margin
			sync.updateOffset('A', 100_000);
			sync.updateOffset('B', 101_000);
			expect(sync.isClockDiverged()).toBe(false);
		});

		it('returns true when divergence exceeds safety margin', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			sync.updateRTT('B', 10_000);

			// safetyMargin = 10000 + 2*1250 + 33333 = 45833
			// Set offsets with huge divergence (500_000 µs)
			sync.updateOffset('A', 100_000);
			sync.updateOffset('B', 600_000);
			expect(sync.isClockDiverged()).toBe(true);
		});

		it('returns false when offsets are equal (divergence is 0)', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			sync.updateRTT('B', 10_000);
			sync.updateOffset('A', 100_000);
			sync.updateOffset('B', 100_000);
			expect(sync.isClockDiverged()).toBe(false);
		});

		it('uses default margin when no RTT data exists', () => {
			const sync = new ClockSync();
			// Default margin = 4 frames = 133_332 µs
			// Set divergence just above the default margin
			sync.updateOffset('A', 0);
			sync.updateOffset('B', 140_000);
			expect(sync.isClockDiverged()).toBe(true);

			// Set divergence just below the default margin
			sync.updateOffset('B', 130_000);
			expect(sync.isClockDiverged()).toBe(false);
		});
	});

	describe('constants', () => {
		it('DEFAULT_SYNC_INTERVAL_MS is 10000', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();
			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing);

			// Immediate fire: 1 ping
			expect(sendPing).toHaveBeenCalledTimes(1);

			// At 9s, no additional ping
			vi.advanceTimersByTime(9_000);
			expect(sendPing).toHaveBeenCalledTimes(1);

			// At 10s, interval fires
			vi.advanceTimersByTime(1_000);
			expect(sendPing).toHaveBeenCalledTimes(2);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('DEFAULT_MARGIN_US is 4 frames', () => {
			const sync = new ClockSync();
			// With no engines tracked, safetyMargin returns DEFAULT_MARGIN_US
			// 4 frames at 30fps = 4 * 33_333 = 133_332
			expect(sync.safetyMargin()).toBe(133_332);
		});
	});

	describe('periodic sync', () => {
		it('sends pings to all engines from provided ID list', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();
			// No updateRTT calls -- engines are provided via engineIds param
			const sendPing = vi.fn();
			sync.startPeriodicSync(['A', 'B'], sendPing, 1000);

			// Immediate fire: 2 pings (A and B)
			expect(sendPing).toHaveBeenCalledTimes(2);
			expect(sendPing).toHaveBeenCalledWith('A', expect.any(Uint8Array));
			expect(sendPing).toHaveBeenCalledWith('B', expect.any(Uint8Array));

			// Advance past one interval
			vi.advanceTimersByTime(1000);
			expect(sendPing).toHaveBeenCalledTimes(4);

			// Advance past another interval
			vi.advanceTimersByTime(1000);
			expect(sendPing).toHaveBeenCalledTimes(6);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('pings engines that have no prior RTT data', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();
			// Engine B has never called updateRTT -- but should still receive pings
			const sendPing = vi.fn();
			sync.startPeriodicSync(['B'], sendPing, 500);

			// Immediate fire: 1 ping
			expect(sendPing).toHaveBeenCalledTimes(1);
			expect(sendPing).toHaveBeenCalledWith('B', expect.any(Uint8Array));

			vi.advanceTimersByTime(500);
			expect(sendPing).toHaveBeenCalledTimes(2);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('stops sending pings after stopPeriodicSync', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 1000);

			// Immediate fire: 1 ping
			expect(sendPing).toHaveBeenCalledTimes(1);

			vi.advanceTimersByTime(1000);
			expect(sendPing).toHaveBeenCalledTimes(2);

			sync.stopPeriodicSync();

			vi.advanceTimersByTime(5000);
			// Should still be 2 -- no more pings sent
			expect(sendPing).toHaveBeenCalledTimes(2);

			vi.useRealTimers();
		});

		it('uses 10s default interval', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing);

			// Immediate fire: 1 ping
			expect(sendPing).toHaveBeenCalledTimes(1);

			// At 9s, no additional ping
			vi.advanceTimersByTime(9_000);
			expect(sendPing).toHaveBeenCalledTimes(1);

			// At 10s, interval fires
			vi.advanceTimersByTime(1_000);
			expect(sendPing).toHaveBeenCalledTimes(2);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('sends incrementing sequence numbers', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();

			const pings: Uint8Array[] = [];
			const sendPing = vi.fn((_id: string, data: Uint8Array) => {
				pings.push(new Uint8Array(data));
			});
			sync.startPeriodicSync(['A'], sendPing, 100);

			// Immediate fire: 1 ping
			expect(pings.length).toBe(1);

			vi.advanceTimersByTime(200);
			expect(pings.length).toBe(3); // 1 immediate + 2 interval

			const view0 = new DataView(pings[0].buffer, pings[0].byteOffset, pings[0].byteLength);
			const view1 = new DataView(pings[1].buffer, pings[1].byteOffset, pings[1].byteLength);
			expect(view1.getUint32(1)).toBe(view0.getUint32(1) + 1);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('addEngine dynamically adds to the periodic sync loop', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 1000);

			// Immediate fire: 1 ping (A only)
			expect(sendPing).toHaveBeenCalledTimes(1);

			// First tick: only A
			vi.advanceTimersByTime(1000);
			expect(sendPing).toHaveBeenCalledTimes(2);
			expect(sendPing).toHaveBeenCalledWith('A', expect.any(Uint8Array));

			// Dynamically add engine B
			sync.addEngine('B');

			// Second tick: both A and B
			vi.advanceTimersByTime(1000);
			expect(sendPing).toHaveBeenCalledTimes(4); // 1 immediate + 1 first tick + 2 second tick
			expect(sendPing).toHaveBeenCalledWith('B', expect.any(Uint8Array));

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('addEngine deduplicates engine IDs', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 1000);

			// Immediate fire: 1 ping
			expect(sendPing).toHaveBeenCalledTimes(1);

			// Add A again -- should not duplicate
			sync.addEngine('A');

			vi.advanceTimersByTime(1000);
			// 1 immediate + 1 interval = 2, not 3
			expect(sendPing).toHaveBeenCalledTimes(2);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});
	});
});
