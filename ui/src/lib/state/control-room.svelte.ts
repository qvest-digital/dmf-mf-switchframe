import type { ControlRoomState } from '$lib/api/types';

const EMPTY_STATE: ControlRoomState = {
	programSource: '',
	previewSource: '',
	transitionType: 'cut',
	transitionDurationMs: 0,
	transitionPosition: 0,
	inTransition: false,
	audioLevels: null,
	tallyState: {},
	sources: {},
	seq: 0,
	timestamp: 0,
};

export function createControlRoomStore() {
	let state = $state<ControlRoomState>({ ...EMPTY_STATE });

	function applyUpdate(update: ControlRoomState) {
		if (update.seq <= state.seq) return;
		state = update;
	}

	function applyFromMoQ(data: Uint8Array) {
		try {
			const update = JSON.parse(new TextDecoder().decode(data)) as ControlRoomState;
			applyUpdate(update);
		} catch {
			// Ignore malformed JSON
		}
	}

	return {
		get state() {
			return state;
		},
		get sourceKeys() {
			return Object.keys(state.sources).sort();
		},
		applyUpdate,
		applyFromMoQ,
	};
}
