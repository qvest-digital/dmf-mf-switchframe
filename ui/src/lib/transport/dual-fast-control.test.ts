import { describe, it, expect, vi } from 'vitest';
import type { FastControl } from './fast-control';
import { createDualFastControl } from './dual-fast-control';

function createMockFastControl(): FastControl {
	return {
		sendTransitionPosition: vi.fn(),
		sendGraphicsLayerPosition: vi.fn(),
		sendDVESlotTransform: vi.fn(),
		sendDVESingleParam: vi.fn(),
		sendDVESlotSource: vi.fn(),
		sendDVESlotEnable: vi.fn(),
		sendDVEScaleMode: vi.fn(),
		sendDVEBorder: vi.fn(),
		writeDatagram: vi.fn(),
		close: vi.fn(),
	};
}

describe('createDualFastControl', () => {
	it('sends to all controls (2 mocks)', () => {
		const a = createMockFastControl();
		const b = createMockFastControl();
		const dual = createDualFastControl([a, b]);

		dual.sendTransitionPosition(0.5);
		expect(a.sendTransitionPosition).toHaveBeenCalledWith(0.5);
		expect(b.sendTransitionPosition).toHaveBeenCalledWith(0.5);

		dual.sendDVESlotTransform(1, 0.1, 0.2, 0.5, 0.5, 45, 0.8);
		expect(a.sendDVESlotTransform).toHaveBeenCalledWith(1, 0.1, 0.2, 0.5, 0.5, 45, 0.8);
		expect(b.sendDVESlotTransform).toHaveBeenCalledWith(1, 0.1, 0.2, 0.5, 0.5, 45, 0.8);

		dual.sendDVESingleParam(2, 0x0004, 90);
		expect(a.sendDVESingleParam).toHaveBeenCalledWith(2, 0x0004, 90);
		expect(b.sendDVESingleParam).toHaveBeenCalledWith(2, 0x0004, 90);
	});

	it('works with single control (1 mock)', () => {
		const a = createMockFastControl();
		const dual = createDualFastControl([a]);

		dual.sendTransitionPosition(0.75);
		expect(a.sendTransitionPosition).toHaveBeenCalledWith(0.75);

		dual.sendGraphicsLayerPosition(3, 100, 200, 400, 300);
		expect(a.sendGraphicsLayerPosition).toHaveBeenCalledWith(3, 100, 200, 400, 300);
	});

	it('works with empty array (0 mocks, no error)', () => {
		const dual = createDualFastControl([]);

		expect(() => dual.sendTransitionPosition(0.5)).not.toThrow();
		expect(() => dual.sendDVESlotTransform(0, 0, 0, 1, 1, 0, 1)).not.toThrow();
		expect(() => dual.sendDVESingleParam(0, 0, 0)).not.toThrow();
		expect(() => dual.sendGraphicsLayerPosition(0, 0, 0, 0, 0)).not.toThrow();
		expect(() => dual.sendDVESlotSource(0, 'cam1')).not.toThrow();
		expect(() => dual.sendDVESlotEnable(0, true)).not.toThrow();
		expect(() => dual.sendDVEScaleMode(0, 1, 0.5, 0.5)).not.toThrow();
		expect(() => dual.sendDVEBorder(0, 2, 4, 128, 128, 128)).not.toThrow();
		expect(() => dual.close()).not.toThrow();
	});

	it('close() calls close on all', () => {
		const a = createMockFastControl();
		const b = createMockFastControl();
		const dual = createDualFastControl([a, b]);

		dual.close();
		expect(a.close).toHaveBeenCalledOnce();
		expect(b.close).toHaveBeenCalledOnce();
	});

	it('fans out all remaining message types', () => {
		const a = createMockFastControl();
		const b = createMockFastControl();
		const dual = createDualFastControl([a, b]);

		dual.sendGraphicsLayerPosition(1, 10, 20, 300, 200);
		expect(a.sendGraphicsLayerPosition).toHaveBeenCalledWith(1, 10, 20, 300, 200);
		expect(b.sendGraphicsLayerPosition).toHaveBeenCalledWith(1, 10, 20, 300, 200);

		dual.sendDVESlotSource(0, 'cam2');
		expect(a.sendDVESlotSource).toHaveBeenCalledWith(0, 'cam2');
		expect(b.sendDVESlotSource).toHaveBeenCalledWith(0, 'cam2');

		dual.sendDVESlotEnable(1, false);
		expect(a.sendDVESlotEnable).toHaveBeenCalledWith(1, false);
		expect(b.sendDVESlotEnable).toHaveBeenCalledWith(1, false);

		dual.sendDVEScaleMode(2, 1, 0.5, 0.5);
		expect(a.sendDVEScaleMode).toHaveBeenCalledWith(2, 1, 0.5, 0.5);
		expect(b.sendDVEScaleMode).toHaveBeenCalledWith(2, 1, 0.5, 0.5);

		dual.sendDVEBorder(3, 2.5, 8, 235, 128, 128);
		expect(a.sendDVEBorder).toHaveBeenCalledWith(3, 2.5, 8, 235, 128, 128);
		expect(b.sendDVEBorder).toHaveBeenCalledWith(3, 2.5, 8, 235, 128, 128);
	});

	it('forwards writeDatagram to all controls', () => {
		const a = createMockFastControl();
		const b = createMockFastControl();
		const dual = createDualFastControl([a, b]);

		const data = new Uint8Array([0x01, 0x02, 0x03]);
		dual.writeDatagram(data);
		expect(a.writeDatagram).toHaveBeenCalledWith(data);
		expect(b.writeDatagram).toHaveBeenCalledWith(data);
	});

	it('continues sending to B even when A throws', () => {
		const a = createMockFastControl();
		const b = createMockFastControl();
		(a.sendTransitionPosition as ReturnType<typeof vi.fn>).mockImplementation(() => {
			throw new Error('A is dead');
		});
		const dual = createDualFastControl([a, b]);

		// safeCall catches per-engine errors so B still receives the datagram
		expect(() => dual.sendTransitionPosition(0.5)).not.toThrow();
		expect(a.sendTransitionPosition).toHaveBeenCalledWith(0.5);
		expect(b.sendTransitionPosition).toHaveBeenCalledWith(0.5);
	});

	it('isolates errors across all message types', () => {
		const a = createMockFastControl();
		const b = createMockFastControl();
		// Make every method on A throw
		for (const key of Object.keys(a) as (keyof FastControl)[]) {
			(a[key] as ReturnType<typeof vi.fn>).mockImplementation(() => {
				throw new Error('A is dead');
			});
		}
		const dual = createDualFastControl([a, b]);

		expect(() => dual.sendTransitionPosition(0.5)).not.toThrow();
		expect(b.sendTransitionPosition).toHaveBeenCalledWith(0.5);

		expect(() => dual.sendGraphicsLayerPosition(1, 10, 20, 300, 200)).not.toThrow();
		expect(b.sendGraphicsLayerPosition).toHaveBeenCalledWith(1, 10, 20, 300, 200);

		expect(() => dual.sendDVESlotTransform(1, 0.1, 0.2, 0.5, 0.5, 45, 0.8)).not.toThrow();
		expect(b.sendDVESlotTransform).toHaveBeenCalledWith(1, 0.1, 0.2, 0.5, 0.5, 45, 0.8);

		expect(() => dual.sendDVESingleParam(2, 4, 90)).not.toThrow();
		expect(b.sendDVESingleParam).toHaveBeenCalledWith(2, 4, 90);

		expect(() => dual.sendDVESlotSource(0, 'cam1')).not.toThrow();
		expect(b.sendDVESlotSource).toHaveBeenCalledWith(0, 'cam1');

		expect(() => dual.sendDVESlotEnable(1, true)).not.toThrow();
		expect(b.sendDVESlotEnable).toHaveBeenCalledWith(1, true);

		expect(() => dual.sendDVEScaleMode(0, 1, 0.5, 0.5)).not.toThrow();
		expect(b.sendDVEScaleMode).toHaveBeenCalledWith(0, 1, 0.5, 0.5);

		expect(() => dual.sendDVEBorder(0, 2, 4, 128, 128, 128)).not.toThrow();
		expect(b.sendDVEBorder).toHaveBeenCalledWith(0, 2, 4, 128, 128, 128);

		expect(() => dual.close()).not.toThrow();
		expect(b.close).toHaveBeenCalled();
	});

	it('fans out commitTransitionPosition to all controls', () => {
		const a = createMockFastControl();
		const b = createMockFastControl();
		a.commitTransitionPosition = vi.fn();
		b.commitTransitionPosition = vi.fn();
		const dual = createDualFastControl([a, b]);

		dual.commitTransitionPosition!(0.75);
		expect(a.commitTransitionPosition).toHaveBeenCalledWith(0.75);
		expect(b.commitTransitionPosition).toHaveBeenCalledWith(0.75);
	});

	it('fans out commitDVESlotTransform to all controls', () => {
		const a = createMockFastControl();
		const b = createMockFastControl();
		a.commitDVESlotTransform = vi.fn();
		b.commitDVESlotTransform = vi.fn();
		const dual = createDualFastControl([a, b]);

		dual.commitDVESlotTransform!(1, 0.1, 0.2, 0.5, 0.5, 45, 0.8);
		expect(a.commitDVESlotTransform).toHaveBeenCalledWith(1, 0.1, 0.2, 0.5, 0.5, 45, 0.8);
		expect(b.commitDVESlotTransform).toHaveBeenCalledWith(1, 0.1, 0.2, 0.5, 0.5, 45, 0.8);
	});

	it('commit continues to B even when A throws', () => {
		const a = createMockFastControl();
		const b = createMockFastControl();
		a.commitTransitionPosition = vi.fn().mockImplementation(() => {
			throw new Error('A is dead');
		});
		b.commitTransitionPosition = vi.fn();
		const dual = createDualFastControl([a, b]);

		expect(() => dual.commitTransitionPosition!(0.5)).not.toThrow();
		expect(a.commitTransitionPosition).toHaveBeenCalledWith(0.5);
		expect(b.commitTransitionPosition).toHaveBeenCalledWith(0.5);
	});

	it('commitDVESlotTransform continues to B even when A throws', () => {
		const a = createMockFastControl();
		const b = createMockFastControl();
		a.commitDVESlotTransform = vi.fn().mockImplementation(() => {
			throw new Error('A is dead');
		});
		b.commitDVESlotTransform = vi.fn();
		const dual = createDualFastControl([a, b]);

		expect(() => dual.commitDVESlotTransform!(0, 0, 0, 1, 1, 0, 1)).not.toThrow();
		expect(a.commitDVESlotTransform).toHaveBeenCalledWith(0, 0, 0, 1, 1, 0, 1);
		expect(b.commitDVESlotTransform).toHaveBeenCalledWith(0, 0, 0, 1, 1, 0, 1);
	});

	it('commit works when controls have no commit methods (optional)', () => {
		const a = createMockFastControl();
		const b = createMockFastControl();
		// Neither a nor b have commit methods — should not throw
		const dual = createDualFastControl([a, b]);

		expect(() => dual.commitTransitionPosition!(0.5)).not.toThrow();
		expect(() => dual.commitDVESlotTransform!(0, 0, 0, 1, 1, 0, 1)).not.toThrow();
	});
});
