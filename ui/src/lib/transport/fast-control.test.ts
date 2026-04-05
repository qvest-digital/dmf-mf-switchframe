import { describe, it, expect, vi } from 'vitest';
import { createFastControl, encodeTransitionPosition, encodeDVESlotTransform } from './fast-control';

describe('encodeDVESlotTransform', () => {
	it('encodes DVE slot transform correctly', () => {
		const buf = encodeDVESlotTransform(2, 100, 200, 480, 270, 0, 1.0);
		expect(buf.byteLength).toBe(26);
		const view = new DataView(buf.buffer, buf.byteOffset);
		expect(view.getUint8(0)).toBe(0x04);
		expect(view.getUint8(1)).toBe(2);
		expect(Math.abs(view.getFloat32(2) - 100)).toBeLessThan(1e-6);
		expect(Math.abs(view.getFloat32(6) - 200)).toBeLessThan(1e-6);
		expect(Math.abs(view.getFloat32(10) - 480)).toBeLessThan(1e-6);
		expect(Math.abs(view.getFloat32(14) - 270)).toBeLessThan(1e-6);
		expect(Math.abs(view.getFloat32(18) - 0)).toBeLessThan(1e-6);
		expect(Math.abs(view.getFloat32(22) - 1.0)).toBeLessThan(1e-6);
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
	it('sends DVE slot transform datagram', () => {
		const writeFn = vi.fn().mockResolvedValue(undefined);
		const mockWriter = { write: writeFn, releaseLock: vi.fn() };
		const mockTransport = {
			datagrams: {
				writable: { getWriter: () => mockWriter },
			},
		} as unknown as WebTransport;

		const fc = createFastControl(mockTransport);
		fc.sendDVESlotTransform(0, 100, 200, 480, 270, 0, 1.0);

		expect(writeFn).toHaveBeenCalledTimes(1);
		const sent = writeFn.mock.calls[0][0] as Uint8Array;
		expect(sent.byteLength).toBe(26);
		expect(sent[0]).toBe(0x04);
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
