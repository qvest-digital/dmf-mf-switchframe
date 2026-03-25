import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import PlayoutRundown from './PlayoutRundown.svelte';
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
		makeItem({ id: 's1', type: 'slate', title: 'BRB', duration_ms: 3000 }),
		makeItem({ id: 'c3', type: 'clip', title: 'Outro', duration_ms: 8000 }),
	];
}

const noop = () => {};

describe('PlayoutRundown', () => {
	afterEach(() => {
		vi.restoreAllMocks();
	});

	it('renders all items as rows', () => {
		const items = makeItems();
		render(PlayoutRundown, {
			props: {
				items,
				currentIndex: -1,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		const listItems = screen.getAllByRole('listitem');
		expect(listItems).toHaveLength(5);
	});

	it('shows type badges', () => {
		const items = makeItems();
		render(PlayoutRundown, {
			props: {
				items,
				currentIndex: -1,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		expect(screen.getAllByText('CLIP', { selector: '.type-badge' }).length).toBeGreaterThanOrEqual(1);
		expect(screen.getByText('LIVE', { selector: '.type-badge' })).toBeTruthy();
		expect(screen.getByText('SLATE', { selector: '.type-badge' })).toBeTruthy();
	});

	it('highlights on-air item with red border', () => {
		const items = makeItems();
		render(PlayoutRundown, {
			props: {
				items,
				currentIndex: 1,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		const listItems = screen.getAllByRole('listitem');
		expect(listItems[1].classList.contains('on-air')).toBe(true);
		expect(listItems[0].classList.contains('on-air')).toBe(false);
		expect(listItems[2].classList.contains('on-air')).toBe(false);
	});

	it('dims completed items', () => {
		const items = makeItems();
		render(PlayoutRundown, {
			props: {
				items,
				currentIndex: 2,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		const listItems = screen.getAllByRole('listitem');
		// Items 0 and 1 should be completed (before currentIndex=2)
		expect(listItems[0].classList.contains('completed')).toBe(true);
		expect(listItems[1].classList.contains('completed')).toBe(true);
		// Item 2 is on-air, not completed
		expect(listItems[2].classList.contains('completed')).toBe(false);
	});

	it('shows NEXT label on next item', () => {
		const items = makeItems();
		render(PlayoutRundown, {
			props: {
				items,
				currentIndex: 1,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		expect(screen.getByText('NEXT')).toBeTruthy();

		const listItems = screen.getAllByRole('listitem');
		expect(listItems[2].classList.contains('is-next')).toBe(true);
		expect(listItems[0].classList.contains('is-next')).toBe(false);
	});

	it('shows progress bar on current item', () => {
		const items = makeItems();
		const { container } = render(PlayoutRundown, {
			props: {
				items,
				currentIndex: 0,
				progress: 0.5,
				itemRemainMs: 5000,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		const progressFill = container.querySelector('.progress-fill');
		expect(progressFill).toBeTruthy();
		expect(progressFill!.getAttribute('style')).toContain('width: 50%');

		const remainTime = container.querySelector('.remain-time');
		expect(remainTime).toBeTruthy();
	});

	it('shows LIVE for live items instead of duration', () => {
		const items = makeItems();
		render(PlayoutRundown, {
			props: {
				items,
				currentIndex: -1,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		expect(screen.getByText('LIVE', { selector: '.live-label' })).toBeTruthy();
	});

	it('shows loop indicator for loop items', () => {
		const items = [
			makeItem({ id: 'loop1', title: 'Looping', duration_ms: 10000, loop: true }),
		];
		const { container } = render(PlayoutRundown, {
			props: {
				items,
				currentIndex: -1,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		const loopIndicator = container.querySelector('.indicator-loop');
		expect(loopIndicator).toBeTruthy();
	});

	it('shows SCTE-35 indicator for items with scte35 events', () => {
		const items = [
			makeItem({
				id: 'scte1',
				title: 'With SCTE',
				secondary_events: [
					{ type: 'scte35_cue', offset_ms: 0, anchor: 'start' },
				],
			}),
		];
		const { container } = render(PlayoutRundown, {
			props: {
				items,
				currentIndex: -1,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		const scteIndicator = container.querySelector('.indicator-scte35');
		expect(scteIndicator).toBeTruthy();
	});

	it('calls onSkipTo on future item interaction', async () => {
		vi.useFakeTimers();
		const items = makeItems();
		const onSkipTo = vi.fn();
		render(PlayoutRundown, {
			props: {
				items,
				currentIndex: 1,
				onNext: noop,
				onSkipTo,
			},
		});

		const listItems = screen.getAllByRole('listitem');
		// Double-click on item 3 (future) to trigger skip
		await fireEvent.dblClick(listItems[3]);
		// First dblclick = first click triggers confirmation
		// Second dblclick = second click executes
		await fireEvent.dblClick(listItems[3]);
		expect(onSkipTo).toHaveBeenCalledWith(3);

		vi.useRealTimers();
	});

	it('disables skip on completed items', async () => {
		vi.useFakeTimers();
		const items = makeItems();
		const onSkipTo = vi.fn();
		render(PlayoutRundown, {
			props: {
				items,
				currentIndex: 2,
				onNext: noop,
				onSkipTo,
			},
		});

		const listItems = screen.getAllByRole('listitem');
		// Try to skip to a completed item (index 0, before currentIndex=2)
		await fireEvent.dblClick(listItems[0]);
		await fireEvent.dblClick(listItems[0]);
		expect(onSkipTo).not.toHaveBeenCalled();

		vi.useRealTimers();
	});

	it('shows completed check marks', () => {
		const items = makeItems();
		const { container } = render(PlayoutRundown, {
			props: {
				items,
				currentIndex: 2,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		const checks = container.querySelectorAll('.indicator-check');
		// Items 0 and 1 are completed
		expect(checks).toHaveLength(2);
	});

	it('renders empty state when no items', () => {
		render(PlayoutRundown, {
			props: {
				items: [],
				currentIndex: -1,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		expect(screen.getByText('No items in playlist')).toBeTruthy();
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
		const { container } = render(PlayoutRundown, {
			props: {
				items,
				currentIndex: -1,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		const dots = container.querySelectorAll('.event-dot');
		expect(dots).toHaveLength(3);
	});

	it('shows play icon on on-air item index', () => {
		const items = makeItems();
		const { container } = render(PlayoutRundown, {
			props: {
				items,
				currentIndex: 1,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		const playIcons = container.querySelectorAll('.play-icon');
		expect(playIcons).toHaveLength(1);
	});

	it('shows strikethrough duration on completed items', () => {
		const items = makeItems();
		const { container } = render(PlayoutRundown, {
			props: {
				items,
				currentIndex: 2,
				onNext: noop,
				onSkipTo: noop,
			},
		});

		const struck = container.querySelectorAll('.duration-struck');
		// Items 0 is completed clip — should have struck duration
		// Item 1 is live — shows LIVE instead, no struck
		expect(struck.length).toBeGreaterThanOrEqual(1);
	});
});
