import { describe, it, expect, afterEach, vi } from 'vitest';
import { flushSync } from 'svelte';
import { getAuthoritativeEngine, setAuthoritativeEngineProvider, setEngineUrls } from './base-url';
import { playoutClips } from './switch-api';

// The page hands the reconciler's health to the request path. That health is
// rebuilt on every state snapshot, about 70 times a second with two engines,
// and an effect that issued a request subscribed to it: the Clip Library
// fetched its list on every snapshot.
describe('authoritative engine read', () => {
	afterEach(() => {
		setAuthoritativeEngineProvider(null);
		setEngineUrls(null);
		vi.unstubAllGlobals();
	});

	it('does not subscribe the reading effect to the provider', () => {
		let engine = $state<'A' | 'B'>('A');
		let snapshots = $state(0);
		setAuthoritativeEngineProvider(() => {
			void snapshots;
			return engine;
		});
		let runs = 0;
		const stop = $effect.root(() => {
			$effect(() => {
				getAuthoritativeEngine();
				runs++;
			});
		});
		flushSync();
		snapshots++;
		flushSync();
		snapshots++;
		flushSync();

		expect(runs).toBe(1);
		engine = 'B';
		expect(getAuthoritativeEngine()).toBe('B');
		stop();
	});

	it('lets an effect issue a dual-engine GET once, whatever snapshots arrive', async () => {
		const fetch = vi.fn().mockResolvedValue(new Response('[]', { status: 200 }));
		vi.stubGlobal('fetch', fetch);
		setEngineUrls([
			{ id: 'A', url: 'https://a.example' },
			{ id: 'B', url: 'https://b.example' },
		]);
		let snapshots = $state(0);
		setAuthoritativeEngineProvider(() => {
			void snapshots;
			return 'A';
		});
		const stop = $effect.root(() => {
			$effect(() => {
				void playoutClips();
			});
		});
		flushSync();
		for (let i = 0; i < 5; i++) {
			snapshots++;
			flushSync();
		}
		await Promise.resolve();

		expect(fetch).toHaveBeenCalledTimes(1);
		expect(fetch.mock.calls[0][0]).toBe('https://a.example/api/playout/clips');
		stop();
	});
});
