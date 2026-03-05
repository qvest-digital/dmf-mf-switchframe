import { describe, it, expect, vi, afterEach } from 'vitest';
import { setupHiDPICanvas } from './canvas-utils';

describe('setupHiDPICanvas', () => {
	afterEach(() => {
		vi.restoreAllMocks();
	});

	it('sets canvas backing resolution to display * dpr', () => {
		vi.stubGlobal('devicePixelRatio', 2);
		const canvas = document.createElement('canvas');
		setupHiDPICanvas(canvas, 640, 360);
		expect(canvas.width).toBe(1280);
		expect(canvas.height).toBe(720);
	});

	it('sets CSS display size', () => {
		vi.stubGlobal('devicePixelRatio', 2);
		const canvas = document.createElement('canvas');
		setupHiDPICanvas(canvas, 640, 360);
		expect(canvas.style.width).toBe('640px');
		expect(canvas.style.height).toBe('360px');
	});

	it('defaults dpr to 1 when devicePixelRatio undefined', () => {
		vi.stubGlobal('devicePixelRatio', undefined);
		const canvas = document.createElement('canvas');
		setupHiDPICanvas(canvas, 320, 180);
		expect(canvas.width).toBe(320);
		expect(canvas.height).toBe(180);
	});

	it('rounds dimensions to avoid subpixel', () => {
		vi.stubGlobal('devicePixelRatio', 1.5);
		const canvas = document.createElement('canvas');
		setupHiDPICanvas(canvas, 321, 181);
		expect(canvas.width).toBe(Math.round(321 * 1.5)); // 482
		expect(canvas.height).toBe(Math.round(181 * 1.5)); // 272
	});
});
