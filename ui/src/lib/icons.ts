/**
 * Shared inline SVG icon paths for the Switchframe UI.
 * Each constant is a d-attribute path string for a 16x16 viewBox.
 * Render with: <svg viewBox="0 0 16 16" width={size} height={size}><path d={ICON} fill="currentColor"/></svg>
 *
 * Stroke-based icons use fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round".
 */

// ── Transport ──
export const ICON_PLAY = 'M4 2.5l9 5.5-9 5.5z';
export const ICON_PAUSE = 'M4 2h3v12H4zM9 2h3v12H9z';
export const ICON_STOP = 'M3 3h10v10H3z';
export const ICON_FRAME_BACK = 'M9 3l-5 5 5 5zM5 3H3v10h2z';
export const ICON_FRAME_FORWARD = 'M7 3l5 5-5 5zM11 3h2v10h-2z';
export const ICON_JUMP_START = 'M10.5 3l-5 5 5 5zM5.5 3H3.5v10h2z';
export const ICON_JUMP_END = 'M5.5 3l5 5-5 5zM10.5 3h2v10h-2z';
export const ICON_EJECT = 'M3 11h10v2H3zM8 3l5 6H3z';
export const ICON_RECORD = 'M8 3a5 5 0 1 0 0 10A5 5 0 0 0 8 3z';
export const ICON_LOOP = 'M13 5.5A5 5 0 0 0 3.5 7M3 10.5A5 5 0 0 0 12.5 9';
export const ICON_LOOP_ARROWS = 'M13.5 5L12 7l-1.5-2M2.5 11L4 9l1.5 2';

// ── CRUD / Actions ──
export const ICON_PLUS = 'M8 3v10M3 8h10';
export const ICON_TRASH = 'M5 4V3a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v1M3 4h10M5 4v9a1 1 0 0 0 1 1h4a1 1 0 0 0 1-1V4M7 7v4M9 7v4';
export const ICON_PENCIL = 'M11.5 2.5l2 2L6 12H4v-2zM10 4l2 2';
export const ICON_COPY = 'M5 3h6a1 1 0 0 1 1 1v6M3 6h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1z';
export const ICON_CHECK = 'M3 8.5l3 3 7-7';
export const ICON_CLOSE = 'M4 4l8 8M12 4l-8 8';
export const ICON_PIN = 'M8 1v5M5 6l1 4h4l1-4zM8 10v5';
export const ICON_SAVE = 'M3 2h8l2 2v8a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1zM6 2v3h4V2M5 9h6v4H5z';

// ── Navigation / Collapse ──
export const ICON_CHEVRON_DOWN = 'M4 6l4 4 4-4';
export const ICON_CHEVRON_RIGHT = 'M6 4l4 4-4 4';
export const ICON_CHEVRON_UP = 'M4 10l4-4 4 4';
export const ICON_CHEVRON_LEFT = 'M10 4l-4 4 4 4';
export const ICON_MENU = 'M2 4h12M2 8h12M2 12h12';

// ── Drag / Reorder ──
export const ICON_GRIP = 'M6 4a1 1 0 1 1 0-2 1 1 0 0 1 0 2zM10 4a1 1 0 1 1 0-2 1 1 0 0 1 0 2zM6 9a1 1 0 1 1 0-2 1 1 0 0 1 0 2zM10 9a1 1 0 1 1 0-2 1 1 0 0 1 0 2zM6 14a1 1 0 1 1 0-2 1 1 0 0 1 0 2zM10 14a1 1 0 1 1 0-2 1 1 0 0 1 0 2z';

// ── Upload / Cloud ──
export const ICON_UPLOAD = 'M8 10V3M5 5l3-3 3 3M3 12h10';
export const ICON_CLOUD_UPLOAD = 'M5 12a3 3 0 0 1-.5-5.96 4.5 4.5 0 0 1 8.77-1.04A3 3 0 0 1 13 11h-1M8 14V8M6 10l2-2 2 2';

// ── Status / Indicators ──
export const ICON_CIRCLE = 'M8 3a5 5 0 1 0 0 10A5 5 0 0 0 8 3z';
export const ICON_EYE = 'M1 8s2.5-5 7-5 7 5 7 5-2.5 5-7 5-7-5-7-5zM8 10a2 2 0 1 0 0-4 2 2 0 0 0 0 4z';
export const ICON_EYE_OFF = 'M2 2l12 12M6.5 6.5a2 2 0 0 0 3 3M1 8s2.5-5 7-5c1.2 0 2.3.3 3.2.8M15 8s-1.2 2.4-3.2 3.8';
export const ICON_LOCK = 'M4 7h8v6H4zM6 7V5a2 2 0 0 1 4 0v2';
export const ICON_WARNING = 'M8 1L1 14h14zM8 6v4M8 11.5v1';
export const ICON_INFO = 'M8 3a5 5 0 1 0 0 10A5 5 0 0 0 8 3zM8 7v4M8 5.5v.5';
export const ICON_HELP = 'M8 3a5 5 0 1 0 0 10A5 5 0 0 0 8 3zM6.5 6a1.5 1.5 0 0 1 3 0c0 1-1.5 1-1.5 2M8 11v.5';

