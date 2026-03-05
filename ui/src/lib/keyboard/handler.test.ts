import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { KeyboardHandler } from './handler';
import { getConfirmMode, setConfirmMode } from '$lib/state/preferences.svelte';

vi.mock('$lib/state/preferences.svelte', () => {
	let _confirmMode = false;
	return {
		getConfirmMode: vi.fn(() => _confirmMode),
		setConfirmMode: vi.fn((val: boolean) => { _confirmMode = val; }),
	};
});

describe('KeyboardHandler', () => {
	let handler: KeyboardHandler;
	let actions: {
		cut: ReturnType<typeof vi.fn>;
		setPreview: ReturnType<typeof vi.fn>;
		hotPunch: ReturnType<typeof vi.fn>;
		autoTransition: ReturnType<typeof vi.fn>;
		fadeToBlack: ReturnType<typeof vi.fn>;
		toggleFullscreen: ReturnType<typeof vi.fn>;
		toggleOverlay: ReturnType<typeof vi.fn>;
	};

	beforeEach(() => {
		actions = {
			cut: vi.fn(),
			setPreview: vi.fn(),
			hotPunch: vi.fn(),
			autoTransition: vi.fn(),
			fadeToBlack: vi.fn(),
			toggleFullscreen: vi.fn(),
			toggleOverlay: vi.fn(),
		};
		handler = new KeyboardHandler({
			onCut: actions.cut,
			onSetPreview: actions.setPreview,
			onHotPunch: actions.hotPunch,
			onAutoTransition: actions.autoTransition,
			onFadeToBlack: actions.fadeToBlack,
			onToggleFullscreen: actions.toggleFullscreen,
			onToggleOverlay: actions.toggleOverlay,
			getSourceKeys: () => ['cam1', 'cam2', 'cam3'],
		});
		handler.attach();
	});

	afterEach(() => {
		handler.detach();
	});

	function press(code: string, opts: Partial<KeyboardEventInit> = {}) {
		const event = new KeyboardEvent('keydown', {
			code,
			bubbles: true,
			cancelable: true,
			...opts,
		});
		document.dispatchEvent(event);
		return event;
	}

	it('Space dispatches cut', () => {
		press('Space');
		expect(actions.cut).toHaveBeenCalled();
	});

	it('Digit1 selects preview source at index 0', () => {
		press('Digit1');
		expect(actions.setPreview).toHaveBeenCalledWith('cam1');
	});

	it('Digit3 selects preview source at index 2', () => {
		press('Digit3');
		expect(actions.setPreview).toHaveBeenCalledWith('cam3');
	});

	it('Digit5 does nothing when only 3 sources', () => {
		press('Digit5');
		expect(actions.setPreview).not.toHaveBeenCalled();
	});

	it('Shift+Digit1 dispatches hot-punch', () => {
		press('Digit1', { shiftKey: true });
		expect(actions.hotPunch).toHaveBeenCalledWith('cam1');
	});

	it('Slash toggles keyboard overlay', () => {
		press('Slash');
		expect(actions.toggleOverlay).toHaveBeenCalled();
	});

	it('Enter dispatches auto-transition', () => {
		press('Enter');
		expect(actions.autoTransition).toHaveBeenCalled();
	});

	it('F1 dispatches fade-to-black', () => {
		press('F1');
		expect(actions.fadeToBlack).toHaveBeenCalled();
	});

	it('F2 dispatches toggle-dsk', () => {
		const toggleDSK = vi.fn();
		handler.detach();
		handler = new KeyboardHandler({
			...handler['actions'],
			onToggleDSK: toggleDSK,
		});
		handler.attach();
		press('F2');
		expect(toggleDSK).toHaveBeenCalled();
	});

	it('Backquote dispatches toggle-fullscreen', () => {
		press('Backquote');
		expect(actions.toggleFullscreen).toHaveBeenCalled();
	});

	it('Ctrl+Space does not dispatch cut', () => {
		press('Space', { ctrlKey: true });
		expect(actions.cut).not.toHaveBeenCalled();
	});

	it('Meta+Digit1 does not dispatch preview', () => {
		press('Digit1', { metaKey: true });
		expect(actions.setPreview).not.toHaveBeenCalled();
	});

	it('ignores events when input is focused', () => {
		const input = document.createElement('input');
		document.body.appendChild(input);
		input.focus();
		const event = new KeyboardEvent('keydown', {
			code: 'Space',
			bubbles: true,
			cancelable: true,
		});
		input.dispatchEvent(event);
		expect(actions.cut).not.toHaveBeenCalled();
		document.body.removeChild(input);
	});
});

describe('Transition keyboard shortcuts', () => {
	it('should handle Alt+1 for Mix type', () => {
		const onSetTransitionType = vi.fn();
		const handler = new KeyboardHandler({
			onCut: vi.fn(),
			onSetPreview: vi.fn(),
			onHotPunch: vi.fn(),
			onAutoTransition: vi.fn(),
			onFadeToBlack: vi.fn(),
			onToggleFullscreen: vi.fn(),
			onToggleOverlay: vi.fn(),
			onSetTransitionType,
			getSourceKeys: () => ['cam1'],
		});
		handler.attach();

		const event = new KeyboardEvent('keydown', {
			code: 'Digit1',
			altKey: true,
			bubbles: true,
		});
		document.dispatchEvent(event);

		expect(onSetTransitionType).toHaveBeenCalledWith('mix');
		handler.detach();
	});

	it('should handle Alt+2 for Dip type', () => {
		const onSetTransitionType = vi.fn();
		const handler = new KeyboardHandler({
			onCut: vi.fn(),
			onSetPreview: vi.fn(),
			onHotPunch: vi.fn(),
			onAutoTransition: vi.fn(),
			onFadeToBlack: vi.fn(),
			onToggleFullscreen: vi.fn(),
			onToggleOverlay: vi.fn(),
			onSetTransitionType,
			getSourceKeys: () => ['cam1'],
		});
		handler.attach();

		const event = new KeyboardEvent('keydown', {
			code: 'Digit2',
			altKey: true,
			bubbles: true,
		});
		document.dispatchEvent(event);

		expect(onSetTransitionType).toHaveBeenCalledWith('dip');
		handler.detach();
	});
});

