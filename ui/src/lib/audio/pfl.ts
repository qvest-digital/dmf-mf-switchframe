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
	let _duckNode: GainNode | null = null;
	let _dimmed = false;
	let _autoDuck = true;
	let _duckActive = false;

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
	 * Get the duck GainNode. All decoder outputs route through this node
	 * before reaching the AudioContext destination. Comms ducking and manual
	 * DIM control this node's gain.
	 */
	function getDuckNode(): GainNode {
		const ctx = getAudioContext();
		if (!_duckNode) {
			_duckNode = ctx.createGain();
			_duckNode.gain.value = 1.0;
			_duckNode.connect(ctx.destination);
		}
		return _duckNode;
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
		await decoder.configure(codec, sampleRate, channels, ctx, getDuckNode());
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

	/** Duck level in dB when dimming program audio (0.1 linear ≈ -20dB). */
	const DUCK_GAIN = 0.1;
	const DUCK_ATTACK_MS = 50;
	const DUCK_RELEASE_MS = 300;

	function applyDuck(): void {
		if (!_duckNode) return;
		const shouldDuck = _dimmed || (_autoDuck && _duckActive);
		const target = shouldDuck ? DUCK_GAIN : 1.0;
		const rampMs = shouldDuck ? DUCK_ATTACK_MS : DUCK_RELEASE_MS;
		_duckNode.gain.cancelScheduledValues(_duckNode.context.currentTime);
		_duckNode.gain.linearRampToValueAtTime(
			target,
			_duckNode.context.currentTime + rampMs / 1000,
		);
	}

	/** Manual DIM toggle — operator holds/toggles to dim program audio. */
	function setDim(dim: boolean): void {
		_dimmed = dim;
		applyDuck();
	}

	/** Enable/disable auto-duck (program ducks when comms audio arrives). */
	function setAutoDuck(enabled: boolean): void {
		_autoDuck = enabled;
		if (!enabled) {
			_duckActive = false;
			applyDuck();
		}
	}

	/**
	 * Called by CommsAudioManager when comms audio frames are being received.
	 * Triggers auto-duck if enabled.
	 */
	function setCommsActive(active: boolean): void {
		_duckActive = active;
		if (_autoDuck) {
			applyDuck();
		}
	}

	return {
		get activeSource() {
			return _activeSource;
		},
		get dimmed() {
			return _dimmed;
		},
		get autoDuck() {
			return _autoDuck;
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
		setDim,
		setAutoDuck,
		setCommsActive,
		destroy,
	};
}
