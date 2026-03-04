export interface KeyboardActions {
	onCut: () => void;
	onSetPreview: (sourceKey: string) => void;
	onHotPunch: (sourceKey: string) => void;
	onAutoTransition: () => void;
	onFadeToBlack: () => void;
	onToggleFullscreen: () => void;
	onToggleOverlay: () => void;
	onSetTransitionType?: (type: string) => void;
	getSourceKeys: () => string[];
}

export class KeyboardHandler {
	private actions: KeyboardActions;
	private listener: ((e: KeyboardEvent) => void) | null = null;

	constructor(actions: KeyboardActions) {
		this.actions = actions;
	}

	attach() {
		this.listener = (e: KeyboardEvent) => this.handleKeydown(e);
		document.addEventListener('keydown', this.listener, true); // capture phase
	}

	detach() {
		if (this.listener) {
			document.removeEventListener('keydown', this.listener, true);
			this.listener = null;
		}
	}

	private handleKeydown(e: KeyboardEvent) {
		// Ignore when focus is in an input/textarea/select/contenteditable
		const tag = (e.target as HTMLElement)?.tagName;
		if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
		if ((e.target as HTMLElement)?.isContentEditable) return;

		// Alt+Digit shortcuts for transition type
		if (e.altKey && !e.ctrlKey && !e.metaKey) {
			if (e.code === 'Digit1') {
				e.preventDefault();
				e.stopPropagation();
				this.actions.onSetTransitionType?.('mix');
				return;
			}
			if (e.code === 'Digit2') {
				e.preventDefault();
				e.stopPropagation();
				this.actions.onSetTransitionType?.('dip');
				return;
			}
		}

		// Ignore when modifier keys are held (avoid conflicts with browser shortcuts)
		if (e.ctrlKey || e.metaKey || e.altKey) return;

		// Digit1-Digit9: preview select or hot-punch
		if (e.code.startsWith('Digit') && e.code.length === 6) {
			const digit = parseInt(e.code[5]);
			if (digit >= 1 && digit <= 9) {
				const keys = this.actions.getSourceKeys();
				const idx = digit - 1;
				if (idx < keys.length) {
					e.preventDefault();
					e.stopPropagation();
					if (e.shiftKey) {
						this.actions.onHotPunch(keys[idx]);
					} else {
						this.actions.onSetPreview(keys[idx]);
					}
				}
			}
			return;
		}

		switch (e.code) {
			case 'Space':
				e.preventDefault();
				e.stopPropagation();
				this.actions.onCut();
				break;
			case 'Enter':
				e.preventDefault();
				e.stopPropagation();
				this.actions.onAutoTransition();
				break;
			case 'F1':
				e.preventDefault();
				e.stopPropagation();
				this.actions.onFadeToBlack();
				break;
			case 'Backquote':
				e.preventDefault();
				e.stopPropagation();
				this.actions.onToggleFullscreen();
				break;
			case 'Slash':
				e.preventDefault();
				e.stopPropagation();
				this.actions.onToggleOverlay();
				break;
		}
	}
}
