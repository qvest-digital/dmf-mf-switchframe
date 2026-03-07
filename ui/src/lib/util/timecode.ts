/**
 * Format a Unix-ms timestamp as local HH:MM:SS.mmm for replay timecode display.
 */
export function formatTimecode(unixMs: number | undefined): string {
	if (!unixMs) return '';
	const d = new Date(unixMs);
	const hh = d.getHours().toString().padStart(2, '0');
	const mm = d.getMinutes().toString().padStart(2, '0');
	const ss = d.getSeconds().toString().padStart(2, '0');
	const ms = d.getMilliseconds().toString().padStart(3, '0');
	return `${hh}:${mm}:${ss}.${ms}`;
}

/**
 * Format a duration in milliseconds as M:SS.s for clip duration display.
 */
export function formatClipDuration(durationMs: number): string {
	if (durationMs <= 0) return '';
	const totalSecs = durationMs / 1000;
	const m = Math.floor(totalSecs / 60);
	const s = Math.floor(totalSecs % 60);
	const tenths = Math.floor((totalSecs * 10) % 10);
	return `${m}:${s.toString().padStart(2, '0')}.${tenths}`;
}
