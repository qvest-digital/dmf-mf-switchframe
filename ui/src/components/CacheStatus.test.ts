import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import CacheStatus from './CacheStatus.svelte';

const mockCacheStatus = {
	cacheDir: '/tmp/cache',
	maxBytes: 10e9,
	usedBytes: 3e9,
	itemCount: 42,
	fetchingCount: 2,
	errorCount: 1,
};

vi.mock('$lib/api/switch-api', () => ({
	playoutCacheStatus: vi.fn().mockResolvedValue({
		cacheDir: '/tmp/cache',
		maxBytes: 10e9,
		usedBytes: 3e9,
		itemCount: 42,
		fetchingCount: 2,
		errorCount: 1,
	}),
}));

describe('CacheStatus', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.clearAllMocks();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('renders collapsed by default', () => {
		const { container } = render(CacheStatus);
		expect(container.textContent).toContain('Cache Status');
		expect(container.querySelector('.section-body')).toBeFalsy();
	});

	it('expands on click and fetches status', async () => {
		const { playoutCacheStatus } = await import('$lib/api/switch-api');
		const { container } = render(CacheStatus);
		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;
		await fireEvent.click(toggle);
		// The $effect fires refresh() which calls playoutCacheStatus
		await vi.waitFor(() => {
			expect(playoutCacheStatus).toHaveBeenCalled();
		});
		// After fetch resolves, section-body should be visible
		await vi.waitFor(() => {
			expect(container.querySelector('.section-body')).toBeTruthy();
		});
	});

	it('shows item count badge after expand', async () => {
		const { container } = render(CacheStatus);
		// No badge when collapsed (no status loaded yet)
		expect(container.querySelector('.section-badge')).toBeFalsy();

		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;
		await fireEvent.click(toggle);
		await vi.waitFor(() => {
			const badge = container.querySelector('.section-badge');
			expect(badge).toBeTruthy();
			expect(badge!.textContent).toBe('42');
		});
	});

	it('shows usage stats when expanded', async () => {
		const { container } = render(CacheStatus);
		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;
		await fireEvent.click(toggle);
		await vi.waitFor(() => {
			expect(container.querySelector('.cache-stats')).toBeTruthy();
		});
		const stats = container.querySelector('.cache-stats')!;
		expect(stats.textContent).toContain('42 cached');
		expect(stats.textContent).toContain('2 fetching');
		expect(stats.textContent).toContain('1 errors');
	});

	it('collapses on second click', async () => {
		const { container } = render(CacheStatus);
		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;

		// Expand
		await fireEvent.click(toggle);
		await vi.waitFor(() => {
			expect(container.querySelector('.section-body')).toBeTruthy();
		});

		// Collapse
		await fireEvent.click(toggle);
		await vi.waitFor(() => {
			expect(container.querySelector('.section-body')).toBeFalsy();
		});
	});
});
