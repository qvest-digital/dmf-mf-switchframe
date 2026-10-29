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

/** What the toggle needs of a media pipeline. */
export interface PFLPipeline {
	setSourceMuted(key: string, muted: boolean): void;
	setSourceGain(key: string, gain: number): void;
}

export interface PFLToggleContext {
	/**
	 * The media pipeline that currently plays this browser's audio: engine
	 * A's, or engine B's after a failover. Read at every change, and by
	 * follow() when the active engine changes.
	 */
	pipeline(): PFLPipeline;
	/** Linear gain of the channel's fader, 0 while the channel is muted. */
	aflGain(key: string): number;
	/** Take program out of the monitor while a channel is soloed. */
	setSolo(solo: boolean): void;
}

export function createPFLToggle(ctx: PFLToggleContext) {
	let activeSource: string | null = null;
	let activeMode: PFLMode = 'off';
	/** The pipeline the solo is applied on. */
	let appliedOn: PFLPipeline | null = null;
	/** Gain the soloed channel plays at: 1 in PFL, its fader in AFL. */
	let gain = 1;
	let busy = false;

	function release() {
		if (appliedOn && activeSource) {
			appliedOn.setSourceMuted(activeSource, true);
			appliedOn.setSourceGain(activeSource, 1);
		}
		appliedOn = null;
	}

	function apply() {
		if (!activeSource) return;
		appliedOn = ctx.pipeline();
		appliedOn.setSourceGain(activeSource, gain);
		appliedOn.setSourceMuted(activeSource, false);
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
			gain = ctx.aflGain(sourceKey);
			apply();
		} else if (activeSource === sourceKey && activeMode === 'afl') {
			clear();
		} else {
			release();
			activeSource = sourceKey;
			activeMode = 'pfl';
			gain = 1;
			apply();
			ctx.setSolo(true);
		}

		setTimeout(() => { busy = false; }, 100);
		return { source: activeSource, mode: activeMode };
	}

	/** End the solo: program back on the monitor. */
	function clear(): PFLToggleResult {
		release();
		if (activeSource) ctx.setSolo(false);
		activeSource = null;
		activeMode = 'off';
		gain = 1;
		return { source: null, mode: 'off' };
	}

	/** Follow a fader or mute change of the channel in AFL. */
	function setAFLGain(sourceKey: string, linearGain: number): void {
		if (activeSource !== sourceKey || activeMode !== 'afl') return;
		gain = linearGain;
		appliedOn?.setSourceGain(sourceKey, gain);
	}

	/** Re-read the AFL gain from the channel's fader. */
	function refreshAFL(): void {
		if (activeSource) setAFLGain(activeSource, ctx.aflGain(activeSource));
	}

	/** Move the solo to the pipeline now active, after a failover or restore. */
	function follow(): void {
		if (!activeSource || ctx.pipeline() === appliedOn) return;
		release();
		apply();
	}

	return {
		toggle,
		clear,
		setAFLGain,
		refreshAFL,
		follow,
		get activeSource() { return activeSource; },
		get activeMode() { return activeMode; },
	};
}
