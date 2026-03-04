import { describe, it, expect } from 'vitest';
import type { AudioChannel, ControlRoomState } from './types';

describe('AudioChannel type', () => {
	it('should conform to expected shape', () => {
		const ch: AudioChannel = { level: -6.0, muted: false, afv: true };
		expect(ch.level).toBe(-6.0);
		expect(ch.muted).toBe(false);
		expect(ch.afv).toBe(true);
	});

	it('should appear in ControlRoomState', () => {
		const state: Partial<ControlRoomState> = {
			audioChannels: { cam1: { level: 0, muted: false, afv: true } },
			masterLevel: -3.0,
			programPeak: [-6.0, -8.0],
		};
		expect(state.audioChannels?.cam1.afv).toBe(true);
		expect(state.programPeak?.[0]).toBe(-6.0);
	});

	it('should round-trip through JSON', () => {
		const ch: AudioChannel = { level: -12.5, muted: true, afv: false };
		const json = JSON.stringify(ch);
		const decoded: AudioChannel = JSON.parse(json);
		expect(decoded).toEqual(ch);
	});
});
