import { describe, it, expect, vi, beforeEach } from 'vitest';
import { DualApiClient } from './dual-api-client';

describe('DualApiClient', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
	});

	// --- addEngine ---

	it('addEngine registers a new engine', async () => {
		const mockFetch = vi
			.fn()
			.mockResolvedValue(new Response(JSON.stringify({ program: 'cam1' }), { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [{ id: 'A', url: 'https://a.example.com' }],
		});

		// Before adding B, getStateSnapshot('B') should throw
		await expect(client.getStateSnapshot('B')).rejects.toThrow('Unknown engine');

		client.addEngine('B', 'https://b.example.com');

		// After adding B, it should work
		await client.getStateSnapshot('B');
		expect(mockFetch).toHaveBeenCalledTimes(1);
		expect(mockFetch.mock.calls[0][0]).toBe('https://b.example.com/api/sync/state-snapshot');
	});

	it('addEngine with identical URL for existing id is a no-op', async () => {
		const mockFetch = vi
			.fn()
			.mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [{ id: 'A', url: 'https://a.example.com' }],
		});

		client.addEngine('A', 'https://a.example.com');

		await client.getStateSnapshot('A');
		expect(mockFetch.mock.calls[0][0]).toBe('https://a.example.com/api/sync/state-snapshot');
	});

	it('addEngine with new URL for existing id updates the target URL', async () => {
		// When an engine is re-provisioned at a new URL, subsequent snapshot
		// targets must go to the new URL, not the dead one.
		const mockFetch = vi
			.fn()
			.mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b-old.example.com' },
			],
		});

		client.addEngine('B', 'https://b-new.example.com');

		await client.getStateSnapshot('B');
		expect(mockFetch.mock.calls[0][0]).toBe('https://b-new.example.com/api/sync/state-snapshot');
	});

	// --- getStateSnapshot ---

	it('getStateSnapshot targets a single engine by id', async () => {
		const responsePayload = { program: 'cam1', preview: 'cam2' };
		const mockFetch = vi
			.fn()
			.mockResolvedValue(new Response(JSON.stringify(responsePayload), { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			],
		});

		const result = await client.getStateSnapshot('A');

		expect(mockFetch).toHaveBeenCalledTimes(1);
		const [url, init] = mockFetch.mock.calls[0] as [string, RequestInit];
		expect(url).toBe('https://a.example.com/api/sync/state-snapshot');
		expect(init.method).toBe('GET');
		expect(result).toEqual(responsePayload);
	});

	it('getStateSnapshot targets engine B', async () => {
		const mockFetch = vi
			.fn()
			.mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			],
		});

		await client.getStateSnapshot('B');

		expect(mockFetch).toHaveBeenCalledTimes(1);
		expect(mockFetch.mock.calls[0][0]).toBe('https://b.example.com/api/sync/state-snapshot');
	});

	it('getStateSnapshot throws on HTTP error', async () => {
		const mockFetch = vi
			.fn()
			.mockResolvedValue(new Response('not found', { status: 404, statusText: 'Not Found' }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [{ id: 'A', url: 'https://a.example.com' }],
		});

		await expect(client.getStateSnapshot('A')).rejects.toThrow(
			'Failed to get snapshot from engine A'
		);
	});

	// --- pushStateSnapshot ---

	it('pushStateSnapshot sends POST to specific engine', async () => {
		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ warnings: ['minor drift'] }), { status: 200 })
		);
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			],
		});

		const snapshot = { program: 'cam1', preview: 'cam2' };
		const result = await client.pushStateSnapshot('B', snapshot);

		expect(mockFetch).toHaveBeenCalledTimes(1);
		const [url, init] = mockFetch.mock.calls[0] as [string, RequestInit];
		expect(url).toBe('https://b.example.com/api/sync/state-snapshot');
		expect(init.method).toBe('POST');
		const body = JSON.parse(init.body as string);
		expect(body.program).toBe('cam1');
		expect(result.warnings).toEqual(['minor drift']);
	});

	it('pushStateSnapshot asks the engine to apply a stale snapshot when forced', async () => {
		const mockFetch = vi.fn().mockResolvedValue(new Response('{"warnings":[]}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);
		const client = new DualApiClient({ engines: [{ id: 'B', url: 'https://b.example.com' }] });

		await client.pushStateSnapshot('B', {}, { force: true });

		expect(mockFetch.mock.calls[0][0]).toBe('https://b.example.com/api/sync/state-snapshot?force=true');
	});

	it('pushStateSnapshot throws on HTTP error', async () => {
		const mockFetch = vi
			.fn()
			.mockResolvedValue(new Response('server error', { status: 500, statusText: 'Internal Server Error' }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [{ id: 'A', url: 'https://a.example.com' }],
		});

		await expect(client.pushStateSnapshot('A', { state: 'test' })).rejects.toThrow(
			'Failed to push snapshot to engine A'
		);
	});

	// --- Unknown engine ---

	it('snapshot methods throw when engine id not found', async () => {
		const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);

		const client = new DualApiClient({
			engines: [{ id: 'A', url: 'https://a.example.com' }],
		});

		// Cast to bypass EngineId type — tests error path for unknown engine IDs
		await expect(client.getStateSnapshot('Z' as 'A')).rejects.toThrow('Unknown engine');
		await expect(client.pushStateSnapshot('Z' as 'A', {})).rejects.toThrow('Unknown engine');
		expect(mockFetch).not.toHaveBeenCalled();
	});

	// --- Auth token ---

	it('includes Authorization header when token is in localStorage', async () => {
		const mockFetch = vi
			.fn()
			.mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);
		localStorage.setItem('switchframe_operator_token', 'secret-token');

		const client = new DualApiClient({
			engines: [{ id: 'A', url: 'https://a.example.com' }],
		});

		await client.getStateSnapshot('A');

		const [, init] = mockFetch.mock.calls[0] as [string, RequestInit];
		expect((init.headers as Record<string, string>)['Authorization']).toBe(
			'Bearer secret-token'
		);
	});

	it('omits Authorization header when no token in localStorage', async () => {
		const mockFetch = vi
			.fn()
			.mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', mockFetch);
		localStorage.removeItem('switchframe_operator_token');

		const client = new DualApiClient({
			engines: [{ id: 'A', url: 'https://a.example.com' }],
		});

		await client.getStateSnapshot('A');

		const [, init] = mockFetch.mock.calls[0] as [string, RequestInit];
		expect((init.headers as Record<string, string>)['Authorization']).toBeUndefined();
	});
});
