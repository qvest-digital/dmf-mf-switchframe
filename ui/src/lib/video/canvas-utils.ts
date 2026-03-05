/**
 * Configure a canvas element for high-DPI (Retina) displays.
 *
 * Sets the backing store resolution to displaySize * devicePixelRatio
 * while keeping the CSS display size at the requested dimensions.
 * This ensures crisp rendering on 2x/3x screens without layout changes.
 */
export function setupHiDPICanvas(
	canvas: HTMLCanvasElement,
	displayWidth: number,
	displayHeight: number,
): void {
	const dpr = window.devicePixelRatio || 1;
	canvas.width = Math.round(displayWidth * dpr);
	canvas.height = Math.round(displayHeight * dpr);
	canvas.style.width = `${displayWidth}px`;
	canvas.style.height = `${displayHeight}px`;
}
