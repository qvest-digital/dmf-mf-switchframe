import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ControlPlaneDiscovery } from './control-plane-discovery';

describe('ControlPlaneDiscovery', () => {
	let fetchMock: ReturnType<typeof vi.fn>;

	beforeEach(() => {
		fetchMock = vi.fn();
		vi.stubGlobal('fetch', fetchMock);
	});

	afterEach(() => {
		vi.restoreAllMocks();
	});

	it('derives session ID and control plane URL from engine hostname', () => {
		const disc = new ControlPlaneDiscovery('abc123.engines.switchframe.dev');
		expect(disc.sessionId).toBe('abc123');
		expect(disc.controlPlaneUrl).toBe('https://control-plane.switchframe.dev');
	});

	it('strips -b suffix from engine B hostname', () => {
		const disc = new ControlPlaneDiscovery('abc123-b.engines.switchframe.dev');
		expect(disc.sessionId).toBe('abc123');
	});

	it('strips -a suffix from engine A dedicated hostname', () => {
		const disc = new ControlPlaneDiscovery('abc123-a.engines.switchframe.dev');
		expect(disc.sessionId).toBe('abc123');
	});

	it('returns null for bare IP addresses', () => {
		const disc = new ControlPlaneDiscovery('10.0.0.1');
		expect(disc.sessionId).toBeNull();
	});

	it('returns null for localhost', () => {
		const disc = new ControlPlaneDiscovery('localhost');
		expect(disc.sessionId).toBeNull();
	});

	it('fetches engine URLs from control plane health endpoint', async () => {
		fetchMock.mockResolvedValueOnce({
			ok: true,
			json: () =>
				Promise.resolve({
					sessionId: 'abc123',
					engines: [
						{ label: 'a', status: 'ready', ip: '10.0.0.1' },
						{ label: 'b', status: 'ready', ip: '10.0.0.2' }
					]
				})
		});

		const disc = new ControlPlaneDiscovery('abc123.engines.switchframe.dev');
		const result = await disc.fetchEngineUrls();
		expect(result).toEqual({
			engineAUrl: 'https://abc123-a.engines.switchframe.dev',
			engineBUrl: 'https://abc123-b.engines.switchframe.dev'
		});
		expect(fetchMock).toHaveBeenCalledWith(
			'https://control-plane.switchframe.dev/api/sessions/abc123/health',
			expect.objectContaining({ signal: expect.any(AbortSignal) })
		);
	});

	it('returns null when control plane is unreachable', async () => {
		fetchMock.mockRejectedValueOnce(new Error('network error'));
		const disc = new ControlPlaneDiscovery('abc123.engines.switchframe.dev');
		const result = await disc.fetchEngineUrls();
		expect(result).toBeNull();
	});

	it('returns null when no engine B found', async () => {
		fetchMock.mockResolvedValueOnce({
			ok: true,
			json: () =>
				Promise.resolve({
					sessionId: 'abc123',
					engines: [{ label: 'a', status: 'ready', ip: '10.0.0.1' }]
				})
		});

		const disc = new ControlPlaneDiscovery('abc123.engines.switchframe.dev');
		const result = await disc.fetchEngineUrls();
		expect(result).toBeNull();
	});

	it('aborts fetch after timeout', async () => {
		fetchMock.mockImplementation(
			(_url: string, init: RequestInit) =>
				new Promise((_resolve, reject) => {
					init.signal?.addEventListener('abort', () => {
						reject(new DOMException('The operation was aborted.', 'AbortError'));
					});
				})
		);
		const disc = new ControlPlaneDiscovery('abc123.engines.switchframe.dev');
		const result = await disc.fetchEngineUrls(100);
		expect(result).toBeNull();
	});
});
