import { describe, it, expect, vi, afterEach } from 'vitest';
import { ClockSync } from './clock-sync';

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
			// Default: 2 frames = 66_666
			expect(margin).toBe(66_666);
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
		it('computes PTP time from average offset', () => {
			const sync = new ClockSync();
			sync.updateOffset('A', 5_000_000); // 5s offset
			sync.updateOffset('B', 3_000_000); // 3s offset

			const before = Date.now() * 1000 + 4_000_000;
			const ptp = sync.ptpNow();
			const after = Date.now() * 1000 + 4_000_000;

			expect(ptp).toBeGreaterThanOrEqual(before - 100_000);
			expect(ptp).toBeLessThanOrEqual(after + 100_000);
		});

		it('returns local time when no offsets known', () => {
			const sync = new ClockSync();
			const before = Date.now() * 1000;
			const ptp = sync.ptpNow();
			const after = Date.now() * 1000;

			expect(ptp).toBeGreaterThanOrEqual(before - 1000);
			expect(ptp).toBeLessThanOrEqual(after + 1000);
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

		it('uses 30s default interval', () => {
			vi.useFakeTimers();
			const sync = new ClockSync();

			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing);

			// Immediate fire: 1 ping
			expect(sendPing).toHaveBeenCalledTimes(1);

			// At 29s, no additional ping
			vi.advanceTimersByTime(29_000);
			expect(sendPing).toHaveBeenCalledTimes(1);

			// At 30s, interval fires
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
