import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Mock the API module before importing the module under test.
vi.mock('$lib/api/switch-api', () => ({
	operatorRegister: vi.fn(),
	operatorReconnect: vi.fn(),
	operatorHeartbeat: vi.fn(),
	setAuthToken: vi.fn(),
}));

import { operatorReconnect, operatorHeartbeat } from '$lib/api/switch-api';

describe('operator state', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		localStorage.clear();
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
