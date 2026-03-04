import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import OutputControls from './OutputControls.svelte';

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

describe('OutputControls', () => {
	it('should render recording control', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		expect(container.textContent).toContain('REC');
	});

	it('should render SRT button', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		expect(container.textContent).toContain('SRT');
	});

	it('should show SRT active indicator', () => {
		const state = {
			...baseState,
			srtOutput: { active: true, mode: 'caller' as const, state: 'active' },
		};
		const { container } = render(OutputControls, { props: { state } });
		const indicator = container.querySelector('.srt-active');
		expect(indicator).toBeTruthy();
	});

	it('should not show SRT active indicator when inactive', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		const indicator = container.querySelector('.srt-active');
		expect(indicator).toBeFalsy();
	});

	it('should have output-controls container', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		const controls = container.querySelector('.output-controls');
		expect(controls).toBeTruthy();
	});
});
