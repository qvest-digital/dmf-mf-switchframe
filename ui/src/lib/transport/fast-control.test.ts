import { describe, it, expect, vi } from 'vitest';
import { createFastControl, encodeTransitionPosition, encodeDVESlotTransform, type FastControlOptions, type WriteErrorCallback } from './fast-control';

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

	it('prepends 8-byte timestamp when ptpNow returns a valid value', () => {
		const writeFn = vi.fn().mockResolvedValue(undefined);
		const mockWriter = { write: writeFn, releaseLock: vi.fn() };
		const mockTransport = {
			datagrams: {
				writable: { getWriter: () => mockWriter },
			},
		} as unknown as WebTransport;

		const fc = createFastControl(mockTransport, { ptpNow: () => 1_700_000_000_000_000 });
		fc.sendTransitionPosition(0.5);

		expect(writeFn).toHaveBeenCalledTimes(1);
		const sent = writeFn.mock.calls[0][0] as Uint8Array;
		// 8 bytes timestamp prefix + 5 bytes transition position
		expect(sent.byteLength).toBe(13);
		// The payload starts at offset 8
		expect(sent[8]).toBe(0x02);
	});

	it('does NOT prepend timestamp when ptpNow returns 0 (no clock sync)', () => {
		const writeFn = vi.fn().mockResolvedValue(undefined);
		const mockWriter = { write: writeFn, releaseLock: vi.fn() };
		const mockTransport = {
			datagrams: {
				writable: { getWriter: () => mockWriter },
			},
		} as unknown as WebTransport;

		const fc = createFastControl(mockTransport, { ptpNow: () => 0 });
		fc.sendTransitionPosition(0.5);

		expect(writeFn).toHaveBeenCalledTimes(1);
		const sent = writeFn.mock.calls[0][0] as Uint8Array;
		// No timestamp prefix — same length as without ptpNow
		expect(sent.byteLength).toBe(5);
		expect(sent[0]).toBe(0x02);
	});

	it('does NOT prepend timestamp when ptpNow is not provided', () => {
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

	it('calls onWriteError callback on write failure', async () => {
		const onWriteError = vi.fn();
		const writeFn = vi.fn().mockRejectedValue(new Error('transport closed'));
		const mockWriter = { write: writeFn, releaseLock: vi.fn() };
		const mockTransport = {
			datagrams: {
				writable: { getWriter: () => mockWriter },
			},
		} as unknown as WebTransport;

		const fc = createFastControl(mockTransport, { onWriteError });
		fc.sendTransitionPosition(0.5);

		// Let the microtask queue flush so .catch() fires
		await vi.waitFor(() => {
			expect(onWriteError).toHaveBeenCalledWith('transition_position');
		});
	});

	it('calls onWriteError with correct message type for each method', async () => {
		const onWriteError = vi.fn();
		const writeFn = vi.fn().mockRejectedValue(new Error('transport closed'));
		const mockWriter = { write: writeFn, releaseLock: vi.fn() };
		const mockTransport = {
			datagrams: {
				writable: { getWriter: () => mockWriter },
			},
		} as unknown as WebTransport;

		const fc = createFastControl(mockTransport, { onWriteError });

		fc.sendTransitionPosition(0.5);
		fc.sendGraphicsLayerPosition(1, 10, 20, 100, 50);
		fc.sendDVESlotTransform(0, 0, 0, 1, 1, 0, 1);
		fc.sendDVESingleParam(0, 0x0002, 480);
		fc.sendDVESlotSource(0, 'cam1');
		fc.sendDVESlotEnable(0, true);
		fc.sendDVEScaleMode(0, 1, 0.5, 0.5);
		fc.sendDVEBorder(0, 2, 4, 128, 128, 128);

		await vi.waitFor(() => {
			expect(onWriteError).toHaveBeenCalledTimes(8);
		});

		const types = onWriteError.mock.calls.map((c: unknown[]) => c[0]);
		expect(types).toEqual([
			'transition_position',
			'graphics_layer_position',
			'dve_slot_transform',
			'dve_single_param',
			'dve_slot_source',
			'dve_slot_enable',
			'dve_scale_mode',
			'dve_border',
		]);
	});

	it('does not throw when no onWriteError callback is provided', async () => {
		const writeFn = vi.fn().mockRejectedValue(new Error('transport closed'));
		const mockWriter = { write: writeFn, releaseLock: vi.fn() };
		const mockTransport = {
			datagrams: {
				writable: { getWriter: () => mockWriter },
			},
		} as unknown as WebTransport;

		const fc = createFastControl(mockTransport);
		fc.sendTransitionPosition(0.5);

		// Flush microtasks — should not throw
		await new Promise((r) => setTimeout(r, 10));
	});

	it('prepends 8-byte PTP timestamp when ptpNow option is set', async () => {
		const writeFn = vi.fn().mockResolvedValue(undefined);
		const mockWriter = { write: writeFn, releaseLock: vi.fn() };
		const mockTransport = {
			datagrams: {
				writable: { getWriter: () => mockWriter },
			},
		} as unknown as WebTransport;

		const ptpTs = 1234567890;
		const fc = createFastControl(mockTransport, { ptpNow: () => ptpTs });
		fc.sendTransitionPosition(0.5);

		expect(writeFn).toHaveBeenCalledTimes(1);
		const sent = writeFn.mock.calls[0][0] as Uint8Array;
		// 8 bytes timestamp + 5 bytes transition position payload = 13 bytes
		expect(sent.byteLength).toBe(13);
		const view = new DataView(sent.buffer, sent.byteOffset);
		const hi = view.getUint32(0);
		const lo = view.getUint32(4);
		expect(hi * 0x100000000 + lo).toBe(ptpTs);
		// Payload byte after timestamp should still be MSG_TRANSITION_POSITION (0x02)
		expect(sent[8]).toBe(0x02);
	});

	it('does not prepend timestamp when ptpNow is not set', async () => {
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
		// No timestamp: exactly 5 bytes
		expect(sent.byteLength).toBe(5);
		expect(sent[0]).toBe(0x02);
	});
});
