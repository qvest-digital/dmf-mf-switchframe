import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import Multiview from './Multiview.svelte';

describe('Multiview with video', () => {
	it('should render canvas elements for each source', () => {
		const state = {
			programSource: 'cam1',
			previewSource: 'cam2',
			transitionType: 'cut',
			transitionDurationMs: 0,
			transitionPosition: 0,
			inTransition: false,
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

		const { container } = render(Multiview, { props: { state } });
		const canvases = container.querySelectorAll('canvas');
		expect(canvases.length).toBe(2);
	});
});
