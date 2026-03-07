/**
 * BT.709 RGB→YCbCr conversion (limited range 16-235/16-240).
 * Matches the server-side YUV420 domain used by the keyer.
 */
export function rgbToYCbCr(r: number, g: number, b: number): { y: number; cb: number; cr: number } {
	const rn = r / 255;
	const gn = g / 255;
	const bn = b / 255;

	const y = 16 + 219 * (0.2126 * rn + 0.7152 * gn + 0.0722 * bn);
	const cb = 128 + 224 * 0.5 * ((bn - (0.2126 * rn + 0.7152 * gn + 0.0722 * bn)) / (1 - 0.0722));
	const cr = 128 + 224 * 0.5 * ((rn - (0.2126 * rn + 0.7152 * gn + 0.0722 * bn)) / (1 - 0.2126));

	return {
		y: Math.round(Math.min(255, Math.max(0, y))),
		cb: Math.round(Math.min(255, Math.max(0, cb))),
		cr: Math.round(Math.min(255, Math.max(0, cr))),
	};
}

/**
 * Approximate YCbCr→RGB→hex for display swatch.
 * Inverse of rgbToYCbCr (BT.709, limited range).
 */
export function ycbcrToHex(y: number, cb: number, cr: number): string {
	const yn = (y - 16) / 219;
	const cbn = (cb - 128) / (224 * 0.5);
	const crn = (cr - 128) / (224 * 0.5);

	const r = Math.round(Math.min(255, Math.max(0, (yn + crn * (1 - 0.2126)) * 255)));
	const g = Math.round(
		Math.min(
			255,
			Math.max(0, (yn - crn * (0.2126 * (1 - 0.2126)) / 0.7152 - cbn * (0.0722 * (1 - 0.0722)) / 0.7152) * 255),
		),
	);
	const b = Math.round(Math.min(255, Math.max(0, (yn + cbn * (1 - 0.0722)) * 255)));

	return `#${r.toString(16).padStart(2, '0')}${g.toString(16).padStart(2, '0')}${b.toString(16).padStart(2, '0')}`;
}

/**
 * Parse a hex color string to RGB components.
 */
export function hexToRgb(hex: string): { r: number; g: number; b: number } {
	const h = hex.replace('#', '');
	return {
		r: parseInt(h.substring(0, 2), 16),
		g: parseInt(h.substring(2, 4), 16),
		b: parseInt(h.substring(4, 6), 16),
	};
}

export interface KeyPreset {
	label: string;
	y: number;
	cb: number;
	cr: number;
}

export const KEY_PRESETS: KeyPreset[] = [
	{ label: 'Green Screen', y: 173, cb: 42, cr: 26 },
	{ label: 'Blue Screen', y: 32, cb: 240, cr: 118 },
];
