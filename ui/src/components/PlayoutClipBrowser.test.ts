import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import PlayoutClipBrowser from './PlayoutClipBrowser.svelte';
import type { PlayoutClip } from '$lib/api/types';

const mockClips: PlayoutClip[] = [
	{ name: 'Intro Bumper', path: '/clips/intro.ts', duration: 15, source: 'clip-store' },
	{ name: 'Recording 1', path: '/clips/rec1.ts', duration: 60, source: 'recording' },
	{ name: 'Holding Slate', path: '/samples/holding.ts', duration: 10, source: 'sample' },
	{ name: 'Bumper In', path: '/samples/bumper.ts', duration: 3, source: 'sample' },
];

vi.mock('$lib/api/switch-api', () => ({
	playoutClips: vi.fn().mockResolvedValue([
		{ name: 'Intro Bumper', path: '/clips/intro.ts', duration: 15, source: 'clip-store' },
		{ name: 'Recording 1', path: '/clips/rec1.ts', duration: 60, source: 'recording' },
		{ name: 'Holding Slate', path: '/samples/holding.ts', duration: 10, source: 'sample' },
		{ name: 'Bumper In', path: '/samples/bumper.ts', duration: 3, source: 'sample' },
	]),
}));

describe('PlayoutClipBrowser', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.clearAllMocks();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('renders with header', () => {
		const { container } = render(PlayoutClipBrowser, {
			props: { onSelect: vi.fn() },
		});
		expect(container.textContent).toContain('CLIP LIBRARY');
	});

	it('fetches and displays clips grouped by source', async () => {
		const { container } = render(PlayoutClipBrowser, {
			props: { onSelect: vi.fn() },
		});

		// Wait for the async fetch to resolve.
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Intro Bumper');
		});

		// Check group headers.
		const headers = container.querySelectorAll('.group-header');
		expect(headers).toHaveLength(2);
		expect(headers[0].textContent).toBe('Uploaded');
		expect(headers[1].textContent).toBe('Samples');

		// Check clip rows.
		const rows = container.querySelectorAll('.clip-row');
		expect(rows).toHaveLength(4);
	});

	it('calls onSelect when a clip is clicked', async () => {
		const onSelect = vi.fn();
		const { container } = render(PlayoutClipBrowser, {
			props: { onSelect },
		});

		await vi.waitFor(() => {
			expect(container.textContent).toContain('Intro Bumper');
		});

		const rows = container.querySelectorAll('.clip-row');
		await fireEvent.click(rows[0]);
		expect(onSelect).toHaveBeenCalledOnce();
		expect(onSelect).toHaveBeenCalledWith({
			name: 'Intro Bumper',
			path: '/clips/intro.ts',
			duration: 15,
		});
	});

	it('shows refresh button', () => {
		const { container } = render(PlayoutClipBrowser, {
			props: { onSelect: vi.fn() },
		});
		const refreshBtn = container.querySelector('.refresh-btn');
		expect(refreshBtn).toBeTruthy();
	});

	it('displays formatted durations', async () => {
		const { container } = render(PlayoutClipBrowser, {
			props: { onSelect: vi.fn() },
		});

		await vi.waitFor(() => {
			expect(container.textContent).toContain('Intro Bumper');
		});

		// 15 seconds = "0:15", 60 seconds = "1:00", 3 seconds = "0:03"
		expect(container.textContent).toContain('0:15');
		expect(container.textContent).toContain('1:00');
		expect(container.textContent).toContain('0:03');
	});
});
