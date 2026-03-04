import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import ProgramPreview from './ProgramPreview.svelte';

describe('ProgramPreview with video', () => {
	const state = {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 0,
		transitionPosition: 0,
		inTransition: false,
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

	it('should render canvas in program window', () => {
		const { container } = render(ProgramPreview, { props: { state } });
		const programCanvas = container.querySelector('.program-window canvas');
		expect(programCanvas).toBeTruthy();
	});

	it('should render canvas in preview window', () => {
		const { container } = render(ProgramPreview, { props: { state } });
		const previewCanvas = container.querySelector('.preview-window canvas');
		expect(previewCanvas).toBeTruthy();
	});

	it('should show source label in program window', () => {
		const { container } = render(ProgramPreview, { props: { state } });
		expect(container.textContent).toContain('Camera 1');
	});
});
