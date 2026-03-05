import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';
import { tick } from 'svelte';
import TransitionControls from './TransitionControls.svelte';

vi.mock('$lib/api/switch-api', () => ({
	cut: vi.fn().mockResolvedValue({}),
	startTransition: vi.fn().mockResolvedValue({}),
	setTransitionPosition: vi.fn().mockResolvedValue(undefined),
	fadeToBlack: vi.fn().mockResolvedValue({}),
	fireAndForget: (p: Promise<unknown>) => p?.catch?.(() => {}),
}));

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

	describe('ARIA labels', () => {
		it('should have aria-label on T-bar slider', () => {
			const { container } = render(TransitionControls, { props: { state: baseState } });
			const tbar = container.querySelector('.tbar-slider') as HTMLInputElement;
			expect(tbar.getAttribute('aria-label')).toBe('Transition position');
		});

		it('should have aria-label on duration select', () => {
			const { container } = render(TransitionControls, { props: { state: baseState } });
			const select = container.querySelector('.duration-select') as HTMLSelectElement;
			expect(select.getAttribute('aria-label')).toBe('Transition duration');
		});

		it('should have aria-label on transition type radio group', () => {
			const { container } = render(TransitionControls, { props: { state: baseState } });
			const group = container.querySelector('.type-selector');
			expect(group?.getAttribute('role')).toBe('radiogroup');
			expect(group?.getAttribute('aria-label')).toBe('Transition type');
		});
	});

	describe('T-bar auto-animation wiring', () => {
		it('should call startTransition API when AUTO is clicked', async () => {
			const { startTransition } = await import('$lib/api/switch-api');
			const { container } = render(TransitionControls, { props: { state: baseState } });

			const autoBtn = container.querySelector('.btn.auto') as HTMLButtonElement;
			autoBtn.click();
			await tick();

			expect(startTransition).toHaveBeenCalledWith('cam2', 'mix', 1000, undefined);
		});

		it('should still show server-driven position for manual T-bar', () => {
			// When no auto animation is active, server position drives the T-bar
			const state = { ...baseState, inTransition: true, transitionPosition: 0.75 };
			const { container } = render(TransitionControls, { props: { state } });
			const tbar = container.querySelector('.tbar-slider') as HTMLInputElement;
			expect(parseFloat(tbar.value)).toBeCloseTo(0.75, 1);
		});

		it('should show T-bar at 0 when not in transition', () => {
			const { container } = render(TransitionControls, { props: { state: baseState } });
			const tbar = container.querySelector('.tbar-slider') as HTMLInputElement;
			expect(parseFloat(tbar.value)).toBe(0);
		});
	});
});
