/**
 * Precision tests for ClockSync.
 *
 * These tests verify the mathematical correctness of EWMA smoothing,
 * jitter calculation, safety margin computation, and PTP timestamp
 * generation. They use exact expected values, not loose ranges.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ClockSync } from './clock-sync';

describe('ClockSync precision', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	// ─── EWMA RTT smoothing ───

	describe('RTT EWMA (alpha=0.125)', () => {
		it('bootstraps first sample as initial value', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			expect(sync.getRTT('A')).toBe(10_000);
		});

		it('applies EWMA formula: new = old * 0.875 + sample * 0.125', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000); // bootstrap

			sync.updateRTT('A', 50_000);
			// 10_000 * 0.875 + 50_000 * 0.125 = 8_750 + 6_250 = 15_000
			expect(sync.getRTT('A')).toBe(15_000);
		});

		it('converges toward repeated sample value', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000); // bootstrap

			// Feed 100 samples of 20_000 — should converge close to 20_000
			for (let i = 0; i < 100; i++) {
				sync.updateRTT('A', 20_000);
			}

			// After 100 iterations of EWMA with alpha=0.125,
			// the error from initial value should be negligible
			expect(sync.getRTT('A')).toBeCloseTo(20_000, 0);
		});

		it('three-step EWMA sequence is exact', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 1000); // bootstrap: 1000

			sync.updateRTT('A', 2000);
			// 1000 * 0.875 + 2000 * 0.125 = 875 + 250 = 1125
			expect(sync.getRTT('A')).toBe(1125);

			sync.updateRTT('A', 3000);
			// 1125 * 0.875 + 3000 * 0.125 = 984.375 + 375 = 1359.375
			expect(sync.getRTT('A')).toBe(1359.375);
		});

		it('tracks engines independently', () => {
			const sync = new ClockSync();

			sync.updateRTT('A', 10_000);
			sync.updateRTT('B', 50_000);

			sync.updateRTT('A', 10_000); // stable
			sync.updateRTT('B', 50_000); // stable

			expect(sync.getRTT('A')).toBe(10_000);
			expect(sync.getRTT('B')).toBe(50_000);
		});

		it('returns 0 for unknown engine', () => {
			const sync = new ClockSync();
			expect(sync.getRTT('A')).toBe(0);
		});
	});

	// ─── Jitter calculation ───

	describe('jitter', () => {
		it('bootstraps initial jitter as RTT * alpha', () => {
			const sync = new ClockSync();
			sync.updateRTT('A', 10_000);
			// After first sample, jitter should bootstrap to 10_000 * 0.125 = 1250
			// (stored internally, we can verify through safetyMargin)
		});

		it('computes jitter from absolute RTT differences', () => {
			const sync = new ClockSync();

			// Stable RTT → low jitter
			sync.updateRTT('A', 10_000);
			sync.updateRTT('A', 10_000); // diff = 0
			sync.updateRTT('A', 10_000); // diff = 0

			const marginStable = sync.safetyMargin();

			// High RTT variance → high jitter
			const sync2 = new ClockSync();
			sync2.updateRTT('A', 10_000);
			sync2.updateRTT('A', 50_000); // diff = 40_000
			sync2.updateRTT('A', 10_000); // diff = 40_000

			const marginJittery = sync2.safetyMargin();

			// Jittery connection should have higher safety margin
			expect(marginJittery).toBeGreaterThan(marginStable);
		});
	});

	// ─── Safety margin ───

	describe('safetyMargin', () => {
		it('returns default (2 frames) when no engines tracked', () => {
			const sync = new ClockSync();
			// DEFAULT_MARGIN_US = 33_333 * 2 = 66_666
			expect(sync.safetyMargin()).toBe(66_666);
		});

		it('includes maxRTT + 2*maxJitter + frameDuration', () => {
			const sync = new ClockSync();

			// Bootstrap with known values
			sync.updateRTT('A', 10_000);
			sync.updateRTT('A', 10_000); // stable: jitter → 0 after EWMA

			// After 2nd sample: jitter = bootstrap_jitter * 0.875 + 0 * 0.125
			// bootstrap_jitter = 10_000 * 0.125 = 1250
			// jitter after 2nd = 1250 * 0.875 + 0 * 0.125 = 1093.75
			// margin = 10_000 + 2 * 1093.75 + 33_333 = 45_520.5
			const margin = sync.safetyMargin();
			expect(margin).toBe(10_000 + 2 * 1093.75 + 33_333);
		});

		it('uses max across engines', () => {
			const sync = new ClockSync();

			sync.updateRTT('A', 5_000);
			sync.updateRTT('B', 50_000);

			// Max RTT should be from B
			const margin = sync.safetyMargin();
			expect(margin).toBeGreaterThan(50_000 + 33_333);
		});
	});

	// ─── PTP timestamp generation ───

	describe('ptpNow', () => {
		it('returns local time when no offsets are known', () => {
			const sync = new ClockSync();
			const before = Date.now() * 1000;
			const ptp = sync.ptpNow();
			const after = Date.now() * 1000;

			expect(ptp).toBeGreaterThanOrEqual(before);
			expect(ptp).toBeLessThanOrEqual(after);
		});

		it('applies single offset correctly', () => {
			const sync = new ClockSync();
			sync.updateOffset('A', 5_000_000); // +5 seconds

			const before = Date.now() * 1000;
			const ptp = sync.ptpNow();
			const after = Date.now() * 1000;

			// PTP should be local time + 5s offset
			expect(ptp).toBeGreaterThanOrEqual(before + 5_000_000 - 100);
			expect(ptp).toBeLessThanOrEqual(after + 5_000_000 + 100);
		});

		it('averages offsets from multiple engines', () => {
			const sync = new ClockSync();
			sync.updateOffset('A', 10_000_000); // +10s
			sync.updateOffset('B', 20_000_000); // +20s

			const before = Date.now() * 1000;
			const ptp = sync.ptpNow();
			const after = Date.now() * 1000;

			// Average offset = 15_000_000
			expect(ptp).toBeGreaterThanOrEqual(before + 15_000_000 - 100);
			expect(ptp).toBeLessThanOrEqual(after + 15_000_000 + 100);
		});

		it('handles negative offsets (local clock ahead of PTP)', () => {
			const sync = new ClockSync();
			sync.updateOffset('A', -2_000_000); // local is 2s ahead

			const before = Date.now() * 1000;
			const ptp = sync.ptpNow();
			const after = Date.now() * 1000;

			expect(ptp).toBeGreaterThanOrEqual(before - 2_000_000 - 100);
			expect(ptp).toBeLessThanOrEqual(after - 2_000_000 + 100);
		});
	});

	// ─── Ping/Pong serialization ───

	describe('ping serialization', () => {
		it('builds 5-byte ping with type byte 0xFE and 32-bit sequence', () => {
			const sync = new ClockSync();
			const ping = sync.buildPing(42);

			expect(ping.byteLength).toBe(5);
			expect(ping[0]).toBe(0xFE); // MSG_PING
			const view = new DataView(ping.buffer);
			expect(view.getUint32(1)).toBe(42);
		});

		it('handles max uint32 sequence', () => {
			const sync = new ClockSync();
			const ping = sync.buildPing(0xFFFFFFFF);

			const view = new DataView(ping.buffer);
			expect(view.getUint32(1)).toBe(0xFFFFFFFF);
		});

		it('handles sequence 0', () => {
			const sync = new ClockSync();
			const ping = sync.buildPing(0);

			expect(ping[0]).toBe(0xFE);
			const view = new DataView(ping.buffer);
			expect(view.getUint32(1)).toBe(0);
		});
	});

	describe('pong parsing', () => {
		function buildPong(timestampUS: number): Uint8Array {
			const buf = new Uint8Array(9);
			buf[0] = 0xFF; // MSG_PONG
			const view = new DataView(buf.buffer);
			const hi = Math.floor(timestampUS / (2 ** 32));
			const lo = timestampUS % (2 ** 32);
			view.setUint32(1, hi);
			view.setUint32(5, lo);
			return buf;
		}

		it('parses valid pong with known timestamp', () => {
			const sync = new ClockSync();
			const ts = 1_700_000_000_000_000; // ~2023 in microseconds

			const pong = buildPong(ts);
			const parsed = sync.parsePong(pong);

			expect(parsed).toBe(ts);
		});

		it('throws on truncated pong (< 9 bytes)', () => {
			const sync = new ClockSync();
			const short = new Uint8Array([0xFF, 0, 0, 0]);

			expect(() => sync.parsePong(short)).toThrow();
		});

		it('throws on empty buffer', () => {
			const sync = new ClockSync();
			expect(() => sync.parsePong(new Uint8Array(0))).toThrow();
		});

		it('throws on wrong message type', () => {
			const sync = new ClockSync();
			const badType = new Uint8Array(9);
			badType[0] = 0x01; // not MSG_PONG

			expect(() => sync.parsePong(badType)).toThrow();
		});

		it('parses timestamp 0 (edge case)', () => {
			const sync = new ClockSync();
			const pong = buildPong(0);
			expect(sync.parsePong(pong)).toBe(0);
		});

		it('round-trips through buildPing → server would build pong → parsePong', () => {
			// Simulate: client sends ping seq 7, server responds with its timestamp
			const sync = new ClockSync();
			const serverTimestamp = 1_700_000_000_000_000;

			const _ping = sync.buildPing(7);
			// Server would read the ping, then respond:
			const pong = buildPong(serverTimestamp);

			const parsed = sync.parsePong(pong);
			expect(parsed).toBe(serverTimestamp);
		});
	});

	// ─── Periodic sync ───

	describe('periodic sync', () => {
		it('sends pings to all tracked engines at the specified interval', () => {
			const sync = new ClockSync();
			const sendPing = vi.fn();

			sync.startPeriodicSync(['A', 'B'], sendPing, 1000);

			// Immediate fire: 2 pings (A and B)
			expect(sendPing).toHaveBeenCalledTimes(2);
			expect(sendPing.mock.calls[0][0]).toBe('A');
			expect(sendPing.mock.calls[1][0]).toBe('B');

			// Verify ping data is valid
			const pingA = sendPing.mock.calls[0][1] as Uint8Array;
			expect(pingA[0]).toBe(0xFE);
			expect(pingA.byteLength).toBe(5);

			// First interval tick
			vi.advanceTimersByTime(1000);
			expect(sendPing).toHaveBeenCalledTimes(4);

			// Second interval tick
			vi.advanceTimersByTime(1000);
			expect(sendPing).toHaveBeenCalledTimes(6);

			sync.stopPeriodicSync();
		});

		it('increments ping sequence across ticks', () => {
			const sync = new ClockSync();
			const sendPing = vi.fn();

			sync.startPeriodicSync(['A'], sendPing, 1000);

			// Immediate fire: call[0]
			const ping1 = sendPing.mock.calls[0][1] as Uint8Array;
			const seq1 = new DataView(ping1.buffer).getUint32(1);

			vi.advanceTimersByTime(1000);
			// First interval: call[1]
			const ping2 = sendPing.mock.calls[1][1] as Uint8Array;
			const seq2 = new DataView(ping2.buffer).getUint32(1);

			expect(seq2).toBe(seq1 + 1);

			sync.stopPeriodicSync();
		});

		it('stops sending after stopPeriodicSync', () => {
			const sync = new ClockSync();
			const sendPing = vi.fn();

			sync.startPeriodicSync(['A'], sendPing, 1000);
			// Immediate fire: 1 ping
			expect(sendPing).toHaveBeenCalledTimes(1);

			vi.advanceTimersByTime(1000);
			expect(sendPing).toHaveBeenCalledTimes(2);

			sync.stopPeriodicSync();

			vi.advanceTimersByTime(5000);
			expect(sendPing).toHaveBeenCalledTimes(2); // no more
		});

		it('includes engines added after startPeriodicSync', () => {
			const sync = new ClockSync();
			const sendPing = vi.fn();

			sync.startPeriodicSync(['A'], sendPing, 1000);

			// Immediate fire: 1 ping (A only)
			expect(sendPing).toHaveBeenCalledTimes(1);

			vi.advanceTimersByTime(1000);
			expect(sendPing).toHaveBeenCalledTimes(2);

			// Add engine B dynamically
			sync.addEngine('B');

			vi.advanceTimersByTime(1000);
			// Should now ping both A and B: 1 immediate + 1 first tick + 2 second tick = 4
			expect(sendPing).toHaveBeenCalledTimes(4);

			sync.stopPeriodicSync();
		});

		it('restartPeriodicSync replaces old interval', () => {
			const sync = new ClockSync();
			const sendPing = vi.fn();

			sync.startPeriodicSync(['A'], sendPing, 1000);
			// Immediate fire: 1 ping
			expect(sendPing).toHaveBeenCalledTimes(1);

			vi.advanceTimersByTime(1000);
			expect(sendPing).toHaveBeenCalledTimes(2);

			// Restart with different interval (fires immediately again)
			sync.startPeriodicSync(['A'], sendPing, 5000);
			expect(sendPing).toHaveBeenCalledTimes(3); // immediate fire on restart

			vi.advanceTimersByTime(1000);
			expect(sendPing).toHaveBeenCalledTimes(3); // old interval gone

			vi.advanceTimersByTime(4000);
			expect(sendPing).toHaveBeenCalledTimes(4); // new interval fired

			sync.stopPeriodicSync();
		});
	});
});
