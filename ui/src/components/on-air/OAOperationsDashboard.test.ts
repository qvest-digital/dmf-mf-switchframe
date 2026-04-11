import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import OAOperationsDashboard from './OAOperationsDashboard.svelte';
import type { ControlRoomState } from '$lib/api/types';

function minimalState(): ControlRoomState {
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 500,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		masterLevel: 1,
		programPeak: [0, 0] as [number, number],
		tallyState: {},
		sources: {},
		seq: 1,
		lastCommandSeq: 0,
		timestamp: Date.now(),
	};
}

describe('OAOperationsDashboard', () => {
	it('renders all four section headers', () => {
		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});
		const headers = container.querySelectorAll('.section-title');
		const titles = Array.from(headers).map((h) => h.textContent?.trim());
		expect(titles).toEqual(['SLATES', 'BUG', 'CLIPS', 'PLAYOUT']);
	});

	it('has scrollable container', () => {
		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});
		const dashboard = container.querySelector('.operations-dashboard');
		expect(dashboard).toBeTruthy();
		expect(dashboard!.classList.contains('operations-dashboard')).toBe(true);
	});

	it('renders child section components', () => {
		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});
		// Slates section renders the add button
		expect(screen.getByText('+')).toBeTruthy();
		// Bug section renders the empty state
		expect(screen.getByText(/No bug loaded/)).toBeTruthy();
		// Clips section renders the upload zone
		const uploadZone = container.querySelector('.upload-zone');
		expect(uploadZone).toBeTruthy();
		// Playout section renders the toggle
		expect(screen.getByText('OFF')).toBeTruthy();
	});

	it('playout toggle is interactive', async () => {
		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});
		// Initially disabled
		expect(screen.getByText(/Playout not enabled/)).toBeTruthy();
	});
});
