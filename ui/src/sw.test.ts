import { describe, it, expect } from 'vitest';
import source from '../static/sw.js?raw';

const ORIGIN = 'https://engine.test';

/** Cache Storage keyed by absolute URL, searched across caches like caches.match. */
function cacheStorage() {
	const stores = new Map<string, Map<string, Response>>();
	const key = (r: RequestInfo | URL) => new URL(r instanceof Request ? r.url : String(r), ORIGIN).href;
	const open = async (name: string) => {
		if (!stores.has(name)) stores.set(name, new Map());
		const store = stores.get(name)!;
		return {
			put: async (r: RequestInfo, res: Response) => void store.set(key(r), res.clone()),
			match: async (r: RequestInfo) => store.get(key(r))?.clone(),
		};
	};
	const match = async (r: RequestInfo) => {
		for (const store of stores.values()) {
			const hit = store.get(key(r));
			if (hit) return hit.clone();
		}
		return undefined;
	};
	return { open, match, keys: async () => [...stores.keys()], delete: async (n: string) => stores.delete(n) };
}

/**
 * Runs sw.js against a fake engine and returns what a page's fetch gets
 * back: the worker's answer, or the network's where the worker leaves the
 * request alone.
 */
function controlledPage(network: (path: string) => Response) {
	const handlers = new Map<string, (e: unknown) => void>();
	const self = { addEventListener: (t: string, h: (e: unknown) => void) => handlers.set(t, h), skipWaiting: async () => {}, clients: { claim: async () => {} } };
	const net = async (r: RequestInfo | URL) => network(new URL(r instanceof Request ? r.url : String(r), ORIGIN).pathname);
	new Function('self', 'caches', 'fetch', source)(self, cacheStorage(), net);
	return async (path: string, init?: RequestInit) => {
		const request = new Request(ORIGIN + path, init);
		let answer: Promise<Response> | undefined;
		handlers.get('fetch')!({ request, respondWith: (p: Promise<Response>) => (answer = p) });
		return answer ? await answer : await net(request);
	};
}

describe('service worker', () => {
	// The page compares its build with version.json to tell an operator on
	// an old build to reload. Answered from cache, it kept naming the first
	// build the browser saw, so every later build showed the reload banner
	// on every load, and reloading never cleared it.
	it('asks the engine for its build every time', async () => {
		let served = 'old-build';
		const page = controlledPage((path) =>
			path === '/_app/version.json' ? new Response(JSON.stringify({ version: served })) : new Response('', { status: 404 }),
		);
		expect((await (await page('/_app/version.json', { cache: 'no-store' })).json()).version).toBe('old-build');
		await new Promise((r) => setTimeout(r)); // where the worker stores a copy, it does so asynchronously
		served = 'new-build';
		expect((await (await page('/_app/version.json', { cache: 'no-store' })).json()).version).toBe('new-build');
		served = 'next-build';
		expect((await (await page('/_app/version.json?t=1')).json()).version).toBe('next-build');
	});

	// The cache is what keeps the page loading while the engine that served
	// it is down.
	it('still serves a build chunk it has seen when the engine is unreachable', async () => {
		let up = true;
		const page = controlledPage(() => {
			if (!up) throw new TypeError('Failed to fetch');
			return new Response('chunk');
		});
		await page('/_app/immutable/entry/app.abc.js');
		await new Promise((r) => setTimeout(r)); // the worker stores the copy asynchronously
		up = false;
		expect(await (await page('/_app/immutable/entry/app.abc.js')).text()).toBe('chunk');
	});
});
