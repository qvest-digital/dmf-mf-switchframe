import { describe, it, expect } from 'vitest';
import { createDissolveRenderer } from './dissolve';

describe('DissolveRenderer', () => {
	it('should create renderer with fallback when WebGPU unavailable', () => {
		// jsdom has no WebGPU, so it should fall back to Canvas 2D
		const canvas = document.createElement('canvas');
		canvas.width = 640;
		canvas.height = 360;

		const renderer = createDissolveRenderer(canvas);
		expect(renderer).toBeDefined();
		expect(renderer.mode).toBe('canvas2d');
	});

	it('should expose setMixFactor method', () => {
		const canvas = document.createElement('canvas');
		const renderer = createDissolveRenderer(canvas);

		renderer.setMixFactor(0.5);
		expect(renderer.mixFactor).toBe(0.5);
	});

	it('should clamp mixFactor to 0-1 range', () => {
		const canvas = document.createElement('canvas');
		const renderer = createDissolveRenderer(canvas);

		renderer.setMixFactor(-0.5);
		expect(renderer.mixFactor).toBe(0);

		renderer.setMixFactor(1.5);
		expect(renderer.mixFactor).toBe(1);
	});

	it('should expose destroy method', () => {
		const canvas = document.createElement('canvas');
		const renderer = createDissolveRenderer(canvas);

		renderer.destroy();
		// Should not throw
	});

	it('should track transition type', () => {
		const canvas = document.createElement('canvas');
		const renderer = createDissolveRenderer(canvas);

		renderer.setTransitionType('dip');
		expect(renderer.transitionType).toBe('dip');
	});
});
