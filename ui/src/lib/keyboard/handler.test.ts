import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { KeyboardHandler } from './handler';

describe('KeyboardHandler', () => {
	let handler: KeyboardHandler;
	let actions: {
		cut: ReturnType<typeof vi.fn>;
		setPreview: ReturnType<typeof vi.fn>;
		hotPunch: ReturnType<typeof vi.fn>;
		toggleOverlay: ReturnType<typeof vi.fn>;
	};

	beforeEach(() => {
		actions = {
			cut: vi.fn(),
			setPreview: vi.fn(),
			hotPunch: vi.fn(),
			toggleOverlay: vi.fn(),
		};
		handler = new KeyboardHandler({
			onCut: actions.cut,
			onSetPreview: actions.setPreview,
			onHotPunch: actions.hotPunch,
			onAutoTransition: vi.fn(),
			onFadeToBlack: vi.fn(),
			onToggleFullscreen: vi.fn(),
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