describe('confirm mode', () => {
	let handler: KeyboardHandler;
	let actions: {
		cut: ReturnType<typeof vi.fn>;
		setPreview: ReturnType<typeof vi.fn>;
		hotPunch: ReturnType<typeof vi.fn>;
		autoTransition: ReturnType<typeof vi.fn>;
		fadeToBlack: ReturnType<typeof vi.fn>;
		toggleFullscreen: ReturnType<typeof vi.fn>;
		toggleOverlay: ReturnType<typeof vi.fn>;
	};

	function press(code: string, opts: Partial<KeyboardEventInit> = {}) {
		const event = new KeyboardEvent('keydown', {
			code,
			bubbles: true,
			cancelable: true,
			...opts,
		});
		document.dispatchEvent(event);
		return event;
	}

	beforeEach(() => {
		vi.useFakeTimers();
		// Enable confirm mode
		vi.mocked(getConfirmMode).mockReturnValue(true);
		actions = {
			cut: vi.fn(),
			setPreview: vi.fn(),
			hotPunch: vi.fn(),
			autoTransition: vi.fn(),
			fadeToBlack: vi.fn(),
			toggleFullscreen: vi.fn(),
			toggleOverlay: vi.fn(),
		};
		handler = new KeyboardHandler({
			onCut: actions.cut,
			onSetPreview: actions.setPreview,
			onHotPunch: actions.hotPunch,
			onAutoTransition: actions.autoTransition,
			onFadeToBlack: actions.fadeToBlack,
			onToggleFullscreen: actions.toggleFullscreen,
			onToggleOverlay: actions.toggleOverlay,
			getSourceKeys: () => ['cam1', 'cam2', 'cam3'],
		});
		handler.attach();
	});

	afterEach(() => {
		handler.detach();
		vi.useRealTimers();
	});

	it('single Space does not execute cut when confirm mode is on', () => {
		press('Space');
		expect(actions.cut).not.toHaveBeenCalled();
	});

	it('single Space sets pendingConfirmAction to "cut"', () => {
		press('Space');
		expect(handler.pendingConfirmAction).toBe('cut');
	});

	it('double Space within 500ms executes cut', () => {
		press('Space');
		expect(actions.cut).not.toHaveBeenCalled();
		press('Space');
		expect(actions.cut).toHaveBeenCalledOnce();
	});

	it('double Space clears pendingConfirmAction after execution', () => {
		press('Space');
		press('Space');
		expect(handler.pendingConfirmAction).toBeNull();
	});

	it('Space then timeout clears pending', () => {
		press('Space');
		expect(handler.pendingConfirmAction).toBe('cut');
		vi.advanceTimersByTime(500);
		expect(handler.pendingConfirmAction).toBeNull();
	});

	it('Space after timeout does not execute cut', () => {
		press('Space');
		vi.advanceTimersByTime(501);
		press('Space');
		expect(actions.cut).not.toHaveBeenCalled();
		// Second press sets new pending
		expect(handler.pendingConfirmAction).toBe('cut');
	});

	it('single Shift+Digit does not execute hot-punch', () => {
		press('Digit1', { shiftKey: true });
		expect(actions.hotPunch).not.toHaveBeenCalled();
	});

	it('single Shift+Digit sets pendingConfirmAction to "hotpunch"', () => {
		press('Digit1', { shiftKey: true });
		expect(handler.pendingConfirmAction).toBe('hotpunch');
	});

	it('double Shift+Digit within 500ms executes hot-punch', () => {
		press('Digit1', { shiftKey: true });
		expect(actions.hotPunch).not.toHaveBeenCalled();
		press('Digit1', { shiftKey: true });
		expect(actions.hotPunch).toHaveBeenCalledWith('cam1');
	});

	it('Shift+Digit1 then Shift+Digit2 does not execute (different key)', () => {
		press('Digit1', { shiftKey: true });
		press('Digit2', { shiftKey: true });
		expect(actions.hotPunch).not.toHaveBeenCalled();
		// Should now have pending for cam2
		expect(handler.pendingConfirmAction).toBe('hotpunch');
	});

	it('confirm mode off bypasses double-press requirement for cut', () => {
		vi.mocked(getConfirmMode).mockReturnValue(false);
		press('Space');
		expect(actions.cut).toHaveBeenCalledOnce();
		expect(handler.pendingConfirmAction).toBeNull();
	});

	it('confirm mode off bypasses double-press requirement for hot-punch', () => {
		vi.mocked(getConfirmMode).mockReturnValue(false);
		press('Digit1', { shiftKey: true });
		expect(actions.hotPunch).toHaveBeenCalledWith('cam1');
		expect(handler.pendingConfirmAction).toBeNull();
	});

	it('AUTO is not gated by confirm mode', () => {
		press('Enter');
		expect(actions.autoTransition).toHaveBeenCalledOnce();
	});

	it('FTB is not gated by confirm mode', () => {
		press('F1');
		expect(actions.fadeToBlack).toHaveBeenCalledOnce();
	});

	it('preview select is not gated by confirm mode', () => {
		press('Digit1');
		expect(actions.setPreview).toHaveBeenCalledWith('cam1');
	});
});
