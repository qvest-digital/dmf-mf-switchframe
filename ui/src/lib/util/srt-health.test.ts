import { describe, it, expect } from 'vitest';
import {
	computeSRTHealth,
	computeOutputHealth,
	formatUptime,
	formatPacketRate,
	formatBitrate,
	formatBytes
} from './srt-health';

// Helper that creates a full SRTSourceInfo with all required fields
function makeSRT(overrides = {}) {
	return {
		mode: 'caller' as const,
		streamID: 'live/cam1',
		latencyMs: 120,
		negotiatedLatencyMs: 120,
		rttMs: 20,
		rttVarMs: 2,
		lossRate: 0,
		bitrateKbps: 4200,
		recvBufMs: 50,
		recvBufPackets: 15,
		flightSize: 8,
		connected: true,
		uptimeMs: 60000,
		packetsReceived: 100000,
		packetsLost: 0,
		packetsDropped: 0,
		packetsRetransmitted: 0,
		packetsBelated: 0,
		...overrides,
	};
}

describe('computeSRTHealth', () => {
	it('returns green for healthy connection', () =>
		expect(computeSRTHealth(makeSRT())).toBe('green'));
	it('returns gray when disconnected', () =>
		expect(computeSRTHealth(makeSRT({ connected: false }))).toBe('gray'));
	it('returns red for high loss', () =>
		expect(computeSRTHealth(makeSRT({ lossRate: 1.5 }))).toBe('red'));
	it('returns red for high RTT', () =>
		expect(computeSRTHealth(makeSRT({ rttMs: 250 }))).toBe('red'));
	it('returns yellow for moderate loss', () =>
		expect(computeSRTHealth(makeSRT({ lossRate: 0.5 }))).toBe('yellow'));
	it('returns yellow for moderate RTT', () =>
		expect(computeSRTHealth(makeSRT({ rttMs: 150 }))).toBe('yellow'));
	it('returns yellow for low buffer with real latency', () =>
		expect(computeSRTHealth(makeSRT({ recvBufMs: 10 }))).toBe('yellow'));
	it('returns green for low buffer with zero latency (local/demo)', () =>
		expect(computeSRTHealth(makeSRT({ recvBufMs: 0, latencyMs: 0 }))).toBe('green'));
	it('returns undefined for non-SRT source', () =>
		expect(computeSRTHealth(undefined)).toBeUndefined());
});

describe('computeOutputHealth', () => {
	it('returns undefined for empty destinations', () =>
		expect(computeOutputHealth([])).toBeUndefined());
	it('returns undefined for undefined', () =>
		expect(computeOutputHealth(undefined)).toBeUndefined());
	it('returns green for healthy active destination', () =>
		expect(computeOutputHealth([{ state: 'active' }])).toBe('green'));
	it('returns red for error state', () =>
		expect(computeOutputHealth([{ state: 'error' }])).toBe('red'));
	it('returns red for overflow', () =>
		expect(computeOutputHealth([{ state: 'active', overflowCount: 5 }])).toBe('red'));
	it('returns yellow for dropped packets', () =>
		expect(computeOutputHealth([{ state: 'active', droppedPackets: 3 }])).toBe('yellow'));
	it('returns undefined when no active destinations', () =>
		expect(computeOutputHealth([{ state: 'stopped' }])).toBeUndefined());
});

describe('formatUptime', () => {
	it('formats seconds', () => expect(formatUptime(45000)).toBe('45s'));
	it('formats minutes', () => expect(formatUptime(125000)).toBe('2m 5s'));
	it('formats hours', () => expect(formatUptime(7325000)).toBe('2h 2m'));
});

describe('formatPacketRate', () => {
	it('formats with rate suffix', () =>
		expect(formatPacketRate(1500, 2.5)).toBe('1,500 (+2.5/s)'));
	it('formats zero rate without suffix', () =>
		expect(formatPacketRate(1500, 0)).toBe('1,500'));
});

describe('formatBitrate', () => {
	it('formats Mbps', () => expect(formatBitrate(4200)).toBe('4.2 Mbps'));
	it('formats kbps', () => expect(formatBitrate(500)).toBe('500 kbps'));
});

describe('formatBytes', () => {
	it('formats GB', () => expect(formatBytes(1_500_000_000)).toBe('1.5 GB'));
	it('formats MB', () => expect(formatBytes(2_500_000)).toBe('2.5 MB'));
	it('formats KB', () => expect(formatBytes(1_500)).toBe('1.5 KB'));
	it('formats bytes', () => expect(formatBytes(42)).toBe('42 B'));
});
