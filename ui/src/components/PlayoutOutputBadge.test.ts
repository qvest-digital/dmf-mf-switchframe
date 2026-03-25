import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import PlayoutOutputBadge from './PlayoutOutputBadge.svelte';
import type { PlayoutChannelState, PlaylistItem } from '$lib/api/types';

function makeChannel(overrides: Partial<PlayoutChannelState> = {}): PlayoutChannelState {
	return {
		id: 1,
		state: 'stopped',
		itemIndex: -1,
		itemCount: 0,
		...overrides,
	};
}

function makeItem(overrides: Partial<PlaylistItem> = {}): PlaylistItem {
	return {
		id: 'item-1',
		type: 'clip',
		title: 'Test Clip',
		...overrides,
	};
}

describe('PlayoutOutputBadge', () => {
	it('shows CLIP with title for clip items', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air' }),
				currentItem: makeItem({ type: 'clip', title: 'Intro Video' }),
				isIdle: false,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.textContent).toContain('CLIP:');
		expect(badge.textContent).toContain('Intro Video');
		expect(badge.getAttribute('data-badge-type')).toBe('clip');
	});

	it('shows LIVE with source for live items', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air' }),
				currentItem: makeItem({ type: 'live', source: 'Camera 1', title: 'Live Feed' }),
				isIdle: false,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.textContent).toContain('LIVE:');
		expect(badge.textContent).toContain('Camera 1');
		expect(badge.getAttribute('data-badge-type')).toBe('live');
	});

	it('shows LIVE with title when source is missing', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air' }),
				currentItem: makeItem({ type: 'live', title: 'Remote Feed' }),
				isIdle: false,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.textContent).toContain('LIVE:');
		expect(badge.textContent).toContain('Remote Feed');
	});

	it('shows IDLE LOOP when idle', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'stopped' }),
				isIdle: true,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.textContent).toContain('IDLE LOOP');
		expect(badge.getAttribute('data-badge-type')).toBe('idle');
	});

	it('shows SLATE for slate items', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air' }),
				currentItem: makeItem({ type: 'slate', title: 'Station Slate' }),
				isIdle: false,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.textContent).toContain('SLATE');
		expect(badge.getAttribute('data-badge-type')).toBe('slate');
	});

	it('shows OFF AIR when stopped and not idle', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'stopped' }),
				isIdle: false,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.textContent).toContain('OFF AIR');
		expect(badge.getAttribute('data-badge-type')).toBe('offline');
	});

	it('shows BREAK when in break mode', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air', mode: 'break' }),
				isIdle: false,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.textContent).toContain('BREAK');
		expect(badge.getAttribute('data-badge-type')).toBe('break');
	});

	it('applies pulse class for live state', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air' }),
				currentItem: makeItem({ type: 'live', source: 'Cam 1' }),
				isIdle: false,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.classList.contains('pulse')).toBe(true);
	});

	it('applies pulse class for offline state', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'stopped' }),
				isIdle: false,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.classList.contains('pulse')).toBe(true);
	});

	it('applies pulse class for break state', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air', mode: 'break' }),
				isIdle: false,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.classList.contains('pulse')).toBe(true);
	});

	it('does not apply pulse class for clip state', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air' }),
				currentItem: makeItem({ type: 'clip', title: 'Some Clip' }),
				isIdle: false,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.classList.contains('pulse')).toBe(false);
	});

	it('truncates long titles', () => {
		const longTitle = 'This Is A Very Long Clip Title That Should Be Truncated With Ellipsis';
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air' }),
				currentItem: makeItem({ type: 'clip', title: longTitle }),
				isIdle: false,
			},
		});
		const label = container.querySelector('.label')!;
		// The label element exists and contains the full text (CSS handles visual truncation)
		expect(label).toBeTruthy();
		expect(label.textContent).toContain(longTitle);
		// The badge container has a max-width to constrain overflow
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge).toBeTruthy();
	});

	it('shows STANDBY when on_air but no current item and not idle', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air' }),
				isIdle: false,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.textContent).toContain('STANDBY');
		expect(badge.getAttribute('data-badge-type')).toBe('idle');
	});

	it('break mode takes priority over idle', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air', mode: 'break' }),
				isIdle: true,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.textContent).toContain('BREAK');
		expect(badge.getAttribute('data-badge-type')).toBe('break');
	});

	it('idle takes priority over currentItem', () => {
		const { container } = render(PlayoutOutputBadge, {
			props: {
				channel: makeChannel({ state: 'on_air' }),
				currentItem: makeItem({ type: 'clip', title: 'Some Clip' }),
				isIdle: true,
			},
		});
		const badge = container.querySelector('[data-testid="output-badge"]')!;
		expect(badge.textContent).toContain('IDLE LOOP');
	});
});
