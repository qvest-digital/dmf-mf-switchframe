import { describe, it, expect } from 'vitest';
import { detectAudioConfigDrift, type AudioDriftResult } from './audio-drift';

function makeChannel(overrides: Partial<{ level: number; muted: boolean; afv: boolean; trim: number }> = {}) {
	return {
		level: 0,
		trim: 0,
		muted: false,
		afv: false,
		peakL: -20,
		peakR: -20,
		eq: [{}, {}, {}],
		compressor: {},
		gainReduction: 0,
		gate: {},
		...overrides,
	};
}

describe('detectAudioConfigDrift', () => {
	it('returns null when both engines have identical config', () => {
		const channelsA = { cam1: makeChannel({ level: -6, muted: false, afv: true, trim: 3 }) };
		const channelsB = { cam1: makeChannel({ level: -6, muted: false, afv: true, trim: 3 }) };
		expect(detectAudioConfigDrift(channelsA, channelsB)).toBeNull();
	});

	it('detects level mismatch', () => {
		const channelsA = { cam1: makeChannel({ level: -6 }) };
		const channelsB = { cam1: makeChannel({ level: -3 }) };
		const result = detectAudioConfigDrift(channelsA, channelsB);
		expect(result).toEqual({ channel: 'cam1', reason: 'config' });
	});

	it('detects muted mismatch', () => {
		const channelsA = { cam1: makeChannel({ muted: false }) };
		const channelsB = { cam1: makeChannel({ muted: true }) };
		const result = detectAudioConfigDrift(channelsA, channelsB);
		expect(result).toEqual({ channel: 'cam1', reason: 'config' });
	});

	it('detects afv mismatch', () => {
		const channelsA = { cam1: makeChannel({ afv: false }) };
		const channelsB = { cam1: makeChannel({ afv: true }) };
		const result = detectAudioConfigDrift(channelsA, channelsB);
		expect(result).toEqual({ channel: 'cam1', reason: 'config' });
	});

	it('detects trim mismatch', () => {
		const channelsA = { cam1: makeChannel({ trim: 0 }) };
		const channelsB = { cam1: makeChannel({ trim: 5 }) };
		const result = detectAudioConfigDrift(channelsA, channelsB);
		expect(result).toEqual({ channel: 'cam1', reason: 'config' });
	});

	it('does NOT flag drift when only peaks differ', () => {
		const chA = makeChannel({ level: -6, muted: false, afv: true, trim: 0 });
		chA.peakL = -20;
		chA.peakR = -18;
		const chB = makeChannel({ level: -6, muted: false, afv: true, trim: 0 });
		chB.peakL = -5;
		chB.peakR = -4;
		expect(detectAudioConfigDrift({ cam1: chA }, { cam1: chB })).toBeNull();
	});

	it('skips channels that only exist in one engine', () => {
		const channelsA = { cam1: makeChannel({ level: -6 }), cam2: makeChannel({ level: -3 }) };
		const channelsB = { cam1: makeChannel({ level: -6 }) };
		// cam2 only in A — no crash, no drift
		expect(detectAudioConfigDrift(channelsA, channelsB)).toBeNull();
	});

	it('returns null when both channel maps are empty', () => {
		expect(detectAudioConfigDrift({}, {})).toBeNull();
	});

	it('returns first divergent channel found', () => {
		const channelsA = {
			cam1: makeChannel({ level: -6 }),
			cam2: makeChannel({ level: -3 }),
		};
		const channelsB = {
			cam1: makeChannel({ level: -6 }),
			cam2: makeChannel({ level: -10 }),
		};
		const result = detectAudioConfigDrift(channelsA, channelsB);
		expect(result).toEqual({ channel: 'cam2', reason: 'config' });
	});
});
