import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import PlayoutTimeline from './PlayoutTimeline.svelte';
import type { PlaylistItem } from '$lib/api/types';

function makeItem(overrides: Partial<PlaylistItem> = {}): PlaylistItem {
	return {
		id: 'item-1',
		type: 'clip',
		title: 'Test Clip',
		duration_ms: 10000,
		...overrides,
	};
}

function makeItems(): PlaylistItem[] {
	return [
		makeItem({ id: 'c1', type: 'clip', title: 'Intro', duration_ms: 10000 }),
		makeItem({ id: 'l1', type: 'live', title: 'Camera 1', duration_ms: undefined }),
		makeItem({ id: 'c2', type: 'clip', title: 'Package', duration_ms: 5000 }),
		makeItem({
			id: 's1', type: 'slate', title: 'BRB', duration_ms: 3000,
			transition: { type: 'dissolve', duration_ms: 500 },
		}),
	];
}

const noop = () => {};

describe('PlayoutTimeline', () => {
	it('renders item blocks for each playlist item', () => {
		const items = makeItems();
		render(PlayoutTimeline, {
			props: {
				items,
				currentIndex: -1,
				selectedIndex: -1,
				onSelect: noop,
				onReorder: noop,
				onDelete: noop,
				onAddItem: noop,
			},
		});

		// Each item should render as a listitem
		const listItems = screen.getAllByRole('listitem');
		expect(listItems).toHaveLength(4);
	});

	it('shows type badges (CLIP, LIVE, SLATE)', () => {
		const items = makeItems();
		render(PlayoutTimeline, {
			props: {
				items,
				currentIndex: -1,
				selectedIndex: -1,
				onSelect: noop,
				onReorder: noop,
				onDelete: noop,
				onAddItem: noop,
			},
		});

		expect(screen.getAllByText('CLIP', { selector: '.block-type-badge' }).length).toBeGreaterThanOrEqual(1);
		expect(screen.getByText('LIVE', { selector: '.block-type-badge' })).toBeTruthy();
		expect(screen.getByText('SLATE', { selector: '.block-type-badge' })).toBeTruthy();
	});

	it('highlights current on-air item', () => {
		const items = makeItems();
		render(PlayoutTimeline, {
			props: {
				items,
				currentIndex: 1,
				selectedIndex: -1,
				onSelect: noop,
				onReorder: noop,
				onDelete: noop,
				onAddItem: noop,
			},
		});

		const listItems = screen.getAllByRole('listitem');
		expect(listItems[1].classList.contains('on-air')).toBe(true);
		expect(listItems[0].classList.contains('on-air')).toBe(false);
	});

	it('highlights selected item', () => {
		const items = makeItems();
		render(PlayoutTimeline, {
			props: {
				items,
				currentIndex: -1,
				selectedIndex: 2,
				onSelect: noop,
				onReorder: noop,
				onDelete: noop,
				onAddItem: noop,
			},
		});

		const listItems = screen.getAllByRole('listitem');
		expect(listItems[2].classList.contains('selected')).toBe(true);
		expect(listItems[0].classList.contains('selected')).toBe(false);
	});

	it('calls onSelect when item clicked', async () => {
		const items = makeItems();
		const onSelect = vi.fn();
		render(PlayoutTimeline, {
			props: {
				items,
				currentIndex: -1,
				selectedIndex: -1,
				onSelect,
				onReorder: noop,
				onDelete: noop,
				onAddItem: noop,
			},
		});

		const listItems = screen.getAllByRole('listitem');
		await fireEvent.click(listItems[2]);
		expect(onSelect).toHaveBeenCalledWith(2);
	});

	it('shows secondary event dots', () => {
		const items = [
			makeItem({
				id: 'c1',
				title: 'With Events',
				secondary_events: [
					{ type: 'graphics_on', offset_ms: 0, anchor: 'start', layer: 1 },
					{ type: 'audio_level', offset_ms: 500, anchor: 'start', source_key: 'cam1', level_db: -6 },
					{ type: 'scte35_cue', offset_ms: -1000, anchor: 'end' },
				],
			}),
		];
		render(PlayoutTimeline, {
			props: {
				items,
				currentIndex: -1,
				selectedIndex: -1,
				onSelect: noop,
				onReorder: noop,
				onDelete: noop,
				onAddItem: noop,
			},
		});

		const dots = document.querySelectorAll('.event-dot');
		expect(dots).toHaveLength(3);
	});

	it('shows transition indicators between items', () => {
		// Item at index 3 has a dissolve transition configured
		const items = makeItems();
		render(PlayoutTimeline, {
			props: {
				items,
				currentIndex: -1,
				selectedIndex: -1,
				onSelect: noop,
				onReorder: noop,
				onDelete: noop,
				onAddItem: noop,
			},
		});

		const diamonds = document.querySelectorAll('.transition-diamond');
		expect(diamonds.length).toBeGreaterThanOrEqual(1);
		// The dissolve indicator should show "D"
		expect(diamonds[0].textContent).toBe('D');
	});

	it('renders empty state when no items', () => {
		render(PlayoutTimeline, {
			props: {
				items: [],
				currentIndex: -1,
				selectedIndex: -1,
				onSelect: noop,
				onReorder: noop,
				onDelete: noop,
				onAddItem: noop,
			},
		});

		expect(screen.getByText('No items in playlist')).toBeTruthy();
	});

	it('calls onDelete via context action', async () => {
		const items = makeItems();
		const onDelete = vi.fn();
		render(PlayoutTimeline, {
			props: {
				items,
				currentIndex: -1,
				selectedIndex: -1,
				onSelect: noop,
				onReorder: noop,
				onDelete,
				onAddItem: noop,
			},
		});

		const listItems = screen.getAllByRole('listitem');
		await fireEvent.contextMenu(listItems[1]);

		const deleteBtn = screen.getByText('Delete');
		await fireEvent.click(deleteBtn);
		expect(onDelete).toHaveBeenCalledWith(1);
	});
});
