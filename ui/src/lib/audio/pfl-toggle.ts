/**
 * PFL toggle utility with debounce guard.
 *
 * Prevents rapid PFL clicks from interleaving mute/unmute operations,
 * which could leave audio in the wrong state. A 100ms cooldown between
 * toggles ensures each operation completes atomically.
 */

export interface PFLToggleContext {
	pflManager: { enablePFL(key: string): void; disablePFL(): void };
	pipeline: { setSourceMuted(key: string, muted: boolean): void };
}

export function createPFLToggle(ctx: PFLToggleContext) {
	let activeSource: string | null = null;
	let busy = false;

	/**
	 * Toggle PFL for a source. Returns the new active source (or null).
	 * If a toggle is already in progress (within the 100ms cooldown),
	 * the call is ignored and the current active source is returned.
	 */
	function toggle(sourceKey: string): string | null {
		if (busy) return activeSource;
		busy = true;

		if (activeSource === sourceKey) {
			// Disable PFL on the currently active source
			ctx.pflManager.disablePFL();
			ctx.pipeline.setSourceMuted(sourceKey, true);
			activeSource = null;
		} else {
			// Mute previous PFL source in pipeline
			if (activeSource) {
				ctx.pipeline.setSourceMuted(activeSource, true);
			}
			// Enable PFL on the new source
			ctx.pflManager.enablePFL(sourceKey);
			ctx.pipeline.setSourceMuted(sourceKey, false);
			activeSource = sourceKey;
		}

		setTimeout(() => { busy = false; }, 100);
		return activeSource;
	}

	return {
		toggle,
		get activeSource() { return activeSource; },
	};
}
