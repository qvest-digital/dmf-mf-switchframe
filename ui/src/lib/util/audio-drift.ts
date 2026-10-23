/**
 * Audio drift detection: compares audio *configuration* between two engines.
 *
 * We compare fader level, mute, AFV, and trim — these are the settings that
 * indicate real state divergence. Peak levels are transient metering data
 * that naturally fluctuates with timing jitter and should not trigger drift.
 */

export interface AudioDriftResult {
	channel: string;
	reason: 'config';
}

interface AudioChannelConfig {
	level: number;
	trim: number;
	muted: boolean;
	afv: boolean;
}

/** Names of the settings in which two engines' copies of a channel differ. */
export function audioConfigDiff(chA: AudioChannelConfig, chB: AudioChannelConfig): string[] {
	const diff: string[] = [];
	if (chA.level !== chB.level) diff.push('level');
	if (chA.trim !== chB.trim) diff.push('trim');
	if (chA.muted !== chB.muted) diff.push('mute');
	if (chA.afv !== chB.afv) diff.push('AFV');
	return diff;
}

/**
 * Detect audio configuration drift between two engines' audio channel maps.
 * Returns the first divergent channel, or null if all shared channels match.
 */
export function detectAudioConfigDrift(
	channelsA: Record<string, AudioChannelConfig>,
	channelsB: Record<string, AudioChannelConfig>,
): AudioDriftResult | null {
	for (const [key, chA] of Object.entries(channelsA)) {
		const chB = channelsB[key];
		if (!chB) continue;
		if (audioConfigDiff(chA, chB).length > 0) {
			return { channel: key, reason: 'config' };
		}
	}
	return null;
}

export interface AudioChannelComparison {
	channel: string;
	levelA: number;
	levelB: number;
	/** Settings that differ between the engines; empty when in sync. */
	diff: string[];
}

/** Per-channel comparison of the channels both engines carry, for display. */
export function compareAudioChannels(
	channelsA: Record<string, AudioChannelConfig> | undefined,
	channelsB: Record<string, AudioChannelConfig> | undefined,
): AudioChannelComparison[] | null {
	if (!channelsA || !channelsB) return null;
	const result: AudioChannelComparison[] = [];
	for (const [key, chA] of Object.entries(channelsA)) {
		const chB = channelsB[key];
		if (!chB) continue;
		result.push({ channel: key, levelA: chA.level, levelB: chB.level, diff: audioConfigDiff(chA, chB) });
	}
	return result.length > 0 ? result : null;
}
