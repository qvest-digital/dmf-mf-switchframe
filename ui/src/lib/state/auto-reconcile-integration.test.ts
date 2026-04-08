/**
 * Integration-style tests for auto-reconcile.
 *
 * These tests verify the actual recovery workflow end-to-end:
 * divergence detected → snapshot fetched → snapshot pushed → recovery complete.
 * They test timing, cooldowns, and error recovery — the things that matter
 * in production when engines actually diverge.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createAutoReconcile, type AutoReconcileConfig } from './auto-reconcile';

interface TestConfig extends AutoReconcileConfig {
	getSnapshot: ReturnType<typeof vi.fn> & AutoReconcileConfig['getSnapshot'];
	pushSnapshot: ReturnType<typeof vi.fn> & AutoReconcileConfig['pushSnapshot'];
	onRecoveryStart: ReturnType<typeof vi.fn> & NonNullable<AutoReconcileConfig['onRecoveryStart']>;
	onRecoveryComplete: ReturnType<typeof vi.fn> & NonNullable<AutoReconcileConfig['onRecoveryComplete']>;
	onRecoveryError: ReturnType<typeof vi.fn> & NonNullable<AutoReconcileConfig['onRecoveryError']>;
}

function createTestConfig(overrides: Partial<AutoReconcileConfig> = {}): TestConfig {
	return {
		getSnapshot: vi.fn().mockResolvedValue({ state: 'full-snapshot-data' }),
		pushSnapshot: vi.fn().mockResolvedValue({ warnings: [] }),
		onRecoveryStart: vi.fn(),
		onRecoveryComplete: vi.fn(),
		onRecoveryError: vi.fn(),
		...overrides,
	} as unknown as TestConfig;
}

describe('auto-reconcile integration', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	// ─── Complete recovery workflow ───

	describe('full recovery cycle', () => {
		it('diverged → waits 2s → fetches snapshot from authoritative → pushes to other', async () => {
			const config = createTestConfig();
			const ar = createAutoReconcile(config);

			// Reconciler reports diverged, A is authoritative
			ar.onSyncStatusChange('diverged', 'A');

			// Snapshot push should NOT happen immediately
			expect(config.getSnapshot).not.toHaveBeenCalled();

			// After 2 seconds, recovery should trigger
			vi.advanceTimersByTime(2000);
			await vi.runAllTimersAsync();

			expect(config.onRecoveryStart).toHaveBeenCalledOnce();
			expect(config.getSnapshot).toHaveBeenCalledWith('A');
			expect(config.pushSnapshot).toHaveBeenCalledWith('B', { state: 'full-snapshot-data' });
			expect(config.onRecoveryComplete).toHaveBeenCalledWith([]);
		});

		it('mismatch → triggers recovery immediately (no delay)', async () => {
			const config = createTestConfig();
			const ar = createAutoReconcile(config);

			// Mismatch = same seq, different state. This is urgent.
			ar.onSyncStatusChange('mismatch', 'A');
			await vi.runAllTimersAsync();

			expect(config.onRecoveryStart).toHaveBeenCalledOnce();
			expect(config.getSnapshot).toHaveBeenCalledWith('A');
			expect(config.pushSnapshot).toHaveBeenCalledWith('B', { state: 'full-snapshot-data' });
		});

		it('recovery targets the non-authoritative engine', async () => {
			const config = createTestConfig();
			const ar = createAutoReconcile(config);

			// B is authoritative — snapshot should be pushed to A
			ar.onSyncStatusChange('mismatch', 'B');
			await vi.runAllTimersAsync();

			expect(config.getSnapshot).toHaveBeenCalledWith('B');
			expect(config.pushSnapshot).toHaveBeenCalledWith('A', expect.anything());
		});

		it('passes warnings from pushSnapshot through to onRecoveryComplete', async () => {
			const config = createTestConfig({
				pushSnapshot: vi.fn().mockResolvedValue({
					warnings: ['audio channel "mic1" not found on target', 'clip player 2 state skipped'],
				}),
			});
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('mismatch', 'A');
			await vi.runAllTimersAsync();

			expect(config.onRecoveryComplete).toHaveBeenCalledWith([
				'audio channel "mic1" not found on target',
				'clip player 2 state skipped',
			]);
		});
	});

	// ─── Cancellation ───

	describe('cancellation', () => {
		it('cancels pending diverged timer when status returns to synced', async () => {
			const config = createTestConfig();
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('diverged', 'A');

			// 1 second later, engines re-sync before the 2s timer fires
			vi.advanceTimersByTime(1000);
			ar.onSyncStatusChange('synced', 'A');

			// Wait past the original 2s point
			vi.advanceTimersByTime(2000);
			await vi.runAllTimersAsync();

			// Recovery should never have triggered
			expect(config.getSnapshot).not.toHaveBeenCalled();
		});

		it('cancels pending diverged timer when status changes to lagging', async () => {
			const config = createTestConfig();
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('diverged', 'A');
			vi.advanceTimersByTime(1000);

			// Downgrade to lagging — not diverged enough to warrant recovery
			ar.onSyncStatusChange('lagging', 'A');

			vi.advanceTimersByTime(2000);
			await vi.runAllTimersAsync();

			expect(config.getSnapshot).not.toHaveBeenCalled();
		});

		it('cancel() stops pending recovery', async () => {
			const config = createTestConfig();
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('diverged', 'A');
			vi.advanceTimersByTime(1000);

			ar.cancel();

			vi.advanceTimersByTime(2000);
			await vi.runAllTimersAsync();

			expect(config.getSnapshot).not.toHaveBeenCalled();
		});
	});

	// ─── Cooldown enforcement ───

	describe('cooldown', () => {
		it('enforces 5s cooldown between pushes', async () => {
			const config = createTestConfig();
			const ar = createAutoReconcile(config);

			// First mismatch → immediate push
			ar.onSyncStatusChange('mismatch', 'A');
			await vi.runAllTimersAsync();
			expect(config.getSnapshot).toHaveBeenCalledTimes(1);

			// Second mismatch 2s later → deferred retry scheduled (fires after remaining cooldown)
			vi.advanceTimersByTime(2000);
			ar.onSyncStatusChange('mismatch', 'A');
			// Don't run all timers yet — the retry is pending
			expect(config.getSnapshot).toHaveBeenCalledTimes(1); // still 1

			// After remaining cooldown (3s) → deferred retry fires
			await vi.advanceTimersByTimeAsync(3100);
			expect(config.getSnapshot).toHaveBeenCalledTimes(2);
		});

		it('cooldown starts after push completes, not when it starts', async () => {
			// If the push takes 1s, cooldown should be 5s AFTER that 1s.
			let resolveSnapshot: (val: unknown) => void;
			const slowPush = new Promise(r => { resolveSnapshot = r; });

			const config = createTestConfig({
				pushSnapshot: vi.fn().mockReturnValue(slowPush),
			});
			const ar = createAutoReconcile(config);

			// Start recovery
			ar.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0); // let getSnapshot resolve

			// Push is in flight — second mismatch should be blocked by pushInFlight
			ar.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			// Complete the push after 1s
			vi.advanceTimersByTime(1000);
			resolveSnapshot!({ warnings: [] });
			await vi.advanceTimersByTimeAsync(0);

			// Now 3s after push completed — still in cooldown, but retry is scheduled
			vi.advanceTimersByTime(3000);
			ar.onSyncStatusChange('mismatch', 'A');
			// Don't run all timers — the deferred retry is pending
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			// After remaining cooldown (~2s) → deferred retry fires
			await vi.advanceTimersByTimeAsync(2100);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);
		});
	});

	// ─── Error handling ───

	describe('error handling', () => {
		it('calls onRecoveryError when getSnapshot fails', async () => {
			const config = createTestConfig({
				getSnapshot: vi.fn().mockRejectedValue(new Error('engine A unreachable')),
			});
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.onRecoveryError).toHaveBeenCalledTimes(1);
			expect(config.onRecoveryError.mock.calls[0][0].message).toBe('engine A unreachable');
			// pushSnapshot should NOT have been called
			expect(config.pushSnapshot).not.toHaveBeenCalled();

			// Retry fires after 2-3s and fails again
			await vi.advanceTimersByTimeAsync(3100);
			expect(config.onRecoveryError).toHaveBeenCalledTimes(2);
		});

		it('calls onRecoveryError when pushSnapshot fails', async () => {
			const config = createTestConfig({
				pushSnapshot: vi.fn().mockRejectedValue(new Error('engine B rejected snapshot')),
			});
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.onRecoveryError).toHaveBeenCalledTimes(1);
			expect(config.onRecoveryError.mock.calls[0][0].message).toBe('engine B rejected snapshot');

			// Retry fires after 2-3s and fails again
			await vi.advanceTimersByTimeAsync(3100);
			expect(config.onRecoveryError).toHaveBeenCalledTimes(2);
		});

		it('wraps non-Error rejections in Error', async () => {
			const config = createTestConfig({
				getSnapshot: vi.fn().mockRejectedValue('string error'),
			});
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.onRecoveryError).toHaveBeenCalledTimes(1);
			const err = config.onRecoveryError.mock.calls[0][0];
			expect(err).toBeInstanceOf(Error);
			expect(err.message).toBe('string error');
		});

		it('allows retry after failed recovery once cooldown expires', async () => {
			const config = createTestConfig({
				getSnapshot: vi.fn()
					.mockRejectedValueOnce(new Error('first attempt fails'))
					.mockResolvedValue({ state: 'recovered' }),
			});
			const ar = createAutoReconcile(config);

			// First attempt fails
			ar.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.onRecoveryError).toHaveBeenCalledTimes(1);

			// Auto-retry fires after 2-3s and succeeds (second call resolves)
			await vi.advanceTimersByTimeAsync(3100);
			expect(config.onRecoveryComplete).toHaveBeenCalledOnce();
		});
	});

	// ─── Concurrent prevention ───

	describe('concurrent push prevention', () => {
		it('prevents overlapping snapshot pushes', async () => {
			let resolveFirst: (val: unknown) => void;
			const slowSnapshot = new Promise(r => { resolveFirst = r; });

			const config = createTestConfig({
				getSnapshot: vi.fn().mockReturnValue(slowSnapshot),
			});
			const ar = createAutoReconcile(config);

			// First mismatch starts recovery
			ar.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.getSnapshot).toHaveBeenCalledTimes(1);

			// Second mismatch while first is in flight — should be dropped
			ar.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.getSnapshot).toHaveBeenCalledTimes(1);

			// Complete first
			resolveFirst!({ state: 'data' });
			await vi.runAllTimersAsync();
		});
	});

	// ─── Destroy lifecycle ───

	describe('destroy', () => {
		it('prevents all future recovery after destroy', async () => {
			const config = createTestConfig();
			const ar = createAutoReconcile(config);

			ar.destroy();

			ar.onSyncStatusChange('mismatch', 'A');
			await vi.runAllTimersAsync();

			ar.onSyncStatusChange('diverged', 'A');
			vi.advanceTimersByTime(3000);
			await vi.runAllTimersAsync();

			expect(config.getSnapshot).not.toHaveBeenCalled();
		});

		it('cancels in-flight diverged timer on destroy', async () => {
			const config = createTestConfig();
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('diverged', 'A');
			vi.advanceTimersByTime(1000);

			ar.destroy();

			vi.advanceTimersByTime(2000);
			await vi.runAllTimersAsync();

			expect(config.getSnapshot).not.toHaveBeenCalled();
		});
	});

	// ─── Transition guard (getAuthoritativeState) ───

	describe('transition guard', () => {
		it('defers snapshot push when getAuthoritativeState returns inTransition: true', async () => {
			let transitioning = true;
			const config = createTestConfig({
				getAuthoritativeState: vi.fn(() => ({ inTransition: transitioning })),
			});
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('mismatch', 'A');
			// Transition guard fires immediately — push should not happen yet
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).not.toHaveBeenCalled();

			// Transition ends — guard polling resolves
			transitioning = false;
			await vi.advanceTimersByTimeAsync(200);
			expect(config.pushSnapshot).toHaveBeenCalledOnce();
		});

		it('proceeds immediately when getAuthoritativeState returns inTransition: false', async () => {
			const config = createTestConfig({
				getAuthoritativeState: vi.fn(() => ({ inTransition: false })),
			});
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('mismatch', 'A');
			await vi.runAllTimersAsync();

			expect(config.pushSnapshot).toHaveBeenCalledOnce();
		});

		it('proceeds immediately when getAuthoritativeState returns null', async () => {
			const config = createTestConfig({
				getAuthoritativeState: vi.fn(() => null),
			});
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('mismatch', 'A');
			await vi.runAllTimersAsync();

			expect(config.pushSnapshot).toHaveBeenCalledOnce();
		});

		it('proceeds after timeout even if transition never ends', async () => {
			const config = createTestConfig({
				getAuthoritativeState: vi.fn(() => ({ inTransition: true })),
			});
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).not.toHaveBeenCalled();

			// After 5s timeout, guard gives up and proceeds
			await vi.advanceTimersByTimeAsync(5100);
			expect(config.pushSnapshot).toHaveBeenCalledOnce();
		});
	});

	// ─── Partial apply re-check (checkCriticalDivergence) ───

	describe('checkCriticalDivergence', () => {
		it('re-pushes when warnings returned and checkCriticalDivergence returns true', async () => {
			// checkCriticalDivergence returns true once, then false so the loop terminates
			let callCount = 0;
			const config = createTestConfig({
				pushSnapshot: vi.fn().mockResolvedValue({ warnings: ['some field skipped'] }),
				checkCriticalDivergence: vi.fn(() => callCount++ === 0),
			});
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('mismatch', 'A');
			// Flush the mismatch → getSnapshot → pushSnapshot microtasks (but not the 500ms re-check timer)
			await vi.advanceTimersByTimeAsync(0);

			// First push completed with warnings
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			// Advance past the 500ms re-check window (cooldown bypassed via lastPushTimes.delete)
			await vi.advanceTimersByTimeAsync(600);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);
		});

		it('does not re-push when warnings returned but checkCriticalDivergence returns false', async () => {
			const config = createTestConfig({
				pushSnapshot: vi.fn().mockResolvedValue({ warnings: ['some field skipped'] }),
				checkCriticalDivergence: vi.fn(() => false),
			});
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('mismatch', 'A');
			await vi.runAllTimersAsync();

			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			await vi.advanceTimersByTimeAsync(600);
			// No re-push since divergence resolved
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);
		});

		it('does not re-push when no warnings even if checkCriticalDivergence returns true', async () => {
			const config = createTestConfig({
				pushSnapshot: vi.fn().mockResolvedValue({ warnings: [] }),
				checkCriticalDivergence: vi.fn(() => true),
			});
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('mismatch', 'A');
			await vi.runAllTimersAsync();

			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			await vi.advanceTimersByTimeAsync(600);
			// No re-push since no partial-apply warnings
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);
		});
	});

	// ─── Timer interaction edge cases ───

	describe('timer edge cases', () => {
		it('does not start duplicate diverged timers', async () => {
			const config = createTestConfig();
			const ar = createAutoReconcile(config);

			// Multiple diverged notifications should not stack timers
			ar.onSyncStatusChange('diverged', 'A');
			vi.advanceTimersByTime(500);
			ar.onSyncStatusChange('diverged', 'A');
			vi.advanceTimersByTime(500);
			ar.onSyncStatusChange('diverged', 'A');

			// Only 1s total has passed — timer started at first diverged
			vi.advanceTimersByTime(1100);
			await vi.runAllTimersAsync();

			// Should have fired exactly once (2s from first diverged)
			expect(config.getSnapshot).toHaveBeenCalledTimes(1);
		});

		it('mismatch during pending diverged timer cancels timer and pushes immediately', async () => {
			const config = createTestConfig();
			const ar = createAutoReconcile(config);

			ar.onSyncStatusChange('diverged', 'A');
			vi.advanceTimersByTime(1000);

			// Escalation: mismatch detected while diverged timer pending
			ar.onSyncStatusChange('mismatch', 'A');
			await vi.runAllTimersAsync();

			// Should have triggered immediately, not waited for diverged timer
			expect(config.getSnapshot).toHaveBeenCalledTimes(1);

			// Original diverged timer should be cancelled
			vi.advanceTimersByTime(2000);
			await vi.runAllTimersAsync();
			expect(config.getSnapshot).toHaveBeenCalledTimes(1); // still just 1
		});
	});
});
