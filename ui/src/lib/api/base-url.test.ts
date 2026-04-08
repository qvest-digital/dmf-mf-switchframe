import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { getApiBaseUrl, setApiBaseUrl, resolveApiUrl, setEngineUrls, getEngineUrls, setExpectDualEngine, bufferPendingCommand, setFlushTimestampInjector } from './base-url';

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

describe('flushPendingCommands timestamp injection', () => {
	let fetchSpy: ReturnType<typeof vi.fn>;

	beforeEach(() => {
		setEngineUrls(null);
		setExpectDualEngine(false);
		setFlushTimestampInjector(null);
		fetchSpy = vi.fn().mockResolvedValue(new Response('ok'));
		vi.stubGlobal('fetch', fetchSpy);
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('should inject timestamps into flushed timed-path commands', () => {
		// Set up: expect dual engine, buffer a timed command
		setExpectDualEngine(true);
		setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

		const originalBody = JSON.stringify({ source: 'cam1' });
		bufferPendingCommand('/api/switch/cut', {
			method: 'POST',
			body: originalBody,
			headers: { 'Content-Type': 'application/json' },
		});

		// Set up timestamp injector
		const injector = vi.fn((path: string, body: string) => {
			const parsed = JSON.parse(body);
			return JSON.stringify({ ...parsed, execute_at: 12345, command_seq: 1 });
		});
		setFlushTimestampInjector(injector);

		// Discover engine B — triggers flush
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);

		expect(fetchSpy).toHaveBeenCalledTimes(1);
		const [url, opts] = fetchSpy.mock.calls[0];
		expect(url).toBe('https://engine-b.example.com/api/switch/cut');
		expect(JSON.parse(opts.body)).toEqual({ source: 'cam1', execute_at: 12345, command_seq: 1 });
		expect(injector).toHaveBeenCalledWith('/api/switch/cut', originalBody);
	});

	it('should NOT inject timestamps into non-timed-path commands', () => {
		setExpectDualEngine(true);
		setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

		const originalBody = JSON.stringify({ level: 0.8 });
		bufferPendingCommand('/api/audio/cam1/level', {
			method: 'POST',
			body: originalBody,
			headers: { 'Content-Type': 'application/json' },
		});

		const injector = vi.fn();
		setFlushTimestampInjector(injector);

		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);

		expect(fetchSpy).toHaveBeenCalledTimes(1);
		const [, opts] = fetchSpy.mock.calls[0];
		expect(JSON.parse(opts.body)).toEqual({ level: 0.8 });
		// Injector should not have been called for non-timed paths
		expect(injector).not.toHaveBeenCalled();
	});

	it('should flush without modification when no injector is set', () => {
		setExpectDualEngine(true);
		setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

		const originalBody = JSON.stringify({ source: 'cam1' });
		bufferPendingCommand('/api/switch/cut', {
			method: 'POST',
			body: originalBody,
		});

		// No injector set — should still flush, just without timestamps
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);

		expect(fetchSpy).toHaveBeenCalledTimes(1);
		const [, opts] = fetchSpy.mock.calls[0];
		expect(JSON.parse(opts.body)).toEqual({ source: 'cam1' });
	});

	it('should handle non-string bodies gracefully during flush', () => {
		setExpectDualEngine(true);
		setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

		// Buffer a command with no body
		bufferPendingCommand('/api/switch/cut', { method: 'POST' });

		const injector = vi.fn();
		setFlushTimestampInjector(injector);

		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);

		expect(fetchSpy).toHaveBeenCalledTimes(1);
		// Injector should not be called when body is not a string
		expect(injector).not.toHaveBeenCalled();
	});

	it('should inject timestamps for pattern-matched timed paths', () => {
		setExpectDualEngine(true);
		setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

		const originalBody = JSON.stringify({ opacity: 1 });
		bufferPendingCommand('/api/graphics/2/on', {
			method: 'POST',
			body: originalBody,
		});

		setFlushTimestampInjector((_path: string, body: string) => {
			const parsed = JSON.parse(body);
			return JSON.stringify({ ...parsed, execute_at: 99999, command_seq: 5 });
		});

		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);

		expect(fetchSpy).toHaveBeenCalledTimes(1);
		const [, opts] = fetchSpy.mock.calls[0];
		expect(JSON.parse(opts.body)).toEqual({ opacity: 1, execute_at: 99999, command_seq: 5 });
	});
});
