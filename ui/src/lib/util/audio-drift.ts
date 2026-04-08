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

		if (
			chA.level !== chB.level ||
			chA.muted !== chB.muted ||
			chA.afv !== chB.afv ||
			chA.trim !== chB.trim
		) {
			return { channel: key, reason: 'config' };
		}
	}
	return null;
}
