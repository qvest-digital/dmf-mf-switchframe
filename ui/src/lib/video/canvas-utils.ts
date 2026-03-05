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
	const w = Math.round(displayWidth * dpr);
	const h = Math.round(displayHeight * dpr);
	if (canvas.width === w && canvas.height === h) return;
	canvas.width = w;
	canvas.height = h;
	canvas.style.width = `${displayWidth}px`;
	canvas.style.height = `${displayHeight}px`;
}
