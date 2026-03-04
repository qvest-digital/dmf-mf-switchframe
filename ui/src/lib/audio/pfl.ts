/** Audio level measurements for a single source (stereo). */
export interface AudioLevels {
	peakL: number;
	peakR: number;
	rmsL: number;
	rmsR: number;
}

/**
 * Creates a PFL (Pre-Fade Listen) manager for client-side per-source audio monitoring.
 *
 * PFL allows the operator to solo-listen to any source without affecting program output.
 * Each operator's PFL selection is independent and purely client-side.
 *
 * In the full implementation, this will:
 * - Create a PrismAudioDecoder per source for decoding MoQ audio tracks
 * - Route decoded audio through AudioContext -> GainNode -> speakers
 * - Maintain metering (levels) for all sources even when not PFL'd
 * - Only route the PFL'd source's audio to the operator's speakers
 */
export function createPFLManager() {
	let _activeSource: string | null = null;
	// TODO: In full implementation, this maps sourceKey -> PrismAudioDecoder instance
	const decoders = new Map<string, { levels: AudioLevels }>();

	/**
	 * Enable PFL for a source. Only one source can be PFL'd at a time.
	 * Switching PFL to a new source automatically replaces the previous one.
	 */
	function enablePFL(sourceKey: string) {
		_activeSource = sourceKey;
		// TODO: In full implementation:
		// 1. Create PrismAudioDecoder for source if not already decoding
		// 2. Route decoded audio to AudioContext -> GainNode -> speakers
		// 3. Mute previous PFL source's speaker routing (but keep decoding for metering)
		if (!decoders.has(sourceKey)) {
			decoders.set(sourceKey, {
				levels: { peakL: 0, peakR: 0, rmsL: 0, rmsR: 0 },
			});
		}
	}

	/** Disable PFL — stop routing any source audio to speakers. */
	function disablePFL() {
		_activeSource = null;
		// TODO: In full implementation, disconnect speaker routing but keep metering
	}

	/** Get the current audio levels for a source. Returns zeros if source is not being metered. */
	function getSourceLevels(sourceKey: string): AudioLevels {
		return (
			decoders.get(sourceKey)?.levels ?? {
				peakL: 0,
				peakR: 0,
				rmsL: 0,
				rmsR: 0,
			}
		);
	}

	/** Clean up all resources: close decoders, release AudioContext. */
	function destroy() {
		_activeSource = null;
		decoders.clear();
		// TODO: In full implementation, close all AudioDecoders and AudioContext
	}

	return {
		get activeSource() {
			return _activeSource;
		},
		enablePFL,
		disablePFL,
		getSourceLevels,
		destroy,
	};
}
