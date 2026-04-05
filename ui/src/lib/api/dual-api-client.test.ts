import { describe, it, expect, vi, beforeEach } from 'vitest';
import { DualApiClient } from './dual-api-client';
import type { ClockSync } from '$lib/transport/clock-sync';

// Minimal ClockSync stub
function makeClockSync(ptpNowValue = 1_000_000, safetyMarginValue = 66_666): ClockSync {
	return {
		ptpNow: vi.fn(() => ptpNowValue),
		safetyMargin: vi.fn(() => safetyMarginValue),
	} as unknown as ClockSync;
}

describe('DualApiClient', () => {
	let seqCounter: number;
	let getCommandSeq: () => number;

	beforeEach(() => {
		vi.restoreAllMocks();
		seqCounter = 0;
		getCommandSeq = () => ++seqCounter;
	});

	it('sends cut to both engines with timestamp', async () => {
		const clockSync = makeClockSync(1_000_000, 66_666);
		const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [
				{ id: 'engine-a', url: 'https://a.example.com' },
				{ id: 'engine-b', url: 'https://b.example.com' },
			],
			clockSync,
			getCommandSeq,
		});

		await client.cut('cam1');

		expect(mockFetch).toHaveBeenCalledTimes(2);

		const [urlA, initA] = mockFetch.mock.calls[0] as [string, RequestInit];
		const [urlB, initB] = mockFetch.mock.calls[1] as [string, RequestInit];

		expect(urlA).toBe('https://a.example.com/api/switch/cut');
		expect(urlB).toBe('https://b.example.com/api/switch/cut');

		const bodyA = JSON.parse(initA.body as string);
		const bodyB = JSON.parse(initB.body as string);

		// Both calls get the same timed payload
		expect(bodyA.source).toBe('cam1');
		expect(bodyA.execute_at).toBe(1_000_000 + 66_666);
		expect(bodyA.command_seq).toBe(1);

		expect(bodyB.source).toBe('cam1');
		expect(bodyB.execute_at).toBe(1_000_000 + 66_666);
		expect(bodyB.command_seq).toBe(1);
	});

	it('increments command_seq on each call', async () => {
		const clockSync = makeClockSync();
		const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [{ id: 'engine-a', url: 'https://a.example.com' }],
			clockSync,
			getCommandSeq,
		});

		await client.cut('cam1');
		await client.cut('cam2');

		const body1 = JSON.parse(mockFetch.mock.calls[0][1].body as string);
		const body2 = JSON.parse(mockFetch.mock.calls[1][1].body as string);

		expect(body1.command_seq).toBe(1);
		expect(body2.command_seq).toBe(2);
	});

	it('one engine failure does not reject', async () => {
		const clockSync = makeClockSync();
		const mockFetch = vi
			.fn()
			.mockRejectedValueOnce(new Error('engine-a timeout'))
			.mockResolvedValueOnce(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [
				{ id: 'engine-a', url: 'https://a.example.com' },
				{ id: 'engine-b', url: 'https://b.example.com' },
			],
			clockSync,
			getCommandSeq,
		});

		// Should not throw even though engine-a failed
		await expect(client.cut('cam1')).resolves.toBeUndefined();
		expect(mockFetch).toHaveBeenCalledTimes(2);
	});

	it('sends preview to both engines', async () => {
		const clockSync = makeClockSync(2_000_000, 33_333);
		const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [
				{ id: 'engine-a', url: 'https://a.example.com' },
				{ id: 'engine-b', url: 'https://b.example.com' },
			],
			clockSync,
			getCommandSeq,
		});

		await client.setPreview('cam3');

		expect(mockFetch).toHaveBeenCalledTimes(2);

		const [urlA, initA] = mockFetch.mock.calls[0] as [string, RequestInit];
		const [urlB] = mockFetch.mock.calls[1] as [string, RequestInit];

		expect(urlA).toBe('https://a.example.com/api/switch/preview');
		expect(urlB).toBe('https://b.example.com/api/switch/preview');

		const bodyA = JSON.parse(initA.body as string);
		expect(bodyA.source).toBe('cam3');
		expect(bodyA.execute_at).toBe(2_000_000 + 33_333);
		expect(bodyA.command_seq).toBe(1);
	});

	it('single engine mode sends to one engine only', async () => {
		const clockSync = makeClockSync();
		const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [{ id: 'engine-a', url: 'https://a.example.com' }],
			clockSync,
			getCommandSeq,
		});

		await client.cut('cam1');

		expect(mockFetch).toHaveBeenCalledTimes(1);
		expect(mockFetch.mock.calls[0][0]).toBe('https://a.example.com/api/switch/cut');
	});

	it('untimed commands skip execute_at', async () => {
		const clockSync = makeClockSync();
		const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [
				{ id: 'engine-a', url: 'https://a.example.com' },
				{ id: 'engine-b', url: 'https://b.example.com' },
			],
			clockSync,
			getCommandSeq,
		});

		await client.setLevel('cam1', 0.8);

		expect(mockFetch).toHaveBeenCalledTimes(2);

		const [, initA] = mockFetch.mock.calls[0] as [string, RequestInit];
		const bodyA = JSON.parse(initA.body as string);

		expect(bodyA.level).toBe(0.8);
		expect(bodyA.execute_at).toBeUndefined();
		expect(bodyA.command_seq).toBeUndefined();
	});

	it('includes Authorization header when authToken is provided', async () => {
		const clockSync = makeClockSync();
		const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [{ id: 'engine-a', url: 'https://a.example.com' }],
			clockSync,
			getCommandSeq,
			authToken: 'secret-token',
		});

		await client.cut('cam1');

		const [, init] = mockFetch.mock.calls[0] as [string, RequestInit];
		expect((init.headers as Record<string, string>)['Authorization']).toBe('Bearer secret-token');
	});

	it('omits Authorization header when authToken is absent', async () => {
		const clockSync = makeClockSync();
		const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [{ id: 'engine-a', url: 'https://a.example.com' }],
			clockSync,
			getCommandSeq,
		});

		await client.cut('cam1');

		const [, init] = mockFetch.mock.calls[0] as [string, RequestInit];
		expect((init.headers as Record<string, string>)['Authorization']).toBeUndefined();
	});

	it('setMasterLevel sends untimed PUT to all engines', async () => {
		const clockSync = makeClockSync();
		const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [
				{ id: 'engine-a', url: 'https://a.example.com' },
				{ id: 'engine-b', url: 'https://b.example.com' },
			],
			clockSync,
			getCommandSeq,
		});

		await client.setMasterLevel(0.5);

		expect(mockFetch).toHaveBeenCalledTimes(2);

		for (const call of mockFetch.mock.calls) {
			const [url, init] = call as [string, RequestInit];
			expect(url).toContain('/api/audio/master');
			expect(init.method).toBe('PUT');
			const body = JSON.parse(init.body as string);
			expect(body.level).toBe(0.5);
			expect(body.execute_at).toBeUndefined();
		}
	});
});
