import { describe, it, expect, vi } from 'vitest';
import { createFastControl, encodeSlotPosition, encodeTransitionPosition } from './fast-control';

describe('encodeSlotPosition', () => {
	it('encodes slot position correctly', () => {
		const buf = encodeSlotPosition(2, 100, 200, 480, 270);
		expect(buf.byteLength).toBe(10);
		const view = new DataView(buf.buffer, buf.byteOffset);
		expect(view.getUint8(0)).toBe(0x01);
		expect(view.getUint8(1)).toBe(2);
		expect(view.getUint16(2)).toBe(100);
		expect(view.getUint16(4)).toBe(200);
		expect(view.getUint16(6)).toBe(480);
		expect(view.getUint16(8)).toBe(270);
	});
});

describe('encodeTransitionPosition', () => {
	it('encodes transition position correctly', () => {
		const buf = encodeTransitionPosition(0.75);
		expect(buf.byteLength).toBe(5);
		const view = new DataView(buf.buffer, buf.byteOffset);
		expect(view.getUint8(0)).toBe(0x02);
		expect(Math.abs(view.getFloat32(1) - 0.75)).toBeLessThan(1e-6);
	});
});

describe('createFastControl', () => {
	it('sends slot position datagram', () => {
		const writeFn = vi.fn().mockResolvedValue(undefined);
		const mockWriter = { write: writeFn, releaseLock: vi.fn() };
		const mockTransport = {
			datagrams: {
				writable: { getWriter: () => mockWriter },
			},
		} as unknown as WebTransport;

		const fc = createFastControl(mockTransport);
		fc.sendSlotPosition(0, 100, 200, 480, 270);

		expect(writeFn).toHaveBeenCalledTimes(1);
		const sent = writeFn.mock.calls[0][0] as Uint8Array;
		expect(sent.byteLength).toBe(10);
		expect(sent[0]).toBe(0x01);
	});

	it('sends transition position datagram', () => {
		const writeFn = vi.fn().mockResolvedValue(undefined);
		const mockWriter = { write: writeFn, releaseLock: vi.fn() };
		const mockTransport = {
			datagrams: {
				writable: { getWriter: () => mockWriter },
			},
		} as unknown as WebTransport;

		const fc = createFastControl(mockTransport);
		fc.sendTransitionPosition(0.5);

		expect(writeFn).toHaveBeenCalledTimes(1);
		const sent = writeFn.mock.calls[0][0] as Uint8Array;
		expect(sent.byteLength).toBe(5);
		expect(sent[0]).toBe(0x02);
	});

	it('close releases writer lock', () => {
		const releaseLock = vi.fn();
		const mockWriter = { write: vi.fn(), releaseLock };
		const mockTransport = {
			datagrams: {
				writable: { getWriter: () => mockWriter },
			},
		} as unknown as WebTransport;

		const fc = createFastControl(mockTransport);
		fc.close();
		expect(releaseLock).toHaveBeenCalled();
	});
});
