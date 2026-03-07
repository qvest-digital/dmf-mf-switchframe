import { describe, it, expect } from 'vitest';
import { tbarPosition, applyKeyStep } from '$lib/util/tbar';

describe('T-bar position calculation', () => {
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

describe('T-bar keyboard step calculation', () => {
	it('ArrowDown increments by 0.01', () => {
		expect(applyKeyStep(0.5, 'ArrowDown', false)).toBeCloseTo(0.51);
	});

	it('ArrowUp decrements by 0.01', () => {
		expect(applyKeyStep(0.5, 'ArrowUp', false)).toBeCloseTo(0.49);
	});

	it('Shift+ArrowDown increments by 0.1', () => {
		expect(applyKeyStep(0.5, 'ArrowDown', true)).toBeCloseTo(0.6);
	});

	it('Shift+ArrowUp decrements by 0.1', () => {
		expect(applyKeyStep(0.5, 'ArrowUp', true)).toBeCloseTo(0.4);
	});

	it('Home sets to 0', () => {
		expect(applyKeyStep(0.7, 'Home', false)).toBe(0);
	});

	it('End sets to 1', () => {
		expect(applyKeyStep(0.3, 'End', false)).toBe(1);
	});

	it('clamps at 0', () => {
		expect(applyKeyStep(0.005, 'ArrowUp', false)).toBe(0);
	});

	it('clamps at 1', () => {
		expect(applyKeyStep(0.995, 'ArrowDown', false)).toBe(1);
	});
});
