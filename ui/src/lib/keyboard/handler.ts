import { getConfirmMode } from '$lib/state/preferences.svelte';

export interface KeyboardActions {
	onCut: () => void;
	onSetPreview: (sourceKey: string) => void;
	onHotPunch: (sourceKey: string) => void;
	onAutoTransition: () => void;
	onFadeToBlack: () => void;
	onToggleFullscreen: () => void;
	onToggleOverlay: () => void;
	onSetTransitionType?: (type: string) => void;
	onToggleDSK?: () => void;
	onRunMacro?: (slotIndex: number) => void;
	scte35Break?: () => void;
	scte35Return?: () => void;
	scte35Hold?: () => void;
	scte35Extend?: () => void;
	layoutTogglePIP?: () => void;
	clipPlayerToggle?: (playerID: number) => void;
	clipPlayerStop?: (playerID: number) => void;
	replayQuickPreset?: (index: number) => void;
	replayMarkIn?: () => void;
	replayMarkOut?: () => void;
	replayPlayPause?: () => void;
	replayStop?: () => void;
	replaySpeedDown?: () => void;
	replaySpeedUp?: () => void;
	replayFrameBack?: () => void;
	replayFrameForward?: () => void;
	commsToggleMute?: () => void;
	isReplayTabActive?: () => boolean;
	getSourceKeys: () => string[];
}

export class KeyboardHandler {
	private actions: KeyboardActions;
	private listener: ((e: KeyboardEvent) => void) | null = null;
	private pendingConfirm: { action: string; key?: string; timestamp: number } | null = null;
	private confirmTimeout: ReturnType<typeof setTimeout> | null = null;

