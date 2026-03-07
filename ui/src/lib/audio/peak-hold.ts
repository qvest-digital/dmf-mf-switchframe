export interface PeakHoldState {
	L: number;
	R: number;
	timeL: number;
	timeR: number;
}

export interface ClipState {
	L: number;
	R: number;
}

export const HOLD_DURATION_MS = 2000;
export const CLIP_THRESHOLD_DB = -1;
export const CLIP_DISPLAY_MS = 3000;

export function updatePeakHold(
	hold: PeakHoldState,
	peakLDb: number,
	peakRDb: number,
	now: number,
): PeakHoldState {
	const result = { ...hold };
	if (peakLDb > result.L || now - result.timeL > HOLD_DURATION_MS) {
		result.L = peakLDb;
		result.timeL = now;
	}
	if (peakRDb > result.R || now - result.timeR > HOLD_DURATION_MS) {
		result.R = peakRDb;
		result.timeR = now;
	}
	return result;
}

export function updateClip(
	clip: ClipState,
	peakLDb: number,
	peakRDb: number,
	now: number,
): ClipState {
	const result = { ...clip };
	if (peakLDb > CLIP_THRESHOLD_DB) result.L = now;
	if (peakRDb > CLIP_THRESHOLD_DB) result.R = now;
	return result;
}

export function isClipActive(clip: ClipState, channel: 'L' | 'R', now: number): boolean {
	return now - clip[channel] < CLIP_DISPLAY_MS;
}
