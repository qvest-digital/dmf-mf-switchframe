import type { AudioChannel } from '$lib/api/types';

export interface AudioChannelComparison {
	channel: string;
	levelA: number;
	levelB: number;
	withinTolerance: boolean;
}

// Absorbs float rounding between engines; any operator change is larger.
const LEVEL_TOLERANCE_DB = 0.05;

/**
 * Compares the mix settings of each channel both engines carry. Peak meters
 * are left out: each engine samples them at its own broadcast instant, so
 * two engines playing the same audio report different peaks.
 */
export function compareAudioChannels(
	channelsA: Record<string, AudioChannel> | undefined,
	channelsB: Record<string, AudioChannel> | undefined,
): AudioChannelComparison[] | null {
	if (!channelsA || !channelsB) return null;
	const result: AudioChannelComparison[] = [];
	for (const [key, chA] of Object.entries(channelsA)) {
		const chB = channelsB[key];
		if (!chB) continue;
		result.push({
			channel: key,
			levelA: chA.level,
			levelB: chB.level,
			withinTolerance:
				Math.abs(chA.level - chB.level) <= LEVEL_TOLERANCE_DB &&
				Math.abs(chA.trim - chB.trim) <= LEVEL_TOLERANCE_DB &&
				chA.muted === chB.muted &&
				chA.afv === chB.afv,
		});
	}
	return result.length > 0 ? result : null;
}
