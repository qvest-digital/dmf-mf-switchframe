import { PrismAudioDecoder } from '$lib/prism/audio-decoder';

/** Audio level measurements for a single source (stereo). */
export interface AudioLevels {
	peakL: number;
	peakR: number;
	rmsL: number;
	rmsR: number;
}

/**
 * Per-source audio state for the PFL manager.
 * Each source gets a PrismAudioDecoder for metering, but only
 * the PFL'd source has its audio routed to speakers (unmuted).
 */
interface SourceAudio {
	decoder: PrismAudioDecoder;
	configured: boolean;
}

/**
 * Creates a PFL (Pre-Fade Listen) manager for client-side per-source audio monitoring.
 *
 * PFL allows the operator to solo-listen to any source without affecting program output.
 * Each operator's PFL selection is independent and purely client-side.
 *
 * All sources have their audio decoded for metering (VU levels), but only the
 * PFL'd source's audio is routed to the operator's speakers (gain unmuted).
 */
export function createPFLManager() {
	let _activeSource: string | null = null;
	const decoders = new Map<string, SourceAudio>();
	let _sharedContext: AudioContext | null = null;
	let _contextResumed = false;

	/**
	 * Get or create a shared AudioContext. Created lazily on first use.
	 * The context starts suspended and must be resumed via a user gesture.
	 */
	function getAudioContext(): AudioContext {
		if (!_sharedContext) {
			_sharedContext = new AudioContext({ sampleRate: 48000, latencyHint: 'interactive' });
		}
		return _sharedContext;
	}

	/**
	 * Resume the AudioContext. Must be called from a user gesture handler
	 * (click, keydown, etc.) to satisfy browser autoplay policy.
	 */
	async function resumeContext(): Promise<void> {
		if (_contextResumed) return;
		const ctx = getAudioContext();
		if (ctx.state === 'suspended') {
			await ctx.resume();
		}
		_contextResumed = true;
	}

	/**
	 * Add a source for audio decoding/metering. Configures the decoder
	 * for AAC, 48kHz, stereo. The decoder is muted by default.
	 */
	async function addSource(
		sourceKey: string,
		codec: string = 'mp4a.40.2',
		sampleRate: number = 48000,
		channels: number = 2,
	): Promise<void> {
		if (decoders.has(sourceKey)) return;

		const decoder = new PrismAudioDecoder();
		const ctx = getAudioContext();
		await decoder.configure(codec, sampleRate, channels, ctx);
		decoder.setMuted(true); // all muted by default
		decoder.enableMetering(); // always meter for VU display

		decoders.set(sourceKey, { decoder, configured: true });
	}

	/**
	 * Remove a source and clean up its audio decoder.
	 */
	function removeSource(sourceKey: string): void {
		const source = decoders.get(sourceKey);
		if (!source) return;
		source.decoder.reset();
		decoders.delete(sourceKey);

		if (_activeSource === sourceKey) {
			_activeSource = null;
		}
	}

	/**
	 * Feed an audio frame to a source's decoder for decoding and metering.
	 */
	function feedAudioFrame(sourceKey: string, data: Uint8Array, timestamp: number): void {
		const source = decoders.get(sourceKey);
		if (!source || !source.configured) return;
		source.decoder.decode(data, timestamp, false);
	}

	/**
	 * Get the raw PrismAudioDecoder for a source.
	 * Used by the media pipeline for direct frame feeding.
	 */
	function getDecoder(sourceKey: string): PrismAudioDecoder | null {
		return decoders.get(sourceKey)?.decoder ?? null;
	}

	/**
	 * Enable PFL for a source. Only one source can be PFL'd at a time.
	 * Switching PFL to a new source automatically mutes the previous one.
	 */
	function enablePFL(sourceKey: string) {
		// Mute previous PFL source
		if (_activeSource && _activeSource !== sourceKey) {
			const prev = decoders.get(_activeSource);
			if (prev) {
				prev.decoder.setMuted(true);
			}
		}

		_activeSource = sourceKey;

		// Unmute the new PFL source
		const source = decoders.get(sourceKey);
		if (source) {
			source.decoder.setMuted(false);
		}

		// Ensure context is resumed (best effort -- may need user gesture)
		resumeContext().catch(() => {
			// Will be retried on next user interaction
		});
	}

	/** Disable PFL -- mute all sources (stop routing audio to speakers). */
	function disablePFL() {
		if (_activeSource) {
			const source = decoders.get(_activeSource);
			if (source) {
				source.decoder.setMuted(true);
			}
		}
		_activeSource = null;
	}

	/** Get the current audio levels for a source. Returns zeros if source is not being metered. */
	function getSourceLevels(sourceKey: string): AudioLevels {
		const source = decoders.get(sourceKey);
		if (!source || !source.configured) {
			return { peakL: 0, peakR: 0, rmsL: 0, rmsR: 0 };
		}

		const levels = source.decoder.getLevels();
		return {
			peakL: levels.peak[0] ?? 0,
			peakR: levels.peak[1] ?? 0,
			rmsL: levels.rms[0] ?? 0,
			rmsR: levels.rms[1] ?? 0,
		};
	}

	/**
	 * Get the playback PTS for a source's audio decoder.
	 * Used as an audio clock for A/V sync in the video renderer.
	 */
	function getPlaybackPTS(sourceKey: string): number {
		const source = decoders.get(sourceKey);
		if (!source) return -1;
		return source.decoder.getPlaybackPTS();
	}

	/** Clean up all resources: close decoders, release AudioContext. */
	function destroy() {
		for (const [, source] of decoders) {
			source.decoder.reset();
		}
		decoders.clear();
		_activeSource = null;

		if (_sharedContext) {
			_sharedContext.close().catch(() => { /* ignore */ });
			_sharedContext = null;
			_contextResumed = false;
		}
	}

	return {
		get activeSource() {
			return _activeSource;
		},
		addSource,
		removeSource,
		feedAudioFrame,
		getDecoder,
		enablePFL,
		disablePFL,
		getSourceLevels,
		getPlaybackPTS,
		resumeContext,
		destroy,
	};
}
