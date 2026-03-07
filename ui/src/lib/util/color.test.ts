import { describe, it, expect } from 'vitest';
import { rgbToYCbCr, ycbcrToHex, KEY_PRESETS } from './color';

describe('rgbToYCbCr', () => {
	it('converts pure green to expected YCbCr', () => {
		const { y, cb, cr } = rgbToYCbCr(0, 255, 0);
		// BT.709 limited range: green → Y≈173, Cb≈42, Cr≈26
		expect(y).toBeGreaterThan(165);
		expect(y).toBeLessThan(180);
		expect(cb).toBeGreaterThan(35);
		expect(cb).toBeLessThan(50);
		expect(cr).toBeGreaterThan(18);
		expect(cr).toBeLessThan(35);
	});

	it('converts pure blue to expected YCbCr', () => {
		const { y, cb, cr } = rgbToYCbCr(0, 0, 255);
		// BT.709 limited range: blue → Y≈32, Cb≈240, Cr≈118
		expect(y).toBeGreaterThan(25);
		expect(y).toBeLessThan(40);
		expect(cb).toBeGreaterThan(230);
		expect(cb).toBeLessThan(250);
	});

	it('converts black to Y=16, Cb=128, Cr=128 (limited range)', () => {
		const { y, cb, cr } = rgbToYCbCr(0, 0, 0);
		expect(y).toBe(16);
		expect(cb).toBe(128);
		expect(cr).toBe(128);
	});

	it('converts white to Y=235, Cb=128, Cr=128 (limited range)', () => {
		const { y, cb, cr } = rgbToYCbCr(255, 255, 255);
		expect(y).toBe(235);
		expect(cb).toBe(128);
		expect(cr).toBe(128);
	});

	it('clamps output to 0-255', () => {
		const { y, cb, cr } = rgbToYCbCr(255, 0, 0);
		expect(y).toBeGreaterThanOrEqual(0);
		expect(y).toBeLessThanOrEqual(255);
		expect(cb).toBeGreaterThanOrEqual(0);
		expect(cb).toBeLessThanOrEqual(255);
		expect(cr).toBeGreaterThanOrEqual(0);
		expect(cr).toBeLessThanOrEqual(255);
	});
});

describe('ycbcrToHex', () => {
	it('converts green screen preset back to approximate hex', () => {
		const hex = ycbcrToHex(173, 42, 26);
		// Should be a greenish hex
		expect(hex).toMatch(/^#[0-9a-f]{6}$/);
	});
});

describe('KEY_PRESETS', () => {
	it('has green and blue presets', () => {
		expect(KEY_PRESETS).toHaveLength(2);
		expect(KEY_PRESETS[0].label).toBe('Green Screen');
		expect(KEY_PRESETS[1].label).toBe('Blue Screen');
	});
});
