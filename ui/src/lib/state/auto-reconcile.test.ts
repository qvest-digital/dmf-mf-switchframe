import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { SyncStatus } from './reconciler.svelte';
import { createAutoReconcile, SnapshotStaleError, type AutoReconcileConfig } from './auto-reconcile';

function makeConfig(overrides: Partial<AutoReconcileConfig> = {}): AutoReconcileConfig {
	return {
		getSnapshot: vi.fn().mockResolvedValue({ programSource: 'cam1', previewSource: 'cam2' }),
		pushSnapshot: vi.fn().mockResolvedValue({ warnings: [] }),
		onRecoveryStart: vi.fn(),
		onRecoveryComplete: vi.fn(),
		onRecoveryError: vi.fn(),
		...overrides,
	};
}

describe('createAutoReconcile', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('triggers snapshot push after 2s of sustained divergence', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('diverged', 'A');

		// At 1.9s — should NOT have triggered yet
		await vi.advanceTimersByTimeAsync(1900);
		expect(config.getSnapshot).not.toHaveBeenCalled();

		// At 2s — should trigger
		await vi.advanceTimersByTimeAsync(200);
		expect(config.onRecoveryStart).toHaveBeenCalledOnce();
		expect(config.getSnapshot).toHaveBeenCalledWith('A');
		expect(config.pushSnapshot).toHaveBeenCalledWith('B', { programSource: 'cam1', previewSource: 'cam2' });
		expect(config.onRecoveryComplete).toHaveBeenCalledWith([]);

		reconcile.destroy();
	});

	it('triggers snapshot push immediately on mismatch', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('mismatch', 'B');

		// Flush microtasks — mismatch is immediate but still async
		await vi.advanceTimersByTimeAsync(0);

		expect(config.onRecoveryStart).toHaveBeenCalledOnce();
		expect(config.getSnapshot).toHaveBeenCalledWith('B');
		expect(config.pushSnapshot).toHaveBeenCalledWith('A', { programSource: 'cam1', previewSource: 'cam2' });
		expect(config.onRecoveryComplete).toHaveBeenCalledWith([]);

		reconcile.destroy();
	});

	describe('forceSnapshotPush', () => {
		it('pushes snapshot immediately (no 2s divergence delay)', async () => {
			const config = makeConfig();
			const reconcile = createAutoReconcile(config);

			reconcile.forceSnapshotPush('A');

			// Immediate — only microtask flush, no 2s wait
			await vi.advanceTimersByTimeAsync(0);

			expect(config.onRecoveryStart).toHaveBeenCalledOnce();
			expect(config.getSnapshot).toHaveBeenCalledWith('A');
			expect(config.pushSnapshot).toHaveBeenCalledWith('B', { programSource: 'cam1', previewSource: 'cam2' });

			reconcile.destroy();
		});

		it('pushes to opposite engine when B is authoritative', async () => {
			const config = makeConfig();
			const reconcile = createAutoReconcile(config);

			reconcile.forceSnapshotPush('B');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.getSnapshot).toHaveBeenCalledWith('B');
			expect(config.pushSnapshot).toHaveBeenCalledWith('A', expect.anything());

			reconcile.destroy();
		});

		it('bypasses cooldown between pushes', async () => {
			const config = makeConfig();
			const reconcile = createAutoReconcile(config);

			// First push
			reconcile.forceSnapshotPush('A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			// Second push, no cooldown wait — should fire
			reconcile.forceSnapshotPush('A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);

			reconcile.destroy();
		});

		it('cancels a pending 2s divergence timer when forced', async () => {
			const config = makeConfig();
			const reconcile = createAutoReconcile(config);

			// Queue a pending divergence push
			reconcile.onSyncStatusChange('diverged', 'A');
			await vi.advanceTimersByTimeAsync(500);
			expect(config.pushSnapshot).not.toHaveBeenCalled();

			// Force push takes over
			reconcile.forceSnapshotPush('A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			// Advance past the original 2s — no additional push
			await vi.advanceTimersByTimeAsync(2000);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			reconcile.destroy();
		});

		it('is a no-op after destroy()', async () => {
			const config = makeConfig();
			const reconcile = createAutoReconcile(config);

			reconcile.destroy();

			reconcile.forceSnapshotPush('A');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.getSnapshot).not.toHaveBeenCalled();
			expect(config.pushSnapshot).not.toHaveBeenCalled();
		});
	});

	it('does NOT trigger on lagging status', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('lagging', 'A');
		await vi.advanceTimersByTimeAsync(5000);

		expect(config.getSnapshot).not.toHaveBeenCalled();
		expect(config.onRecoveryStart).not.toHaveBeenCalled();

		reconcile.destroy();
	});

	it('does NOT trigger on synced status', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('synced', 'A');
		await vi.advanceTimersByTimeAsync(5000);

		expect(config.getSnapshot).not.toHaveBeenCalled();

		reconcile.destroy();
	});

	it('does NOT trigger on single-engine status', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('single-engine', 'A');
		await vi.advanceTimersByTimeAsync(5000);

		expect(config.getSnapshot).not.toHaveBeenCalled();

		reconcile.destroy();
	});

	it('does NOT trigger on disconnected status', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('disconnected', 'A');
		await vi.advanceTimersByTimeAsync(5000);

		expect(config.getSnapshot).not.toHaveBeenCalled();

		reconcile.destroy();
	});

	it('cancels pending divergence push if status returns to synced before 2s', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('diverged', 'A');
		await vi.advanceTimersByTimeAsync(1500);

		// Status recovers before the 2s timer fires
		reconcile.onSyncStatusChange('synced', 'A');
		await vi.advanceTimersByTimeAsync(1000);

		expect(config.getSnapshot).not.toHaveBeenCalled();
		expect(config.onRecoveryStart).not.toHaveBeenCalled();

		reconcile.destroy();
	});

	it('passes warnings from pushSnapshot to onRecoveryComplete', async () => {
		const config = makeConfig({
			pushSnapshot: vi.fn().mockResolvedValue({ warnings: ['clock skew detected', 'audio state reset'] }),
		});
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);

		expect(config.onRecoveryComplete).toHaveBeenCalledWith(['clock skew detected', 'audio state reset']);

		reconcile.destroy();
	});

	it('calls onRecoveryError on getSnapshot failure', async () => {
		const error = new Error('engine unreachable');
		const config = makeConfig({
			getSnapshot: vi.fn().mockRejectedValue(error),
		});
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);

		expect(config.onRecoveryStart).toHaveBeenCalledOnce();
		expect(config.onRecoveryError).toHaveBeenCalledWith(error);
		expect(config.onRecoveryComplete).not.toHaveBeenCalled();

		reconcile.destroy();
	});

	it('calls onRecoveryError on pushSnapshot failure', async () => {
		const error = new Error('push failed');
		const config = makeConfig({
			pushSnapshot: vi.fn().mockRejectedValue(error),
		});
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);

		expect(config.onRecoveryStart).toHaveBeenCalledOnce();
		expect(config.onRecoveryError).toHaveBeenCalledWith(error);
		expect(config.onRecoveryComplete).not.toHaveBeenCalled();

		reconcile.destroy();
	});

	it('prevents concurrent snapshot pushes', async () => {
		let resolvePush!: (value: { warnings: string[] }) => void;
		const config = makeConfig({
			pushSnapshot: vi.fn().mockImplementation(() => new Promise((r) => { resolvePush = r; })),
		});
		const reconcile = createAutoReconcile(config);

		// First mismatch triggers a push
		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);
		expect(config.onRecoveryStart).toHaveBeenCalledOnce();

		// Second mismatch while first is in flight — should NOT start another
		reconcile.onSyncStatusChange('mismatch', 'B');
		await vi.advanceTimersByTimeAsync(0);
		expect(config.onRecoveryStart).toHaveBeenCalledOnce(); // still 1

		// Resolve the first push
		resolvePush({ warnings: [] });
		await vi.advanceTimersByTimeAsync(0);
		expect(config.onRecoveryComplete).toHaveBeenCalledOnce();

		reconcile.destroy();
	});

	it('allows new push after previous completes and cooldown expires', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		// First push
		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);
		expect(config.onRecoveryStart).toHaveBeenCalledTimes(1);

		// Advance past cooldown (5s)
		vi.advanceTimersByTime(5000);

		// Second push after first completes and cooldown expires
		reconcile.onSyncStatusChange('mismatch', 'B');
		await vi.advanceTimersByTimeAsync(0);
		expect(config.onRecoveryStart).toHaveBeenCalledTimes(2);
		expect(config.getSnapshot).toHaveBeenLastCalledWith('B');
		expect(config.pushSnapshot).toHaveBeenLastCalledWith('A', { programSource: 'cam1', previewSource: 'cam2' });

		reconcile.destroy();
	});

	it('respects cooldown between pushes', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		// First push
		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);
		expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

		// Second mismatch within cooldown -- should NOT push
		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);
		expect(config.pushSnapshot).toHaveBeenCalledTimes(1); // Still 1

		// After cooldown expires -- should push
		vi.advanceTimersByTime(5000);
		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);
		expect(config.pushSnapshot).toHaveBeenCalledTimes(2);

		reconcile.destroy();
	});

	it('cancel() cancels pending timer but does not affect in-flight push', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		// Start a diverged timer
		reconcile.onSyncStatusChange('diverged', 'A');
		await vi.advanceTimersByTimeAsync(1000);

		// Cancel it
		reconcile.cancel();
		await vi.advanceTimersByTimeAsync(2000);

		expect(config.getSnapshot).not.toHaveBeenCalled();

		reconcile.destroy();
	});

	it('destroy() prevents future pushes', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.destroy();

		// Mismatch after destroy — should NOT trigger
		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);

		expect(config.getSnapshot).not.toHaveBeenCalled();
		expect(config.onRecoveryStart).not.toHaveBeenCalled();
	});

	it('destroy() cancels pending divergence timer', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('diverged', 'A');
		await vi.advanceTimersByTimeAsync(1000);

		reconcile.destroy();
		await vi.advanceTimersByTimeAsync(2000);

		expect(config.getSnapshot).not.toHaveBeenCalled();
	});

	it('gets snapshot from authoritative engine and pushes to the other', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		// Authoritative is B → should get from B, push to A
		reconcile.onSyncStatusChange('mismatch', 'B');
		await vi.advanceTimersByTimeAsync(0);

		expect(config.getSnapshot).toHaveBeenCalledWith('B');
		expect(config.pushSnapshot).toHaveBeenCalledWith('A', expect.anything());

		reconcile.destroy();
	});

	it('works without optional callbacks', async () => {
		const config: AutoReconcileConfig = {
			getSnapshot: vi.fn().mockResolvedValue({ data: 'test' }),
			pushSnapshot: vi.fn().mockResolvedValue({ warnings: ['w1'] }),
		};
		const reconcile = createAutoReconcile(config);

		// Should not throw even without onRecoveryStart/Complete/Error
		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);

		expect(config.getSnapshot).toHaveBeenCalled();
		expect(config.pushSnapshot).toHaveBeenCalled();

		reconcile.destroy();
	});

	it('wraps non-Error rejections in Error for onRecoveryError', async () => {
		const config = makeConfig({
			getSnapshot: vi.fn().mockRejectedValue('string error'),
		});
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);

		expect(config.onRecoveryError).toHaveBeenCalledOnce();
		const err = (config.onRecoveryError as ReturnType<typeof vi.fn>).mock.calls[0][0];
		expect(err).toBeInstanceOf(Error);
		expect(err.message).toBe('string error');

		reconcile.destroy();
	});

	it('recovering status cancels pending timer', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('diverged', 'A');
		await vi.advanceTimersByTimeAsync(1000);

		reconcile.onSyncStatusChange('recovering', 'A');
		await vi.advanceTimersByTimeAsync(2000);

		expect(config.getSnapshot).not.toHaveBeenCalled();

		reconcile.destroy();
	});

	it('rapid oscillation between diverged and synced does not leak timers', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		// Rapidly toggle between diverged and synced 10 times
		for (let i = 0; i < 10; i++) {
			reconcile.onSyncStatusChange('diverged', 'A');
			await vi.advanceTimersByTimeAsync(100);
			reconcile.onSyncStatusChange('synced', 'A');
			await vi.advanceTimersByTimeAsync(100);
		}

		// Wait well past the diverge delay -- should NOT trigger any push
		await vi.advanceTimersByTimeAsync(5000);
		expect(config.getSnapshot).not.toHaveBeenCalled();
		expect(config.onRecoveryStart).not.toHaveBeenCalled();

		reconcile.destroy();
	});

	it('sustained diverged after oscillation still triggers push', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		// Oscillate a few times
		for (let i = 0; i < 3; i++) {
			reconcile.onSyncStatusChange('diverged', 'A');
			await vi.advanceTimersByTimeAsync(500);
			reconcile.onSyncStatusChange('synced', 'A');
			await vi.advanceTimersByTimeAsync(200);
		}

		// Now stay diverged
		reconcile.onSyncStatusChange('diverged', 'A');
		await vi.advanceTimersByTimeAsync(2100);

		expect(config.onRecoveryStart).toHaveBeenCalledOnce();
		expect(config.getSnapshot).toHaveBeenCalledWith('A');

		reconcile.destroy();
	});

	it('allows retry after previous push errors', async () => {
		let callCount = 0;
		const config = makeConfig({
			getSnapshot: vi.fn().mockImplementation(() => {
				callCount++;
				if (callCount === 1) {
					return Promise.reject(new Error('transient failure'));
				}
				return Promise.resolve({ programSource: 'cam1' });
			}),
		});
		const reconcile = createAutoReconcile(config);

		// First mismatch triggers push that fails
		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);
		expect(config.onRecoveryError).toHaveBeenCalledOnce();
		expect(config.onRecoveryComplete).not.toHaveBeenCalled();

		// Advance past cooldown (5s) so the retry is allowed
		vi.advanceTimersByTime(5000);

		// Second mismatch should retry (pushInFlight should have been cleared)
		reconcile.onSyncStatusChange('mismatch', 'A');
		await vi.advanceTimersByTimeAsync(0);
		expect(config.onRecoveryComplete).toHaveBeenCalledOnce();

		reconcile.destroy();
	});

	// ── Transition guard ──

	describe('transition guard', () => {
		it('defers push during inTransition', async () => {
			let transitionActive = true;
			const config = makeConfig({
				getAuthoritativeState: () => ({ inTransition: transitionActive }),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			// Push should be deferred while inTransition is true
			await vi.advanceTimersByTimeAsync(0);
			expect(config.getSnapshot).not.toHaveBeenCalled();

			// End transition after 300ms
			vi.advanceTimersByTime(200);
			transitionActive = false;
			await vi.advanceTimersByTimeAsync(100);

			expect(config.getSnapshot).toHaveBeenCalledWith('A');
			expect(config.onRecoveryComplete).toHaveBeenCalled();

			reconcile.destroy();
		});

		it('blocks a concurrent push that arrives during the transition wait', async () => {
			// Regression: the re-entrancy guard (pushInFlight) must be held
			// continuously from function entry through the transition-wait await.
			// Previously the flag was only set after the wait, so a second
			// invocation arriving while the first was parked in waitForTransitionEnd
			// could pass the guard and run a concurrent snapshot pull+push to the
			// same target — a state-consistency hazard since reconcile swaps full
			// engine state.
			let transitionActive = true;
			const config = makeConfig({
				getAuthoritativeState: () => ({ inTransition: transitionActive }),
			});
			const reconcile = createAutoReconcile(config);

			// First mismatch: parks in the transition wait.
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.getSnapshot).not.toHaveBeenCalled();

			// Second mismatch DURING the transition wait — must be blocked.
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			// End the transition; the first (and only) push proceeds after the
			// next 100ms transition poll.
			transitionActive = false;
			await vi.advanceTimersByTimeAsync(100);

			expect(config.getSnapshot).toHaveBeenCalledTimes(1);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			reconcile.destroy();
		});

		it('times out transition guard after 5s', async () => {
			const config = makeConfig({
				getAuthoritativeState: () => ({ inTransition: true }), // never ends
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			// Advance past 5s timeout
			await vi.advanceTimersByTimeAsync(5100);

			// Should have proceeded despite transition still active
			expect(config.getSnapshot).toHaveBeenCalledWith('A');

			reconcile.destroy();
		});

		it('proceeds immediately when no transition is active', async () => {
			const config = makeConfig({
				getAuthoritativeState: () => ({ inTransition: false }),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.getSnapshot).toHaveBeenCalledWith('A');

			reconcile.destroy();
		});

		it('proceeds when getAuthoritativeState is not provided', async () => {
			const config = makeConfig(); // no getAuthoritativeState
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.getSnapshot).toHaveBeenCalledWith('A');

			reconcile.destroy();
		});
	});

	// ── Per-engine cooldown ──

	describe('per-engine cooldown', () => {
		it('allows push to different engine while one is in cooldown', async () => {
			const config = makeConfig();
			const reconcile = createAutoReconcile(config);

			// Push to B (authoritative=A, target=B)
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);
			expect(config.pushSnapshot).toHaveBeenCalledWith('B', expect.anything());

			// Immediately push to A (authoritative=B, target=A) — different target, no cooldown
			reconcile.onSyncStatusChange('mismatch', 'B');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);
			expect(config.pushSnapshot).toHaveBeenLastCalledWith('A', expect.anything());

			reconcile.destroy();
		});
	});

	// ── Retry on failed push ──

	describe('retry on failed push', () => {
		it('retries once after push failure', async () => {
			let callCount = 0;
			const config = makeConfig({
				pushSnapshot: vi.fn().mockImplementation(() => {
					callCount++;
					if (callCount === 1) return Promise.reject(new Error('push failed'));
					return Promise.resolve({ warnings: [] });
				}),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.onRecoveryError).toHaveBeenCalledOnce();

			// Retry fires after 2s + jitter (up to 3s)
			await vi.advanceTimersByTimeAsync(3100);
			expect(config.onRecoveryComplete).toHaveBeenCalledOnce();

			reconcile.destroy();
		});

		it('does not retry more than once', async () => {
			const config = makeConfig({
				pushSnapshot: vi.fn().mockRejectedValue(new Error('persistent failure')),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.onRecoveryError).toHaveBeenCalledTimes(1);

			// Wait for retry
			await vi.advanceTimersByTimeAsync(3100);
			expect(config.onRecoveryError).toHaveBeenCalledTimes(2);

			// No further retries
			await vi.advanceTimersByTimeAsync(10000);
			expect(config.onRecoveryError).toHaveBeenCalledTimes(2);

			reconcile.destroy();
		});
	});

	// ── Partial apply detection ──

	describe('partial apply detection', () => {
		it('re-pushes when critical divergence persists after warnings', async () => {
			let pushCount = 0;
			const config = makeConfig({
				pushSnapshot: vi.fn().mockImplementation(() => {
					pushCount++;
					if (pushCount === 1) return Promise.resolve({ warnings: ['audio state skipped'] });
					return Promise.resolve({ warnings: [] });
				}),
				checkCriticalDivergence: vi.fn()
					.mockReturnValueOnce(true) // first check: still diverged
					.mockReturnValueOnce(false), // after re-push: resolved
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.onRecoveryComplete).toHaveBeenCalledTimes(1);

			// After 500ms, checkCriticalDivergence is called and re-push triggered
			await vi.advanceTimersByTimeAsync(600);
			expect(config.checkCriticalDivergence).toHaveBeenCalled();
			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);

			reconcile.destroy();
		});

		it('does not re-push when no critical divergence and no any divergence after warnings', async () => {
			const config = makeConfig({
				pushSnapshot: vi.fn().mockResolvedValue({ warnings: ['minor issue'] }),
				checkCriticalDivergence: vi.fn().mockReturnValue(false),
				checkAnyDivergence: vi.fn().mockReturnValue(false),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.onRecoveryComplete).toHaveBeenCalledTimes(1);

			// After 500ms, both checks happen but no re-push
			await vi.advanceTimersByTimeAsync(600);
			expect(config.checkCriticalDivergence).toHaveBeenCalled();
			expect(config.checkAnyDivergence).toHaveBeenCalled();
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			reconcile.destroy();
		});

		it('re-pushes with normal cooldown when non-critical divergence persists after warnings', async () => {
			let pushCount = 0;
			const config = makeConfig({
				pushSnapshot: vi.fn().mockImplementation(() => {
					pushCount++;
					if (pushCount === 1) return Promise.resolve({ warnings: ['audio_skipped'] });
					return Promise.resolve({ warnings: [] });
				}),
				checkCriticalDivergence: vi.fn().mockReturnValue(false),
				checkAnyDivergence: vi.fn()
					.mockReturnValueOnce(true)   // first check: still diverged on non-critical fields
					.mockReturnValueOnce(false),  // after re-push: resolved
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.onRecoveryComplete).toHaveBeenCalledTimes(1);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			// After 500ms, checkAnyDivergence triggers re-push (respects cooldown)
			await vi.advanceTimersByTimeAsync(600);
			expect(config.checkCriticalDivergence).toHaveBeenCalled();
			expect(config.checkAnyDivergence).toHaveBeenCalled();
			// Re-push is scheduled but cooldown is active (5s), so it's deferred
			// Advance past the cooldown remaining (~4.4s after the 600ms)
			await vi.advanceTimersByTimeAsync(4500);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);

			reconcile.destroy();
		});

		it('does not re-push on warnings when checkAnyDivergence is not provided', async () => {
			const config = makeConfig({
				pushSnapshot: vi.fn().mockResolvedValue({ warnings: ['audio_skipped'] }),
				checkCriticalDivergence: vi.fn().mockReturnValue(false),
				// checkAnyDivergence not provided
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.onRecoveryComplete).toHaveBeenCalledTimes(1);

			// After 500ms, critical check runs but no any-divergence check available
			await vi.advanceTimersByTimeAsync(600);
			expect(config.checkCriticalDivergence).toHaveBeenCalled();
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			reconcile.destroy();
		});

		it('does not run post-push callback after destroy()', async () => {
			const config = makeConfig({
				pushSnapshot: vi.fn().mockResolvedValue({ warnings: ['audio_skipped'] }),
				checkCriticalDivergence: vi.fn().mockReturnValue(true),
				checkAnyDivergence: vi.fn().mockReturnValue(true),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.onRecoveryComplete).toHaveBeenCalledTimes(1);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			// Destroy before the 500ms post-push callback fires
			reconcile.destroy();
			await vi.advanceTimersByTimeAsync(600);

			// Neither divergence check should have been called — destroyed guard prevents it
			expect(config.checkCriticalDivergence).not.toHaveBeenCalled();
			expect(config.checkAnyDivergence).not.toHaveBeenCalled();
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);
		});

		it('pushInFlight prevents bidirectional push when authority flips mid-flight', async () => {
			let resolvePush!: (value: { warnings: string[] }) => void;
			const config = makeConfig({
				pushSnapshot: vi.fn().mockImplementation(() => new Promise((r) => { resolvePush = r; })),
			});
			const reconcile = createAutoReconcile(config);

			// First mismatch: A authoritative, push to B
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);
			expect(config.pushSnapshot).toHaveBeenCalledWith('B', expect.anything());

			// Authority flips to B while push is in flight — should NOT start reverse push
			reconcile.onSyncStatusChange('mismatch', 'B');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1); // still 1, blocked by pushInFlight

			// Resolve the first push
			resolvePush({ warnings: [] });
			await vi.advanceTimersByTimeAsync(0);
			expect(config.onRecoveryComplete).toHaveBeenCalledTimes(1);
			// No second push was initiated — no bidirectional push occurred
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			reconcile.destroy();
		});

		it('critical divergence takes priority over any-divergence and bypasses cooldown', async () => {
			let pushCount = 0;
			const config = makeConfig({
				pushSnapshot: vi.fn().mockImplementation(() => {
					pushCount++;
					if (pushCount === 1) return Promise.resolve({ warnings: ['audio_skipped'] });
					return Promise.resolve({ warnings: [] });
				}),
				checkCriticalDivergence: vi.fn().mockReturnValueOnce(true).mockReturnValue(false),
				checkAnyDivergence: vi.fn().mockReturnValue(true),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			// After 500ms, critical divergence found — should bypass cooldown and re-push
			await vi.advanceTimersByTimeAsync(600);
			expect(config.checkCriticalDivergence).toHaveBeenCalled();
			// checkAnyDivergence should NOT be called because critical took priority
			expect(config.checkAnyDivergence).not.toHaveBeenCalled();
			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);

			reconcile.destroy();
		});
	});

	// ── Reconcile lock ──

	describe('reconcile lock', () => {
		it('skips push when first lock (A) cannot be acquired', async () => {
			const config = makeConfig({
				acquireLock: vi.fn().mockResolvedValue({ acquired: false }),
				releaseLock: vi.fn().mockResolvedValue(undefined),
				onReconcileBusy: vi.fn(),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			// Lexical acquisition order: 'A' first, 'B' second. First failure aborts.
			expect(config.acquireLock).toHaveBeenCalledWith('A');
			expect(config.acquireLock).not.toHaveBeenCalledWith('B');
			expect(config.onReconcileBusy).toHaveBeenCalledWith('A');
			expect(config.getSnapshot).not.toHaveBeenCalled();

			reconcile.destroy();
		});

		it('releases first lock and aborts if second lock cannot be acquired', async () => {
			let call = 0;
			const config = makeConfig({
				acquireLock: vi.fn().mockImplementation(() => {
					call++;
					if (call === 1) return Promise.resolve({ acquired: true, lockId: 'lock-A' });
					return Promise.resolve({ acquired: false });
				}),
				releaseLock: vi.fn().mockResolvedValue(undefined),
				onReconcileBusy: vi.fn(),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.acquireLock).toHaveBeenNthCalledWith(1, 'A');
			expect(config.acquireLock).toHaveBeenNthCalledWith(2, 'B');
			expect(config.releaseLock).toHaveBeenCalledWith('A', 'lock-A');
			expect(config.onReconcileBusy).toHaveBeenCalledWith('B');
			expect(config.getSnapshot).not.toHaveBeenCalled();

			reconcile.destroy();
		});

		it('acquires both engine locks in lexical order and passes source lockId to getSnapshot', async () => {
			let call = 0;
			const config = makeConfig({
				acquireLock: vi.fn().mockImplementation(() => {
					call++;
					const lockId = call === 1 ? 'lock-A' : 'lock-B';
					return Promise.resolve({ acquired: true, lockId });
				}),
				releaseLock: vi.fn().mockResolvedValue(undefined),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.acquireLock).toHaveBeenNthCalledWith(1, 'A');
			expect(config.acquireLock).toHaveBeenNthCalledWith(2, 'B');
			// authoritative is 'A', so the source lockId is 'A''s = 'lock-A'.
			expect(config.getSnapshot).toHaveBeenCalledWith('A', 'lock-A');
			expect(config.pushSnapshot).toHaveBeenCalledWith('B', expect.anything());
			// Release in reverse: B first, then A.
			expect(config.releaseLock).toHaveBeenNthCalledWith(1, 'B', 'lock-B');
			expect(config.releaseLock).toHaveBeenNthCalledWith(2, 'A', 'lock-A');

			reconcile.destroy();
		});

		it('releases both locks even on push failure', async () => {
			let call = 0;
			const config = makeConfig({
				pushSnapshot: vi.fn().mockRejectedValue(new Error('push failed')),
				acquireLock: vi.fn().mockImplementation(() => {
					call++;
					const lockId = call === 1 ? 'lock-A' : 'lock-B';
					return Promise.resolve({ acquired: true, lockId });
				}),
				releaseLock: vi.fn().mockResolvedValue(undefined),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.releaseLock).toHaveBeenCalledWith('A', 'lock-A');
			expect(config.releaseLock).toHaveBeenCalledWith('B', 'lock-B');

			reconcile.destroy();
		});

		it('dual-browser stampede: two reconcile instances on the same lock server, only one completes', async () => {
			// Shared mock lock server — mirrors the real server's per-engine
			// reconcileLockState semantics. Two browsers compete for the same
			// pair of engine locks; the one that loses the race aborts.
			const held = new Map<string, string>(); // engine -> lockId held
			let counter = 0;
			const mkLockServer = () => ({
				acquireLock: vi.fn(async (engine: string) => {
					if (held.has(engine)) return { acquired: false };
					const lockId = `lock-${engine}-${++counter}`;
					held.set(engine, lockId);
					return { acquired: true, lockId };
				}),
				releaseLock: vi.fn(async (engine: string, lockId: string) => {
					if (held.get(engine) === lockId) held.delete(engine);
				}),
			});

			const busyX = vi.fn();
			const busyY = vi.fn();
			const pushX = vi.fn().mockResolvedValue({ warnings: [] });
			const pushY = vi.fn().mockResolvedValue({ warnings: [] });

			// Browser X: authoritative=A. Browser Y: authoritative=B. Same lock server.
			const lockServer = mkLockServer();
			const configX: AutoReconcileConfig = {
				...makeConfig(),
				...lockServer,
				pushSnapshot: pushX,
				onReconcileBusy: busyX,
			};
			const configY: AutoReconcileConfig = {
				...makeConfig(),
				...lockServer,
				pushSnapshot: pushY,
				onReconcileBusy: busyY,
			};
			const X = createAutoReconcile(configX);
			const Y = createAutoReconcile(configY);

			// Fire both in the same synchronous tick so they race for 'A' first.
			X.onSyncStatusChange('mismatch', 'A');
			Y.onSyncStatusChange('mismatch', 'B');
			await vi.advanceTimersByTimeAsync(0);

			const completed = (pushX.mock.calls.length > 0 ? 1 : 0) + (pushY.mock.calls.length > 0 ? 1 : 0);
			const busied = busyX.mock.calls.length + busyY.mock.calls.length;

			expect(completed).toBe(1);
			expect(busied).toBeGreaterThanOrEqual(1);

			X.destroy();
			Y.destroy();
		});

		it('proceeds without lock when acquireLock not provided', async () => {
			const config = makeConfig(); // no lock functions
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.getSnapshot).toHaveBeenCalledWith('A');
			expect(config.pushSnapshot).toHaveBeenCalled();

			reconcile.destroy();
		});
	});

	it('mismatch with authoritative B gets B snapshot and pushes to A', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('mismatch', 'B');
		await vi.advanceTimersByTimeAsync(0);

		expect(config.getSnapshot).toHaveBeenCalledWith('B');
		expect(config.pushSnapshot).toHaveBeenCalledWith('A', expect.anything());

		reconcile.destroy();
	});

	// ── Cooldown race prevention ──

	describe('cooldown race prevention', () => {
		it('does not allow concurrent pushes during cooldown retry', async () => {
			let pushCount = 0;
			const config = makeConfig({
				getSnapshot: vi.fn().mockImplementation(async () => { pushCount++; return {}; }),
				pushSnapshot: vi.fn().mockResolvedValue({ warnings: [] }),
			});
			const reconcile = createAutoReconcile(config);

			// First push
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(pushCount).toBe(1);

			// Second trigger during cooldown — should NOT start a concurrent push
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(100);
			expect(pushCount).toBe(1); // still 1, blocked by pushInFlight during cooldown

			// After cooldown expires, the scheduled retry runs
			await vi.advanceTimersByTimeAsync(5000);
			expect(pushCount).toBeLessThanOrEqual(2); // at most 2

			reconcile.destroy();
		});

		it('releases pushInFlight when cooldown timer is cancelled by new mismatch', async () => {
			const config = makeConfig();
			const reconcile = createAutoReconcile(config);

			// First push
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			// Trigger during cooldown — schedules cooldown retry timer with pushInFlight=true
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1); // blocked by cooldown

			// New mismatch cancels the cooldown timer, clearing pushInFlight,
			// then tries to push again (but cooldown is still active, so re-schedules)
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1); // still blocked by cooldown

			// Wait for cooldown to expire — the re-scheduled timer fires
			await vi.advanceTimersByTimeAsync(5100);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);

			reconcile.destroy();
		});
	});

	// ── Push timeout ──

	describe('push timeout', () => {
		it('times out if getSnapshot hangs for more than 10s', async () => {
			const config = makeConfig({
				getSnapshot: vi.fn().mockImplementation(() => new Promise(() => {})), // never resolves
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.onRecoveryStart).toHaveBeenCalledOnce();

			// Advance past 10s timeout
			await vi.advanceTimersByTimeAsync(10_100);

			expect(config.onRecoveryError).toHaveBeenCalledOnce();
			const err = (config.onRecoveryError as ReturnType<typeof vi.fn>).mock.calls[0][0];
			expect(err.message).toBe('Snapshot push timed out');
			expect(config.onRecoveryComplete).not.toHaveBeenCalled();

			reconcile.destroy();
		});

		it('times out if pushSnapshot hangs for more than 10s', async () => {
			const config = makeConfig({
				pushSnapshot: vi.fn().mockImplementation(() => new Promise(() => {})), // never resolves
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.onRecoveryStart).toHaveBeenCalledOnce();

			// Advance past 10s timeout
			await vi.advanceTimersByTimeAsync(10_100);

			expect(config.onRecoveryError).toHaveBeenCalledOnce();
			const err = (config.onRecoveryError as ReturnType<typeof vi.fn>).mock.calls[0][0];
			expect(err.message).toBe('Snapshot push timed out');
			expect(config.onRecoveryComplete).not.toHaveBeenCalled();

			reconcile.destroy();
		});

		it('does not time out when push completes within 10s', async () => {
			const config = makeConfig();
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			expect(config.onRecoveryComplete).toHaveBeenCalledOnce();
			expect(config.onRecoveryError).not.toHaveBeenCalled();

			reconcile.destroy();
		});
	});

	describe('stale snapshot (409) retry', () => {
		it('force-pulls and retries once when push returns stale conflict', async () => {
			let pushCount = 0;
			let getSnapshotCount = 0;
			const config = makeConfig({
				getSnapshot: vi.fn().mockImplementation(() => {
					getSnapshotCount++;
					return Promise.resolve({ programSource: 'cam1', fetch: getSnapshotCount });
				}),
				pushSnapshot: vi.fn().mockImplementation(() => {
					pushCount++;
					if (pushCount === 1) {
						return Promise.reject(new SnapshotStaleError(100, 105));
					}
					return Promise.resolve({ warnings: [] });
				}),
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			// Flush any pending microtasks from the retry chain.
			await vi.advanceTimersByTimeAsync(10);

			expect(getSnapshotCount).toBe(2);
			expect(pushCount).toBe(2);
			expect(config.onRecoveryComplete).toHaveBeenCalledOnce();
			expect(config.onRecoveryError).not.toHaveBeenCalled();

			reconcile.destroy();
		});

		it('escalates via notification when retry also returns stale conflict', async () => {
			const onStaleConflict = vi.fn();
			const config = makeConfig({
				pushSnapshot: vi.fn().mockRejectedValue(new SnapshotStaleError(100, 110)),
				onStaleConflict,
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			await vi.advanceTimersByTimeAsync(10);

			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);
			expect(onStaleConflict).toHaveBeenCalledOnce();
			// No generic error: stale is handled, not a transport failure.
			expect(config.onRecoveryError).not.toHaveBeenCalled();

			reconcile.destroy();
		});

		it('does not escalate when the seq gap is narrowing between retry attempts', async () => {
			const onStaleConflict = vi.fn();
			const config = makeConfig({
				// First push: gap = 110 - 100 = 10. Retry: gap = 108 - 105 = 3 (narrower).
				// Engines are converging; retry should return without firing the banner.
				pushSnapshot: vi
					.fn()
					.mockRejectedValueOnce(new SnapshotStaleError(100, 110))
					.mockRejectedValueOnce(new SnapshotStaleError(105, 108)),
				onStaleConflict,
			});
			const reconcile = createAutoReconcile(config);

			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			await vi.advanceTimersByTimeAsync(10);

			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);
			expect(onStaleConflict).not.toHaveBeenCalled();
			expect(config.onRecoveryError).not.toHaveBeenCalled();

			reconcile.destroy();
		});
	});

	describe('pushInFlight release when a diverge timer overlaps the cooldown', () => {
		it('does not permanently disable reconciliation when a diverged status fires during the transition wait', async () => {
			let inTransition = false;
			const config = makeConfig({
				getAuthoritativeState: () => ({ inTransition }),
			});
			const reconcile = createAutoReconcile(config);

			// 1. A successful push starts the 5s per-target cooldown clock.
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			// 2. A new mismatch arrives while a transition is active, inside the
			//    cooldown window — executeSnapshotPush holds pushInFlight and
			//    awaits the transition end.
			inTransition = true;
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);

			// 3. A 'diverged' status fires during that await. Its 2s timer does
			//    NOT own the pushInFlight flag.
			reconcile.onSyncStatusChange('diverged', 'A');

			// 4. The transition ends; the awaited push resumes, sees the cooldown
			//    still active and a pending timer, and returns.
			inTransition = false;
			await vi.advanceTimersByTimeAsync(200);

			// 5. The diverge timer (2s) and the cooldown (5s) both elapse — the
			//    deferred push must eventually execute.
			await vi.advanceTimersByTimeAsync(10_000);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);

			// 6. Future mismatches must still reconcile (flag not stuck forever).
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(10_000);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(3);

			reconcile.destroy();
		});

		it('does not leave the flag stuck when a synced status cancels the overlapping diverge timer', async () => {
			let inTransition = false;
			const config = makeConfig({
				getAuthoritativeState: () => ({ inTransition }),
			});
			const reconcile = createAutoReconcile(config);

			// Successful push -> cooldown active.
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(1);

			// Mismatch during a transition; diverged fires during the wait.
			inTransition = true;
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			reconcile.onSyncStatusChange('diverged', 'A');

			// Transition ends; push resumes into the cooldown branch.
			inTransition = false;
			await vi.advanceTimersByTimeAsync(200);

			// 'synced' cancels the diverge timer before it fires.
			reconcile.onSyncStatusChange('synced', 'A');

			// A later mismatch (after the cooldown expired) must still push.
			await vi.advanceTimersByTimeAsync(6000);
			reconcile.onSyncStatusChange('mismatch', 'A');
			await vi.advanceTimersByTimeAsync(0);
			expect(config.pushSnapshot).toHaveBeenCalledTimes(2);

			reconcile.destroy();
		});
	});
});
