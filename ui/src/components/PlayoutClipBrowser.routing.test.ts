import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render } from '@testing-library/svelte';
import { flushSync } from 'svelte';
import PlayoutClipBrowser from './PlayoutClipBrowser.svelte';
import { createReconciler } from '$lib/state/reconciler.svelte';
import { setEngineUrls, setAuthoritativeEngineProvider } from '$lib/api/base-url';
import { resetCircuitBreakers, resetPreferredEngine } from '$lib/api/switch-api';
import type { ControlRoomState } from '$lib/api/types';

// The clip browser fetches on mount from an $effect, through the real
// dual-engine GET routing. That routing asks the reconciler which engine is
// authoritative, and the reconciler's health changes on every state message:
// the fetch must not subscribe the effect to it, or every state message
// re-runs the effect and issues another GET.
describe('PlayoutClipBrowser with dual-engine routing', () => {
	let fetchMock: ReturnType<typeof vi.fn>;

	beforeEach(() => {
		resetCircuitBreakers();
		resetPreferredEngine();
		fetchMock = vi.fn(() => Promise.resolve(new Response('[]', {
			status: 200,
			headers: { 'content-type': 'application/json' },
		})));
		vi.stubGlobal('fetch', fetchMock);
		setEngineUrls([
			{ id: 'A', url: 'http://engine-a' },
			{ id: 'B', url: 'http://engine-b' },
		]);
	});

	afterEach(() => {
		setAuthoritativeEngineProvider(null);
		setEngineUrls(null);
		vi.unstubAllGlobals();
	});

	it('fetches the clip list once while engine state keeps arriving', () => {
		const reconciler = createReconciler();
		setAuthoritativeEngineProvider(() => reconciler.health.authoritative);

		render(PlayoutClipBrowser, { props: { onSelect: vi.fn() } });
		flushSync();
		expect(fetchMock).toHaveBeenCalledTimes(1);

		for (let seq = 2; seq < 12; seq++) {
			reconciler.ingest('A', { seq, timestamp: Date.now() } as ControlRoomState);
			flushSync();
		}

		expect(fetchMock).toHaveBeenCalledTimes(1);
		expect(fetchMock.mock.calls[0][0]).toBe('http://engine-a/api/playout/clips');
	});
});