// ── Media / Content ──
export const ICON_IMAGE = 'M2 3h12v10H2zM2 10l3-3 2 2 3-3 4 4M6 6.5a1 1 0 1 1 0-2 1 1 0 0 1 0 2z';
export const ICON_TEXT = 'M3 3h10v2H3zM8 5v8M5 13h6';
export const ICON_TICKER = 'M2 6h12M2 10h12M5 6v4M11 6v4';
export const ICON_CODE = 'M5 4L1 8l4 4M11 4l4 4-4 4M9 2l-2 12';
export const ICON_FILM = 'M2 3h12v10H2zM2 6h12M2 10h12M5 3v10M11 3v10';
export const ICON_CLOCK = 'M8 3a5 5 0 1 0 0 10A5 5 0 0 0 8 3zM8 5v3l2 2';
export const ICON_CAPTION = 'M2 3h12v10H2zM4 8h3M4 10h5';

// ── Layout / Composition ──
export const ICON_LAYERS = 'M8 2l6 3-6 3-6-3zM2 8l6 3 6-3M2 11l6 3 6-3';
export const ICON_GRID = 'M2 2h5v5H2zM9 2h5v5H9zM2 9h5v5H2zM9 9h5v5H9z';
export const ICON_CROP = 'M5 1v10h10M1 5h10v10';
export const ICON_KEY = 'M10 2a4 4 0 0 0-3.7 5.5L2 12v2h2l4.5-4.3A4 4 0 1 0 10 2z';

// ── Audio specific ──
export const ICON_MIC = 'M8 1a2 2 0 0 0-2 2v4a2 2 0 0 0 4 0V3a2 2 0 0 0-2-2zM4 7a4 4 0 0 0 8 0M8 11v3M6 14h4';
export const ICON_SPEAKER = 'M3 6h2l4-3v10L5 10H3zM11 5.5a3 3 0 0 1 0 5';

// ── Broadcast / SCTE ──
export const ICON_BROADCAST = 'M4 12A6 6 0 0 1 4 4M12 4a6 6 0 0 1 0 8M6 10a3 3 0 0 1 0-4M10 6a3 3 0 0 1 0 4M8 8h.01';
export const ICON_AD_BREAK = 'M2 3h12v10H2zM5 6h1v4H5M7 6h2l1 2-1 2H7z';
export const ICON_RETURN = 'M12 5l-4 4 4 4M8 9H2';

// ── Team / Operators ──
export const ICON_USER = 'M8 2a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM3 14a5 5 0 0 1 10 0';
export const ICON_USERS = 'M6 7a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zM1 14a5 5 0 0 1 10 0M11 3a2.5 2.5 0 1 1 0 5M13 14a5 5 0 0 0-3-4.5';
export const ICON_LINK = 'M7 9l2-2M6 12l-2 2a2 2 0 0 1-3-3l2-2M10 4l2-2a2 2 0 0 1 3 3l-2 2';

// ── Presets / Macros ──
export const ICON_PRESET = 'M3 2h4v4H3zM9 2h4v4H9zM3 8h4v4H3zM10 9l1.5 2.5L14 9';
export const ICON_BOLT = 'M9 1L4 9h4l-1 6 5-8H8z';

// ── Misc ──
export const ICON_REFRESH = 'M13 3l-1 4-4-1M3 13l1-4 4 1M12.5 6.5A5.5 5.5 0 0 0 4 4M3.5 9.5A5.5 5.5 0 0 0 12 12';

// ── Rendering helper ──
export type IconStyle = 'fill' | 'stroke';

/**
 * Render an inline SVG icon as an HTML string for use in {@html} blocks.
 * @param path SVG path d-attribute
 * @param size Icon size in px (default 14)
 * @param style 'fill' for solid icons, 'stroke' for line icons (default 'stroke')
 */
export function renderIcon(path: string, size = 14, style: IconStyle = 'stroke'): string {
	const attrs = style === 'fill'
		? 'fill="currentColor"'
		: 'fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"';
	return `<svg viewBox="0 0 16 16" width="${size}" height="${size}" ${attrs}><path d="${path}"/></svg>`;
}
