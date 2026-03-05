import type { ControlRoomState } from '$lib/api/types';

const EMPTY_STATE: ControlRoomState = {
	programSource: '',
	previewSource: '',
	transitionType: 'cut',
	transitionDurationMs: 0,
	transitionPosition: 0,
	inTransition: false,
	ftbActive: false,
	audioChannels: undefined,
	masterLevel: 0,
	programPeak: [0, 0],
	tallyState: {},
	sources: {},
	seq: 0,
	timestamp: 0,
};

/** Timeout (ms) after which a pending optimistic action is discarded. */
const PENDING_TIMEOUT_MS = 2000;

interface PendingAction {
	programSource?: string;
	previewSource?: string;
	timestamp: number;
}

export function createControlRoomStore() {
	let state = $state<ControlRoomState>({ ...EMPTY_STATE });
	let pendingAction = $state<PendingAction | null>(null);
	let lastServerUpdate = $state(Date.now());

	function applyUpdate(update: ControlRoomState) {
		if (update.seq <= state.seq) return;
		state = update;
		lastServerUpdate = Date.now();
		// Clear pending if server state matches the optimistic prediction or it expired
		if (pendingAction) {
			const expired = Date.now() - pendingAction.timestamp > PENDING_TIMEOUT_MS;
			const confirmed =
				(pendingAction.programSource && update.programSource === pendingAction.programSource) ||
				(pendingAction.previewSource && update.previewSource === pendingAction.previewSource);
			if (expired || confirmed) {
				pendingAction = null;
			}
		}
	}

	function applyFromMoQ(data: Uint8Array) {
		try {
			const update = JSON.parse(new TextDecoder().decode(data)) as ControlRoomState;
			applyUpdate(update);
		} catch {
			// Ignore malformed JSON
		}
	}

	function getEffectiveState(): ControlRoomState {
		if (!pendingAction) return state;
		// Return server state when pending action has expired (no mutation — cleanup happens in applyUpdate)
		if (Date.now() - pendingAction.timestamp > PENDING_TIMEOUT_MS) {
			return state;
		}
		const effective = { ...state };
		if (pendingAction.programSource) {
			effective.programSource = pendingAction.programSource;
			effective.tallyState = { ...state.tallyState };
			effective.tallyState[pendingAction.programSource] = 'program';
			// Old program source becomes preview-like
			if (state.programSource && state.programSource !== pendingAction.programSource) {
				effective.tallyState[state.programSource] = 'preview';
			}
		}
		if (pendingAction.previewSource) {
			effective.previewSource = pendingAction.previewSource;
			effective.tallyState = { ...(effective.tallyState || state.tallyState) };
			effective.tallyState[pendingAction.previewSource] = 'preview';
		}
		return effective;
	}

	function optimisticCut(source: string) {
		pendingAction = { programSource: source, timestamp: Date.now() };
	}

	function optimisticPreview(source: string) {
		pendingAction = { previewSource: source, timestamp: Date.now() };
	}

	return {
		get state() {
			return state;
		},
		get effectiveState() {
			return getEffectiveState();
		},
		get sourceKeys() {
			return Object.keys(state.sources).sort();
		},
		get lastServerUpdate() {
			return lastServerUpdate;
		},
		applyUpdate,
		applyFromMoQ,
		optimisticCut,
		optimisticPreview,
	};
}
