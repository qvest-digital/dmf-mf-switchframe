import { describe, it, expect, vi } from 'vitest';
import { renderDissolveFallback, renderDipFallback } from './dissolve-fallback';

// Mock canvas context
function createMockContext(): CanvasRenderingContext2D {
	const drawCalls: Array<{ alpha: number }> = [];
	return {
		globalAlpha: 1.0,
		canvas: { width: 640, height: 360 },
		drawImage: vi.fn(function(this: any) {
			drawCalls.push({ alpha: this.globalAlpha });
		}),
		fillStyle: '',
		fillRect: vi.fn(),
		_drawCalls: drawCalls,
	} as unknown as CanvasRenderingContext2D;
}

describe('renderDissolveFallback', () => {
	it('should draw source A at full alpha when mixFactor is 0', () => {
		const ctx = createMockContext();
		const canvasA = {} as HTMLCanvasElement;
		const canvasB = {} as HTMLCanvasElement;

		renderDissolveFallback(ctx, canvasA, canvasB, 0.0);

		expect(ctx.drawImage).toHaveBeenCalledTimes(2);
	});

	it('should draw source B at full alpha when mixFactor is 1', () => {
		const ctx = createMockContext();
		const canvasA = {} as HTMLCanvasElement;
		const canvasB = {} as HTMLCanvasElement;

		renderDissolveFallback(ctx, canvasA, canvasB, 1.0);

		expect(ctx.drawImage).toHaveBeenCalledTimes(2);
	});

	it('should draw both sources at mixFactor 0.5', () => {
		const ctx = createMockContext();
		const canvasA = {} as HTMLCanvasElement;
		const canvasB = {} as HTMLCanvasElement;

		renderDissolveFallback(ctx, canvasA, canvasB, 0.5);

		expect(ctx.drawImage).toHaveBeenCalledTimes(2);
	});

	it('should reset globalAlpha to 1.0 after rendering', () => {
		const ctx = createMockContext();
		const canvasA = {} as HTMLCanvasElement;
		const canvasB = {} as HTMLCanvasElement;

		renderDissolveFallback(ctx, canvasA, canvasB, 0.7);

		expect(ctx.globalAlpha).toBe(1.0);
	});

	it('should handle missing canvasB (FTB mode)', () => {
		const ctx = createMockContext();
		const canvasA = {} as HTMLCanvasElement;

		renderDissolveFallback(ctx, canvasA, null, 0.5);

		// Should draw A dimmed, no B
		expect(ctx.drawImage).toHaveBeenCalledTimes(1);
		expect(ctx.globalAlpha).toBe(1.0);
	});
});

describe('renderDipFallback', () => {
	it('should draw A fading out in first half', () => {
		const ctx = createMockContext();
		const canvasA = {} as HTMLCanvasElement;
		const canvasB = {} as HTMLCanvasElement;

		renderDipFallback(ctx, canvasA, canvasB, 0.25);

		// Should fill black + draw A
		expect(ctx.fillRect).toHaveBeenCalled();
		expect(ctx.drawImage).toHaveBeenCalledTimes(1);
	});

	it('should draw B fading in in second half', () => {
		const ctx = createMockContext();
		const canvasA = {} as HTMLCanvasElement;
		const canvasB = {} as HTMLCanvasElement;

		renderDipFallback(ctx, canvasA, canvasB, 0.75);

		expect(ctx.fillRect).toHaveBeenCalled();
		expect(ctx.drawImage).toHaveBeenCalledTimes(1);
	});

	it('should reset globalAlpha after rendering', () => {
		const ctx = createMockContext();
		renderDipFallback(ctx, {} as HTMLCanvasElement, {} as HTMLCanvasElement, 0.5);
		expect(ctx.globalAlpha).toBe(1.0);
	});
});
