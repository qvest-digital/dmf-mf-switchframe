import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { getApiBaseUrl, setApiBaseUrl, resolveApiUrl, setEngineUrls, getEngineUrls, setExpectDualEngine, setFlushTimestampInjector, setOnNewEngineDiscovered, isTimedPath, isNonIdempotent } from './base-url';

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

describe('isTimedPath — upload paths (H5)', () => {
	it('recognizes /api/stingers as timed', () => {
		expect(isTimedPath('/api/stingers')).toBe(true);
	});

	it('recognizes graphics image upload as timed', () => {
		expect(isTimedPath('/api/graphics/1/image')).toBe(true);
		expect(isTimedPath('/api/graphics/42/image')).toBe(true);
	});

	it('recognizes graphics sequence upload as timed', () => {
		expect(isTimedPath('/api/graphics/1/sequence')).toBe(true);
	});

	it('recognizes stmap upload as timed', () => {
		expect(isTimedPath('/api/stmaps/my-map/upload')).toBe(true);
	});

	it('recognizes color-grade LUT upload as timed', () => {
		expect(isTimedPath('/api/color-grade/lut')).toBe(true);
	});

	it('recognizes AI segment upload as timed', () => {
		expect(isTimedPath('/api/sources/cam1/ai-segment')).toBe(true);
	});

	it('recognizes captions mode as timed', () => {
		expect(isTimedPath('/api/captions/mode')).toBe(true);
	});

	it('recognizes transition position as timed', () => {
		expect(isTimedPath('/api/switch/transition/position')).toBe(true);
	});

	it('still recognizes existing timed paths', () => {
		expect(isTimedPath('/api/switch/cut')).toBe(true);
		expect(isTimedPath('/api/switch/preview')).toBe(true);
		expect(isTimedPath('/api/audio/level')).toBe(true);
		expect(isTimedPath('/api/graphics/1/on')).toBe(true);
	});

	it('does not match non-timed paths', () => {
		expect(isTimedPath('/api/sources')).toBe(false);
		expect(isTimedPath('/api/switch/state')).toBe(false);
	});
});

describe('isNonIdempotent — playout pod operations', () => {
	it('recognizes fire as non-idempotent', () => {
		expect(isNonIdempotent('/api/playout/channels/0/pods/pod1/fire')).toBe(true);
	});

	it('recognizes dump as non-idempotent', () => {
		expect(isNonIdempotent('/api/playout/channels/1/pods/my-pod/dump')).toBe(true);
	});

	it('recognizes hold as non-idempotent', () => {
		expect(isNonIdempotent('/api/playout/channels/0/pods/pod-a/hold')).toBe(true);
	});

	it('recognizes extend as non-idempotent', () => {
		expect(isNonIdempotent('/api/playout/channels/2/pods/pod-b/extend')).toBe(true);
	});

	it('recognizes macro run as non-idempotent', () => {
		expect(isNonIdempotent('/api/macros/my-macro/run')).toBe(true);
	});

	it('recognizes scte35 cue as non-idempotent', () => {
		expect(isNonIdempotent('/api/scte35/cue')).toBe(true);
	});

	it('does not match non-idempotent for regular paths', () => {
		expect(isNonIdempotent('/api/switch/cut')).toBe(false);
		expect(isNonIdempotent('/api/switch/state')).toBe(false);
	});
});

// Pending command buffer removed — snapshot push replaces command replay.
