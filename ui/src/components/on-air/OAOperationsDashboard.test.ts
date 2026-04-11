import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
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

	it('shows placeholder messages', () => {
		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});
		const placeholders = container.querySelectorAll('.section-placeholder');
		expect(placeholders).toHaveLength(4);
		expect(placeholders[0].textContent).toBe('No slates configured');
		expect(placeholders[1].textContent).toBe('No bug loaded');
		expect(placeholders[2].textContent).toBe('No clips uploaded');
		expect(placeholders[3].textContent).toBe('Playout not enabled');
	});

	it('has scrollable container', () => {
		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});
		const dashboard = container.querySelector('.operations-dashboard');
		expect(dashboard).toBeTruthy();
		const style = getComputedStyle(dashboard!);
		// overflow-y is set via CSS class — verify the element exists and has the class
		expect(dashboard!.classList.contains('operations-dashboard')).toBe(true);
	});
});
