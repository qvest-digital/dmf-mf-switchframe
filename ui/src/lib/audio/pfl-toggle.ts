/**
 * PFL/AFL: route one channel to this browser's monitor in place of program.
 *
 * Cycles through three states per channel: OFF -> PFL -> AFL -> OFF. A
 * different channel's button moves the solo there, in PFL.
 *
 * PFL (Pre-Fader Listen) plays the channel at unity gain.
 * AFL (After-Fader Listen) plays it at its fader level, silent while the
 * channel is muted.
 *
 * While a channel is soloed, program leaves the monitor so the channel is
 * heard alone. Only the local monitor changes; the program mix does not.
 *
 * Prevents rapid clicks from interleaving mute/unmute operations,
 * which could leave audio in the wrong state. A 100ms cooldown between
 * toggles ensures each operation completes atomically.
 */

/** Monitoring mode for a channel: off, pre-fader listen, or after-fader listen. */
export type PFLMode = 'off' | 'pfl' | 'afl';

export interface PFLToggleResult {
	source: string | null;
	mode: PFLMode;
}

export interface PFLToggleContext {
	/** The media pipeline that decodes each channel's audio. */
	pipeline: {
		setSourceMuted(key: string, muted: boolean): void;
		setSourceGain(key: string, gain: number): void;
	};
	/** Linear gain of the channel's fader, 0 while the channel is muted. */
	aflGain(key: string): number;
	/** Take program out of the monitor while a channel is soloed. */
	setSolo(solo: boolean): void;
}

export function createPFLToggle(ctx: PFLToggleContext) {
	let activeSource: string | null = null;
	let activeMode: PFLMode = 'off';
	let busy = false;

	function release(key: string) {
		ctx.pipeline.setSourceMuted(key, true);
		ctx.pipeline.setSourceGain(key, 1);
	}

	/**
	 * Cycle the monitoring mode for a source. Returns the new state.
	 *
	 * Same source: OFF -> PFL -> AFL -> OFF
	 * Different source: activates PFL on the new source (mutes previous).
	 *
	 * If a toggle is already in progress (within the 100ms cooldown),
	 * the call is ignored and the current state is returned.
	 */
	function toggle(sourceKey: string): PFLToggleResult {
		if (busy) return { source: activeSource, mode: activeMode };
		busy = true;

		if (activeSource === sourceKey && activeMode === 'pfl') {
			activeMode = 'afl';
			ctx.pipeline.setSourceGain(sourceKey, ctx.aflGain(sourceKey));
		} else if (activeSource === sourceKey && activeMode === 'afl') {
			release(sourceKey);
			ctx.setSolo(false);
			activeSource = null;
			activeMode = 'off';
		} else {
			if (activeSource) release(activeSource);
			ctx.pipeline.setSourceGain(sourceKey, 1);
			ctx.pipeline.setSourceMuted(sourceKey, false);
			ctx.setSolo(true);
			activeSource = sourceKey;
			activeMode = 'pfl';
		}

		setTimeout(() => { busy = false; }, 100);
		return { source: activeSource, mode: activeMode };
	}

	/** Follow a fader or mute change of the channel in AFL. */
	function refreshAFL(): void {
		if (activeSource && activeMode === 'afl') {
			ctx.pipeline.setSourceGain(activeSource, ctx.aflGain(activeSource));
		}
	}

	return {
		toggle,
		refreshAFL,
		get activeSource() { return activeSource; },
		get activeMode() { return activeMode; },
	};
}
