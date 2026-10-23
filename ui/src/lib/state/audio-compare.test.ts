import { describe, it, expect } from 'vitest';
import { compareAudioChannels } from './audio-compare';
import type { AudioChannel } from '$lib/api/types';

function channel(overrides: Partial<AudioChannel> = {}): AudioChannel {
	return {
		level: 0,
		trim: 0,
		muted: false,
		afv: true,
		peakL: -12,
		peakR: -12,
		eq: [
			{ frequency: 100, gain: 0, q: 1, enabled: false },
			{ frequency: 1000, gain: 0, q: 1, enabled: false },
			{ frequency: 8000, gain: 0, q: 1, enabled: false },
		],
		compressor: { threshold: 0, ratio: 1, attack: 5, release: 100, makeupGain: 0 },
		gainReduction: 0,
		gate: { threshold: -60, attack: 1, release: 100, hold: 10 },
		...overrides,
	} as AudioChannel;
}

describe('compareAudioChannels', () => {
	it('keeps a channel in sync while the engines report different peaks', () => {
		// Each engine samples its meters at its own broadcast instant, so a
		// steady input still yields different peaks; comparing them made the
		// pane flicker between in sync and out of tolerance.
		const result = compareAudioChannels(
			{ cam1: channel({ peakL: -6, peakR: -6 }) },
			{ cam1: channel({ peakL: -30, peakR: -30 }) },
		);
		expect(result).toEqual([{ channel: 'cam1', levelA: 0, levelB: 0, withinTolerance: true }]);
	});

	it('flags a fader level that differs between engines', () => {
		const result = compareAudioChannels(
			{ cam1: channel({ level: -10 }) },
			{ cam1: channel({ level: -4 }) },
		);
		expect(result).toEqual([{ channel: 'cam1', levelA: -10, levelB: -4, withinTolerance: false }]);
	});

	it.each([
		['trim', { trim: 6 }],
		['mute', { muted: true }],
		['audio-follows-video', { afv: false }],
	] as const)('flags a %s that differs between engines', (_name, override) => {
		const result = compareAudioChannels({ cam1: channel() }, { cam1: channel(override) });
		expect(result?.[0].withinTolerance).toBe(false);
	});

	it('skips channels only one engine carries', () => {
		const result = compareAudioChannels(
			{ cam1: channel(), cam2: channel() },
			{ cam1: channel() },
		);
		expect(result?.map((c) => c.channel)).toEqual(['cam1']);
	});

	it('returns null when either engine has no audio state', () => {
		expect(compareAudioChannels(undefined, { cam1: channel() })).toBeNull();
		expect(compareAudioChannels({}, {})).toBeNull();
	});
});
