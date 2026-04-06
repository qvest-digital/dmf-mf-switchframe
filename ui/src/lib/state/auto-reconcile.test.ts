import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { SyncStatus } from './reconciler.svelte';
import { createAutoReconcile, type AutoReconcileConfig } from './auto-reconcile';

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

	it('mismatch with authoritative B gets B snapshot and pushes to A', async () => {
		const config = makeConfig();
		const reconcile = createAutoReconcile(config);

		reconcile.onSyncStatusChange('mismatch', 'B');
		await vi.advanceTimersByTimeAsync(0);

		expect(config.getSnapshot).toHaveBeenCalledWith('B');
		expect(config.pushSnapshot).toHaveBeenCalledWith('A', expect.anything());

		reconcile.destroy();
	});
});
