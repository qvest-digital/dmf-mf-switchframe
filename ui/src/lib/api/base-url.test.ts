import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { getApiBaseUrl, setApiBaseUrl, resolveApiUrl, setEngineUrls, getEngineUrls, setExpectDualEngine, bufferPendingCommand, setFlushTimestampInjector, setOnNewEngineDiscovered } from './base-url';

describe('base-url', () => {
	beforeEach(() => {
		setApiBaseUrl('');
	});

	it('should return empty string by default (same-origin)', () => {
		expect(getApiBaseUrl()).toBe('');
	});

	it('should return configured URL after setApiBaseUrl', () => {
		setApiBaseUrl('https://localhost:8080');
		expect(getApiBaseUrl()).toBe('https://localhost:8080');
	});

	it('should strip trailing slash', () => {
		setApiBaseUrl('https://localhost:8080/');
		expect(getApiBaseUrl()).toBe('https://localhost:8080');
	});

	it('should resolve relative URLs with base', () => {
		setApiBaseUrl('https://localhost:8080');
		expect(resolveApiUrl('/api/switch/cut')).toBe('https://localhost:8080/api/switch/cut');
	});

	it('should resolve relative URLs without base (same-origin)', () => {
		expect(resolveApiUrl('/api/switch/cut')).toBe('/api/switch/cut');
	});
});

describe('engine URLs', () => {
	beforeEach(() => {
		setEngineUrls(null);
	});

	it('getEngineUrls returns null by default', () => {
		expect(getEngineUrls()).toBeNull();
	});

	it('setEngineUrls stores and retrieves engine URLs', () => {
		const engines = [
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		];
		setEngineUrls(engines);
		expect(getEngineUrls()).toEqual(engines);
	});

	it('setEngineUrls(null) clears engine URLs', () => {
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);
		expect(getEngineUrls()).not.toBeNull();
		setEngineUrls(null);
		expect(getEngineUrls()).toBeNull();
	});
});

describe('onNewEngineDiscovered callback', () => {
	beforeEach(() => {
		setEngineUrls(null);
		setExpectDualEngine(false);
		setFlushTimestampInjector(null);
		setOnNewEngineDiscovered(null);
	});

	afterEach(() => {
		setOnNewEngineDiscovered(null);
	});

	it('calls the callback when a new engine is discovered', () => {
		const cb = vi.fn();
		setOnNewEngineDiscovered(cb);
		setExpectDualEngine(true);
		setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

		// Discover engine B
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);

		expect(cb).toHaveBeenCalledTimes(1);
		expect(cb).toHaveBeenCalledWith('B');
	});

	it('does not call the callback when no new engines are added', () => {
		const cb = vi.fn();
		setOnNewEngineDiscovered(cb);

		// Set same engine again
		setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);
		setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

		expect(cb).not.toHaveBeenCalled();
	});

	it('does not call the callback when callback is null', () => {
		// Should not throw
		setOnNewEngineDiscovered(null);
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);
	});

	it('discards pending command buffer when new engine is discovered', () => {
		const fetchSpy = vi.fn().mockResolvedValue(new Response('ok'));
		vi.stubGlobal('fetch', fetchSpy);

		setExpectDualEngine(true);
		setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);
		bufferPendingCommand('/api/switch/cut', {
			method: 'POST',
			body: JSON.stringify({ source: 'cam1' }),
		});

		// Discover engine B — pending buffer should be discarded, no replay
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);

		// No fetch calls from buffer replay (callback approach replaces replay)
		expect(fetchSpy).not.toHaveBeenCalled();
		vi.unstubAllGlobals();
	});

	it('calls callback for each new engine discovered simultaneously', () => {
		const cb = vi.fn();
		setOnNewEngineDiscovered(cb);

		// Go from empty to two engines at once — both B and C are new
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);

		// When prevIds is empty, skip first engine (A was implicit single-engine target)
		expect(cb).toHaveBeenCalledTimes(1);
		expect(cb).toHaveBeenCalledWith('B');
	});
});
