import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';
import PlayoutItem from './PlayoutItem.svelte';
import type { PlaylistItem, SecondaryEvent } from '$lib/api/types';

vi.mock('$lib/state/notifications.svelte', () => ({
	notify: vi.fn(),
}));

function makeItem(overrides: Partial<PlaylistItem> = {}): PlaylistItem {
	return {
		id: 'item-1',
		type: 'clip',
		title: 'Test Clip',
		duration_ms: 30000,
		...overrides,
	};
}

function makeEvent(overrides: Partial<SecondaryEvent> = {}): SecondaryEvent {
	return {
		type: 'graphics_on',
		layer: 1,
		offset_ms: 0,
		...overrides,
	};
}

describe('PlayoutItem', () => {
	it('renders item title', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem({ title: 'My Bumper' }), index: 0 },
		});
		expect(container.textContent).toContain('My Bumper');
	});

	it('shows 1-based index (index=4 shows "5")', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem(), index: 4 },
		});
		const indexEl = container.querySelector('.item-index');
		expect(indexEl).toBeTruthy();
		expect(indexEl!.textContent).toBe('5');
	});

	it('shows CLIP type badge with .clip class', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem({ type: 'clip' }), index: 0 },
		});
		const badge = container.querySelector('.item-type-badge');
		expect(badge).toBeTruthy();
		expect(badge!.textContent?.trim()).toBe('CLIP');
		expect(badge!.classList.contains('clip')).toBe(true);
	});

	it('shows LIVE type badge with .live class', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem({ type: 'live' }), index: 0 },
		});
		const badge = container.querySelector('.item-type-badge');
		expect(badge).toBeTruthy();
		expect(badge!.textContent?.trim()).toBe('LIVE');
		expect(badge!.classList.contains('live')).toBe(true);
	});

	it('shows SLATE type badge with .slate class', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem({ type: 'slate' }), index: 0 },
		});
		const badge = container.querySelector('.item-type-badge');
		expect(badge).toBeTruthy();
		expect(badge!.textContent?.trim()).toBe('SLATE');
		expect(badge!.classList.contains('slate')).toBe(true);
	});

	it('shows formatted duration (90000ms -> "1:30")', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem({ duration_ms: 90000 }), index: 0 },
		});
		const duration = container.querySelector('.item-duration');
		expect(duration).toBeTruthy();
		expect(duration!.textContent).toBe('1:30');
	});

	it('shows "--:--" for missing duration', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem({ duration_ms: undefined }), index: 0 },
		});
		const duration = container.querySelector('.item-duration');
		expect(duration).toBeTruthy();
		expect(duration!.textContent).toBe('--:--');
	});

	it('shows HOLD badge when hold: true', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem({ hold: true }), index: 0 },
		});
		const holdBadge = container.querySelector('.hold-badge');
		expect(holdBadge).toBeTruthy();
		expect(holdBadge!.textContent).toBe('HOLD');
	});

	it('does not show HOLD badge when hold: false', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem({ hold: false }), index: 0 },
		});
		const holdBadge = container.querySelector('.hold-badge');
		expect(holdBadge).toBeFalsy();
	});

	it('applies .on-air class when isOnAir', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem(), index: 0, isOnAir: true },
		});
		const el = container.querySelector('.playout-item');
		expect(el).toBeTruthy();
		expect(el!.classList.contains('on-air')).toBe(true);
	});

	it('applies .next class when isNext', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem(), index: 0, isNext: true },
		});
		const el = container.querySelector('.playout-item');
		expect(el).toBeTruthy();
		expect(el!.classList.contains('next')).toBe(true);
	});

	it('applies .completed class with .strikethrough title', () => {
		const { container } = render(PlayoutItem, {
			props: { item: makeItem(), index: 0, isCompleted: true },
		});
		const el = container.querySelector('.playout-item');
		expect(el).toBeTruthy();
		expect(el!.classList.contains('completed')).toBe(true);

		const title = container.querySelector('.item-title');
		expect(title).toBeTruthy();
		expect(title!.classList.contains('strikethrough')).toBe(true);
	});

	it('shows secondary events toggle when events exist ("Events (1)")', () => {
		const { container } = render(PlayoutItem, {
			props: {
				item: makeItem({
					secondary_events: [makeEvent()],
				}),
				index: 0,
			},
		});
		expect(container.textContent).toContain('Events (1)');
	});
});
