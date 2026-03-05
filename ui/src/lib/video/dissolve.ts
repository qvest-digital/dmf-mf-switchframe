import { renderDissolveFallback, renderDipFallback } from './dissolve-fallback';

export type DissolveMode = 'webgpu' | 'canvas2d';
export type TransitionType = 'mix' | 'dip' | 'ftb';

export interface DissolveRenderer {
	readonly mode: DissolveMode;
	mixFactor: number;
	transitionType: TransitionType;

	/**
	 * Set the blend mix factor (0.0 = all A, 1.0 = all B).
	 */
	setMixFactor(factor: number): void;

	/**
	 * Set the transition type for rendering.
	 */
	setTransitionType(type: TransitionType): void;

	/**
	 * Render a dissolve frame. Called from requestAnimationFrame.
	 * @param sourceA - Source A canvas (outgoing)
	 * @param sourceB - Source B canvas (incoming), null for FTB
	 */
	render(
		sourceA: HTMLCanvasElement | OffscreenCanvas | null,
		sourceB: HTMLCanvasElement | OffscreenCanvas | null,
	): void;

	/**
	 * Release GPU/canvas resources.
	 */
	destroy(): void;
}

/**
 * Creates a Canvas 2D dissolve renderer for browser-side transition preview.
 * The server produces the authoritative blended output; this is operator preview only.
 */
export function createDissolveRenderer(canvas: HTMLCanvasElement): DissolveRenderer {
	let _mixFactor = 0;
	let _transitionType: TransitionType = 'mix';

	// Canvas 2D fallback (jsdom and non-WebGPU browsers)
	const ctx = canvas.getContext('2d');

	const renderer: DissolveRenderer = {
		get mode(): DissolveMode {
			return 'canvas2d';
		},
		get mixFactor() { return _mixFactor; },
		set mixFactor(v: number) { _mixFactor = Math.max(0, Math.min(1, v)); },
		get transitionType() { return _transitionType; },
		set transitionType(v: TransitionType) { _transitionType = v; },

		setMixFactor(factor: number) {
			_mixFactor = Math.max(0, Math.min(1, factor));
		},

		setTransitionType(type: TransitionType) {
			_transitionType = type;
		},

		render(sourceA, sourceB) {
			if (!ctx) return;

			switch (_transitionType) {
				case 'mix':
				case 'ftb':
					renderDissolveFallback(ctx, sourceA, sourceB, _mixFactor);
					break;
				case 'dip':
					renderDipFallback(ctx, sourceA, sourceB, _mixFactor);
					break;
			}
		},

		destroy() {
			// Canvas 2D context cleanup — nothing to release
		},
	};

	return renderer;
}
