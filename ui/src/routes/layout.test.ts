import { describe, it, expect, vi, afterEach } from 'vitest';
import { render } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { tokenRefusedBy, tokenReplaced } from '$lib/api/auth-gate';

const check = vi.fn(async () => true);
vi.mock('$app/state', () => ({ updated: { check: () => check() } }));

import Layout from './+layout.svelte';

describe('root layout', () => {
	afterEach(() => {
		tokenReplaced();
		vi.useRealTimers();
	});

	// The page is what every tab runs; the watcher only helps if it starts there.
	it('reloads a tab on a stale build whose token the engines refuse', async () => {
		vi.useFakeTimers();
		const reload = vi.fn();
		const original = window.location;
		Object.defineProperty(window, 'location', { configurable: true, value: { ...original, reload } });
		try {
			const children = createRawSnippet(() => ({ render: () => '<p>page</p>' }));
			const { unmount } = render(Layout, { props: { children } });
			tokenRefusedBy(original.origin, [original.origin]);
			await vi.advanceTimersByTimeAsync(60_000);
			expect(check).toHaveBeenCalled();
			expect(reload).toHaveBeenCalledTimes(1);
			unmount();
		} finally {
			Object.defineProperty(window, 'location', { configurable: true, value: original });
		}
	});
});
