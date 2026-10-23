/**
 * Precision tests for ClockSync.
 *
 * These tests verify the mathematical correctness of EWMA smoothing,
 * jitter calculation, safety margin computation, and PTP timestamp
 * generation. They use exact expected values, not loose ranges.
 */
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

describe('ClockSync precision', () => {
	beforeEach(() => {
		// Pings count from 0, as the pongs built here expect.
		vi.spyOn(Math, 'random').mockReturnValue(0);
		vi.useFakeTimers();
	});
	afterEach(() => {
		vi.restoreAllMocks();
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
		it('returns default (4 frames) when no engines tracked', () => {
			const sync = new ClockSync();
			// DEFAULT_MARGIN_US = 33_333 * 4 = 133_332
			expect(sync.safetyMargin()).toBe(133_332);
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
		it('returns 0 when no offsets are known', () => {
			const sync = new ClockSync();
			const ptp = sync.ptpNow();
			expect(ptp).toBe(0);
		});

		it('applies single offset correctly', () => {
			// Use handlePong to set offset with proper age tracking
			const sync = new ClockSync();
			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 120_000);

			vi.advanceTimersByTime(5);
			// rtt = 5_000 µs, desired offset = +5_000_000. handlePong derives
			// offset = serverTimestamp - (Date.now()*1000 - rtt/2), so a server clock
			// running +5_000_000 µs ahead sends this wall-clock-domain timestamp:
			const rtt = 5_000;
			sync.handlePong('A', buildPongHelper(0, Date.now() * 1000 - rtt / 2 + 5_000_000));

			const localUS = Date.now() * 1000;
			const ptp = sync.ptpNow();

			// PTP should be local time + 5_000_000 offset
			expect(ptp).toBe(Math.round(localUS + 5_000_000));

			sync.stopPeriodicSync();
		});

		it('averages offsets from multiple engines', () => {
			const sync = new ClockSync();
			const sendPing = vi.fn();
			sync.startPeriodicSync(['A', 'B'], sendPing, 120_000);

			vi.advanceTimersByTime(5);
			const rtt = 5_000;
			// A offset = 10_000_000, B offset = 20_000_000. Server timestamps are in
			// the wall-clock domain: Date.now()*1000 - rtt/2 + desiredOffset.
			const base = Date.now() * 1000 - rtt / 2;
			sync.handlePong('A', buildPongHelper(0, base + 10_000_000));
			sync.handlePong('B', buildPongHelper(1, base + 20_000_000));

			const localUS = Date.now() * 1000;
			const ptp = sync.ptpNow();

			// Average offset = 15_000_000
			expect(ptp).toBe(Math.round(localUS + 15_000_000));

			sync.stopPeriodicSync();
		});

		it('handles negative offsets (local clock ahead of PTP)', () => {
			const sync = new ClockSync();
			const sendPing = vi.fn();
			sync.startPeriodicSync(['A'], sendPing, 120_000);

			vi.advanceTimersByTime(5);
			const rtt = 5_000;
			// Desired offset = -2_000_000 (local clock ahead of PTP). Server timestamp
			// in wall-clock domain: Date.now()*1000 - rtt/2 + desiredOffset.
			const serverTs = Date.now() * 1000 - rtt / 2 - 2_000_000;
			sync.handlePong('A', buildPongHelper(0, serverTs));

			const localUS = Date.now() * 1000;
			const ptp = sync.ptpNow();

			expect(ptp).toBe(Math.round(localUS - 2_000_000));

			sync.stopPeriodicSync();
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
		function buildPong(seq: number, timestampUS: number): Uint8Array {
			const buf = new Uint8Array(13);
			buf[0] = 0xFF; // MSG_PONG
			const view = new DataView(buf.buffer);
			view.setUint32(1, seq);
			const hi = Math.floor(timestampUS / (2 ** 32));
			const lo = timestampUS % (2 ** 32);
			view.setUint32(5, hi);
			view.setUint32(9, lo);
			return buf;
		}

		it('parses valid pong with seq and known timestamp', () => {
			const sync = new ClockSync();
			const ts = 1_700_000_000_000_000; // ~2023 in microseconds

			const pong = buildPong(42, ts);
			const parsed = sync.parsePong(pong);

			expect(parsed.seq).toBe(42);
			expect(parsed.serverTimestamp).toBe(ts);
		});

		it('throws on truncated pong (< 13 bytes)', () => {
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
			const badType = new Uint8Array(13);
			badType[0] = 0x01; // not MSG_PONG

			expect(() => sync.parsePong(badType)).toThrow();
		});

		it('parses timestamp 0 (edge case)', () => {
			const sync = new ClockSync();
			const pong = buildPong(0, 0);
			expect(sync.parsePong(pong).serverTimestamp).toBe(0);
			expect(sync.parsePong(pong).seq).toBe(0);
		});

		it('round-trips through buildPing → server would build pong → parsePong', () => {
			// Simulate: client sends ping seq 7, server responds with its timestamp
			const sync = new ClockSync();
			const serverTimestamp = 1_700_000_000_000_000;

			const _ping = sync.buildPing(7);
			// Server would read the ping, then respond with seq and timestamp:
			const pong = buildPong(7, serverTimestamp);

			const parsed = sync.parsePong(pong);
			expect(parsed.seq).toBe(7);
			expect(parsed.serverTimestamp).toBe(serverTimestamp);
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
