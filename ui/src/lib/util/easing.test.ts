import { describe, it, expect } from 'vitest';
import { smoothstep, cubicBezier, getEasingFunction, EASING_PRESETS } from './easing';

describe('smoothstep', () => {
	it('returns 0 at t=0', () => {
		expect(smoothstep(0)).toBe(0);
	});

	it('returns 1 at t=1', () => {
		expect(smoothstep(1)).toBe(1);
	});

	it('returns 0.5 at t=0.5', () => {
		expect(smoothstep(0.5)).toBe(0.5);
	});

	it('returns ~0.15625 at t=0.25', () => {
		expect(smoothstep(0.25)).toBeCloseTo(0.15625, 5);
	});

	it('is symmetric: smoothstep(t) + smoothstep(1-t) = 1', () => {
		for (let i = 0; i <= 50; i++) {
			const t = i / 100;
			expect(smoothstep(t) + smoothstep(1 - t)).toBeCloseTo(1, 9);
		}
	});
});

describe('cubicBezier', () => {
	it('ease-in-out at t=0.5 returns ~0.5', () => {
		const fn = cubicBezier(0.42, 0, 0.58, 1.0);
		expect(fn(0.5)).toBeCloseTo(0.5, 1);
	});

	it('ease-in at t=0.5 returns ~0.315', () => {
		const fn = cubicBezier(0.42, 0, 1.0, 1.0);
		expect(fn(0.5)).toBeCloseTo(0.315, 1);
	});

	it('returns 0 at t=0 for all curves', () => {
		const fn = cubicBezier(0.25, 0.1, 0.25, 1.0);
		expect(fn(0)).toBe(0);
	});

	it('returns 1 at t=1 for all curves', () => {
		const fn = cubicBezier(0.25, 0.1, 0.25, 1.0);
		expect(fn(1)).toBe(1);
	});

	it('handles linear-like control points', () => {
		// cubic-bezier(0, 0, 1, 1) should approximate linear
		const fn = cubicBezier(0, 0, 1, 1);
		expect(fn(0.5)).toBeCloseTo(0.5, 1);
		expect(fn(0.25)).toBeCloseTo(0.25, 1);
		expect(fn(0.75)).toBeCloseTo(0.75, 1);
	});
});

describe('getEasingFunction', () => {
	it('returns identity for linear', () => {
		const fn = getEasingFunction('linear');
		expect(fn(0)).toBe(0);
		expect(fn(0.5)).toBe(0.5);
		expect(fn(1)).toBe(1);
	});

	it('returns smoothstep for smoothstep preset', () => {
		const fn = getEasingFunction('smoothstep');
		expect(fn(0.25)).toBeCloseTo(0.15625, 5);
		expect(fn(0.5)).toBeCloseTo(0.5, 5);
	});

	it('returns a function for custom control points', () => {
		const fn = getEasingFunction('custom', 0.42, 0, 0.58, 1);
		expect(typeof fn).toBe('function');
		expect(fn(0)).toBe(0);
		expect(fn(1)).toBe(1);
	});

	it('returns a working function for named presets', () => {
		const fn = getEasingFunction('ease');
		expect(fn(0)).toBe(0);
		expect(fn(1)).toBe(1);
		// ease should be faster than linear at t=0.25
		expect(fn(0.25)).toBeGreaterThan(0.25);
	});

	it('falls back to smoothstep for unknown preset', () => {
		const fn = getEasingFunction('nonexistent' as any);
		expect(fn(0.25)).toBeCloseTo(0.15625, 5);
	});
});

describe('EASING_PRESETS', () => {
	it('has all required preset keys', () => {
		const expectedKeys = ['linear', 'ease', 'ease-in', 'ease-out', 'ease-in-out', 'smoothstep'];
		for (const key of expectedKeys) {
			expect(EASING_PRESETS).toHaveProperty(key);
		}
	});

	it('each preset has x1, y1, x2, y2 fields', () => {
		for (const [name, preset] of Object.entries(EASING_PRESETS)) {
			expect(preset).toHaveProperty('x1');
			expect(preset).toHaveProperty('y1');
			expect(preset).toHaveProperty('x2');
			expect(preset).toHaveProperty('y2');
			expect(typeof preset.x1).toBe('number');
			expect(typeof preset.y1).toBe('number');
			expect(typeof preset.x2).toBe('number');
			expect(typeof preset.y2).toBe('number');
		}
	});

	it('endpoint invariants: all preset functions return 0 at t=0 and 1 at t=1', () => {
		for (const [name, preset] of Object.entries(EASING_PRESETS)) {
			if (name === 'smoothstep') {
				// smoothstep uses the inline formula, not cubicBezier
				expect(smoothstep(0)).toBe(0);
				expect(smoothstep(1)).toBe(1);
			} else {
				const fn = cubicBezier(preset.x1, preset.y1, preset.x2, preset.y2);
				expect(fn(0)).toBe(0);
				expect(fn(1)).toBe(1);
			}
		}
	});
});