	get pendingConfirmAction(): string | null {
		return this.pendingConfirm?.action ?? null;
	}

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
		this.clearPendingConfirm();
	}

	private setPendingConfirm(action: string, key?: string) {
		this.clearPendingConfirm();
		this.pendingConfirm = { action, key, timestamp: Date.now() };
		this.confirmTimeout = setTimeout(() => {
			this.pendingConfirm = null;
			this.confirmTimeout = null;
		}, 500);
	}

	private clearPendingConfirm() {
		if (this.confirmTimeout) {
			clearTimeout(this.confirmTimeout);
			this.confirmTimeout = null;
		}
		this.pendingConfirm = null;
	}

	private handleKeydown(e: KeyboardEvent) {
		// Ignore key repeats (holding a key down) to prevent rapid toggling
		if (e.repeat) return;

		// Ignore when focus is in an input/textarea/select/contenteditable
		const tag = (e.target as HTMLElement)?.tagName;
		if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
		if ((e.target as HTMLElement)?.isContentEditable) return;

		// Ctrl+Digit shortcuts for macro triggers (Ctrl+1-9)
		if (e.ctrlKey && !e.metaKey && !e.altKey) {
			if (e.code.startsWith('Digit') && e.code.length === 6) {
				const digit = parseInt(e.code[5]);
				if (digit >= 1 && digit <= 9) {
					e.preventDefault();
					e.stopPropagation();
					this.actions.onRunMacro?.(digit - 1);
					return;
				}
			}
		}

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

		// Backtick: toggle comms mute
		if (e.code === 'Backquote' && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
			e.preventDefault();
			e.stopPropagation();
			this.actions.commsToggleMute?.();
			return;
		}

		// Ignore when modifier keys are held (avoid conflicts with browser shortcuts)
		if (e.ctrlKey || e.metaKey || e.altKey) return;

		// Shift+1/2/3: Quick replay presets.
		if (e.shiftKey) {
			const replayMatch = e.code.match(/^Digit([1-3])$/);
			if (replayMatch && this.actions.replayQuickPreset) {
				e.preventDefault();
				e.stopPropagation();
				this.actions.replayQuickPreset(parseInt(replayMatch[1]) - 1);
				return;
			}
		}

		// Shift+letter shortcuts for SCTE-35 operations
		if (e.shiftKey) {
			switch (e.code) {
				case 'KeyB':
					e.preventDefault();
					e.stopPropagation();
					this.actions.scte35Break?.();
					return;
				case 'KeyR':
					e.preventDefault();
					e.stopPropagation();
					this.actions.scte35Return?.();
					return;
				case 'KeyH':
					e.preventDefault();
					e.stopPropagation();
					this.actions.scte35Hold?.();
					return;
				case 'KeyE':
					e.preventDefault();
					e.stopPropagation();
					this.actions.scte35Extend?.();
					return;
			}
		}

		// F5-F8: clip player controls
		if (e.code.startsWith('F') && !e.ctrlKey && !e.altKey && !e.metaKey) {
			const num = parseInt(e.code.slice(1));
			if (num >= 5 && num <= 8) {
				e.preventDefault();
				e.stopPropagation();
				const playerID = num - 4; // F5=Player 1, F6=Player 2, F7=Player 3, F8=Player 4
				if (e.shiftKey) {
					this.actions.clipPlayerStop?.(playerID);
				} else {
					this.actions.clipPlayerToggle?.(playerID);
				}
				return;
			}
		}

		// Replay shortcuts (only when replay tab is active).
		if (this.actions.isReplayTabActive?.()) {
			if (!e.shiftKey) {
				switch (e.code) {
					case 'KeyI':
						if (this.actions.replayMarkIn) { e.preventDefault(); e.stopPropagation(); this.actions.replayMarkIn(); return; }
						break;
					case 'KeyO':
						if (this.actions.replayMarkOut) { e.preventDefault(); e.stopPropagation(); this.actions.replayMarkOut(); return; }
						break;
					case 'Space':
						if (this.actions.replayPlayPause) { e.preventDefault(); e.stopPropagation(); this.actions.replayPlayPause(); return; }
						break;
					case 'Escape':
						if (this.actions.replayStop) { e.preventDefault(); e.stopPropagation(); this.actions.replayStop(); return; }
						break;
					case 'KeyJ':
						if (this.actions.replaySpeedDown) { e.preventDefault(); e.stopPropagation(); this.actions.replaySpeedDown(); return; }
						break;
					case 'KeyK':
						if (this.actions.replayPlayPause) { e.preventDefault(); e.stopPropagation(); this.actions.replayPlayPause(); return; }
						break;
					case 'KeyL':
						if (this.actions.replaySpeedUp) { e.preventDefault(); e.stopPropagation(); this.actions.replaySpeedUp(); return; }
						break;
					case 'ArrowLeft':
						if (this.actions.replayFrameBack) { e.preventDefault(); e.stopPropagation(); this.actions.replayFrameBack(); return; }
						break;
					case 'ArrowRight':
						if (this.actions.replayFrameForward) { e.preventDefault(); e.stopPropagation(); this.actions.replayFrameForward(); return; }
						break;
				}
			}
		}

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
						if (getConfirmMode()) {
							if (this.pendingConfirm?.action === 'hotpunch' && this.pendingConfirm?.key === keys[idx]) {
								this.clearPendingConfirm();
								this.actions.onHotPunch(keys[idx]);
							} else {
								this.setPendingConfirm('hotpunch', keys[idx]);
							}
						} else {
							this.actions.onHotPunch(keys[idx]);
						}
					} else {
						this.actions.onSetPreview(keys[idx]);
					}
				}
			}
			return;
		}

		// Layout/PIP shortcuts (no modifier)
		if ((e.code === 'KeyP' && !e.shiftKey) || e.code === 'F3') {
			e.preventDefault();
			e.stopPropagation();
			this.actions.layoutTogglePIP?.();
			return;
		}

		switch (e.code) {
			case 'Space':
				e.preventDefault();
				e.stopPropagation();
				if (getConfirmMode()) {
					if (this.pendingConfirm?.action === 'cut') {
						this.clearPendingConfirm();
						this.actions.onCut();
					} else {
						this.setPendingConfirm('cut');
					}
				} else {
					this.actions.onCut();
				}
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
			case 'F2':
				e.preventDefault();
				e.stopPropagation();
				this.actions.onToggleDSK?.();
				break;
			case 'F4':
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
