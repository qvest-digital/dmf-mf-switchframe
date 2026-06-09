import { apiCall, cut, startTransition } from '$lib/api/switch-api';
import type { ControlRoomState } from '$lib/api/types';

/**
 * Minimal store surface needed by the switch actions — matches the store
 * returned by createControlRoomStore (control-room.svelte.ts).
 */
export interface SwitchActionStore {
	/** Raw server-confirmed state. */
	state: ControlRoomState;
	/** Server state overlaid with the pending optimistic action. */
	effectiveState: ControlRoomState;
	optimisticCut(source: string): void;
}

/**
 * Cut the current preview source to program (keyboard Cut / SimpleMode Cut).
 *
 * Reads effectiveState, not the raw server state: the preview monitor renders
 * effectiveState, which overlays the operator's pending optimistic preview.
 * The server-confirmed previewSource lags by a REST round trip plus the MoQ
 * state update, so an operator pressing '2' then Cut in quick succession would
 * otherwise put the OLD preview source on air while seeing the new one in the
 * preview monitor.
 */
export function cutToPreview(store: SwitchActionStore): void {
	const preview = store.effectiveState.previewSource;
	if (preview) {
		store.optimisticCut(preview);
		apiCall(cut(preview), 'Cut failed');
	}
}

/**
 * Start an auto transition from the current preview source (keyboard Auto /
 * SimpleMode Dissolve). Uses effectiveState for the same reason as
 * cutToPreview: the transition must target the preview the operator sees.
 */
export function autoTransitionToPreview(
	store: SwitchActionStore,
	type: string,
	durationMs: number,
	errorContext = 'Transition failed'
): void {
	const s = store.effectiveState;
	if (s.previewSource && !s.inTransition && !s.ftbActive) {
		apiCall(startTransition(s.previewSource, type, durationMs), errorContext);
	}
}
