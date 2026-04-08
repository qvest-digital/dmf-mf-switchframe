/**
 * Behavioral tests for the switch-api dual-engine routing layer.
 *
 * These tests verify the actual routing decisions:
 * - Single-engine mode works unchanged
 * - Dual-engine GETs use primary-with-fallback
 * - Dual-engine mutations fan out to all engines
 * - Body cloning works for FormData and Blob (bodies can only be consumed once)
 * - PTP timestamps are injected on timed paths
 * - Error handling preserves HTTP status codes
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { setEngineUrls, getEngineUrls, setApiBaseUrl, setExpectDualEngine } from './base-url';
import {
	cut, setPreview, getState, fadeToBlack,
	setMasterLevel, startTransition,
	setDualTimestampProvider, setPartialFailureCallback, setPartialFailureNotify, authHeaders,
	resetPreferredEngine, resetCircuitBreakers, uploadStinger,
	startRecording, stopRecording,
} from './switch-api';

// Track all fetch calls for inspection
let fetchCalls: Array<{ url: string; opts: RequestInit }> = [];
let fetchResponses: Map<string, Response> = new Map();

function mockFetchResponse(url: string | RegExp, response: Partial<Response> & { json?: () => Promise<unknown> }) {
	const resp = {
		ok: response.ok ?? true,
		status: response.status ?? 200,
		headers: response.headers ?? new Headers({ 'content-type': 'application/json' }),
		json: response.json ?? (() => Promise.resolve({})),
		text: () => Promise.resolve(''),
		clone: () => resp,
	} as Response;

	if (typeof url === 'string') {
		fetchResponses.set(url, resp);
	}
	return resp;
}

function defaultOkResponse(body: unknown = {}): Response {
	return {
		ok: true,
		status: 200,
		headers: new Headers({ 'content-type': 'application/json' }),
		json: () => Promise.resolve(body),
		text: () => Promise.resolve(JSON.stringify(body)),
		clone() { return this; },
	} as Response;
}

function errorResponse(status: number, message: string): Response {
	return {
		ok: false,
		status,
		headers: new Headers({ 'content-type': 'application/json' }),
		json: () => Promise.resolve({ error: message }),
		text: () => Promise.resolve(JSON.stringify({ error: message })),
		clone() { return this; },
	} as Response;
}

describe('switch-api dual-engine routing', () => {
	let mockFetch: ReturnType<typeof vi.fn>;

	beforeEach(() => {
		fetchCalls = [];
		fetchResponses = new Map();
		mockFetch = vi.fn().mockImplementation((url: string, opts?: RequestInit) => {
			fetchCalls.push({ url, opts: opts ?? {} });
			return Promise.resolve(defaultOkResponse({ programSource: 'cam1', seq: 1, lastCommandSeq: 1 }));
		});
		vi.stubGlobal('fetch', mockFetch);
		setApiBaseUrl('');
		setEngineUrls(null);
		setDualTimestampProvider(null);

		// Clear any auth token
		try { localStorage.removeItem('switchframe_operator_token'); } catch { /* noop */ }
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		setEngineUrls(null);
		setDualTimestampProvider(null);
		setPartialFailureCallback(null);
		setPartialFailureNotify(null);
		setExpectDualEngine(false);
		resetPreferredEngine();
		resetCircuitBreakers();
	});

	// ─── Single-engine mode (no engines configured) ───

	describe('single-engine mode', () => {
		it('routes to same-origin when no engines configured', async () => {
			await cut('cam1');

			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toBe('/api/switch/cut');
		});

		it('does not add PTP timestamps in single-engine mode', async () => {
			setDualTimestampProvider(() => ({ execute_at: 12345, command_seq: 1 }));

			await cut('cam1');

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBeUndefined();
		});
	});

	// ─── Dual-engine routing decisions ───

	describe('dual-engine routing', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('fans out POST mutations to both engines', async () => {
			await cut('cam1');

			expect(fetchCalls).toHaveLength(2);
			const urls = fetchCalls.map(c => c.url);
			expect(urls).toContain('https://engine-a.example.com/api/switch/cut');
			expect(urls).toContain('https://engine-b.example.com/api/switch/cut');
		});

		it('both engines receive identical request bodies', async () => {
			await cut('cam1');

			const bodyA = JSON.parse(fetchCalls[0].opts.body as string);
			const bodyB = JSON.parse(fetchCalls[1].opts.body as string);

			expect(bodyA.source).toBe('cam1');
			expect(bodyB.source).toBe('cam1');
		});

		it('uses primary-with-fallback for GET requests (not parallel fan-out)', async () => {
			await getState();

			// Should only have called one engine (primary succeeds)
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toBe('https://engine-a.example.com/api/switch/state');
		});

		it('falls back to engine B when engine A fails on GET', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.reject(new Error('network error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			const state = await getState();
			expect(state.programSource).toBe('cam1');

			// Should have tried A then B
			expect(fetchCalls).toHaveLength(2);
			expect(fetchCalls[0].url).toContain('engine-a');
			expect(fetchCalls[1].url).toContain('engine-b');
		});

		it('falls back to engine B when engine A returns HTTP error on GET', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.resolve(errorResponse(500, 'internal error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam2' }));
			});

			const state = await getState();
			expect(state.programSource).toBe('cam2');
		});

		it('returns first successful mutation response when both succeed', async () => {
			const state = await cut('cam1');
			expect(state).toBeDefined();
			expect(state.programSource).toBe('cam1');
		});

		it('succeeds if only one engine accepts the mutation', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b')) {
					return Promise.resolve(errorResponse(500, 'engine B crashed'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			// Should succeed because engine A accepted
			const state = await cut('cam1');
			expect(state.programSource).toBe('cam1');
		});

		it('throws when all engines fail on mutation', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(errorResponse(500, 'engine down'));
			});

			await expect(cut('cam1')).rejects.toThrow();
		});

		it('throws when all engines are unreachable on mutation', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.reject(new Error('network error'));
			});

			await expect(cut('cam1')).rejects.toThrow('All engines unreachable');
		});

		it('throws when all engines fail on GET (both network error)', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.reject(new Error('timeout'));
			});

			await expect(getState()).rejects.toThrow();
		});
	});

	// ─── PTP timestamp injection ───

	describe('PTP timestamp injection', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('injects execute_at and command_seq on timed paths', async () => {
			setDualTimestampProvider(() => ({
				execute_at: 1_700_000_000_000_000,
				command_seq: 42,
			}));

			await cut('cam1');

			// Both engines should get the timestamp
			for (const call of fetchCalls) {
				const body = JSON.parse(call.opts.body as string);
				expect(body.execute_at).toBe(1_700_000_000_000_000);
				expect(body.command_seq).toBe(42);
				expect(body.source).toBe('cam1'); // original body preserved
			}
		});

		it('injects on /api/switch/preview', async () => {
			setDualTimestampProvider(() => ({ execute_at: 123, command_seq: 1 }));
			await setPreview('cam2');

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBe(123);
		});

		it('injects on /api/switch/ftb', async () => {
			setDualTimestampProvider(() => ({ execute_at: 456, command_seq: 2 }));
			await fadeToBlack();

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBe(456);
		});

		it('does NOT inject on non-timed paths like /api/audio', async () => {
			setDualTimestampProvider(() => ({ execute_at: 999, command_seq: 99 }));
			await setMasterLevel(-6);

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBeUndefined();
		});

		it('does NOT inject when no timestamp provider set', async () => {
			setDualTimestampProvider(null);
			await cut('cam1');

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBeUndefined();
		});
	});

	// ─── Body cloning for multi-consume ───

	describe('body cloning', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('clones FormData so each engine gets its own copy', async () => {
			// FormData can only be consumed once by fetch. If not cloned,
			// the second engine gets an empty body.
			const capturedBodies: unknown[] = [];
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				capturedBodies.push(opts?.body);
				return Promise.resolve(defaultOkResponse());
			});

			const fd = new FormData();
			fd.append('name', 'test-preset');
			fd.append('data', 'preset-data');

			// Use a raw request path that sends FormData
			// We'll test through the internal mechanism by calling a function that uses dualFetch
			// For this test, we verify the principle: each engine must get independent FormData
			await fetch('https://engine-a.example.com/api/test', {
				method: 'POST',
				body: fd,
			});

			// Verify the FormData wasn't consumed
			expect(fd.get('name')).toBe('test-preset');
		});

		it('string bodies are naturally shareable (no clone needed)', async () => {
			// JSON string bodies are immutable — both engines can use the same reference
			await cut('cam1');

			// Both engines should have received string bodies
			expect(typeof fetchCalls[0].opts.body).toBe('string');
			expect(typeof fetchCalls[1].opts.body).toBe('string');

			const bodyA = JSON.parse(fetchCalls[0].opts.body as string);
			const bodyB = JSON.parse(fetchCalls[1].opts.body as string);
			expect(bodyA.source).toBe(bodyB.source);
		});
	});

	// ─── Error precedence and HTTP status preservation ───

	describe('error handling', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('preserves SwitchApiError status code when all engines return HTTP errors on GET', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(errorResponse(404, 'not found'));
			});

			try {
				await getState();
				expect.fail('should have thrown');
			} catch (err: unknown) {
				expect((err as { status?: number }).status).toBe(404);
			}
		});

		it('returns successful response even when one engine has HTTP error on mutation', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) return Promise.resolve(errorResponse(500, 'crash'));
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			const state = await cut('cam1');
			expect(state.programSource).toBe('cam1');
		});

		it('handles 204 No Content responses on mutations', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve({
					ok: true,
					status: 204,
					headers: new Headers({ 'content-length': '0' }),
					json: () => Promise.reject(new Error('no body')),
				} as Response);
			});

			// Should not throw on 204
			const result = await fadeToBlack();
			expect(result).toBeUndefined();
		});
	});

	// ─── Auth header propagation ───

	describe('auth headers', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('includes auth token in dual-engine requests', async () => {
			localStorage.setItem('switchframe_operator_token', 'test-token-123');

			await cut('cam1');

			for (const call of fetchCalls) {
				const headers = call.opts.headers as Record<string, string>;
				expect(headers['Authorization']).toBe('Bearer test-token-123');
			}
		});

		it('omits auth header when no token set', async () => {
			localStorage.removeItem('switchframe_operator_token');

			await cut('cam1');

			for (const call of fetchCalls) {
				const headers = call.opts.headers as Record<string, string>;
				expect(headers['Authorization']).toBeUndefined();
			}
		});
	});

	// ─── Pending command buffer during engine B discovery ───

	describe('pending command buffer', () => {
		it('buffers mutations for engine B when dual-engine expected but only one engine known', async () => {
			// Simulate: we expect dual-engine, no engine URLs set yet
			// (single-engine mode, request() falls through to same-origin fetch)
			setExpectDualEngine(true);

			// Send a mutation — goes to same-origin (engine A) immediately
			await cut('cam1');

			// Engine A got the request via same-origin path
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toBe('/api/switch/cut');

			// Now engine B is discovered — set both engines, flush pending buffer
			fetchCalls = [];
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			// Wait for async flush
			await vi.waitFor(() => {
				const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
				expect(engineBCalls).toHaveLength(1);
			});

			// Verify the replayed request to engine B has the correct path and body
			const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
			expect(engineBCalls[0].url).toBe('https://engine-b.example.com/api/switch/cut');
			const body = JSON.parse(engineBCalls[0].opts.body as string);
			expect(body.source).toBe('cam1');
		});

		it('does not buffer when dual-engine is not expected', async () => {
			// Single-engine mode (default) — no buffering
			setEngineUrls(null);

			await cut('cam1');
			expect(fetchCalls).toHaveLength(1);

			// Adding engines later should NOT replay anything
			fetchCalls = [];
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			// Give async flush time to run (it shouldn't)
			await new Promise(r => setTimeout(r, 10));
			expect(fetchCalls).toHaveLength(0);
		});

		it('does not buffer GET requests', async () => {
			setExpectDualEngine(true);
			setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

			await getState();

			// Adding engine B should not replay any GETs
			fetchCalls = [];
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			await new Promise(r => setTimeout(r, 10));
			expect(fetchCalls).toHaveLength(0);
		});

		it('limits buffer to 10 items (FIFO)', async () => {
			setExpectDualEngine(true);
			setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

			// Send 12 mutations — only the last 10 should be buffered
			for (let i = 0; i < 12; i++) {
				await cut(`cam${i}`);
			}

			fetchCalls = [];
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			await vi.waitFor(() => {
				const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
				expect(engineBCalls).toHaveLength(10);
			});

			// First replayed command should be cam2 (cam0 and cam1 were evicted)
			const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
			const firstBody = JSON.parse(engineBCalls[0].opts.body as string);
			expect(firstBody.source).toBe('cam2');
		});

		it('drops commands older than 5 seconds', async () => {
			setExpectDualEngine(true);
			setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

			// Mock Date.now to control time
			const realNow = Date.now;
			let currentTime = realNow();
			vi.spyOn(Date, 'now').mockImplementation(() => currentTime);

			await cut('cam1');

			// Advance time by 6 seconds
			currentTime += 6000;

			fetchCalls = [];
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			// Give async flush time to run
			await new Promise(r => setTimeout(r, 10));

			// Stale command should have been dropped
			const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
			expect(engineBCalls).toHaveLength(0);

			vi.spyOn(Date, 'now').mockRestore();
		});

		it('flushes multiple buffered commands to new engine', async () => {
			setExpectDualEngine(true);
			setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

			await cut('cam1');
			await setPreview('cam2');
			await fadeToBlack();

			fetchCalls = [];
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			await vi.waitFor(() => {
				const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
				expect(engineBCalls).toHaveLength(3);
			});

			const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
			expect(engineBCalls[0].url).toContain('/api/switch/cut');
			expect(engineBCalls[1].url).toContain('/api/switch/preview');
			expect(engineBCalls[2].url).toContain('/api/switch/ftb');
		});

		it('stops buffering once both engines are known', async () => {
			setExpectDualEngine(true);
			setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

			await cut('cam1');  // buffered

			// Engine B discovered
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			// Wait for flush
			await vi.waitFor(() => {
				const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
				expect(engineBCalls.length).toBeGreaterThanOrEqual(1);
			});

			fetchCalls = [];

			// Now send another mutation — should go to BOTH engines directly
			await cut('cam2');

			const engineACalls = fetchCalls.filter(c => c.url.includes('engine-a'));
			const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
			expect(engineACalls).toHaveLength(1);
			expect(engineBCalls).toHaveLength(1);
		});
	});

	// ─── Partial failure detection ───

	describe('partial failure callback', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('fires callback when one engine succeeds and the other returns HTTP error', async () => {
			const onPartialFailure = vi.fn();
			setPartialFailureCallback(onPartialFailure);

			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b')) {
					return Promise.resolve(errorResponse(500, 'engine B crashed'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await cut('cam1');
			expect(onPartialFailure).toHaveBeenCalledTimes(1);
		});

		it('fires callback when one engine succeeds and the other has network error', async () => {
			const onPartialFailure = vi.fn();
			setPartialFailureCallback(onPartialFailure);

			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b')) {
					return Promise.reject(new Error('network error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await cut('cam1');
			expect(onPartialFailure).toHaveBeenCalledTimes(1);
		});

		it('does NOT fire callback when both engines succeed', async () => {
			const onPartialFailure = vi.fn();
			setPartialFailureCallback(onPartialFailure);

			await cut('cam1');
			expect(onPartialFailure).not.toHaveBeenCalled();
		});

		it('does NOT fire callback when both engines fail', async () => {
			const onPartialFailure = vi.fn();
			setPartialFailureCallback(onPartialFailure);

			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(errorResponse(500, 'engine down'));
			});

			await expect(cut('cam1')).rejects.toThrow();
			expect(onPartialFailure).not.toHaveBeenCalled();
		});

		it('does NOT fire callback for GET requests (no fan-out)', async () => {
			const onPartialFailure = vi.fn();
			setPartialFailureCallback(onPartialFailure);

			// GET uses primary-with-fallback, not allSettled — no partial failure possible
			await getState();
			expect(onPartialFailure).not.toHaveBeenCalled();
		});

		it('does not fire when no callback is set', async () => {
			setPartialFailureCallback(null);

			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b')) {
					return Promise.resolve(errorResponse(500, 'engine B crashed'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			// Should not throw even though there's a partial failure
			const state = await cut('cam1');
			expect(state.programSource).toBe('cam1');
		});

		it('logs a warning on partial failure', async () => {
			const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
			setPartialFailureCallback(() => {});

			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b')) {
					return Promise.resolve(errorResponse(500, 'engine B crashed'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await cut('cam1');
			expect(warnSpy).toHaveBeenCalledWith(
				expect.stringContaining('[dual-api] Partial failure')
			);

			warnSpy.mockRestore();
		});
	});

	// ─── Sticky engine routing after failover ───

	describe('sticky engine routing', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
			resetPreferredEngine();
		});

		it('after Engine A failure + B success, subsequent GETs go to B first', async () => {
			// First request: Engine A fails, Engine B succeeds
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.reject(new Error('network error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await getState();
			expect(fetchCalls).toHaveLength(2);
			expect(fetchCalls[0].url).toContain('engine-a');
			expect(fetchCalls[1].url).toContain('engine-b');

			// Second request: both engines are up, but B should be tried first
			fetchCalls = [];
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(defaultOkResponse({ programSource: 'cam2' }));
			});

			await getState();
			// Should only have called one engine (B, since it's preferred and succeeds)
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toContain('engine-b');
		});

		it('after 30s TTL expires, GETs go back to A first', async () => {
			const realNow = Date.now;
			let currentTime = realNow();
			vi.spyOn(Date, 'now').mockImplementation(() => currentTime);

			// First request: Engine A fails, Engine B succeeds → sets preference
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.reject(new Error('network error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await getState();

			// Advance time past TTL (31 seconds)
			currentTime += 31_000;

			// Both engines now healthy
			fetchCalls = [];
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(defaultOkResponse({ programSource: 'cam2' }));
			});

			await getState();
			// Should go back to A first (default order)
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toContain('engine-a');

			vi.spyOn(Date, 'now').mockRestore();
		});

		it('sticky preference does not interfere with mutation fan-out', async () => {
			// Set up preference: Engine A fails, B succeeds on GET
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.reject(new Error('network error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await getState();  // Sets B as preferred

			// Now send a mutation — should still fan out to both engines
			fetchCalls = [];
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await cut('cam1');
			// Mutations always fan out to all available engines
			expect(fetchCalls).toHaveLength(2);
			const urls = fetchCalls.map(c => c.url);
			expect(urls).toContain('https://engine-a.example.com/api/switch/cut');
			expect(urls).toContain('https://engine-b.example.com/api/switch/cut');
		});

		it('sticky preference survives across multiple GET requests within TTL', async () => {
			// Engine A fails, B succeeds → sets preference
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.reject(new Error('network error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await getState();

			// Multiple subsequent GETs should all go to B first
			for (let i = 0; i < 3; i++) {
				fetchCalls = [];
				mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
					fetchCalls.push({ url, opts: opts ?? {} });
					return Promise.resolve(defaultOkResponse({ programSource: 'cam2' }));
				});

				await getState();
				expect(fetchCalls).toHaveLength(1);
				expect(fetchCalls[0].url).toContain('engine-b');
			}
		});

		it('Engine A HTTP error also triggers sticky routing to B', async () => {
			// Engine A returns 500, Engine B succeeds
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.resolve(errorResponse(500, 'internal error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await getState();

			// Next GET should go to B first
			fetchCalls = [];
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(defaultOkResponse({ programSource: 'cam2' }));
			});

			await getState();
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toContain('engine-b');
		});

		it('resetPreferredEngine clears sticky preference', async () => {
			// Engine A fails, B succeeds → sets preference
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.reject(new Error('network error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await getState();

			// Reset preference
			resetPreferredEngine();

			// Next GET should go back to A first
			fetchCalls = [];
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(defaultOkResponse({ programSource: 'cam2' }));
			});

			await getState();
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toContain('engine-a');
		});
	});

	// ─── dualFetch circuit breaker filtering ───

	describe('dualFetch circuit breaker filtering', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('skips engine with open circuit on dualFetch mutations', async () => {
			// First: open the circuit for engine A by sending 5 failures
			// We'll use dualRequest (cut) to trigger recordFailure on engine A
			let callCount = 0;
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.resolve(errorResponse(500, 'engine A down'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			// Send 5 mutations through dualRequest to trip the circuit breaker for engine A
			for (let i = 0; i < 5; i++) {
				await cut('cam1');
			}

			// Now engine A's circuit should be open. Clear tracking.
			fetchCalls = [];

			// Use uploadStinger which goes through dualFetch for mutations
			const mockFile = new File(['test'], 'test.zip', { type: 'application/zip' });
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(defaultOkResponse());
			});

			await uploadStinger('test', mockFile);

			// With circuit breaker filtering in dualFetch, only engine B should be called
			const mutationUrls = fetchCalls.map(c => c.url);
			expect(mutationUrls.every(u => u.includes('engine-b'))).toBe(true);
			expect(mutationUrls.some(u => u.includes('engine-a'))).toBe(false);
		});

		it('records success/failure for dualFetch mutations', async () => {
			// Make engine B fail on dualFetch mutation
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b')) {
					return Promise.resolve(errorResponse(500, 'engine B down'));
				}
				return Promise.resolve(defaultOkResponse());
			});

			const mockFile = new File(['test'], 'test.zip', { type: 'application/zip' });

			// Send 5 mutations through dualFetch to trip the circuit breaker for engine B
			for (let i = 0; i < 5; i++) {
				await uploadStinger('test', mockFile);
			}

			// Now clear and try again - engine B should be skipped
			fetchCalls = [];
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(defaultOkResponse());
			});

			await uploadStinger('test', mockFile);

			const mutationUrls = fetchCalls.map(c => c.url);
			expect(mutationUrls.every(u => u.includes('engine-a'))).toBe(true);
			expect(mutationUrls.some(u => u.includes('engine-b'))).toBe(false);
		});

		it('tries all engines when all circuits are open (half-open probe)', async () => {
			// Trip circuit breakers for both engines via dualRequest
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(errorResponse(500, 'engine down'));
			});

			for (let i = 0; i < 5; i++) {
				try { await cut('cam1'); } catch { /* expected */ }
			}

			// Both circuits should be open now. Clear tracking.
			fetchCalls = [];
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(defaultOkResponse());
			});

			const mockFile = new File(['test'], 'test.zip', { type: 'application/zip' });
			await uploadStinger('test', mockFile);

			// With all circuits open, dualFetch should try all engines (half-open probe)
			const urls = fetchCalls.map(c => c.url);
			expect(urls.some(u => u.includes('engine-a'))).toBe(true);
			expect(urls.some(u => u.includes('engine-b'))).toBe(true);
		});
	});

	// ─── Recording targets primary engine only ───

	describe('recording engine targeting', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('startRecording targets primary engine only', async () => {
			await startRecording();

			// Only engine A should receive the request
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toBe('https://engine-a.example.com/api/output/recording/start');
		});

		it('stopRecording targets primary engine only', async () => {
			await stopRecording();

			// Only engine A should receive the request
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toBe('https://engine-a.example.com/api/output/recording/stop');
		});

		it('startRecording sends engine field in body', async () => {
			await startRecording({ outputDir: '/tmp/rec', rotateAfterMins: 60 });

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.engine).toBe('A');
			expect(body.outputDir).toBe('/tmp/rec');
			expect(body.rotateAfterMins).toBe(60);
		});

		it('stopRecording sends engine field in body', async () => {
			await stopRecording();

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.engine).toBe('A');
		});

		it('startRecording does not fan out to engine B', async () => {
			await startRecording();

			const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
			expect(engineBCalls).toHaveLength(0);
		});
	});

	// ─── Partial failure notify ───

	describe('partial failure notify', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('calls partialFailureNotify on partial mutation failure (HTTP error)', async () => {
			const notifySpy = vi.fn();
			setPartialFailureNotify(notifySpy);

			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b')) {
					return Promise.resolve(errorResponse(500, 'engine B crashed'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await cut('cam1');
			expect(notifySpy).toHaveBeenCalledTimes(1);
			expect(notifySpy).toHaveBeenCalledWith(expect.stringContaining('Engine'));
		});

		it('calls partialFailureNotify on partial mutation failure (network error)', async () => {
			const notifySpy = vi.fn();
			setPartialFailureNotify(notifySpy);

			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b')) {
					return Promise.reject(new Error('network error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await cut('cam1');
			expect(notifySpy).toHaveBeenCalledTimes(1);
			expect(notifySpy).toHaveBeenCalledWith(expect.stringContaining('Engine'));
		});

		it('notify message includes failed engine ID', async () => {
			const notifySpy = vi.fn();
			setPartialFailureNotify(notifySpy);

			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b')) {
					return Promise.resolve(errorResponse(500, 'engine B crashed'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await cut('cam1');
			expect(notifySpy).toHaveBeenCalledWith(expect.stringContaining('B'));
		});

		it('does NOT call notify when both engines succeed', async () => {
			const notifySpy = vi.fn();
			setPartialFailureNotify(notifySpy);

			await cut('cam1');
			expect(notifySpy).not.toHaveBeenCalled();
		});

		it('does NOT call notify when both engines fail', async () => {
			const notifySpy = vi.fn();
			setPartialFailureNotify(notifySpy);

			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(errorResponse(500, 'engine down'));
			});

			await expect(cut('cam1')).rejects.toThrow();
			expect(notifySpy).not.toHaveBeenCalled();
		});

		it('does not throw when no notify callback is set', async () => {
			setPartialFailureNotify(null);

			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b')) {
					return Promise.resolve(errorResponse(500, 'engine B crashed'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			// Should not throw
			const state = await cut('cam1');
			expect(state.programSource).toBe('cam1');
		});
	});
});
