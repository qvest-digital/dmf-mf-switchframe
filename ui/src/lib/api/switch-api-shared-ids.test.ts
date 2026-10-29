/**
 * What the UI creates on both engines of a pair it later addresses by one
 * ID in a request fanned out to both. Each engine numbers graphics layers,
 * pods and SCTE-35 events itself, and those numbers part once one engine has
 * restarted or missed a create, so the create request has to name the ID.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { setEngineUrls } from './base-url';
import {
	graphicsAddLayer, podCreate, scte35Cue,
	resetCircuitBreakers, resetPreferredEngine, setPartialFailureNotify,
} from './switch-api';

type Call = { url: string; body: Record<string, unknown> | undefined };
let calls: Call[] = [];
let answer: (url: string, body: Record<string, unknown> | undefined) => Response;

function json(status: number, body: unknown): Response {
	return {
		ok: status < 400,
		status,
		headers: new Headers({ 'content-type': 'application/json' }),
		json: () => Promise.resolve(body),
		text: () => Promise.resolve(JSON.stringify(body)),
	} as Response;
}

beforeEach(() => {
	calls = [];
	answer = () => json(200, {});
	vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, opts?: RequestInit) => {
		const body = opts?.body ? JSON.parse(opts.body as string) : undefined;
		calls.push({ url, body });
		return Promise.resolve(answer(url, body));
	}));
	setEngineUrls([
		{ id: 'A', url: 'https://engine-a.example.com' },
		{ id: 'B', url: 'https://engine-b.example.com' },
	]);
});

afterEach(() => {
	vi.unstubAllGlobals();
	setEngineUrls(null);
	setPartialFailureNotify(null);
	resetCircuitBreakers();
	resetPreferredEngine();
});

describe('graphicsAddLayer', () => {
	it('adds the layer on engine B under the number engine A picked', async () => {
		// B has handed out fewer layer numbers than A, as after a restart.
		answer = (url, body) => url.startsWith('https://engine-a')
			? json(201, { id: 7 })
			: json(201, { id: (body?.id as number | undefined) ?? 0 });

		const layer = await graphicsAddLayer();

		expect(layer.id).toBe(7);
		const toB = calls.filter((c) => c.url === 'https://engine-b.example.com/api/graphics');
		expect(toB).toHaveLength(1);
		expect(toB[0].body).toEqual({ id: 7 });
	});

	it('warns when engine B does not take the layer', async () => {
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		answer = (url) => url.startsWith('https://engine-a') ? json(201, { id: 3 }) : json(409, { error: 'taken' });

		await expect(graphicsAddLayer()).resolves.toEqual({ id: 3 });
		expect(calls.find((c) => c.url.startsWith('https://engine-b'))?.body).toEqual({ id: 3 });
		expect(notify).toHaveBeenCalledWith(expect.stringContaining('Engine B (HTTP 409)'));
	});
});

describe('graphicsAddLayer retry', () => {
	const toB = () => calls.filter((c) => c.url === 'https://engine-b.example.com/api/graphics');

	it('asks engine B again with the same ID after a server error', async () => {
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		let bCalls = 0;
		answer = (url, body) => {
			if (url.startsWith('https://engine-a')) return json(201, { id: 7 });
			bCalls++;
			return bCalls === 1 ? json(503, { error: 'busy' }) : json(201, { id: body?.id });
		};

		await expect(graphicsAddLayer()).resolves.toEqual({ id: 7 });
		expect(toB().map((c) => c.body)).toEqual([{ id: 7 }, { id: 7 }]);
		expect(notify).not.toHaveBeenCalled();
	});

	it('counts a 409 on the retry as the first request having landed', async () => {
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		let bCalls = 0;
		vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, opts?: RequestInit) => {
			calls.push({ url, body: JSON.parse(opts?.body as string) });
			if (url.startsWith('https://engine-a')) return Promise.resolve(json(201, { id: 7 }));
			bCalls++;
			return bCalls === 1 ? Promise.reject(new TypeError('Failed to fetch')) : Promise.resolve(json(409, { error: 'taken' }));
		}));

		await expect(graphicsAddLayer()).resolves.toEqual({ id: 7 });
		expect(toB()).toHaveLength(2);
		expect(notify).not.toHaveBeenCalled();
	});

	it('warns when engine B fails the retry too', async () => {
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		answer = (url) => url.startsWith('https://engine-a') ? json(201, { id: 7 }) : json(503, { error: 'busy' });

		await expect(graphicsAddLayer()).resolves.toEqual({ id: 7 });
		expect(toB()).toHaveLength(2);
		expect(notify).toHaveBeenCalledWith(expect.stringContaining('Engine B (HTTP 503)'));
	});

	it('lets engine B pick the number when engine A does not answer in time', async () => {
		vi.useFakeTimers();
		vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, opts?: RequestInit) => {
			const body = JSON.parse(opts?.body as string);
			calls.push({ url, body });
			if (url.startsWith('https://engine-b')) return Promise.resolve(json(201, { id: 4 }));
			if (body.id !== undefined) return Promise.resolve(json(201, { id: body.id }));
			// Engine A accepts the connection and never answers.
			return new Promise((_, reject) => {
				opts?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
			});
		}));

		const added = graphicsAddLayer();
		await vi.advanceTimersByTimeAsync(3_000);
		await expect(added).resolves.toEqual({ id: 4 });
		const toA = calls.filter((c) => c.url === 'https://engine-a.example.com/api/graphics');
		expect(toA.map((c) => c.body)).toEqual([{}, { id: 4 }]);
		vi.useRealTimers();
	});
});

describe('podCreate', () => {
	it('sends both engines one pod ID', async () => {
		await podCreate(1, { template: 'standard' });
		expect(calls).toHaveLength(2);
		expect(calls[0].body?.id).toMatch(/^pod-[0-9a-f]{8}$/);
		expect(calls[1].body?.id).toBe(calls[0].body?.id);
		expect(calls[0].body?.template).toBe('standard');
	});
});

describe('scte35Cue', () => {
	it('sends both engines one splice event ID', async () => {
		await scte35Cue({ commandType: 'splice_insert', isOut: true, durationMs: 30000 });
		const [a, b] = calls.map((c) => c.body?.eventId);
		expect(a).toEqual(expect.any(Number));
		expect(a).toBeGreaterThan(0);
		expect(b).toBe(a);
	});

	it('sends both engines one segmentation event ID per descriptor', async () => {
		await scte35Cue({
			commandType: 'time_signal',
			descriptors: [
				{ segmentationType: 0x34, upidType: 0, upid: '' },
				{ segmentationType: 0x30, upidType: 0, upid: '' },
			],
		});
		const [a, b] = calls.map((c) => (c.body?.descriptors as { segEventId: number }[]).map((d) => d.segEventId));
		expect(a).toHaveLength(2);
		a.forEach((id) => expect(id).toBeGreaterThan(0));
		expect(b).toEqual(a);
		expect(calls[0].body?.eventId).toBeUndefined();
	});

	it('keeps an event ID the caller chose', async () => {
		await scte35Cue({ commandType: 'splice_insert', isOut: true, eventId: 42 });
		expect(calls.map((c) => c.body?.eventId)).toEqual([42, 42]);
	});
});
