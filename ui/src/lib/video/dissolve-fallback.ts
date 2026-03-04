/**
 * Renders a dissolve transition using Canvas 2D globalAlpha compositing.
 * Used as fallback when WebGPU is unavailable.
 *
 * @param ctx - The 2D rendering context of the preview/output canvas
 * @param canvasA - Source A canvas (outgoing). May be an HTMLCanvasElement or OffscreenCanvas.
 * @param canvasB - Source B canvas (incoming). Null for FTB mode.
 * @param mixFactor - Blend position: 0.0 = all A, 1.0 = all B
 */
export function renderDissolveFallback(
	ctx: CanvasRenderingContext2D,
	canvasA: HTMLCanvasElement | OffscreenCanvas | null,
	canvasB: HTMLCanvasElement | OffscreenCanvas | null,
	mixFactor: number,
): void {
	const { width, height } = ctx.canvas;

	// Draw source A (dimmed by inverse of mixFactor for FTB, full for mix)
	if (canvasA) {
		ctx.globalAlpha = canvasB ? 1.0 : (1.0 - mixFactor);
		ctx.drawImage(canvasA as CanvasImageSource, 0, 0, width, height);
	}

	// Draw source B on top with mixFactor alpha
	if (canvasB) {
		ctx.globalAlpha = mixFactor;
		ctx.drawImage(canvasB as CanvasImageSource, 0, 0, width, height);
	}

	// Reset alpha
	ctx.globalAlpha = 1.0;
}

/**
 * Renders a dip-to-black transition using Canvas 2D.
 *
 * @param ctx - The 2D rendering context
 * @param canvasA - Source A canvas (outgoing)
 * @param canvasB - Source B canvas (incoming)
 * @param position - 0.0->0.5: A fades to black. 0.5->1.0: black fades to B
 */
export function renderDipFallback(
	ctx: CanvasRenderingContext2D,
	canvasA: HTMLCanvasElement | OffscreenCanvas | null,
	canvasB: HTMLCanvasElement | OffscreenCanvas | null,
	position: number,
): void {
	const { width, height } = ctx.canvas;

	// Clear to black
	ctx.fillStyle = 'black';
	ctx.globalAlpha = 1.0;
	ctx.fillRect(0, 0, width, height);

	if (position < 0.5) {
		// Phase 1: A fading to black
		if (canvasA) {
			ctx.globalAlpha = 1.0 - 2.0 * position;
			ctx.drawImage(canvasA as CanvasImageSource, 0, 0, width, height);
		}
	} else {
		// Phase 2: black fading to B
		if (canvasB) {
			ctx.globalAlpha = 2.0 * position - 1.0;
			ctx.drawImage(canvasB as CanvasImageSource, 0, 0, width, height);
		}
	}

	ctx.globalAlpha = 1.0;
}
