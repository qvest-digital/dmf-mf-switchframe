// Shared reactive state for AR prompt mode.
// When active, ProgramPreview shows crosshair cursor and handles clicks.

export interface PromptMarker {
	id: string;
	x: number;
	y: number;
	label: string;
}

export type ARInteractionMode = 'off' | 'point' | 'box' | 'place';

interface ARPromptState {
	mode: ARInteractionMode;
	callback: ((type: string, points: number[][]) => void) | null;
	placeCallback: ((pixelX: number, pixelY: number) => void) | null;
	prompts: PromptMarker[];
}

let arPromptState = $state<ARPromptState>({
	mode: 'off',
	callback: null,
	placeCallback: null,
	prompts: [],
});

export function getArPromptState(): ARPromptState {
	return arPromptState;
}

export function startPromptMode(callback: (type: string, points: number[][]) => void) {
	arPromptState.mode = 'point';
	arPromptState.callback = callback;
	arPromptState.placeCallback = null;
}

export function startPlaceMode(callback: (pixelX: number, pixelY: number) => void) {
	arPromptState.mode = 'place';
	arPromptState.placeCallback = callback;
	arPromptState.callback = null;
}

export function stopPromptMode() {
	arPromptState.mode = 'off';
	arPromptState.callback = null;
	arPromptState.placeCallback = null;
}

export function setPromptMarkers(markers: PromptMarker[]) {
	arPromptState.prompts = markers;
}
