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
	// Always update CSS display size — the renderer may have overridden
	// canvas.width/height to match the video frame resolution, which can
	// coincidentally equal displayWidth*dpr (e.g. 1280x720 video on a 2x
	// Retina with a 640x360 container). Without this, the guard below
	// would skip the style update, leaving stale pixel dimensions after
	// a browser resize.
	canvas.style.width = `${displayWidth}px`;
	canvas.style.height = `${displayHeight}px`;
	if (canvas.width === w && canvas.height === h) return;
	canvas.width = w;
	canvas.height = h;
}
