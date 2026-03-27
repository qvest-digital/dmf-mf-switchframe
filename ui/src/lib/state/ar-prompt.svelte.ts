// Shared reactive state for AR prompt mode.
// When active, ProgramPreview shows crosshair cursor and handles clicks.

interface PromptMarker {
	id: string;
	x: number;
	y: number;
	label: string;
}

interface ARPromptState {
	mode: 'off' | 'point' | 'box';
	callback: ((type: string, points: number[][]) => void) | null;
	prompts: PromptMarker[];
}

let arPromptState = $state<ARPromptState>({
	mode: 'off',
	callback: null,
	prompts: [],
});

export function getArPromptState(): ARPromptState {
	return arPromptState;
}

export function startPromptMode(callback: (type: string, points: number[][]) => void) {
	arPromptState.mode = 'point';
	arPromptState.callback = callback;
}

export function stopPromptMode() {
	arPromptState.mode = 'off';
	arPromptState.callback = null;
}

export function setPromptMarkers(markers: PromptMarker[]) {
	arPromptState.prompts = markers;
}
