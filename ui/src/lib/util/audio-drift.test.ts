import { describe, it, expect } from 'vitest';
import { detectAudioConfigDrift, compareAudioChannels, type AudioDriftResult } from './audio-drift';

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

describe('compareAudioChannels', () => {
	it('keeps a channel in sync while the engines report different peaks', () => {
		// Each engine samples its meters at its own broadcast instant, so a
		// steady input still yields different peaks; comparing them made the
		// redundancy pane flicker between in sync and out of tolerance.
		const quiet = makeChannel({ level: -6 });
		quiet.peakL = quiet.peakR = -40;
		const result = compareAudioChannels({ cam1: makeChannel({ level: -6 }) }, { cam1: quiet });
		expect(result).toEqual([{ channel: 'cam1', levelA: -6, levelB: -6, diff: [] }]);
	});

	it('flags any fader difference, as the drift detector does', () => {
		// Both engines store the commanded value unchanged, so equal settings
		// are equal bits; a tolerance would show in sync what the drift
		// detector reconciles.
		const channelsA = { cam1: makeChannel({ level: -6 }) };
		const channelsB = { cam1: makeChannel({ level: -6.001 }) };
		expect(compareAudioChannels(channelsA, channelsB)?.[0].diff).toEqual(['level']);
		expect(detectAudioConfigDrift(channelsA, channelsB)).not.toBeNull();
	});

	it('names every setting that differs', () => {
		const result = compareAudioChannels(
			{ cam1: makeChannel({ trim: 0, muted: false, afv: false }) },
			{ cam1: makeChannel({ trim: 6, muted: true, afv: true }) },
		);
		expect(result?.[0].diff).toEqual(['trim', 'mute', 'AFV']);
	});

	it('skips channels only one engine carries', () => {
		const result = compareAudioChannels({ cam1: makeChannel(), cam2: makeChannel() }, { cam1: makeChannel() });
		expect(result?.map((c) => c.channel)).toEqual(['cam1']);
	});

	it('returns null when either engine has no audio state', () => {
		expect(compareAudioChannels(undefined, { cam1: makeChannel() })).toBeNull();
		expect(compareAudioChannels({}, {})).toBeNull();
	});
});
