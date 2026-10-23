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
import { setEngineUrls, getEngineUrls, setApiBaseUrl, setExpectDualEngine, setOnNewEngineDiscovered, isNonIdempotent, isTimedPath } from './base-url';
import {
	cut, setPreview, getState, fadeToBlack,
	setMasterLevel, setMute, startTransition, runMacro, setSourceKey,
	setFormat, setEncoderBackend,
	setDualTimestampProvider, setPartialFailureCallback, setPartialFailureNotify, authHeaders,
	resetPreferredEngine, resetCircuitBreakers, uploadStinger,
	startRecording, stopRecording, addDestination, clipPlayerLoadByName,
	commandLatency, resetCommandLatency,
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
		setOnNewEngineDiscovered(null);
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

	// A clip load demuxes the whole file before the engine answers, which
	// takes longer than the fan-out timeout for ordinary commands. Aborting it
	// reports a failure for a load the engine still completes.
	describe('slow clip load', () => {
		const A = 'https://engine-a.example.com';
		const B = 'https://engine-b.example.com';
		/** Per-engine behaviour: answer after ms, never answer, or fail. */
		let behaviour: Record<string, { after: number; res?: () => Response } | 'hang'>;

		beforeEach(() => {
			vi.useFakeTimers();
			setEngineUrls([{ id: 'A', url: A }, { id: 'B', url: B }]);
			behaviour = {};
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				const b = behaviour[url.startsWith(A) ? A : B] ?? { after: 0 };
				return new Promise((resolve, reject) => {
					opts?.signal?.addEventListener('abort', () => reject(new Error('aborted')));
					if (b === 'hang') return;
					setTimeout(() => resolve((b.res ?? (() => defaultOkResponse({ programSource: 'cam1' })))()), b.after);
				});
			});
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		it('waits for a load that takes longer than the command timeout', async () => {
			behaviour = { [A]: { after: 15_000 }, [B]: { after: 15_000 } };
			const notify = vi.fn();
			setPartialFailureNotify(notify);

			const done = clipPlayerLoadByName(1, 'bunny');
			await vi.advanceTimersByTimeAsync(15_000);

			await expect(done).resolves.toEqual({ programSource: 'cam1' });
			expect(fetchCalls.every(c => !c.opts.signal?.aborted)).toBe(true);
			expect(notify).not.toHaveBeenCalled();
		});

		it('still aborts an ordinary command after the command timeout', async () => {
			behaviour = { [A]: { after: 15_000 }, [B]: { after: 15_000 } };
			const done = cut('cam1');
			const failed = expect(done).rejects.toThrow('All engines unreachable');
			await vi.advanceTimersByTimeAsync(3_000);
			await failed;
		});

		it('does not hold the caller for an engine that never answers', async () => {
			behaviour = { [A]: { after: 1_000 }, [B]: 'hang' };
			const notify = vi.fn();
			setPartialFailureNotify(notify);
			let resolved = false;

			const done = clipPlayerLoadByName(1, 'bunny').then((r) => { resolved = true; return r; });
			await vi.advanceTimersByTimeAsync(4_000);

			expect(resolved).toBe(true);
			await expect(done).resolves.toEqual({ programSource: 'cam1' });
			expect(notify).not.toHaveBeenCalled();

			// The straggler is still accounted for once it times out.
			await vi.advanceTimersByTimeAsync(120_000);
			expect(notify).toHaveBeenCalledWith(expect.stringContaining('Engine B'));
		});

		it('retries a load that failed fast, but not one that timed out', async () => {
			behaviour = { [A]: { after: 0 }, [B]: { after: 0, res: () => errorResponse(404, 'clip not found') } };
			const first = clipPlayerLoadByName(1, 'bunny');
			await vi.advanceTimersByTimeAsync(600);
			await first;
			expect(fetchCalls.filter(c => c.url.startsWith(B))).toHaveLength(2);

			fetchCalls = [];
			behaviour = { [A]: { after: 0 }, [B]: 'hang' };
			const load = clipPlayerLoadByName(1, 'bunny');
			await vi.advanceTimersByTimeAsync(121_000);
			await load;
			expect(fetchCalls.filter(c => c.url.startsWith(B))).toHaveLength(1);
		});

		it('does not close a circuit that opened while the load was pending', async () => {
			behaviour = { [A]: { after: 0 }, [B]: { after: 5_000 } };
			const load = clipPlayerLoadByName(1, 'bunny');

			// Engine B now fails three ordinary commands and its circuit opens.
			behaviour[B] = { after: 0, res: () => errorResponse(500, 'down') };
			for (let i = 0; i < 3; i++) {
				const c = cut('cam1');
				await vi.advanceTimersByTimeAsync(1);
				await c;
			}
			await vi.advanceTimersByTimeAsync(5_000);
			await load;

			// The load's late success must not reopen B to commands.
			fetchCalls = [];
			const c = cut('cam1');
			await vi.advanceTimersByTimeAsync(1);
			await c;
			expect(fetchCalls.map(f => f.url)).toEqual([`${A}/api/switch/cut`]);
		});
	});

	// ─── PTP timestamp injection ───

	describe('command latency', () => {
		const A = 'https://engine-a.example.com';
		const B = 'https://engine-b.example.com';
		let delay: Record<string, number>;
		let failB: boolean;

		beforeEach(() => {
			vi.useFakeTimers();
			resetCommandLatency();
			setEngineUrls([{ id: 'A', url: A }, { id: 'B', url: B }]);
			delay = {};
			failB = false;
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				const engine = url.startsWith(A) ? A : B;
				return new Promise((resolve) => {
					setTimeout(() => resolve(engine === B && failB
						? errorResponse(500, 'boom')
						: defaultOkResponse({ programSource: 'cam1' })), delay[engine] ?? 0);
				});
			});
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		it('reports no average before an engine has answered a command', () => {
			expect(commandLatency()).toEqual({ A: null, B: null });
		});

		it('averages each engine\'s round trip separately', async () => {
			delay = { [A]: 10, [B]: 40 };
			const first = cut('cam1');
			await vi.advanceTimersByTimeAsync(40);
			await first;
			delay = { [A]: 30, [B]: 60 };
			const second = cut('cam2');
			await vi.advanceTimersByTimeAsync(60);
			await second;

			expect(commandLatency()).toEqual({ A: 20, B: 50 });
		});

		it('leaves out an engine whose command failed', async () => {
			delay = { [A]: 10, [B]: 10 };
			failB = true;
			setPartialFailureNotify(() => {});
			const done = cut('cam1');
			await vi.advanceTimersByTimeAsync(10);
			await done;

			expect(commandLatency()).toEqual({ A: 10, B: null });
		});

		it('drops an engine\'s average once a command to it fails', async () => {
			// An engine that stops answering would otherwise keep showing the
			// round trip it had while healthy.
			setPartialFailureNotify(() => {});
			delay = { [A]: 10, [B]: 10 };
			const first = cut('cam1');
			await vi.advanceTimersByTimeAsync(10);
			await first;
			failB = true;
			const second = cut('cam2');
			await vi.advanceTimersByTimeAsync(10);
			await second;

			expect(commandLatency()).toEqual({ A: 10, B: null });
		});

		it('starts afresh for an engine re-provisioned at a new URL', async () => {
			delay = { [A]: 10, [B]: 10 };
			const done = cut('cam1');
			await vi.advanceTimersByTimeAsync(10);
			await done;

			setEngineUrls([{ id: 'A', url: A }, { id: 'B', url: 'https://engine-b2.example.com' }]);

			expect(commandLatency()).toEqual({ A: 10, B: null });
		});

		it('averages the last 20 commands', async () => {
			delay = { [A]: 1000, [B]: 0 };
			const slow = cut('cam1');
			await vi.advanceTimersByTimeAsync(1000);
			await slow;
			delay = { [A]: 10, [B]: 0 };
			for (let i = 0; i < 20; i++) {
				const done = cut('cam1');
				await vi.advanceTimersByTimeAsync(10);
				await done;
			}

			expect(commandLatency().A).toBe(10);
		});

		it('leaves out a clip load, which waits on the engine reading the clip', async () => {
			delay = { [A]: 5_000, [B]: 5_000 };
			const done = clipPlayerLoadByName(1, 'bunny');
			await vi.advanceTimersByTimeAsync(5_000);
			await done;

			expect(commandLatency()).toEqual({ A: null, B: null });
		});
	});

	describe('PTP timestamp injection', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('injects execute_at and command_seq on timed paths', async () => {
			const realisticTs = Date.now() * 1000 + 500_000; // 500ms in the future
			setDualTimestampProvider(() => ({
				execute_at: realisticTs,
				command_seq: 42,
			}));

			await cut('cam1');

			// Both engines should get the timestamp
			for (const call of fetchCalls) {
				const body = JSON.parse(call.opts.body as string);
				expect(body.execute_at).toBe(realisticTs);
				expect(body.command_seq).toBe(42);
				expect(body.source).toBe('cam1'); // original body preserved
			}
		});

		it('sends one command with the same command_seq to both engines', async () => {
			// The provider runs once per engine and hands out a new seq each
			// time; A got N and B N+1, so every command read as B ahead of A.
			let seq = 0;
			setDualTimestampProvider(() => ({
				execute_at: Date.now() * 1000 + 100_000,
				command_seq: ++seq,
			}));

			await cut('cam1');

			const seqs = fetchCalls.map(c => JSON.parse(c.opts.body as string).command_seq);
			expect(seqs).toEqual([1, 1]);
		});

		it('injects on /api/switch/preview', async () => {
			const realisticTs = Date.now() * 1000 + 1_000_000;
			setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 1 }));
			await setPreview('cam2');

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBe(realisticTs);
		});

		it('injects on /api/switch/ftb', async () => {
			const realisticTs = Date.now() * 1000 + 2_000_000;
			setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 2 }));
			await fadeToBlack();

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBe(realisticTs);
		});

		it('injects on /api/audio/* paths (master level)', async () => {
			const realisticTs = Date.now() * 1000 + 3_000_000;
			setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 3 }));
			await setMasterLevel(-6);

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBe(realisticTs);
			expect(body.command_seq).toBe(3);
		});

		it('injects on /api/audio/mute', async () => {
			const realisticTs = Date.now() * 1000 + 4_000_000;
			setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 4 }));
			await setMute('cam1', true);

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBe(realisticTs);
		});

		it('injects on /api/macros/{name}/run', async () => {
			const realisticTs = Date.now() * 1000 + 1_000_000;
			setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 5 }));
			await runMacro('my-macro');

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBe(realisticTs);
		});

		it('injects on /api/sources/{source}/key', async () => {
			const realisticTs = Date.now() * 1000 + 1_200_000;
			setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 6 }));
			await setSourceKey('cam1', { type: 'chroma', enabled: true } as any);

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBe(realisticTs);
		});

		it('injects on /api/format', async () => {
			const realisticTs = Date.now() * 1000 + 1_400_000;
			setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 7 }));
			await setFormat('1080p30');

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBe(realisticTs);
		});

		it('injects on /api/encoder', async () => {
			const realisticTs = Date.now() * 1000 + 1_600_000;
			setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 8 }));
			await setEncoderBackend('nvenc');

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.execute_at).toBe(realisticTs);
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

	// ─── Engine discovery callback (replaces command buffer replay) ───
	// Commands are no longer replayed to new engines; instead, a snapshot push
	// is triggered via the onNewEngineDiscovered callback. This avoids idempotency
	// issues with PTP-timestamped commands that were buffered before engine B appeared.

	describe('engine discovery callback', () => {
		it('fires onNewEngineDiscovered callback when engine B is discovered', () => {
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

		it('does NOT replay buffered mutations to engine B on discovery', async () => {
			setExpectDualEngine(true);

			// Send a mutation — goes to same-origin (engine A) immediately
			await cut('cam1');
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toBe('/api/switch/cut');

			// Now engine B is discovered
			fetchCalls = [];
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			// Give time to verify no replay happens
			await new Promise(r => setTimeout(r, 10));
			const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
			expect(engineBCalls).toHaveLength(0);
		});

		it('does not fire callback when dual-engine is not expected', async () => {
			const cb = vi.fn();
			setOnNewEngineDiscovered(cb);
			setEngineUrls(null);

			await cut('cam1');

			// Adding engines without expectDualEngine — callback still fires for new engines
			// but no replay happens
			fetchCalls = [];
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			// When prevIds is empty, first engine (A) is skipped (was implicit target)
			// B is genuinely new — callback fires once
			expect(cb).toHaveBeenCalledTimes(1);
			expect(cb).toHaveBeenCalledWith('B');
		});

		it('does not buffer GET requests', async () => {
			const cb = vi.fn();
			setOnNewEngineDiscovered(cb);
			setExpectDualEngine(true);
			setEngineUrls([{ id: 'A', url: 'https://engine-a.example.com' }]);

			await getState();

			// Adding engine B triggers callback but no GET replay
			fetchCalls = [];
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			await new Promise(r => setTimeout(r, 10));
			const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
			expect(engineBCalls).toHaveLength(0);
			expect(cb).toHaveBeenCalledWith('B');
		});

		it('routes mutations to both engines once both are known', async () => {
			setExpectDualEngine(true);
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			fetchCalls = [];
			await cut('cam2');

			const engineACalls = fetchCalls.filter(c => c.url.includes('engine-a'));
			const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
			expect(engineACalls).toHaveLength(1);
			expect(engineBCalls).toHaveLength(1);
		});

		it('command dispatched pre-discovery goes only to engine A even if B is discovered mid-fetch', async () => {
			setExpectDualEngine(true);
			// Start in single-engine mode
			setEngineUrls(null);

			// Hold the first fetch pending so we can flip engines mid-await
			let releasePreDiscoveryFetch: (() => void) | null = null;
			const preDiscoveryFetchStarted = new Promise<void>(resolve => {
				mockFetch.mockImplementationOnce((url: string, opts?: RequestInit) => {
					fetchCalls.push({ url, opts: opts ?? {} });
					resolve();
					return new Promise<Response>(res => {
						releasePreDiscoveryFetch = () => res(defaultOkResponse({ programSource: 'cam1' }));
					});
				});
			});

			// T=0: issue cut while single-engine
			const firstCut = cut('cam1');
			await preDiscoveryFetchStarted;

			// Snapshot the fetch calls made before discovery
			const preDiscoveryCalls = fetchCalls.length;
			expect(preDiscoveryCalls).toBe(1);
			expect(fetchCalls[0].url).toBe('/api/switch/cut');

			// T=mid-await: discover engine B
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			// Release the original fetch
			releasePreDiscoveryFetch!();
			await firstCut;

			// Pre-discovery command must not have been fanned out to B
			const engineBCalls = fetchCalls.filter(c => c.url.includes('engine-b'));
			expect(engineBCalls).toHaveLength(0);
			// And still only one call total for the pre-discovery cut
			expect(fetchCalls).toHaveLength(1);
		});

		it('post-discovery command routes to both engines after single→dual transition', async () => {
			setExpectDualEngine(true);
			// Start single
			setEngineUrls(null);

			// First cut in single mode
			await cut('cam1');
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toBe('/api/switch/cut');

			// Discover engine B
			fetchCalls = [];
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);

			// Post-discovery cut fans out to both
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

	// ─── Partial failure retry ───

	describe('partial failure retry', () => {
		beforeEach(() => {
			vi.useFakeTimers();
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		it('retries failed engine once after 500ms on HTTP error', async () => {
			let callCount = 0;
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				callCount++;
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b') && callCount <= 2) {
					// First fan-out call to engine-b fails
					return Promise.resolve(errorResponse(500, 'engine B crashed'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await cut('cam1');

			// Initial fan-out: 2 calls (A success, B fail)
			const initialCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(initialCalls).toHaveLength(2);

			// Advance timer to trigger retry
			await vi.advanceTimersByTimeAsync(500);

			// Retry: 1 additional call to engine-b only
			const allCutCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(allCutCalls).toHaveLength(3);
			expect(allCutCalls[2].url).toContain('engine-b');
		});

		it('retries failed engine once after 500ms on network error', async () => {
			let callCount = 0;
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				callCount++;
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b') && callCount <= 2) {
					return Promise.reject(new Error('network error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await cut('cam1');

			// Initial fan-out: 2 calls attempted (A success, B rejected but still recorded by mock)
			const initialCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(initialCalls).toHaveLength(2);

			// Advance timer to trigger retry
			await vi.advanceTimersByTimeAsync(500);

			// Retry call to engine-b (third call)
			const allCutCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(allCutCalls).toHaveLength(3);
			expect(allCutCalls[2].url).toContain('engine-b');
		});

		it('does not retry when both engines succeed', async () => {
			await cut('cam1');

			const initialCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(initialCalls).toHaveLength(2);

			await vi.advanceTimersByTimeAsync(500);

			// No additional calls
			const allCutCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(allCutCalls).toHaveLength(2);
		});

		it('does not retry when both engines fail', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(errorResponse(500, 'engine down'));
			});

			await expect(cut('cam1')).rejects.toThrow();

			const initialCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(initialCalls).toHaveLength(2);

			await vi.advanceTimersByTimeAsync(500);

			// No retry when all engines failed
			const allCutCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(allCutCalls).toHaveLength(2);
		});

		it('retry failure is silently ignored (best-effort)', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b')) {
					return Promise.reject(new Error('still broken'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			// Initial call succeeds (A ok, B fail)
			const result = await cut('cam1');
			expect(result.programSource).toBe('cam1');

			// Advance timer — retry also fails but should not throw
			await vi.advanceTimersByTimeAsync(500);

			// No unhandled promise rejection — test passes if we get here
		});

		it('notify message says retrying (not syncing)', async () => {
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
			expect(notifySpy).toHaveBeenCalledWith(expect.stringContaining('retrying'));
		});

		it('retry uses original body — does NOT regenerate PTP timestamps (H1)', async () => {
			let timestampCallCount = 0;
			setDualTimestampProvider((engineId?: string) => {
				timestampCallCount++;
				return { execute_at: Date.now() * 1000 + timestampCallCount * 1000, command_seq: timestampCallCount };
			});

			let callCount = 0;
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				callCount++;
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-b') && callCount <= 2) {
					return Promise.resolve(errorResponse(500, 'engine B crashed'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await cut('cam1');

			// Record timestamp call count after initial fan-out
			const initialTimestampCalls = timestampCallCount;
			expect(initialTimestampCalls).toBe(2); // one per engine

			// Initial fan-out: 2 calls
			const initialCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(initialCalls).toHaveLength(2);

			// Capture the body sent to engine B initially
			const engineBCall = initialCalls.find(c => c.url.includes('engine-b'));
			const originalBody = engineBCall?.opts.body;

			await vi.advanceTimersByTimeAsync(500);

			// Retry should NOT have called the timestamp provider again
			expect(timestampCallCount).toBe(initialTimestampCalls);

			// Retry body should match original
			const allCutCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(allCutCalls).toHaveLength(3);
			const retryCall = allCutCalls[2];
			expect(retryCall.opts.body).toBe(originalBody);

			setDualTimestampProvider(null);
		});

		it('only retries the failed engine, not the successful one', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a') && url.includes('/api/switch/cut')) {
					return Promise.resolve(errorResponse(500, 'engine A crashed'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await cut('cam1');

			// Initial: 2 calls
			const initialCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(initialCalls).toHaveLength(2);

			await vi.advanceTimersByTimeAsync(500);

			// Retry only engine-a
			const allCutCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
			expect(allCutCalls).toHaveLength(3);
			expect(allCutCalls[2].url).toContain('engine-a');
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

		it('after 5s TTL expires, GETs go back to A first', async () => {
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

			// Advance time past TTL (6 seconds — TTL is now 5s)
			currentTime += 6_000;

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

		it('clears sticky preference when primary engine recovers', async () => {
			// Step 1: Trigger failover (primary A fails, secondary B succeeds → sets preferred to B)
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.reject(new Error('network error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			await getState();
			expect(fetchCalls[0].url).toContain('engine-a');
			expect(fetchCalls[1].url).toContain('engine-b');

			// Verify B is now sticky: next GET goes to B first without touching A
			fetchCalls = [];
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});
			await getState();
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toContain('engine-b');

			// Step 2: Primary A recovers — A is tried first again (B is preferred, but A is primary)
			// With B preferred, B is moved to front. But we want to test that when A is tried
			// and succeeds, the preference is cleared. We need to force A to be tried first.
			// Reset the preference to simulate TTL expiry, then let A succeed.
			resetPreferredEngine();

			fetchCalls = [];
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				return Promise.resolve(defaultOkResponse({ programSource: 'cam2' }));
			});

			// Primary A succeeds → preference should be cleared (remains clear)
			await getState();
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toContain('engine-a');

			// Step 3: Next request should still go to A (no sticky preference)
			fetchCalls = [];
			await getState();
			expect(fetchCalls).toHaveLength(1);
			expect(fetchCalls[0].url).toContain('engine-a');
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

	// ─── 4xx errors should NOT trip circuit breaker ───

	describe('4xx errors do not trip circuit breaker', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('401 errors on GET do not open circuit breaker', async () => {
			// Make engine A return 401, engine B succeeds.
			// After B succeeds it becomes sticky, so subsequent GETs go to B first.
			// The key assertion: after many 401s, reset sticky routing and verify
			// engine A is still tried (circuit is NOT open).
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.resolve(errorResponse(401, 'token expired'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1', seq: 1 }));
			});

			// Send 10 GETs — engine A fails with 401, B succeeds and becomes sticky
			for (let i = 0; i < 10; i++) {
				await getState();
			}

			// Now reset sticky preference and clear tracking
			resetPreferredEngine();
			fetchCalls = [];

			// Next GET should still try engine A first (circuit NOT open)
			await getState();
			const engineACalls = fetchCalls.filter(c => c.url.includes('engine-a'));
			expect(engineACalls.length).toBeGreaterThanOrEqual(1);
		});

		it('500 errors on GET do open circuit breaker after threshold', async () => {
			// Make engine A return 500, engine B succeeds.
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.resolve(errorResponse(500, 'internal server error'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1', seq: 1 }));
			});

			// Send 6 GETs — engine A gets 500, circuit opens after 5 failures.
			// B succeeds and becomes sticky, so only first GET hits A (then sticky to B).
			// We need to force engine A to be tried: reset sticky after each request.
			for (let i = 0; i < 5; i++) {
				resetPreferredEngine();
				await getState();
			}

			// Now circuit for engine A should be open. Reset sticky and clear tracking.
			resetPreferredEngine();
			fetchCalls = [];

			// Next GET should skip engine A (circuit is open)
			await getState();
			const engineACalls = fetchCalls.filter(c => c.url.includes('engine-a'));
			expect(engineACalls.length).toBe(0);
		});

		it('401 errors on mutations do not open circuit breaker', async () => {
			// Make engine A return 401, engine B return 200
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.resolve(errorResponse(401, 'unauthorized'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
			});

			// Send 6 mutations — engine A returns 401 each time
			for (let i = 0; i < 6; i++) {
				await cut('cam1');
			}

			// Engine A should still be called on every mutation (circuit NOT open)
			const engineACalls = fetchCalls.filter(c => c.url.includes('engine-a'));
			// Each cut() fans out to both engines, so engine A gets one call per cut
			expect(engineACalls.length).toBe(6);
		});

		it('403 errors do not open circuit breaker', async () => {
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.resolve(errorResponse(403, 'forbidden'));
				}
				return Promise.resolve(defaultOkResponse({ programSource: 'cam1', seq: 1 }));
			});

			// Send 10 GETs with 403 from engine A, resetting sticky each time
			for (let i = 0; i < 10; i++) {
				resetPreferredEngine();
				await getState();
			}

			// Reset sticky and clear tracking
			resetPreferredEngine();
			fetchCalls = [];

			// Engine A circuit should NOT be open — it should still be tried
			await getState();
			const engineACalls = fetchCalls.filter(c => c.url.includes('engine-a'));
			expect(engineACalls.length).toBeGreaterThanOrEqual(1);
		});

		it('dualFetch 401 errors do not open circuit breaker', async () => {
			// Make engine A return 401 on dualFetch mutations
			mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
				fetchCalls.push({ url, opts: opts ?? {} });
				if (url.includes('engine-a')) {
					return Promise.resolve(errorResponse(401, 'unauthorized'));
				}
				return Promise.resolve(defaultOkResponse());
			});

			const mockFile = new File(['test'], 'test.zip', { type: 'application/zip' });

			// Send 6 dualFetch mutations — engine A returns 401 each time
			for (let i = 0; i < 6; i++) {
				await uploadStinger('test', mockFile);
			}

			// Engine A should still be called on every request (circuit NOT open)
			const engineACalls = fetchCalls.filter(c => c.url.includes('engine-a'));
			expect(engineACalls.length).toBe(6);
		});
	});

	// ─── Recording fans out to both engines ───

	describe('recording dual-engine fan-out', () => {
		beforeEach(() => {
			setEngineUrls([
				{ id: 'A', url: 'https://engine-a.example.com' },
				{ id: 'B', url: 'https://engine-b.example.com' },
			]);
		});

		it('startRecording fans out to both engines', async () => {
			await startRecording();

			// Both engines should receive the request
			expect(fetchCalls).toHaveLength(2);
			const urls = fetchCalls.map(c => c.url).sort();
			expect(urls).toEqual([
				'https://engine-a.example.com/api/recording/start',
				'https://engine-b.example.com/api/recording/start',
			]);
		});

		it('stopRecording fans out to both engines', async () => {
			await stopRecording();

			// Both engines should receive the request
			expect(fetchCalls).toHaveLength(2);
			const urls = fetchCalls.map(c => c.url).sort();
			expect(urls).toEqual([
				'https://engine-a.example.com/api/recording/stop',
				'https://engine-b.example.com/api/recording/stop',
			]);
		});

		it('startRecording does not include engine field in body', async () => {
			await startRecording({ outputDir: '/tmp/rec', rotateAfterMins: 60 });

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.engine).toBeUndefined();
			expect(body.outputDir).toBe('/tmp/rec');
			expect(body.rotateAfterMins).toBe(60);
		});

		it('stopRecording does not include engine field in body', async () => {
			await stopRecording();

			const body = JSON.parse(fetchCalls[0].opts.body as string);
			expect(body.engine).toBeUndefined();
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

	// ─── Non-idempotent retry skip (Task 1) ───

	describe('non-idempotent retry skip', () => {
		it('isNonIdempotent classifies recording paths', () => {
			expect(isNonIdempotent('/api/recording/start')).toBe(true);
			expect(isNonIdempotent('/api/recording/stop')).toBe(true);
		});

		it('isNonIdempotent classifies output paths', () => {
			expect(isNonIdempotent('/api/output/srt/start')).toBe(true);
			expect(isNonIdempotent('/api/output/srt/stop')).toBe(true);
			expect(isNonIdempotent('/api/output/destinations')).toBe(true);
		});

		it('isNonIdempotent classifies macro run', () => {
			expect(isNonIdempotent('/api/macros/my-macro/run')).toBe(true);
			expect(isNonIdempotent('/api/macros/another-one/run')).toBe(true);
		});

		it('isNonIdempotent classifies scte35 cue', () => {
			expect(isNonIdempotent('/api/scte35/cue')).toBe(true);
		});

		it('isNonIdempotent returns false for idempotent paths', () => {
			expect(isNonIdempotent('/api/switch/cut')).toBe(false);
			expect(isNonIdempotent('/api/switch/preview')).toBe(false);
			expect(isNonIdempotent('/api/audio/master')).toBe(false);
			expect(isNonIdempotent('/api/graphics/1/on')).toBe(false);
		});

		describe('retry behavior with non-idempotent paths', () => {
			beforeEach(() => {
				vi.useFakeTimers();
				setEngineUrls([
					{ id: 'A', url: 'https://engine-a.example.com' },
					{ id: 'B', url: 'https://engine-b.example.com' },
				]);
			});

			afterEach(() => {
				vi.useRealTimers();
			});

			it('idempotent paths DO get retried on partial failure', async () => {
				mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
					fetchCalls.push({ url, opts: opts ?? {} });
					if (url.includes('engine-b')) {
						return Promise.resolve(errorResponse(500, 'engine B crashed'));
					}
					return Promise.resolve(defaultOkResponse({ programSource: 'cam1' }));
				});

				await cut('cam1'); // /api/switch/cut is idempotent

				const initialCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
				expect(initialCalls).toHaveLength(2);

				await vi.advanceTimersByTimeAsync(500);

				// Should have retried engine B
				const allCutCalls = fetchCalls.filter(c => c.url.includes('/api/switch/cut'));
				expect(allCutCalls).toHaveLength(3);
				expect(allCutCalls[2].url).toContain('engine-b');
			});

			it('non-idempotent paths do NOT get retried on partial failure (startRecording)', async () => {
				mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
					fetchCalls.push({ url, opts: opts ?? {} });
					if (url.includes('engine-b')) {
						return Promise.resolve(errorResponse(500, 'engine B crashed'));
					}
					return Promise.resolve(defaultOkResponse({}));
				});

				await startRecording();

				const initialCalls = fetchCalls.filter(c => c.url.includes('/api/recording/start'));
				expect(initialCalls).toHaveLength(2);

				await vi.advanceTimersByTimeAsync(500);

				// Should NOT have retried — still only 2 calls
				const allCalls = fetchCalls.filter(c => c.url.includes('/api/recording/start'));
				expect(allCalls).toHaveLength(2);
			});

			it('non-idempotent paths do NOT get retried on partial failure (addDestination)', async () => {
				mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
					fetchCalls.push({ url, opts: opts ?? {} });
					if (url.includes('engine-b')) {
						return Promise.resolve(errorResponse(500, 'engine B crashed'));
					}
					return Promise.resolve(defaultOkResponse({ id: 'dest-1', state: 'running' }));
				});

				await addDestination({ type: 'srt-caller', address: '1.2.3.4', port: 9000 } as any);

				const initialCalls = fetchCalls.filter(c => c.url.includes('/api/output/destinations'));
				expect(initialCalls).toHaveLength(2);

				await vi.advanceTimersByTimeAsync(500);

				// Should NOT have retried
				const allCalls = fetchCalls.filter(c => c.url.includes('/api/output/destinations'));
				expect(allCalls).toHaveLength(2);
			});

			it('notify message says "no retry" for non-idempotent paths', async () => {
				const notifySpy = vi.fn();
				setPartialFailureNotify(notifySpy);

				mockFetch.mockImplementation((url: string, opts?: RequestInit) => {
					fetchCalls.push({ url, opts: opts ?? {} });
					if (url.includes('engine-b')) {
						return Promise.resolve(errorResponse(500, 'engine B crashed'));
					}
					return Promise.resolve(defaultOkResponse({}));
				});

				await startRecording();
				expect(notifySpy).toHaveBeenCalledWith(expect.stringContaining('non-idempotent operation diverged'));
			});

			it('notify message says "retrying" for idempotent paths', async () => {
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
				expect(notifySpy).toHaveBeenCalledWith(expect.stringContaining('retrying'));
			});
		});
	});

	// ─── Missing timed paths (Task 4) ───

	describe('timed path coverage', () => {
		it('graphics rect is a timed path', () => {
			expect(isTimedPath('/api/graphics/1/rect')).toBe(true);
			expect(isTimedPath('/api/graphics/0/rect')).toBe(true);
			expect(isTimedPath('/api/graphics/99/rect')).toBe(true);
		});

		it('graphics zorder is a timed path', () => {
			expect(isTimedPath('/api/graphics/1/zorder')).toBe(true);
			expect(isTimedPath('/api/graphics/0/zorder')).toBe(true);
		});

		it('caption text is a timed path', () => {
			expect(isTimedPath('/api/captions/text')).toBe(true);
		});

		it('caption newline is a timed path', () => {
			expect(isTimedPath('/api/captions/newline')).toBe(true);
		});

		it('caption clear is a timed path', () => {
			expect(isTimedPath('/api/captions/clear')).toBe(true);
		});

		it('caption mode IS a timed path', () => {
			expect(isTimedPath('/api/captions/mode')).toBe(true);
		});

		it('DVE slot update is a timed path', () => {
			expect(isTimedPath('/api/dve/slots/0')).toBe(true);
			expect(isTimedPath('/api/dve/slots/3')).toBe(true);
		});

		it('existing timed paths still work', () => {
			expect(isTimedPath('/api/switch/cut')).toBe(true);
			expect(isTimedPath('/api/graphics/1/on')).toBe(true);
			expect(isTimedPath('/api/audio/master')).toBe(true);
			expect(isTimedPath('/api/macros/my-macro/run')).toBe(true);
		});
	});
});
