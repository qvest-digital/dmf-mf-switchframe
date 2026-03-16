import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';
import { fireEvent } from '@testing-library/svelte';
import OutputControls from './OutputControls.svelte';
import { getConfirmMode, setConfirmMode } from '$lib/state/preferences.svelte';

const baseState = {
	programSource: 'cam1',
	previewSource: 'cam2',
	transitionType: 'cut',
	transitionDurationMs: 0,
	transitionPosition: 0,
	inTransition: false,
	ftbActive: false,
	audioChannels: undefined,
	masterLevel: 0,
	programPeak: [0, 0] as [number, number],
	tallyState: { cam1: 'program' as const, cam2: 'preview' as const },
	sources: {
		cam1: { key: 'cam1', label: 'Camera 1', type: 'demo' as const, status: 'healthy' as const },
		cam2: { key: 'cam2', label: 'Camera 2', type: 'demo' as const, status: 'healthy' as const },
	},
	seq: 1,
	timestamp: Date.now(),
};

describe('OutputControls', () => {
	it('should render recording control', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		expect(container.textContent).toContain('REC');
	});

	it('should render I/O button', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		expect(container.textContent).toContain('I/O');
	});

	it('should show I/O active indicator when ioPanelVisible', () => {
		const { container } = render(OutputControls, { props: { state: baseState, ioPanelVisible: true } });
		const indicator = container.querySelector('.io-active');
		expect(indicator).toBeTruthy();
	});

	it('should show I/O active indicator when SRT output is active', () => {
		const state = {
			...baseState,
			srtOutput: { active: true, mode: 'caller' as const, state: 'active' },
		};
		const { container } = render(OutputControls, { props: { state } });
		const indicator = container.querySelector('.io-active');
		expect(indicator).toBeTruthy();
	});

	it('should not show I/O active indicator when inactive and panel closed', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		const indicator = container.querySelector('.io-active');
		expect(indicator).toBeFalsy();
	});

	it('should have output-controls container', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		const controls = container.querySelector('.output-controls');
		expect(controls).toBeTruthy();
	});

	it('should render a CONFIRM button', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		expect(container.textContent).toContain('CONFIRM');
	});

	it('should toggle confirm mode on click', async () => {
		setConfirmMode(false);
		const { container } = render(OutputControls, { props: { state: baseState } });
		const btn = container.querySelector('.confirm-btn') as HTMLButtonElement;
		expect(btn).toBeTruthy();
		expect(getConfirmMode()).toBe(false);
		await fireEvent.click(btn);
		expect(getConfirmMode()).toBe(true);
		await fireEvent.click(btn);
		expect(getConfirmMode()).toBe(false);
	});

	it('should show active state when confirm mode is on', () => {
		setConfirmMode(true);
		const { container } = render(OutputControls, { props: { state: baseState } });
		const btn = container.querySelector('.confirm-btn');
		expect(btn).toBeTruthy();
		expect(btn?.classList.contains('confirm-active')).toBe(true);
	});

	it('I/O button calls onToggleIOPanel callback', async () => {
		const onToggleIOPanel = vi.fn();
		const { container } = render(OutputControls, { props: { state: baseState, onToggleIOPanel } });
		const ioBtn = container.querySelector('.io-btn') as HTMLButtonElement;
		expect(ioBtn).toBeTruthy();
		await fireEvent.click(ioBtn);
		expect(onToggleIOPanel).toHaveBeenCalledOnce();
	});

	it('I/O button shows io-active class when ioPanelVisible is true', () => {
		const { container } = render(OutputControls, { props: { state: baseState, ioPanelVisible: true } });
		const ioBtn = container.querySelector('.io-btn');
		expect(ioBtn).toBeTruthy();
		expect(ioBtn?.classList.contains('io-active')).toBe(true);
	});

	it('I/O button shows io-warning class when SRT input is unhealthy', () => {
		const state = {
			...baseState,
			sources: {
				...baseState.sources,
				srt1: { key: 'srt1', label: 'SRT 1', type: 'srt' as const, status: 'no_signal' as const },
			},
		};
		const { container } = render(OutputControls, { props: { state } });
		const ioBtn = container.querySelector('.io-btn');
		expect(ioBtn).toBeTruthy();
		expect(ioBtn?.classList.contains('io-warning')).toBe(true);
	});

	it('I/O button does not show io-warning when io-active takes precedence', () => {
		const state = {
			...baseState,
			sources: {
				...baseState.sources,
				srt1: { key: 'srt1', label: 'SRT 1', type: 'srt' as const, status: 'no_signal' as const },
			},
		};
		const { container } = render(OutputControls, { props: { state, ioPanelVisible: true } });
		const ioBtn = container.querySelector('.io-btn');
		expect(ioBtn).toBeTruthy();
		// When active, io-active takes priority over io-warning
		expect(ioBtn?.classList.contains('io-active')).toBe(true);
		expect(ioBtn?.classList.contains('io-warning')).toBe(false);
	});
});
