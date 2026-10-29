/**
 * A replay is exported as a clip on every engine of a pair, and the clip is
 * later deleted or pinned by ID with one request fanned out to all of them.
 * Each engine stores the clip under the ID the replay request carries, so
 * the request has to carry one, the same on every engine.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { setEngineUrls } from './base-url';
import { replayPlay, replayQuick, runMacro, resetCircuitBreakers, resetPreferredEngine } from './switch-api';

let bodies: Array<{ url: string; body: Record<string, unknown> }> = [];

beforeEach(() => {
	bodies = [];
	vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, opts?: RequestInit) => {
		bodies.push({ url, body: JSON.parse(opts?.body as string) });
		return Promise.resolve({
			ok: true,
			status: 200,
			headers: new Headers({ 'content-type': 'application/json' }),
			json: () => Promise.resolve({}),
			text: () => Promise.resolve('{}'),
		} as Response);
	}));
	setEngineUrls([
		{ id: 'A', url: 'https://engine-a.example.com' },
		{ id: 'B', url: 'https://engine-b.example.com' },
	]);
});

afterEach(() => {
	vi.unstubAllGlobals();
	setEngineUrls(null);
	resetCircuitBreakers();
	resetPreferredEngine();
});

const HEX32 = /^[0-9a-f]{32}$/;

describe.each([
	['replayPlay', 'clipId', () => replayPlay('cam1', 1, false)],
	['replayQuick', 'clipId', () => replayQuick(5, 1, 'cam1')],
	['runMacro', 'runId', () => runMacro('replay')],
] as const)('%s', (_name, field, send) => {
	it(`sends both engines one ${field}`, async () => {
		await send();
		expect(bodies).toHaveLength(2);
		const [a, b] = bodies.map((c) => c.body[field]);
		expect(a).toMatch(HEX32);
		expect(b).toBe(a);
	});

	it(`sends a fresh ${field} for every request`, async () => {
		await send();
		await send();
		expect(bodies[0].body[field]).not.toBe(bodies[2].body[field]);
	});
});
