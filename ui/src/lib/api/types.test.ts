import { describe, it, expect } from 'vitest';
import type { AudioChannel, ControlRoomState } from './types';

function makeChannel(overrides: Partial<AudioChannel> = {}): AudioChannel {
	return {
		level: 0,
		trim: 0,
		muted: false,
		afv: false,
		peakL: -Infinity,
		peakR: -Infinity,
		eq: [
			{ frequency: 100, gain: 0, q: 1, enabled: false },
			{ frequency: 1000, gain: 0, q: 1, enabled: false },
			{ frequency: 10000, gain: 0, q: 1, enabled: false },
		],
		compressor: { threshold: -20, ratio: 4, attack: 10, release: 100, makeupGain: 0 },
		gainReduction: 0,
		...overrides,
	};
}

describe('AudioChannel type', () => {
	it('should conform to expected shape', () => {
		const ch: AudioChannel = makeChannel({ level: -6.0, muted: false, afv: true });
		expect(ch.level).toBe(-6.0);
		expect(ch.muted).toBe(false);
		expect(ch.afv).toBe(true);
	});

	it('should appear in ControlRoomState', () => {
		const state: Partial<ControlRoomState> = {
			audioChannels: { cam1: makeChannel({ level: 0, afv: true }) },
			masterLevel: -3.0,
			programPeak: [-6.0, -8.0],
		};
		expect(state.audioChannels?.cam1.afv).toBe(true);
		expect(state.programPeak?.[0]).toBe(-6.0);
	});

	it('should round-trip through JSON', () => {
		const ch: AudioChannel = makeChannel({ level: -12.5, muted: true, peakL: -20, peakR: -25 });
		const json = JSON.stringify(ch);
		const decoded: AudioChannel = JSON.parse(json);
		expect(decoded).toEqual(ch);
	});
});
