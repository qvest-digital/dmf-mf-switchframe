import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ClockSync } from './clock-sync';

function buildPongHelper(seq: number, timestampUS: number): Uint8Array {
	const buf = new Uint8Array(13);
	buf[0] = 0xFF;
	const view = new DataView(buf.buffer);
	view.setUint32(1, seq);
	const hi = Math.floor(timestampUS / (2 ** 32));
	const lo = timestampUS % (2 ** 32);
	view.setUint32(5, hi);
	view.setUint32(9, lo);
	return buf;
}

describe('ClockSync', () => {
	beforeEach(() => {
		// Pings count from 0, as the pongs built here expect.
		vi.spyOn(Math, 'random').mockReturnValue(0);
	});

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

	describe('delayed first sample', () => {
		it('starts over from a sample far below the estimate', () => {
			// Measured on a page still opening its streams: the first pong took
			// 6.7s, the next ones about 30ms. Smoothed, the pane showed seconds
			// of RTT for minutes, and the safety margin grew with it.
			const sync = new ClockSync();
			sync.updateRTT('A', 6_668_000);
			sync.updateRTT('A', 27_000);

			expect(sync.getRTT('A')).toBe(27_000);
			expect(sync.safetyMargin()).toBe(27_000 + 2 * 27_000 * 0.125 + 33_333);
		});

		it('keeps smoothing a drop short of a quarter', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 40_000);
			sync.updateRTT('A', 12_000);

			expect(sync.getRTT('A')).toBe(40_000 * 0.875 + 12_000 * 0.125);
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

			vi.advanceTimersByTime(5);
			const rtt = 5_000; // 5ms = 5_000 µs
			// serverTimestamp is in the server wall-clock (Unix) epoch — the same
			// domain as Date.now() — so the offset is serverTs - (Date.now()*1000 - rtt/2).
			const wallNowUS = Date.now() * 1000;
			// A offset = 5_000_000, B offset = 3_000_000 (seq 0=A, 1=B from first ping round)
			sync.handlePong('A', buildPongHelper(0, wallNowUS - rtt / 2 + 5_000_000));
			sync.handlePong('B', buildPongHelper(1, wallNowUS - rtt / 2 + 3_000_000));

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

	describe('getOffsetDivergence', () => {
		it('returns 0 when fewer than 2 valid offsets exist', () => {
			const sync = new ClockSync();
			sync.updateOffset('A', 5_000_000);
			expect(sync.getOffsetDivergence()).toBe(0);
		});

		it('returns absolute difference between 2 valid offsets', () => {
			const sync = new ClockSync();
			sync.updateOffset('A', 5_000_000);
			sync.updateOffset('B', 8_000_000);
			expect(sync.getOffsetDivergence()).toBe(3_000_000);
		});

		it('returns 0 when one offset is stale (>20s)', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();
			sync.updateOffset('A', 5_000_000);
			// Advance past 20s to make A stale
			vi.advanceTimersByTime(21_000);
			sync.updateOffset('B', 8_000_000);
			expect(sync.getOffsetDivergence()).toBe(0);
			vi.useRealTimers();
		});

		it('considers offsets valid at exactly 20s', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();
			sync.updateOffset('A', 5_000_000);
			vi.advanceTimersByTime(20_000);
			sync.updateOffset('B', 8_000_000);
			// A is exactly 20s old — still valid (<=)
			expect(sync.getOffsetDivergence()).toBe(3_000_000);
			vi.useRealTimers();
		});
	});

	describe('isClockDiverged', () => {
		it('returns false when fewer than 2 valid offsets exist', () => {
			const sync = new ClockSync();
			sync.updateOffset('A', 5_000_000);
			expect(sync.isClockDiverged()).toBe(false);
		});

		it('returns false when one offset is stale (>20s)', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			sync.updateRTT('B', 10_000);
			sync.updateOffset('A', 5_000_000);
			// Advance past 20s to make A stale
			vi.advanceTimersByTime(21_000);
			sync.updateOffset('B', 100_000_000); // big divergence but A is stale
			expect(sync.isClockDiverged()).toBe(false);
			vi.useRealTimers();
		});

		it('returns true when divergence exceeds safety margin', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			sync.updateRTT('B', 10_000);
			// Safety margin is ~45833 us, so set divergence well above that
			sync.updateOffset('A', 0);
			sync.updateOffset('B', 1_000_000); // 1s divergence
			expect(sync.isClockDiverged()).toBe(true);
		});

		it('returns false when divergence is within safety margin', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			sync.updateRTT('B', 10_000);
			// Safety margin is ~45833 us, set divergence below that
			sync.updateOffset('A', 0);
			sync.updateOffset('B', 100); // 100 us divergence
			expect(sync.isClockDiverged()).toBe(false);
		});
	});

	describe('ping seq', () => {
		function firstPingSeq(sync: ClockSync): number {
			let seq = -1;
			sync.startPeriodicSync(['A'], (_id, data) => {
				if (seq < 0) seq = new DataView(data.buffer).getUint32(1);
			});
			sync.stopPeriodicSync();
			return seq;
		}

		it('starts each browser at its own seq', () => {
			// Every viewer of an engine's control track receives every pong.
			// Two browsers counting from the same seq would take each other's
			// pongs for their own and measure a wrong RTT.
			vi.mocked(Math.random).mockReturnValueOnce(0.25).mockReturnValueOnce(0.75);
			const a = firstPingSeq(new ClockSync());
			const b = firstPingSeq(new ClockSync());

			expect(a).toBe(2 ** 30);
			expect(b).toBe(3 * 2 ** 30);
		});

		it('keeps the newest pending pings across a seq wrap', () => {
			// Past the cap the oldest pings are dropped. Ordered by seq, the
			// newest pings after a wrap would look oldest and their pongs go
			// unmatched.
			vi.useFakeTimers();
			let t = 0;
			vi.spyOn(performance, 'now').mockImplementation(() => t);
			vi.mocked(Math.random).mockReturnValueOnce((2 ** 32 - 100) / 2 ** 32);
			const sync = new ClockSync();
			let last = 0;
			sync.startPeriodicSync(['A'], (_id, data) => { last = new DataView(data.buffer).getUint32(1); }, 1000);
			// 101 pings: 100 before the wrap, then seq 0, which overflows the cap.
			for (let i = 0; i < 100; i++) {
				t += 1;
				vi.advanceTimersByTime(1000);
			}
			sync.stopPeriodicSync();
			vi.useRealTimers();

			expect(last).toBe(0);
			const pong = new Uint8Array(13);
			pong[0] = 0xFF;
			new DataView(pong.buffer).setUint32(1, last);
			sync.handlePong('A', pong);
			expect(sync.getOffset('A')).not.toBe(0);
		});

		it('wraps at the uint32 the ping carries and still matches the pong', () => {
			vi.mocked(Math.random).mockReturnValueOnce((2 ** 32 - 1) / 2 ** 32);
			const sync = new ClockSync();
			const seqs: number[] = [];
			sync.startPeriodicSync(['A', 'B'], (_id, data) => seqs.push(new DataView(data.buffer).getUint32(1)));
			sync.stopPeriodicSync();

			expect(seqs).toEqual([2 ** 32 - 1, 0]);
			const pong = new Uint8Array(13);
			pong[0] = 0xFF;
			sync.handlePong('B', pong);
			// An unmatched pong is dropped and leaves the offset unset.
			expect(sync.getOffset('B')).not.toBe(0);
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

		it('parses 13-byte pong datagram with seq and timestamp', () => {
			const sync = new ClockSync();
			const pong = new Uint8Array(13);
			pong[0] = 0xFF;
			const view = new DataView(pong.buffer);
			view.setUint32(1, 42); // seq
			const hi = Math.floor(1_700_000_000_000_000 / 2 ** 32);
			const lo = 1_700_000_000_000_000 % 2 ** 32;
			view.setUint32(5, hi);
			view.setUint32(9, lo);

			const { seq, serverTimestamp } = sync.parsePong(pong);
			expect(seq).toBe(42);
			expect(serverTimestamp).toBe(1_700_000_000_000_000);
		});

		it('throws on too-short pong data', () => {
			const sync = new ClockSync();
			expect(() => sync.parsePong(new Uint8Array(12))).toThrow('expected >= 13 bytes, got 12');
			expect(() => sync.parsePong(new Uint8Array(0))).toThrow('expected >= 13 bytes, got 0');
		});

		it('throws on wrong message type in pong', () => {
			const sync = new ClockSync();
			const bad = new Uint8Array(13);
			bad[0] = 0xFE; // PING type, not PONG
			expect(() => sync.parsePong(bad)).toThrow('expected type 0xff, got 0xfe');
		});
	});

	describe('handlePong', () => {
		function buildPong(seq: number, timestampUS: number): Uint8Array {
			const buf = new Uint8Array(13);
			buf[0] = 0xFF;
			const view = new DataView(buf.buffer);
			view.setUint32(1, seq);
			const hi = Math.floor(timestampUS / (2 ** 32));
			const lo = timestampUS % (2 ** 32);
			view.setUint32(5, hi);
			view.setUint32(9, lo);
			return buf;
		}

		it('computes offset correctly from ping/pong round trip', () => {
			vi.useFakeTimers({ now: 1_000_000 }); // Date.now() = 1_000_000 ms
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 60_000);
			// doPingRound records pingSentTimes for seq 0 (A)

			// Advance 10ms to simulate network delay
			vi.advanceTimersByTime(10);

			const rtt = 10_000;
			const knownOffset = 5_000_000;
			// serverTs is in the server wall-clock (Unix) epoch, same as Date.now().
			const wallNowUS = Date.now() * 1000;
			const serverTs = wallNowUS - rtt / 2 + knownOffset;

			const pong = buildPong(0, serverTs); // seq 0 = A
			sync.handlePong('A', pong);

			// offset = serverTs - (Date.now()*1000 - rtt/2) = knownOffset = 5_000_000
			expect(sync.getOffset('A')).toBe(knownOffset);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('ignores pong when no pending ping exists', () => {
			const sync = new ClockSync();
			// Never started periodic sync, so no pingSentTimes recorded
			const pong = buildPong(99, 1_000_000_000_000); // unknown seq
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

			const sentAt = 0;
			const rtt = 20_000; // 20ms in µs
			const serverTs = sentAt + rtt / 2; // zero offset
			const pong = buildPong(0, serverTs); // seq 0 = A
			sync.handlePong('A', pong);

			// RTT should be bootstrapped to 20_000 µs
			expect(sync.getRTT('A')).toBe(rtt);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('uses sub-millisecond precision for RTT', () => {
			vi.useFakeTimers({ now: 1_000_000 }); // Date.now() = 1_000_000 ms
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 60_000);

			vi.advanceTimersByTime(10);

			const rtt = 10_000;
			const knownOffset = 1_000;
			// serverTs is in the server wall-clock (Unix) epoch, same as Date.now().
			const wallNowUS = Date.now() * 1000;
			const serverTs = wallNowUS - rtt / 2 + knownOffset;
			sync.handlePong('A', buildPong(0, serverTs)); // seq 0 = A

			expect(sync.getOffset('A')).toBe(knownOffset);
			expect(sync.getRTT('A')).toBe(rtt);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		// Regression: the server sends its wall-clock (`time.Now().UnixMicro()`,
		// ~1.7e15) while the browser's `performance.now()` is small (ms since page
		// load). The offset and ptpNow MUST stay in the wall-clock epoch — mixing
		// the two epochs produced offsets the size of navigationStart and a ptpNow
		// of ~2× wall-clock (~year 2080), which the server's PTP-prefix dispatcher
		// (strips only 2020-2040) would NOT strip, dropping the datagram.
		it('keeps ptpNow in the server wall-clock epoch (cross-epoch regression)', () => {
			// performance.now() starts at 0; Date.now() base is a realistic 2025 epoch.
			const WALL_BASE_MS = 1_748_000_000_000; // ≈ 2025-05-23 (UnixMicro ~1.748e15)
			vi.useFakeTimers({ now: WALL_BASE_MS });
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 120_000);
			// pingSentTimes[0] = performance.now()*1000 = 0 µs

			// Cross-region RTT of 40ms; server clock is ~30ms ahead of the client.
			vi.advanceTimersByTime(40);
			const rtt = 40_000;
			const trueSkewUS = 30_000;
			// Server's wall-clock at the moment it replied (mid-flight), in the
			// real Unix-micro epoch — completely unrelated to performance.now().
			const wallNowUS = Date.now() * 1000;
			const serverWallUS = wallNowUS - rtt / 2 + trueSkewUS;
			sync.handlePong('A', buildPong(0, serverWallUS));

			// RTT is still measured on the monotonic clock and is exact.
			expect(sync.getRTT('A')).toBe(rtt);
			// Offset is the true (small) skew, not a navigationStart-sized value.
			expect(sync.getOffset('A')).toBe(trueSkewUS);

			// ptpNow ≈ server wall-clock (within half the RTT), NOT ~2× wall-clock.
			const ptp = sync.ptpNow();
			expect(Math.abs(ptp - serverWallUS)).toBeLessThanOrEqual(rtt / 2);

			// And it falls inside the dispatcher's PTP-prefix stripping window
			// (2020-01-01 .. 2040-01-01 in µs) so datagrams are not dropped.
			expect(ptp).toBeGreaterThan(1_577_836_800_000_000);
			expect(ptp).toBeLessThan(2_208_988_800_000_000);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});
	});

	describe('ptpNow staleness filtering', () => {
		it('excludes stale offsets older than 20s', () => {
			vi.useFakeTimers({ now: 1_000_000 }); // Date.now() = 1_000_000 ms
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 120_000);

			// Simulate pong after 5ms (seq 0 = A from first ping round)
			vi.advanceTimersByTime(5);
			const rtt = 5_000;
			// serverTimestamp is in the server wall-clock (Unix) epoch, same as Date.now().
			const wallNowUS = Date.now() * 1000;
			const pongA = buildPongHelper(0, wallNowUS - rtt / 2 + 100_000);
			sync.handlePong('A', pongA);

			// Offset is valid, ptpNow should include it
			const ptpBefore = sync.ptpNow();
			const localBefore = Date.now() * 1000;
			expect(ptpBefore).toBe(Math.round(localBefore + 100_000));

			// Advance 21s to make the offset stale
			vi.advanceTimersByTime(21_000);

			// Now ptpNow should return 0 (no valid offsets, safer than stale time)
			const ptpAfter = sync.ptpNow();
			expect(ptpAfter).toBe(0);

			sync.stopPeriodicSync();
			vi.useRealTimers();
		});

		it('averages only valid (non-stale) offsets', () => {
			vi.useFakeTimers({ now: 1_000_000 }); // Date.now() = 1_000_000 ms
			const sync = new ClockSync();

			// updateOffset now sets offsetAge, so offsets are immediately valid
			sync.updateOffset('A', 100_000);
			sync.updateOffset('B', 200_000);

			// Both offsets have valid age -> average = 150_000
			const ptpWithAge = sync.ptpNow();
			const localWithAge = Date.now() * 1000;
			expect(ptpWithAge).toBe(Math.round(localWithAge + 150_000));

			// Use handlePong to set offsets via the ping/pong flow
			const sendPing = vi.fn();
			sync.startPeriodicSync(['A', 'B'], sendPing, 120_000);
			// A gets seq 0, B gets seq 1

			vi.advanceTimersByTime(5);
			const rtt = 5_000;
			// serverTimestamp is in the server wall-clock (Unix) epoch, same as Date.now().
			const wallNowUS = Date.now() * 1000;
			// Set offset for A = 100_000 (seq 0)
			sync.handlePong('A', buildPongHelper(0, wallNowUS - rtt / 2 + 100_000));
			// Set offset for B = 200_000 (seq 1)
			sync.handlePong('B', buildPongHelper(1, wallNowUS - rtt / 2 + 200_000));

			// Both valid: average = 150_000
			const ptpBoth = sync.ptpNow();
			const localBoth = Date.now() * 1000;
			expect(ptpBoth).toBe(Math.round(localBoth + 150_000));

			// Advance 15s, then refresh only B via a new ping round
			vi.advanceTimersByTime(15_000);

			// Restart sync for B only to get a fresh pingSentTimes entry
			sync.stopPeriodicSync();
			sync.startPeriodicSync(['B'], sendPing, 120_000);
			// B gets seq 2 (pingSeq continues from 2)

			vi.advanceTimersByTime(5);
			const newRtt = 5_000;
			// serverTimestamp is in the server wall-clock (Unix) epoch, same as Date.now().
			const newWallNowUS = Date.now() * 1000;
			sync.handlePong('B', buildPongHelper(2, newWallNowUS - newRtt / 2 + 200_000));

			// Now A's age is ~15s (still valid), B's age is fresh
			// Wait 6 more seconds to push A past 20s
			vi.advanceTimersByTime(6_000);

			// A's age is now ~21s (stale), B's age is ~6s (valid)
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

		it('uses max-min for 3+ engines (not hardcoded for 2)', () => {
			const sync = new ClockSync();
			sync.updateOffset('A', 1_000_000);
			sync.updateOffset('B', 5_000_000);
			sync.updateOffset('C' as any, 3_000_000);
			// max(5M) - min(1M) = 4M, not abs(A-B) = 4M (coincidentally same)
			// Use offset values where max-min differs from abs(first-second)
			expect(sync.getOffsetDivergence()).toBe(4_000_000);
		});

		it('returns max-min divergence for 3 engines where order matters', () => {
			const sync = new ClockSync();
			// If we used abs(validOffsets[0] - validOffsets[1]) we'd get abs(1M - 3M) = 2M
			// But correct max-min should be 5M - 1M = 4M
			sync.updateOffset('A', 1_000_000);
			sync.updateOffset('B', 3_000_000);
			sync.updateOffset('C' as any, 5_000_000);
			expect(sync.getOffsetDivergence()).toBe(4_000_000);
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

	describe('ptpNowForEngine', () => {
		it('returns per-engine adjusted timestamp', () => {
			const cs = new ClockSync();
			cs.updateOffset('A' as any, 1000);
			cs.updateOffset('B' as any, -500);

			const nowA = cs.ptpNowForEngine('A' as any);
			const nowB = cs.ptpNowForEngine('B' as any);
			expect(nowA - nowB).toBeCloseTo(1500, -2);
		});

		it('falls back to ptpNow for unknown engine', () => {
			const cs = new ClockSync();
			cs.updateOffset('A' as any, 1000);
			const now = cs.ptpNowForEngine('C' as any);
			const fallback = cs.ptpNow();
			expect(Math.abs(now - fallback)).toBeLessThan(100);
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

	describe('isConverged', () => {
		it('returns false when no engines are tracked', () => {
			const sync = new ClockSync();
			expect(sync.isConverged()).toBe(false);
		});

		it('returns false when fewer than 2 engines are tracked', () => {
			const sync = new ClockSync();
			sync.addEngine('A');
			sync.updateRTT('A', 10_000);
			sync.updateOffset('A', 5_000);
			expect(sync.isConverged()).toBe(false);
		});

		it('returns false when RTT is missing for one engine', () => {
			const sync = new ClockSync();
			sync.addEngine('A');
			sync.addEngine('B');
			sync.updateRTT('A', 10_000);
			sync.updateOffset('A', 5_000);
			// B has no RTT
			sync.updateOffset('B', 6_000);
			expect(sync.isConverged()).toBe(false);
		});

		it('returns false when offset is missing for one engine', () => {
			const sync = new ClockSync();
			sync.addEngine('A');
			sync.addEngine('B');
			sync.updateRTT('A', 10_000);
			sync.updateRTT('B', 10_000);
			sync.updateOffset('A', 5_000);
			// B has no offset
			expect(sync.isConverged()).toBe(false);
		});

		it('returns true when all engines have RTT and offset', () => {
			const sync = new ClockSync();
			sync.addEngine('A');
			sync.addEngine('B');
			sync.updateRTT('A', 10_000);
			sync.updateRTT('B', 10_000);
			sync.updateOffset('A', 5_000);
			sync.updateOffset('B', 6_000);
			expect(sync.isConverged()).toBe(true);
		});

		it('returns false when an offset is stale (>20s)', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();
			sync.addEngine('A');
			sync.addEngine('B');
			sync.updateRTT('A', 10_000);
			sync.updateRTT('B', 10_000);
			sync.updateOffset('A', 5_000);
			sync.updateOffset('B', 6_000);
			expect(sync.isConverged()).toBe(true);

			// Advance past stale threshold (20s)
			vi.advanceTimersByTime(21_000);
			expect(sync.isConverged()).toBe(false);
			vi.useRealTimers();
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
