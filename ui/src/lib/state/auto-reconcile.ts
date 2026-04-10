import type { SyncStatus } from './reconciler.svelte';
import type { EngineId } from '$lib/api/types';

export interface AutoReconcileConfig {
	getSnapshot: (engine: EngineId) => Promise<unknown>;
	pushSnapshot: (engine: EngineId, snapshot: unknown) => Promise<{ warnings: string[] }>;
	getAuthoritativeState?: () => { inTransition?: boolean } | null;
	checkCriticalDivergence?: () => boolean;
	checkAnyDivergence?: () => boolean;
	acquireLock?: (engine: EngineId) => Promise<{ acquired: boolean; lockId?: string }>;
	releaseLock?: (engine: EngineId, lockId: string) => Promise<void>;
	refreshLock?: (engine: EngineId, lockId: string) => Promise<void>;
	onRecoveryStart?: () => void;
	onRecoveryComplete?: (warnings: string[]) => void;
	onRecoveryError?: (error: Error) => void;
}

const DIVERGE_DELAY_MS = 2000;
const COOLDOWN_MS = 5000;
const PUSH_TIMEOUT_MS = 10_000;
const TRANSITION_POLL_MS = 100;
const TRANSITION_WAIT_TIMEOUT_MS = 5000;
const LOCK_ACQUIRE_TIMEOUT_MS = 5000;
const LOCK_REFRESH_INTERVAL_MS = 5000; // Lock TTL is 15s
const PARTIAL_RECHECK_DELAY_MS = 500;
const RETRY_BASE_DELAY_MS = 2000;
const RETRY_JITTER_MS = 1000;

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
	let cooldownHoldingFlight = false; // true when pushInFlight was set by cooldown scheduling
	let destroyed = false;
	// Per-engine cooldown tracking
	const lastPushTimes = new Map<EngineId, number>();
	let retryCount = 0;
	// Track the latest authoritative engine so timer callbacks use the
	// current value, not the one captured when the timer was created.
	let currentAuthoritative: EngineId = 'A';

	// Telemetry counters
	let pushTotal = 0;
	let pushSuccesses = 0;
	let pushErrors = 0;
	let lastError = '';
	let divergenceDetectedTotal = 0;

	function clearPendingTimer() {
		if (pendingTimer !== null) {
			clearTimeout(pendingTimer);
			pendingTimer = null;
			// If the cancelled timer held the pushInFlight flag (cooldown retry),
			// release it so future pushes aren't permanently blocked.
			if (cooldownHoldingFlight) {
				pushInFlight = false;
				cooldownHoldingFlight = false;
			}
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
				setTimeout(check, TRANSITION_POLL_MS);
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
			await waitForTransitionEnd(TRANSITION_WAIT_TIMEOUT_MS);
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
			// Cooldown active — schedule a retry so the push isn't lost.
			// Set pushInFlight to prevent concurrent calls from racing past
			// the cooldown check while the timer is pending.
			if (pendingTimer === null && !pushInFlight) {
				pushInFlight = true;
				cooldownHoldingFlight = true;
				pendingTimer = setTimeout(() => {
					pendingTimer = null;
					pushInFlight = false;
					cooldownHoldingFlight = false;
					executeSnapshotPush(currentAuthoritative);
				}, remaining);
			}
			return;
		}

		// Acquire lock before push (with timeout)
		let lockId: string | undefined;
		if (config.acquireLock) {
			try {
				const lockPromise = config.acquireLock(target);
				const lock = await Promise.race([
					lockPromise,
					new Promise<{ acquired: false }>((resolve) =>
						setTimeout(() => resolve({ acquired: false }), LOCK_ACQUIRE_TIMEOUT_MS)
					),
				]);
				if (!lock.acquired) {
					return;
				}
				lockId = lock.lockId;
			} catch {
				// Lock acquisition failed (engine unreachable) — skip this cycle.
				// The next sync status change will retry.
				return;
			}
		}

		pushInFlight = true;
		pushTotal++;

		// Start periodic lock refresh to prevent expiry during long operations
		let lockRefreshInterval: ReturnType<typeof setInterval> | null = null;
		if (lockId && config.refreshLock) {
			lockRefreshInterval = setInterval(() => {
				config.refreshLock!(target, lockId!).catch(() => {
					// Lock lost — abort will happen via timeout
				});
			}, LOCK_REFRESH_INTERVAL_MS);
		}

		const timeoutController = new AbortController();
		const timeoutTimer = setTimeout(() => timeoutController.abort(), PUSH_TIMEOUT_MS);
		try {
			config.onRecoveryStart?.();
			const snapshot = await Promise.race([
				config.getSnapshot(authoritative),
				new Promise<never>((_, reject) => {
					timeoutController.signal.addEventListener('abort', () =>
						reject(new Error('Snapshot push timed out')));
				}),
			]);
			const result = await Promise.race([
				config.pushSnapshot(target, snapshot),
				new Promise<never>((_, reject) => {
					timeoutController.signal.addEventListener('abort', () =>
						reject(new Error('Snapshot push timed out')));
				}),
			]);
			clearTimeout(timeoutTimer);
			retryCount = 0; // reset on success
			pushSuccesses++;
			const warnings = result.warnings ?? [];
			config.onRecoveryComplete?.(warnings);

			// Partial apply detection: if warnings present, check for remaining divergence
			if (warnings.length > 0) {
				setTimeout(() => {
					if (destroyed) return;
					// First check critical (re-push immediately, bypass cooldown)
					if (config.checkCriticalDivergence?.()) {
						lastPushTimes.delete(target);
						executeSnapshotPush(currentAuthoritative);
						return;
					}
					// Then check any divergence (re-push with normal cooldown)
					if (config.checkAnyDivergence?.()) {
						executeSnapshotPush(currentAuthoritative);
					}
				}, PARTIAL_RECHECK_DELAY_MS);
			}
		} catch (err) {
			clearTimeout(timeoutTimer);
			pushErrors++;
			lastError = toError(err).message;
			config.onRecoveryError?.(toError(err));
			// Retry once after 2s + jitter
			if (retryCount < 1) {
				retryCount++;
				const delay = RETRY_BASE_DELAY_MS + Math.random() * RETRY_JITTER_MS;
				const retryTarget = target;
				setTimeout(() => {
					// Clear cooldown for the target so retry can proceed
					lastPushTimes.delete(retryTarget);
					executeSnapshotPush(currentAuthoritative);
				}, delay);
			}
		} finally {
			if (lockRefreshInterval) {
				clearInterval(lockRefreshInterval);
			}
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
			divergenceDetectedTotal++;
			clearPendingTimer();
			executeSnapshotPush(authoritative);
			return;
		}

		if (status === 'diverged') {
			divergenceDetectedTotal++;
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
		get stats() {
			return {
				pushTotal,
				pushSuccesses,
				pushErrors,
				lastError,
				lastPushAgoMs: lastPushTimes.size > 0 ? Date.now() - Math.max(...lastPushTimes.values()) : -1,
				cooldownActive: pushInFlight,
				divergenceDetectedTotal,
			};
		},
	};
}
