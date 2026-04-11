import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OASchedulePanel from './OASchedulePanel.svelte';
import OAScheduleItem from './OAScheduleItem.svelte';
import type { PlayoutChannelState, PlaylistItem } from '$lib/api/types';

function makeChannel(overrides: Partial<PlayoutChannelState> = {}): PlayoutChannelState {
	return {
		id: 1,
		state: 'on_air',
		itemIndex: 0,
		itemCount: 3,
		itemProgress: 0.5,
		...overrides,
	};
}

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
		makeItem({ id: 'c1', type: 'clip', title: 'Intro', duration_ms: 30000 }),
		makeItem({ id: 'l1', type: 'live', title: 'Camera 1', duration_ms: undefined }),
		makeItem({ id: 'c2', type: 'clip', title: 'Package', duration_ms: 15000 }),
		makeItem({ id: 's1', type: 'slate', title: 'BRB', duration_ms: 5000 }),
	];
}

describe('OASchedulePanel', () => {
	afterEach(() => {
		vi.restoreAllMocks();
	});

	it('renders PLAYOUT SCHEDULE header', () => {
		render(OASchedulePanel, {
			props: {
				playoutChannel: makeChannel(),
				items: makeItems(),
			},
		});
		expect(screen.getByText('PLAYOUT SCHEDULE')).toBeTruthy();
	});

	it('renders edit button when callback provided', () => {
		const editFn = vi.fn();
		render(OASchedulePanel, {
			props: {
				playoutChannel: makeChannel(),
				items: [],
				onEditSchedule: editFn,
			},
		});
		const btn = screen.getByText('Edit');
		expect(btn).toBeTruthy();
	});

	it('renders as-run button when callback provided', () => {
		const asRunFn = vi.fn();
		render(OASchedulePanel, {
			props: {
				playoutChannel: makeChannel(),
				items: [],
				onShowAsRun: asRunFn,
			},
		});
		const btn = screen.getByText('As-Run');
		expect(btn).toBeTruthy();
	});

	it('calls onEditSchedule when edit button clicked', async () => {
		const editFn = vi.fn();
		render(OASchedulePanel, {
			props: {
				playoutChannel: makeChannel(),
				items: [],
				onEditSchedule: editFn,
			},
		});
		await fireEvent.click(screen.getByText('Edit'));
		expect(editFn).toHaveBeenCalledOnce();
	});

	it('calls onShowAsRun when as-run button clicked', async () => {
		const asRunFn = vi.fn();
		render(OASchedulePanel, {
			props: {
				playoutChannel: makeChannel(),
				items: [],
				onShowAsRun: asRunFn,
			},
		});
		await fireEvent.click(screen.getByText('As-Run'));
		expect(asRunFn).toHaveBeenCalledOnce();
	});

	it('renders all schedule items', () => {
		const items = makeItems();
		render(OASchedulePanel, {
			props: {
				playoutChannel: makeChannel({ itemIndex: 0 }),
				items,
			},
		});
		const listItems = screen.getAllByRole('listitem');
		expect(listItems).toHaveLength(4);
	});

	it('shows empty state when no items', () => {
		render(OASchedulePanel, {
			props: {
				playoutChannel: makeChannel({ itemIndex: -1 }),
				items: [],
			},
		});
		expect(screen.getByText('No items in schedule')).toBeTruthy();
	});

	it('hides buttons when no callbacks provided', () => {
		render(OASchedulePanel, {
			props: {
				playoutChannel: makeChannel(),
				items: [],
			},
		});
		expect(screen.queryByText('Edit')).toBeFalsy();
		expect(screen.queryByText('As-Run')).toBeFalsy();
	});
});

