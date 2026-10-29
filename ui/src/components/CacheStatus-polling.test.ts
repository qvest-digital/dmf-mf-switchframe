import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import CacheStatus from './CacheStatus.svelte';
import { setEngineUrls } from '$lib/api/base-url';
import { tokenReplaced } from '$lib/api/auth-gate';

// The panel polls every 5s while it is open, through the real API client.
describe('CacheStatus polling', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		localStorage.setItem('switchframe_operator_token', 'expired-sign-in');
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example' },
			{ id: 'B', url: 'https://engine-b.example' },
		]);
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.unstubAllGlobals();
		setEngineUrls(null);
		tokenReplaced();
		localStorage.clear();
	});

	async function openFor(ms: number) {
		const { container } = render(CacheStatus);
		await fireEvent.click(container.querySelector('.section-toggle') as HTMLButtonElement);
		await vi.advanceTimersByTimeAsync(ms);
	}

	// Without the gate: 13 polls a minute, each sent to both engines.
	it('asks each engine once in a minute when they refuse the token', async () => {
		const fetchMock = vi.fn(async () => new Response('{"error":"invalid token"}', { status: 401 }));
		vi.stubGlobal('fetch', fetchMock);

		await openFor(60_000);

		expect(fetchMock).toHaveBeenCalledTimes(2);
	});

	// Without backoff: a request to each engine every 5s.
	it('backs off while neither engine answers', async () => {
		const fetchMock = vi.fn(async () => { throw new TypeError('Failed to fetch'); });
		vi.stubGlobal('fetch', fetchMock);

		await openFor(60_000);

		// Polls at 0, 10, 30 and 60s.
		expect(fetchMock).toHaveBeenCalledTimes(4 * 2);
	});
});
