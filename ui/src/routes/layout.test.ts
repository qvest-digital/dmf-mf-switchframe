import { describe, it, expect, vi, afterEach } from 'vitest';
import { render } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { tokenRefusedBy, tokenReplaced } from '$lib/api/auth-gate';

vi.mock('$app/environment', () => ({ version: 'loaded-build', browser: true, dev: false, building: false }));
vi.mock('$lib/util/activity', () => ({ idleForMs: () => 10 * 60_000, changesInFlight: () => false, trackChange: () => () => {} }));

import Layout from './+layout.svelte';

describe('root layout', () => {
	afterEach(() => {
		tokenReplaced();
		vi.unstubAllGlobals();
		vi.useRealTimers();
		sessionStorage.clear();
	});

	// The page is what every tab runs; the watcher only helps if it starts there.
	it('reloads an untouched tab on a stale build whose token the engines refuse', async () => {
		vi.useFakeTimers();
		const fetchMock = vi.fn(async (_url: RequestInfo | URL) => new Response(JSON.stringify({ version: 'newer-build' })));
		vi.stubGlobal('fetch', fetchMock);
		const reload = vi.fn();
		const original = window.location;
		Object.defineProperty(window, 'location', { configurable: true, value: { ...original, reload } });
		try {
			const children = createRawSnippet(() => ({ render: () => '<p>page</p>' }));
			const { unmount } = render(Layout, { props: { children } });
			tokenRefusedBy(original.origin, [original.origin]);
			await vi.advanceTimersByTimeAsync(60_000);
			expect(String(fetchMock.mock.calls[0][0])).toBe('/_app/version.json');
			expect(reload).toHaveBeenCalledTimes(1);
			unmount();
		} finally {
			Object.defineProperty(window, 'location', { configurable: true, value: original });
		}
	});
});
