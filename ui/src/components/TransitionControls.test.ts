import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import TransitionControls from './TransitionControls.svelte';

const baseState = {
	programSource: 'cam1',
	previewSource: 'cam2',
	transitionType: 'cut',
	transitionDurationMs: 0,
	transitionPosition: 0,
	inTransition: false,
	ftbActive: false,
	audioLevels: null,
	audioChannels: null,
	masterLevel: 0,
	programPeak: [0, 0] as [number, number],
	tallyState: { cam1: 'program' as const, cam2: 'preview' as const },
	sources: {
		cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' as const, lastFrameTime: 0 },
		cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' as const, lastFrameTime: 0 },
	},
	seq: 1,
	timestamp: Date.now(),
};

describe('TransitionControls', () => {
	it('should render CUT, AUTO, and FTB buttons', () => {
		const { container } = render(TransitionControls, { props: { state: baseState } });
		const buttons = container.querySelectorAll('.btn');
		expect(buttons.length).toBeGreaterThanOrEqual(3);
		expect(container.textContent).toContain('CUT');
		expect(container.textContent).toContain('AUTO');
		expect(container.textContent).toContain('FTB');
	});

	it('should enable AUTO when preview is set and not in transition', () => {
		const { container } = render(TransitionControls, { props: { state: baseState } });
		const autoBtn = container.querySelector('.btn.auto') as HTMLButtonElement;
		expect(autoBtn.disabled).toBe(false);
	});

	it('should disable AUTO when no preview source', () => {
		const state = { ...baseState, previewSource: '' };
		const { container } = render(TransitionControls, { props: { state } });
		const autoBtn = container.querySelector('.btn.auto') as HTMLButtonElement;
		expect(autoBtn.disabled).toBe(true);
	});

	it('should disable AUTO during transition', () => {
		const state = { ...baseState, inTransition: true };
		const { container } = render(TransitionControls, { props: { state } });
		const autoBtn = container.querySelector('.btn.auto') as HTMLButtonElement;
		expect(autoBtn.disabled).toBe(true);
	});

	it('should disable AUTO when FTB is active', () => {
		const state = { ...baseState, ftbActive: true };
		const { container } = render(TransitionControls, { props: { state } });
		const autoBtn = container.querySelector('.btn.auto') as HTMLButtonElement;
		expect(autoBtn.disabled).toBe(true);
	});

	it('should enable FTB when not in mix/dip transition', () => {
		const { container } = render(TransitionControls, { props: { state: baseState } });
		const ftbBtn = container.querySelector('.btn.ftb') as HTMLButtonElement;
		expect(ftbBtn.disabled).toBe(false);
	});

	it('should show FTB active state', () => {
		const state = { ...baseState, ftbActive: true };
		const { container } = render(TransitionControls, { props: { state } });
		const ftbBtn = container.querySelector('.btn.ftb') as HTMLButtonElement;
		expect(ftbBtn.classList.contains('active')).toBe(true);
	});

	it('should render T-bar slider', () => {
		const { container } = render(TransitionControls, { props: { state: baseState } });
		const tbar = container.querySelector('.tbar-slider');
		expect(tbar).toBeTruthy();
	});

	it('should render transition type selector', () => {
		const { container } = render(TransitionControls, { props: { state: baseState } });
		expect(container.textContent).toContain('Mix');
		expect(container.textContent).toContain('Dip');
	});

	it('should render duration selector', () => {
		const { container } = render(TransitionControls, { props: { state: baseState } });
		const select = container.querySelector('.duration-select');
		expect(select).toBeTruthy();
	});

	it('should show T-bar position during transition', () => {
		const state = { ...baseState, inTransition: true, transitionPosition: 0.5 };
		const { container } = render(TransitionControls, { props: { state } });
		const tbar = container.querySelector('.tbar-slider') as HTMLInputElement;
		if (tbar) {
			expect(parseFloat(tbar.value)).toBeCloseTo(0.5, 1);
		}
	});
});
