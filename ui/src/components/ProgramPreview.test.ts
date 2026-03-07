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
		ftbActive: false,
		audioChannels: undefined,
		masterLevel: 0,
		programPeak: [0, 0] as [number, number],
		tallyState: { cam1: 'program' as const, cam2: 'preview' as const },
		sources: {
			cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' as const },
			cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' as const },
		},
		seq: 1,
		timestamp: Date.now(),
	};

	it('should render canvas in program window', () => {
		const { container } = render(ProgramPreview, { props: { state } });
		const programCanvas = container.querySelector('.program-monitor canvas');
		expect(programCanvas).toBeTruthy();
	});

	it('should render canvas in preview window', () => {
		const { container } = render(ProgramPreview, { props: { state } });
		const previewCanvas = container.querySelector('.preview-monitor canvas');
		expect(previewCanvas).toBeTruthy();
	});

	it('should render canvases without id attributes', () => {
		const { container } = render(ProgramPreview, { props: { state } });
		const canvases = container.querySelectorAll('canvas');
		for (const canvas of canvases) {
			expect(canvas.id).toBe('');
		}
	});

	it('should show source label in program window', () => {
		const { container } = render(ProgramPreview, { props: { state } });
		expect(container.textContent).toContain('Camera 1');
	});

	it('should not show health alarm when program source is healthy', () => {
		const { container } = render(ProgramPreview, { props: { state } });
		expect(container.querySelector('.health-alarm')).toBeNull();
	});

	it('should show health alarm when program source is offline', () => {
		const degradedState = {
			...state,
			sources: {
				...state.sources,
				cam1: { ...state.sources.cam1, status: 'offline' as const },
			},
		};
		const { container } = render(ProgramPreview, { props: { state: degradedState } });
		const alarm = container.querySelector('.health-alarm');
		expect(alarm).toBeTruthy();
		expect(alarm?.textContent).toContain('Camera 1');
		expect(alarm?.textContent).toContain('OFFLINE');
	});

	it('should show health alarm only on program monitor, not preview', () => {
		const degradedState = {
			...state,
			sources: {
				...state.sources,
				cam1: { ...state.sources.cam1, status: 'no_signal' as const },
			},
		};
		const { container } = render(ProgramPreview, { props: { state: degradedState } });
		const programAlarm = container.querySelector('.program-monitor .health-alarm');
		const previewAlarm = container.querySelector('.preview-monitor .health-alarm');
		expect(programAlarm).toBeTruthy();
		expect(previewAlarm).toBeNull();
	});
});
