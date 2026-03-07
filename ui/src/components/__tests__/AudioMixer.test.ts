import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { updatePeakHold, updateClip, isClipActive, CLIP_THRESHOLD_DB, CLIP_DISPLAY_MS } from '$lib/audio/peak-hold';

describe('peak hold logic', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('captures new peak', () => {
		vi.setSystemTime(1000);
		let hold = { L: -96, R: -96, timeL: 0, timeR: 0 };
		hold = updatePeakHold(hold, -6, -12, Date.now());
		expect(hold.L).toBe(-6);
		expect(hold.R).toBe(-12);
	});

	it('holds peak for 2 seconds when lower value arrives', () => {
		vi.setSystemTime(1000);
		let hold = { L: -96, R: -96, timeL: 0, timeR: 0 };
		hold = updatePeakHold(hold, -6, -96, Date.now());

		// 1 second later — lower peak should not replace
		vi.setSystemTime(2000);
		hold = updatePeakHold(hold, -12, -96, Date.now());
		expect(hold.L).toBe(-6);
	});

	it('decays after 2 seconds', () => {
		vi.setSystemTime(1000);
		let hold = { L: -96, R: -96, timeL: 0, timeR: 0 };
		hold = updatePeakHold(hold, -6, -96, Date.now());

		// 2.1 seconds later — hold decays to current level
		vi.setSystemTime(3100);
		hold = updatePeakHold(hold, -12, -96, Date.now());
		expect(hold.L).toBe(-12);
	});

	it('updates peak when higher value arrives', () => {
		vi.setSystemTime(1000);
		let hold = { L: -96, R: -96, timeL: 0, timeR: 0 };
		hold = updatePeakHold(hold, -12, -96, Date.now());

		vi.setSystemTime(1500);
		hold = updatePeakHold(hold, -3, -96, Date.now());
		expect(hold.L).toBe(-3);
	});

	it('tracks L and R channels independently', () => {
		vi.setSystemTime(1000);
		let hold = { L: -96, R: -96, timeL: 0, timeR: 0 };
		hold = updatePeakHold(hold, -6, -20, Date.now());
		expect(hold.L).toBe(-6);
		expect(hold.R).toBe(-20);

		vi.setSystemTime(1500);
		hold = updatePeakHold(hold, -18, -3, Date.now());
		expect(hold.L).toBe(-6); // held
		expect(hold.R).toBe(-3); // updated (higher)
	});
});

describe('clip detection', () => {
	it('detects clip above CLIP_THRESHOLD_DB', () => {
		const now = Date.now();
		const clip = updateClip({ L: 0, R: 0 }, -0.5, -96, now);
		expect(clip.L).toBe(now);
		expect(clip.R).toBe(0);
	});

	it('does not clip at CLIP_THRESHOLD_DB or below', () => {
		const now = Date.now();
		const clip = updateClip({ L: 0, R: 0 }, CLIP_THRESHOLD_DB, -96, now);
		expect(clip.L).toBe(0);
	});

	it('clip expires after CLIP_DISPLAY_MS', () => {
		vi.useFakeTimers();
		vi.setSystemTime(1000);
		const clip = { L: 1000, R: 0 };

		// At 3999ms — still active
		expect(isClipActive(clip, 'L', 3999)).toBe(true);

		// At 4001ms — expired
		expect(isClipActive(clip, 'L', 4001)).toBe(false);

		vi.useRealTimers();
	});
});
