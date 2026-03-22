/**
 * PFL/AFL toggle utility with debounce guard.
 *
 * Cycles through three states: OFF -> PFL -> AFL -> OFF.
 *
 * PFL (Pre-Fader Listen) plays source audio at unity gain.
 * AFL (After-Fader Listen) plays source audio at the channel's fader level.
 *
 * Prevents rapid clicks from interleaving mute/unmute operations,
 * which could leave audio in the wrong state. A 100ms cooldown between
 * toggles ensures each operation completes atomically.
 */
import type { PFLMode } from './pfl';

export interface PFLToggleResult {
	source: string | null;
	mode: PFLMode;
}

export interface PFLToggleContext {
	pflManager: {
		enablePFL(key: string, mode?: PFLMode): void;
		disablePFL(): void;
	};
	pipeline: { setSourceMuted(key: string, muted: boolean): void };
}

export function createPFLToggle(ctx: PFLToggleContext) {
	let activeSource: string | null = null;
	let activeMode: PFLMode = 'off';
	let busy = false;

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

		if (activeSource === sourceKey) {
			// Cycle the mode on the same source
			if (activeMode === 'pfl') {
				// PFL -> AFL
				activeMode = 'afl';
				ctx.pflManager.enablePFL(sourceKey, 'afl');
			} else if (activeMode === 'afl') {
				// AFL -> OFF
				ctx.pflManager.disablePFL();
				ctx.pipeline.setSourceMuted(sourceKey, true);
				activeSource = null;
				activeMode = 'off';
			} else {
				// off -> PFL (shouldn't normally happen via this path)
				activeMode = 'pfl';
				ctx.pflManager.enablePFL(sourceKey, 'pfl');
				ctx.pipeline.setSourceMuted(sourceKey, false);
			}
		} else {
			// New source: mute previous, enable PFL on new source
			if (activeSource) {
				ctx.pipeline.setSourceMuted(activeSource, true);
			}
			ctx.pflManager.enablePFL(sourceKey, 'pfl');
			ctx.pipeline.setSourceMuted(sourceKey, false);
			activeSource = sourceKey;
			activeMode = 'pfl';
		}

		setTimeout(() => { busy = false; }, 100);
		return { source: activeSource, mode: activeMode };
	}

	return {
		toggle,
		get activeSource() { return activeSource; },
		get activeMode() { return activeMode; },
	};
}
