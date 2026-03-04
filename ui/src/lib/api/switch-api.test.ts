import { describe, it, expect, vi, beforeEach } from 'vitest';
import { cut, setPreview, setLabel, getState, getSources } from './switch-api';

describe('switch-api', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
	});

	it('cut sends POST with source', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await cut('cam1');
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/cut', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ source: 'cam1' }),
		});
		expect(result.programSource).toBe('cam1');
	});

	it('setPreview sends POST with source', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ previewSource: 'cam2', seq: 2 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await setPreview('cam2');
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/preview', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ source: 'cam2' }),
		});
		expect(result.previewSource).toBe('cam2');
	});

	it('setLabel sends POST with label', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ sources: { cam1: { label: 'Camera 1' } } }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await setLabel('cam1', 'Camera 1');
		expect(mockFetch).toHaveBeenCalledWith('/api/sources/cam1/label', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ label: 'Camera 1' }),
		});
	});

	it('getState fetches current state', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 5 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await getState();
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/state');
		expect(result.seq).toBe(5);
	});

	it('getSources fetches source list', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () =>
				Promise.resolve({
					cam1: { key: 'cam1', status: 'healthy', lastFrameTime: 100 },
				}),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await getSources();
		expect(mockFetch).toHaveBeenCalledWith('/api/sources');
		expect(result.cam1.status).toBe('healthy');
	});

	it('cut throws on error response', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: false,
			status: 404,
			json: () => Promise.resolve({ error: 'source "cam99" not found' }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await expect(cut('cam99')).rejects.toThrow('source "cam99" not found');
	});
});
