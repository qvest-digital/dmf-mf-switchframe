import { describe, it, expect } from 'vitest';

describe('T-bar position calculation', () => {
	function tbarPosition(clientY: number, rectTop: number, rectHeight: number): number {
		return Math.max(0, Math.min(1, (clientY - rectTop) / rectHeight));
	}

	it('maps Y coordinate to 0-1 range', () => {
		expect(tbarPosition(200, 100, 200)).toBe(0.5);
	});

	it('returns 0 at top of track', () => {
		expect(tbarPosition(100, 100, 200)).toBe(0);
	});

	it('returns 1 at bottom of track', () => {
		expect(tbarPosition(300, 100, 200)).toBe(1);
	});

	it('clamps below 0', () => {
		expect(tbarPosition(50, 100, 200)).toBe(0);
	});

	it('clamps above 1', () => {
		expect(tbarPosition(400, 100, 200)).toBe(1);
	});
});
