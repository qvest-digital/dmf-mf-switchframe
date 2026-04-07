import type { SyncStatus } from './reconciler.svelte';
import type { EngineId } from '$lib/api/types';

export interface AutoReconcileConfig {
	getSnapshot: (engine: EngineId) => Promise<unknown>;
	pushSnapshot: (engine: EngineId, snapshot: unknown) => Promise<{ warnings: string[] }>;
	getAuthoritativeState?: () => { inTransition?: boolean } | null;
	checkCriticalDivergence?: () => boolean;
	acquireLock?: (engine: EngineId) => Promise<{ acquired: boolean; lockId?: string }>;
	releaseLock?: (engine: EngineId, lockId: string) => Promise<void>;
	onRecoveryStart?: () => void;
	onRecoveryComplete?: (warnings: string[]) => void;
	onRecoveryError?: (error: Error) => void;
}

const DIVERGE_DELAY_MS = 2000;
const COOLDOWN_MS = 5000;

function otherEngine(engine: EngineId): EngineId {
	return engine === 'A' ? 'B' : 'A';
}

function toError(err: unknown): Error {
	if (err instanceof Error) return err;
	return new Error(String(err));
}

export function createAutoReconcile(config: AutoReconcileConfig) {
	let pendingTimer: ReturnType<typeof setTimeout> | null = null;
	let pushInFlight = false;
	let destroyed = false;
	// Per-engine cooldown tracking
	const lastPushTimes = new Map<EngineId, number>();
	let retryCount = 0;
	// Track the latest authoritative engine so timer callbacks use the
	// current value, not the one captured when the timer was created.
	let currentAuthoritative: EngineId = 'A';

	function clearPendingTimer() {
		if (pendingTimer !== null) {
			clearTimeout(pendingTimer);
			pendingTimer = null;
		}
	}

	/** Wait for an active transition to complete, polling every 100ms up to timeoutMs. */
	function waitForTransitionEnd(timeoutMs: number): Promise<void> {
		return new Promise((resolve) => {
			const start = Date.now();
			const check = () => {
				const state = config.getAuthoritativeState?.();
				if (!state?.inTransition || Date.now() - start > timeoutMs) {
					resolve();
					return;
				}
				setTimeout(check, 100);
			};
			check();
		});
	}

	async function executeSnapshotPush(authoritative: EngineId) {
		if (pushInFlight || destroyed) return;

		// Transition guard: wait for active transition to complete
		const state = config.getAuthoritativeState?.();
		if (state?.inTransition) {
			pushInFlight = true;
			await waitForTransitionEnd(5000);
			if (destroyed) {
				pushInFlight = false;
				return;
			}
			pushInFlight = false;
		}

		if (pushInFlight || destroyed) return;

		const target = otherEngine(authoritative);
		const remaining = COOLDOWN_MS - (Date.now() - (lastPushTimes.get(target) ?? 0));
		if (remaining > 0) {
			// Cooldown active — schedule a retry so the push isn't lost
			if (pendingTimer === null) {
				pendingTimer = setTimeout(() => {
					pendingTimer = null;
					executeSnapshotPush(currentAuthoritative);
				}, remaining);
			}
			return;
		}

		// Acquire lock before push
		let lockId: string | undefined;
		if (config.acquireLock) {
			const lock = await config.acquireLock(target);
			if (!lock.acquired) {
				// Another browser is reconciling — skip this cycle
				return;
			}
			lockId = lock.lockId;
		}

		pushInFlight = true;

		try {
			config.onRecoveryStart?.();
			const snapshot = await config.getSnapshot(authoritative);
			const result = await config.pushSnapshot(target, snapshot);
			retryCount = 0; // reset on success
			config.onRecoveryComplete?.(result.warnings);

			// Partial apply detection: if warnings and critical divergence persists, re-push
			if (result.warnings.length > 0 && config.checkCriticalDivergence) {
				setTimeout(() => {
					if (config.checkCriticalDivergence?.()) {
						// Critical fields still diverged — re-push (bypass cooldown for this target)
						lastPushTimes.delete(target);
						executeSnapshotPush(currentAuthoritative);
					}
				}, 500);
			}
		} catch (err) {
			config.onRecoveryError?.(toError(err));
			// Retry once after 2s + jitter
			if (retryCount < 1) {
				retryCount++;
				const delay = 2000 + Math.random() * 1000;
				const retryTarget = target;
				setTimeout(() => {
					// Clear cooldown for the target so retry can proceed
					lastPushTimes.delete(retryTarget);
					executeSnapshotPush(currentAuthoritative);
				}, delay);
			}
		} finally {
			pushInFlight = false;
			lastPushTimes.set(target, Date.now());
			if (lockId && config.releaseLock) {
				config.releaseLock(target, lockId);
			}
		}
	}

	function onSyncStatusChange(status: SyncStatus, authoritative: EngineId) {
		if (destroyed) return;

		// Always track the latest authoritative engine
		currentAuthoritative = authoritative;

		if (status === 'mismatch') {
			clearPendingTimer();
			executeSnapshotPush(authoritative);
			return;
		}

		if (status === 'diverged') {
			// Only start a timer if one isn't already pending
			if (pendingTimer === null) {
				pendingTimer = setTimeout(() => {
					pendingTimer = null;
					// Use currentAuthoritative (latest) instead of the closure-captured value
					executeSnapshotPush(currentAuthoritative);
				}, DIVERGE_DELAY_MS);
			}
			return;
		}

		// Any other status: cancel pending timer
		clearPendingTimer();
	}

	function cancel() {
		clearPendingTimer();
	}

	function destroy() {
		destroyed = true;
		clearPendingTimer();
	}

	return {
		onSyncStatusChange,
		cancel,
		destroy,
	};
}
