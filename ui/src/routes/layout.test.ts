import { describe, it, expect, vi, afterEach } from 'vitest';
import { render } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { tokenRefusedBy, tokenReplaced } from '$lib/api/auth-gate';

vi.mock('$app/environment', () => ({ version: 'loaded-build', browser: true, dev: false, building: false }));
vi.mock('$lib/util/activity', () => ({ idleForMs: () => 10 * 60_000, hiddenForMs: () => 0, reloadWouldLoseWork: () => false, trackChange: () => () => {}, holdReload: () => () => {} }));

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

	// A toast that dismissed itself after 5 s left a tab nobody was watching
	// on the old build with nothing to show for it.
	it('keeps a reload banner up on a stale build until the page reloads', async () => {
		vi.useFakeTimers();
		vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ version: 'newer-build' }))));
		const reload = vi.fn();
		const original = window.location;
		Object.defineProperty(window, 'location', { configurable: true, value: { ...original, reload } });
		try {
			const children = createRawSnippet(() => ({ render: () => '<p>page</p>' }));
			const { container, unmount } = render(Layout, { props: { children } });
			await vi.advanceTimersByTimeAsync(60_000);
			const banner = () => container.querySelector('[data-testid="stale-build-banner"]');
			expect(banner()).not.toBeNull();
			await vi.advanceTimersByTimeAsync(30 * 60_000);
			expect(banner()).not.toBeNull();
			expect(reload).not.toHaveBeenCalled();
			(banner()!.querySelector('button') as HTMLButtonElement).click();
			expect(reload).toHaveBeenCalledTimes(1);
			unmount();
		} finally {
			Object.defineProperty(window, 'location', { configurable: true, value: original });
		}
	});

	// Reading sessionStorage throws where the browser blocks site storage;
	// the root layout also covers sign-in and must still render.
	it('renders and still flags a stale build where site storage is blocked', async () => {
		vi.useFakeTimers();
		vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ version: 'newer-build' }))));
		const original = Object.getOwnPropertyDescriptor(window, 'sessionStorage');
		Object.defineProperty(window, 'sessionStorage', { configurable: true, get: () => { throw new DOMException('blocked', 'SecurityError'); } });
		try {
			const children = createRawSnippet(() => ({ render: () => '<p>page</p>' }));
			const { container, unmount } = render(Layout, { props: { children } });
			expect(container.textContent).toContain('page');
			await vi.advanceTimersByTimeAsync(60_000);
			expect(container.querySelector('[data-testid="stale-build-banner"]')).not.toBeNull();
			unmount();
		} finally {
			if (original) Object.defineProperty(window, 'sessionStorage', original);
			else delete (window as unknown as Record<string, unknown>).sessionStorage;
		}
	});
});
