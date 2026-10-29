import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Mock the API module before importing the module under test.
vi.mock('$lib/api/switch-api', () => ({
	operatorRegister: vi.fn(),
	operatorReconnect: vi.fn(),
	operatorHeartbeat: vi.fn(),
	setAuthToken: vi.fn(),
	SwitchApiError: class extends Error {
		constructor(public status: number, message: string) { super(message); }
	},
}));

import { operatorRegister, operatorReconnect, operatorHeartbeat, setAuthToken, SwitchApiError } from '$lib/api/switch-api';

describe('operator state', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		localStorage.clear();
		sessionStorage.clear();
		vi.resetModules();
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	it('reconnect failure stops heartbeat timer', async () => {
		// Dynamic import to get fresh module state after resetModules.
		const mod = await import('./operator.svelte');

		// Seed a stored token so reconnect() proceeds.
		localStorage.setItem('switchframe_operator_token', 'test-token');

		// First reconnect succeeds — starts heartbeat.
		vi.mocked(operatorReconnect).mockResolvedValueOnce({
			id: 'op1',
			name: 'Test',
			role: 'director' as const,
		});
		const ok = await mod.reconnect();
		expect(ok).toBe(true);

		// Verify heartbeat fires.
		vi.mocked(operatorHeartbeat).mockResolvedValue({ ok: true });
		await vi.advanceTimersByTimeAsync(10_000);
		expect(operatorHeartbeat).toHaveBeenCalled();

		// Now reconnect fails.
		vi.mocked(operatorReconnect).mockRejectedValueOnce(new Error('network error'));
		const failed = await mod.reconnect();
		expect(failed).toBe(false);

		// Clear mock call count, advance time, verify heartbeat does NOT fire.
		vi.mocked(operatorHeartbeat).mockClear();
		await vi.advanceTimersByTimeAsync(30_000);
		expect(operatorHeartbeat).not.toHaveBeenCalled();
	});

	// A heartbeat the engines answer 401 forever was sent every 10s.
	it('stops the heartbeat at a 401 and reconnects once', async () => {
		const mod = await import('./operator.svelte');
		localStorage.setItem('switchframe_operator_token', 'test-token');
		vi.mocked(operatorReconnect).mockResolvedValueOnce({ id: 'op1', name: 'Test', role: 'director' as const });
		expect(await mod.reconnect()).toBe(true);
		vi.mocked(operatorReconnect).mockClear();

		vi.mocked(operatorHeartbeat).mockRejectedValue(new SwitchApiError(401, 'invalid token'));
		vi.mocked(operatorReconnect).mockRejectedValue(new SwitchApiError(401, 'invalid token'));
		await vi.advanceTimersByTimeAsync(120_000);

		expect(operatorHeartbeat).toHaveBeenCalledTimes(1);
		expect(operatorReconnect).toHaveBeenCalledTimes(1);
		expect(mod.getSession()).toBeNull();
	});

	it('backs the heartbeat off while the engines do not answer', async () => {
		const mod = await import('./operator.svelte');
		localStorage.setItem('switchframe_operator_token', 'test-token');
		vi.mocked(operatorReconnect).mockResolvedValueOnce({ id: 'op1', name: 'Test', role: 'director' as const });
		expect(await mod.reconnect()).toBe(true);

		vi.mocked(operatorHeartbeat).mockReset();
		vi.mocked(operatorHeartbeat).mockRejectedValue(new TypeError('Failed to fetch'));
		await vi.advanceTimersByTimeAsync(120_000);

		// At 10, 30, 60, 90 and 120s rather than every 10s.
		expect(operatorHeartbeat).toHaveBeenCalledTimes(5);
		mod.disconnect();
	});

	it('caches session in sessionStorage on register', async () => {
		const mod = await import('./operator.svelte');

		vi.mocked(operatorRegister).mockResolvedValueOnce({
			id: 'op1',
			name: 'Alice',
			role: 'director' as const,
			token: 'tok-1',
		});
		await mod.register('Alice', 'director');

		const cached = sessionStorage.getItem('switchframe_operator_session');
		expect(cached).toBeTruthy();
		const parsed = JSON.parse(cached!);
		expect(parsed.id).toBe('op1');
		expect(parsed.name).toBe('Alice');
		expect(parsed.role).toBe('director');
	});

	// Registration fans out to both engines of a dual-engine pair. Each must
	// get the same token, the one this browser then sends to both.
	it('registers with a client-generated token in the engine format', async () => {
		const mod = await import('./operator.svelte');
		vi.mocked(operatorRegister).mockImplementationOnce(async (name, role, id, token) => ({ id: id!, name, role, token: token! }));
		await mod.register('Alice', 'director');

		const [, , id, token] = vi.mocked(operatorRegister).mock.calls.at(-1)!;
		expect(id).toBeTruthy();
		expect(token).toMatch(/^[0-9a-f]{64}$/);
		expect(setAuthToken).toHaveBeenLastCalledWith(token);
	});

	it('generates a different token for each registration', async () => {
		const mod = await import('./operator.svelte');
		expect(mod.newOperatorToken()).not.toBe(mod.newOperatorToken());
	});

	it('caches session in sessionStorage on successful reconnect', async () => {
		const mod = await import('./operator.svelte');
		localStorage.setItem('switchframe_operator_token', 'test-token');

		vi.mocked(operatorReconnect).mockResolvedValueOnce({
			id: 'op2',
			name: 'Bob',
			role: 'director' as const,
		});
		await mod.reconnect();

		const cached = sessionStorage.getItem('switchframe_operator_session');
		expect(cached).toBeTruthy();
		const parsed = JSON.parse(cached!);
		expect(parsed.id).toBe('op2');
		expect(parsed.name).toBe('Bob');
		expect(parsed.role).toBe('director');
	});

	it('uses cached session for instant display during reconnect', async () => {
		const mod = await import('./operator.svelte');
		localStorage.setItem('switchframe_operator_token', 'test-token');

		// Pre-populate session cache
		sessionStorage.setItem('switchframe_operator_session', JSON.stringify({
			id: 'cached-id', name: 'Cached', role: 'director',
		}));

		// Make reconnect hang (never resolves) to test the instant display
		let resolveReconnect!: (v: any) => void;
		vi.mocked(operatorReconnect).mockImplementation(() => new Promise(r => { resolveReconnect = r; }));

		const promise = mod.reconnect();

		// Session should be populated from cache immediately
		const session = mod.getSession();
		expect(session).toBeTruthy();
		expect(session!.name).toBe('Cached');
		expect(session!.id).toBe('cached-id');

		// Resolve reconnect with fresh data
		resolveReconnect({ id: 'fresh-id', name: 'Fresh', role: 'operator' });
		await promise;

		// Session should now have fresh data
		const updated = mod.getSession();
		expect(updated!.id).toBe('fresh-id');
		expect(updated!.name).toBe('Fresh');
	});

	it('clears session cache on reconnect failure', async () => {
		const mod = await import('./operator.svelte');
		localStorage.setItem('switchframe_operator_token', 'test-token');

		// Pre-populate session cache
		sessionStorage.setItem('switchframe_operator_session', JSON.stringify({
			id: 'cached-id', name: 'Cached', role: 'director',
		}));

		vi.mocked(operatorReconnect).mockRejectedValueOnce(new Error('network error'));
		await mod.reconnect();

		// Cache should be cleared
		expect(sessionStorage.getItem('switchframe_operator_session')).toBeNull();
		// Session should be null
		expect(mod.getSession()).toBeNull();
	});

	it('disconnect stops heartbeat timer', async () => {
		const mod = await import('./operator.svelte');

		localStorage.setItem('switchframe_operator_token', 'test-token');

		vi.mocked(operatorReconnect).mockResolvedValueOnce({
			id: 'op1',
			name: 'Test',
			role: 'director' as const,
		});
		await mod.reconnect();

		vi.mocked(operatorHeartbeat).mockResolvedValue({ ok: true });
		await vi.advanceTimersByTimeAsync(10_000);
		expect(operatorHeartbeat).toHaveBeenCalled();

		mod.disconnect();

		vi.mocked(operatorHeartbeat).mockClear();
		await vi.advanceTimersByTimeAsync(30_000);
		expect(operatorHeartbeat).not.toHaveBeenCalled();
	});
});
