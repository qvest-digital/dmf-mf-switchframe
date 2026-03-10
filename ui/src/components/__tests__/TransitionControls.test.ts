import { describe, it, expect } from 'vitest';
import { scrubberPosition, applyKeyStep } from '$lib/util/tbar';

describe('Scrubber position calculation', () => {
	it('maps X coordinate to 0-1 range', () => {
		expect(scrubberPosition(200, 100, 200)).toBe(0.5);
	});

	it('returns 0 at left of track', () => {
		expect(scrubberPosition(100, 100, 200)).toBe(0);
	});

	it('returns 1 at right of track', () => {
		expect(scrubberPosition(300, 100, 200)).toBe(1);
	});

	it('clamps below 0', () => {
		expect(scrubberPosition(50, 100, 200)).toBe(0);
	});

	it('clamps above 1', () => {
		expect(scrubberPosition(400, 100, 200)).toBe(1);
	});
});

describe('Scrubber keyboard step calculation', () => {
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
