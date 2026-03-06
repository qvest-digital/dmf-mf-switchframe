import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

describe('peak hold logic', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	function updatePeakHold(
		hold: { L: number; R: number; timeL: number; timeR: number },
		peakLDb: number,
		peakRDb: number,
	) {
		const now = Date.now();
		if (peakLDb > hold.L || now - hold.timeL > 2000) {
			hold.L = peakLDb;
			hold.timeL = now;
		}
		if (peakRDb > hold.R || now - hold.timeR > 2000) {
			hold.R = peakRDb;
			hold.timeR = now;
		}
		return { ...hold };
	}

	it('captures new peak', () => {
		vi.setSystemTime(1000);
		let hold = { L: -96, R: -96, timeL: 0, timeR: 0 };
		hold = updatePeakHold(hold, -6, -12);
		expect(hold.L).toBe(-6);
		expect(hold.R).toBe(-12);
	});

	it('holds peak for 2 seconds when lower value arrives', () => {
		vi.setSystemTime(1000);
		let hold = { L: -96, R: -96, timeL: 0, timeR: 0 };
		hold = updatePeakHold(hold, -6, -96);

		// 1 second later — lower peak should not replace
		vi.setSystemTime(2000);
		hold = updatePeakHold(hold, -12, -96);
		expect(hold.L).toBe(-6);
	});

	it('decays after 2 seconds', () => {
		vi.setSystemTime(1000);
		let hold = { L: -96, R: -96, timeL: 0, timeR: 0 };
		hold = updatePeakHold(hold, -6, -96);

		// 2.1 seconds later — hold decays to current level
		vi.setSystemTime(3100);
		hold = updatePeakHold(hold, -12, -96);
		expect(hold.L).toBe(-12);
	});

	it('updates peak when higher value arrives', () => {
		vi.setSystemTime(1000);
		let hold = { L: -96, R: -96, timeL: 0, timeR: 0 };
		hold = updatePeakHold(hold, -12, -96);

		vi.setSystemTime(1500);
		hold = updatePeakHold(hold, -3, -96);
		expect(hold.L).toBe(-3);
	});

	it('tracks L and R channels independently', () => {
		vi.setSystemTime(1000);
		let hold = { L: -96, R: -96, timeL: 0, timeR: 0 };
		hold = updatePeakHold(hold, -6, -20);
		expect(hold.L).toBe(-6);
		expect(hold.R).toBe(-20);

		vi.setSystemTime(1500);
		hold = updatePeakHold(hold, -18, -3);
		expect(hold.L).toBe(-6); // held
		expect(hold.R).toBe(-3); // updated (higher)
	});
});

describe('clip detection', () => {
	it('detects clip above -1 dBFS', () => {
		const now = Date.now();
		const clip = { L: 0, R: 0 };
		const peakLDb = -0.5;
		if (peakLDb > -1) clip.L = now;
		expect(clip.L).toBe(now);
	});

	it('does not clip at -1 dBFS or below', () => {
		const clip = { L: 0, R: 0 };
		const peakLDb = -1;
		if (peakLDb > -1) clip.L = Date.now();
		expect(clip.L).toBe(0);
	});

	it('clip expires after 3 seconds', () => {
		vi.useFakeTimers();
		vi.setSystemTime(1000);
		const clip = { L: 1000, R: 0 };

		// At 3999ms — clip.L was set at 1000, so 3999-1000=2999 < 3000 → still active
		vi.setSystemTime(3999);
		expect(Date.now() - clip.L < 3000).toBe(true);

		// At 4001ms — clip.L was set at 1000, so 4001-1000=3001 >= 3000 → expired
		vi.setSystemTime(4001);
		expect(Date.now() - clip.L < 3000).toBe(false);

		vi.useRealTimers();
	});
});
