import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { updatePeakHold, CLIP_THRESHOLD_DB, CLIP_DISPLAY_MS } from '$lib/audio/peak-hold';

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
		const clip = { L: 0, R: 0 };
		const peakLDb = -0.5;
		if (peakLDb > CLIP_THRESHOLD_DB) clip.L = now;
		expect(clip.L).toBe(now);
	});

	it('does not clip at CLIP_THRESHOLD_DB or below', () => {
		const clip = { L: 0, R: 0 };
		const peakLDb = CLIP_THRESHOLD_DB;
		if (peakLDb > CLIP_THRESHOLD_DB) clip.L = Date.now();
		expect(clip.L).toBe(0);
	});

	it('clip expires after CLIP_DISPLAY_MS', () => {
		vi.useFakeTimers();
		vi.setSystemTime(1000);
		const clip = { L: 1000, R: 0 };

		// At 3999ms — clip.L was set at 1000, so 3999-1000=2999 < CLIP_DISPLAY_MS → still active
		vi.setSystemTime(3999);
		expect(Date.now() - clip.L < CLIP_DISPLAY_MS).toBe(true);

		// At 4001ms — clip.L was set at 1000, so 4001-1000=3001 >= CLIP_DISPLAY_MS → expired
		vi.setSystemTime(4001);
		expect(Date.now() - clip.L < CLIP_DISPLAY_MS).toBe(false);

		vi.useRealTimers();
	});
});