describe('OAScheduleItem', () => {
	it('renders title', () => {
		render(OAScheduleItem, {
			props: {
				title: 'Morning Show Intro',
				itemType: 'clip',
				state: 'ready',
			},
		});
		expect(screen.getByText('Morning Show Intro')).toBeTruthy();
	});

	it('renders scheduled time', () => {
		render(OAScheduleItem, {
			props: {
				title: 'Live Segment',
				scheduledTime: '08:30:00',
				itemType: 'live',
				state: 'ready',
			},
		});
		expect(screen.getByText('08:30:00')).toBeTruthy();
	});

	it('renders duration in M:SS format', () => {
		render(OAScheduleItem, {
			props: {
				title: 'Clip',
				duration: 90,
				itemType: 'clip',
				state: 'ready',
			},
		});
		expect(screen.getByText('1:30')).toBeTruthy();
	});

	it('shows ON AIR badge when state is on_air', () => {
		render(OAScheduleItem, {
			props: {
				title: 'Live',
				itemType: 'live',
				state: 'on_air',
			},
		});
		const badge = screen.getByText('ON AIR');
		expect(badge).toBeTruthy();
		expect(badge.classList.contains('badge-on-air')).toBe(true);
	});

	it('shows ARMED badge when state is armed', () => {
		render(OAScheduleItem, {
			props: {
				title: 'Next Item',
				itemType: 'clip',
				state: 'armed',
			},
		});
		const badge = screen.getByText('ARMED');
		expect(badge).toBeTruthy();
		expect(badge.classList.contains('badge-armed')).toBe(true);
	});

	it('shows READY badge when state is ready', () => {
		render(OAScheduleItem, {
			props: {
				title: 'Upcoming',
				itemType: 'clip',
				state: 'ready',
			},
		});
		const badge = screen.getByText('READY');
		expect(badge).toBeTruthy();
		expect(badge.classList.contains('badge-ready')).toBe(true);
	});

	it('shows DONE badge with checkmark when state is done', () => {
		render(OAScheduleItem, {
			props: {
				title: 'Completed',
				itemType: 'clip',
				state: 'done',
			},
		});
		const badge = screen.getByText(/DONE/);
		expect(badge).toBeTruthy();
		expect(badge.classList.contains('badge-done')).toBe(true);
	});

	it('done items have dimmed opacity class', () => {
		const { container } = render(OAScheduleItem, {
			props: {
				title: 'Old Clip',
				itemType: 'clip',
				state: 'done',
			},
		});
		const item = container.querySelector('.schedule-item');
		expect(item?.classList.contains('is-done')).toBe(true);
	});

	it('on-air items have brighter background class', () => {
		const { container } = render(OAScheduleItem, {
			props: {
				title: 'Current',
				itemType: 'live',
				state: 'on_air',
			},
		});
		const item = container.querySelector('.schedule-item');
		expect(item?.classList.contains('is-on-air')).toBe(true);
	});

	it('shows progress bar when on_air with duration and elapsed', () => {
		const { container } = render(OAScheduleItem, {
			props: {
				title: 'Playing',
				itemType: 'clip',
				state: 'on_air',
				duration: 60,
				elapsed: 30,
			},
		});
		const progressFill = container.querySelector('.progress-fill');
		expect(progressFill).toBeTruthy();
		expect(progressFill?.getAttribute('style')).toContain('width: 50%');
	});

	it('does not show progress bar when state is ready', () => {
		const { container } = render(OAScheduleItem, {
			props: {
				title: 'Upcoming',
				itemType: 'clip',
				state: 'ready',
				duration: 60,
			},
		});
		const progressFill = container.querySelector('.progress-fill');
		expect(progressFill).toBeFalsy();
	});

	it('shows spot count for pod items', () => {
		render(OAScheduleItem, {
			props: {
				title: 'Ad Pod 1',
				itemType: 'pod',
				state: 'ready',
				spotCount: 3,
			},
		});
		expect(screen.getByText('3 spots')).toBeTruthy();
	});

	it('shows singular spot label for single spot', () => {
		render(OAScheduleItem, {
			props: {
				title: 'Ad Pod 2',
				itemType: 'pod',
				state: 'ready',
				spotCount: 1,
			},
		});
		expect(screen.getByText('1 spot')).toBeTruthy();
	});

	it('sets data-item-type attribute', () => {
		const { container } = render(OAScheduleItem, {
			props: {
				title: 'Clip',
				itemType: 'clip',
				state: 'ready',
			},
		});
		const item = container.querySelector('[data-item-type="clip"]');
		expect(item).toBeTruthy();
	});

	it('sets data-state attribute', () => {
		const { container } = render(OAScheduleItem, {
			props: {
				title: 'Armed Item',
				itemType: 'clip',
				state: 'armed',
			},
		});
		const item = container.querySelector('[data-state="armed"]');
		expect(item).toBeTruthy();
	});

	it('armed items have glow on color bar', () => {
		const { container } = render(OAScheduleItem, {
			props: {
				title: 'Next Up',
				itemType: 'clip',
				state: 'armed',
			},
		});
		const item = container.querySelector('.schedule-item');
		expect(item?.classList.contains('is-armed')).toBe(true);
	});

	it('clamps progress at 100%', () => {
		const { container } = render(OAScheduleItem, {
			props: {
				title: 'Overrun',
				itemType: 'clip',
				state: 'on_air',
				duration: 30,
				elapsed: 45,
			},
		});
		const progressFill = container.querySelector('.progress-fill');
		expect(progressFill?.getAttribute('style')).toContain('width: 100%');
	});
});
