import type { SyncStatus } from './reconciler.svelte';
import type { EngineId } from '$lib/api/types';

/**
 * Thrown by pushSnapshot when the target engine has advanced past the
 * snapshot's captured seq (HTTP 409). The auto-reconcile loop treats this
 * differently from transport errors: it pushes the target's state back the
 * other way, and escalates only when that is stale too, twice in a row.
 */
export class SnapshotStaleError extends Error {
	constructor(
		public snapshotSeq: number,
		public currentSeq: number,
	) {
		super(`snapshot stale: captured at seq=${snapshotSeq}, engine at seq=${currentSeq}`);
		this.name = 'SnapshotStaleError';
	}
}

/**
 * Thrown by getSnapshot when the engine answers 503: it has just started
 * and not yet taken its peer's state, so its own is not the one to copy.
 * The push is retried quietly once the engine is ready.
 */
export class EngineStartingError extends Error {
	constructor(public engine: EngineId) {
		super(`engine ${engine} is still starting`);
		this.name = 'EngineStartingError';
	}
}

/**
 * Thrown by pullFromPeer when the engine has no pull from its peer, as one
 * built before it: its state is then relayed through the browser.
 */
export class PullUnsupportedError extends Error {
	constructor(public engine: EngineId) {
		super(`engine ${engine} cannot pull its peer's state`);
		this.name = 'PullUnsupportedError';
	}
}

export interface AutoReconcileConfig {
	/** Has the engine take its peer's state straight from the peer, under the
	 * lock held on the peer; a snapshot then never passes through the browser.
	 * Throws as pushSnapshot does, PullUnsupportedError where the engine has
	 * no such pull, and EngineStartingError while the peer is starting. */
	pullFromPeer?: (engine: EngineId, peerLockId?: string, opts?: { force: true }) => Promise<{ warnings: string[] }>;
	getSnapshot: (engine: EngineId, lockId?: string) => Promise<unknown>;
	/** force applies the snapshot even when the target has advanced past it. */
	pushSnapshot: (engine: EngineId, snapshot: unknown, opts?: { force: true }) => Promise<{ warnings: string[] }>;
	getAuthoritativeState?: () => { inTransition?: boolean } | null;
	checkCriticalDivergence?: () => boolean;
	checkAnyDivergence?: () => boolean;
	acquireLock?: (engine: EngineId) => Promise<{ acquired: boolean; lockId?: string }>;
	releaseLock?: (engine: EngineId, lockId: string) => Promise<void>;
	refreshLock?: (engine: EngineId, lockId: string) => Promise<void>;
	onRecoveryStart?: () => void;
	onRecoveryComplete?: (warnings: string[]) => void;
	onRecoveryError?: (error: Error) => void;
	/** Called when a push returns 409 stale and the push back from its target
	 * does too, twice in a row without a successful push in between. Signals that engines can't be reconciled automatically — operator action needed. */
	onStaleConflict?: (target: EngineId, snapshotSeq: number, currentSeq: number) => void;
	/** Called when a push and the push back are both refused as stale for the
	 * first time since a successful push. Ends the round without an alarm;
	 * another round follows unless the engines report synced first. */
	onStaleRound?: () => void;
	/** Called when either the source or target lock acquisition failed because
	 * another browser holds it — the peer is reconciling; this browser backs off.
	 * Prevents the AB-vs-BA snapshot stampede that would otherwise silently swap
	 * engine states. */
	onReconcileBusy?: (engine: EngineId) => void;
	/** Called when the source engine is still starting; the push is retried
	 * after ENGINE_STARTING_RETRY_MS. */
	onEngineStarting?: (engine: EngineId) => void;
}

const DIVERGE_DELAY_MS = 2000;
const COOLDOWN_MS = 5000;
// A snapshot carries every graphics layer's image and rendered frame, up to
// the 64 MiB an engine takes, read from one engine and sent to the other:
// at 10 Mbit/s that is about 50 s each way for the largest.
export const PUSH_TIMEOUT_MS = 120_000;
const TRANSITION_POLL_MS = 100;
const TRANSITION_WAIT_TIMEOUT_MS = 5000;
const LOCK_ACQUIRE_TIMEOUT_MS = 5000;
const LOCK_REFRESH_INTERVAL_MS = 5000; // Lock TTL is 15s
const PARTIAL_RECHECK_DELAY_MS = 500;
const RETRY_BASE_DELAY_MS = 2000;
const RETRY_JITTER_MS = 1000;
const ENGINE_STARTING_RETRY_MS = 2000;

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
	// Pushes refused as stale in both directions since the last successful
	// one. Commands still arriving on both engines refuse a round once; the
	// engines need an operator only when it happens again.
	let doubleStaleCount = 0;
	// Set while an operator push waits for its retry. Auto pushes follow the
	// reconciler's authority and would overwrite the state the operator chose
	// to keep, so none starts in the meantime.
	let operatorRetryPending = false;
	let retryTimer: ReturnType<typeof setTimeout> | null = null;
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

	async function executeSnapshotPush(authoritative: EngineId, overrideStale = false) {
		if (pushInFlight || destroyed) return;

		// Hold the re-entrancy guard continuously from here through the
		// transition-wait, lock acquisition, and push. Previously the flag was
		// dropped across these awaits, leaving a window in which a second
		// invocation (a new 'mismatch' status or a retry timer) could pass the
		// guard above and run a concurrent snapshot pull+push to the same
		// target — a state-consistency hazard since reconcile swaps full engine
		// state. Every early-return below must release it, EXCEPT the cooldown
		// path, which deliberately keeps it held until its pending timer fires
		// (tracked via cooldownHoldingFlight so clearPendingTimer can release it).
		pushInFlight = true;

		// Transition guard: wait for active transition to complete
		const state = config.getAuthoritativeState?.();
		if (state?.inTransition) {
			await waitForTransitionEnd(TRANSITION_WAIT_TIMEOUT_MS);
		}

		if (destroyed) {
			pushInFlight = false;
			return;
		}

		const target = otherEngine(authoritative);
		const remaining = COOLDOWN_MS - (Date.now() - (lastPushTimes.get(target) ?? 0));
		if (remaining > 0) {
			// Cooldown active — schedule a retry so the push isn't lost. Keep
			// pushInFlight held by the pending timer so concurrent calls can't
			// race past the cooldown check while the timer is pending.
			if (pendingTimer === null) {
				cooldownHoldingFlight = true;
				pendingTimer = setTimeout(() => {
					pendingTimer = null;
					pushInFlight = false;
					cooldownHoldingFlight = false;
					executeSnapshotPush(currentAuthoritative);
				}, remaining);
			} else if (!cooldownHoldingFlight) {
				// The pending timer is a diverge-delay timer created by
				// onSyncStatusChange('diverged') during the transition-wait await
				// above. Its callback does NOT release pushInFlight (it re-invokes
				// executeSnapshotPush, which would no-op at the re-entrancy guard),
				// and clearPendingTimer only releases the flag for cooldown timers.
				// Keeping the flag held here would permanently disable
				// reconciliation, so release it — the diverge timer owns the retry.
				pushInFlight = false;
			}
			// A cooldown timer is pending (it owns the flag release). Stay held.
			return;
		}

		// Acquire BOTH engine locks in a fixed lexical order (A before B) to
		// avoid AB-vs-BA deadlock when two browsers try to reconcile in
		// opposite directions simultaneously. Without the source-side lock
		// the GET /sync/state-snapshot capture is unauthenticated — a peer
		// could pull a contradictory snapshot from the other engine and both
		// reconciles would silently swap engine states.
		const first: EngineId = 'A';
		const second: EngineId = 'B';
		let firstLockId: string | undefined;
		let secondLockId: string | undefined;
		if (config.acquireLock) {
			try {
				const acquireWithTimeout = (engine: EngineId) =>
					Promise.race([
						config.acquireLock!(engine),
						new Promise<{ acquired: false }>((resolve) =>
							setTimeout(() => resolve({ acquired: false }), LOCK_ACQUIRE_TIMEOUT_MS),
						),
					]);

				const lock1 = await acquireWithTimeout(first);
				if (!lock1.acquired) {
					pushInFlight = false;
					config.onReconcileBusy?.(first);
					return;
				}
				firstLockId = lock1.lockId;

				const lock2 = await acquireWithTimeout(second);
				if (!lock2.acquired) {
					if (firstLockId && config.releaseLock) {
						config.releaseLock(first, firstLockId).catch(() => {});
					}
					pushInFlight = false;
					config.onReconcileBusy?.(second);
					return;
				}
				secondLockId = lock2.lockId;
			} catch {
				if (firstLockId && config.releaseLock) {
					config.releaseLock(first, firstLockId).catch(() => {});
				}
				pushInFlight = false;
				return;
			}
		}

		// Select the lockId for the source engine so it can be passed to the
		// GET. Holding the target lock simultaneously is what prevents a peer
		// browser from starting a reverse-direction reconcile.
		const sourceLockId = authoritative === first ? firstLockId : secondLockId;

		// pushInFlight is already held continuously from function entry.
		pushTotal++;

		// Start periodic lock refresh to prevent expiry during long operations.
		// Refresh both held locks — losing the source lock mid-push would let
		// a peer open a new reconcile window and race our apply.
		let lockRefreshInterval: ReturnType<typeof setInterval> | null = null;
		if (config.refreshLock && (firstLockId || secondLockId)) {
			lockRefreshInterval = setInterval(() => {
				if (firstLockId) {
					config.refreshLock!(first, firstLockId).catch(() => {});
				}
				if (secondLockId) {
					config.refreshLock!(second, secondLockId).catch(() => {});
				}
			}, LOCK_REFRESH_INTERVAL_MS);
		}

		const timeoutController = new AbortController();
		const timeoutTimer = setTimeout(() => timeoutController.abort(), PUSH_TIMEOUT_MS);
		try {
			config.onRecoveryStart?.();
			const timeoutRace = <T,>(p: Promise<T>): Promise<T> => Promise.race([
				p,
				new Promise<never>((_, reject) => {
					timeoutController.signal.addEventListener('abort', () =>
						reject(new Error('Snapshot push timed out')));
				}),
			]);

			// Copies from's state to `to`: the engines move it between
			// themselves, and only to an engine that cannot is it relayed.
			const copyState = async (from: EngineId, to: EngineId, fromLockId: string | undefined, force: boolean) => {
				if (config.pullFromPeer) {
					try {
						return await timeoutRace(force
							? config.pullFromPeer(to, fromLockId, { force: true })
							: config.pullFromPeer(to, fromLockId));
					} catch (err) {
						if (!(err instanceof PullUnsupportedError)) throw err;
					}
				}
				const snapshot = await timeoutRace(fromLockId ? config.getSnapshot(from, fromLockId) : config.getSnapshot(from));
				return timeoutRace(force
					? config.pushSnapshot(to, snapshot, { force: true })
					: config.pushSnapshot(to, snapshot));
			};

			let result: { warnings: string[] };
			try {
				result = await copyState(authoritative, target, sourceLockId, overrideStale);
			} catch (pushErr) {
				if (pushErr instanceof SnapshotStaleError) {
					// The target holds a newer command than the source, so its
					// state is the one to keep: push it the other way. Only when
					// that is refused too do the engines need an operator.
					const targetLockId = target === first ? firstLockId : secondLockId;
					try {
						result = await copyState(target, authoritative, targetLockId, false);
					} catch (reverseErr) {
						if (reverseErr instanceof SnapshotStaleError) {
							clearTimeout(timeoutTimer);
							doubleStaleCount++;
							if (doubleStaleCount >= 2) {
								config.onStaleConflict?.(authoritative, reverseErr.snapshotSeq, reverseErr.currentSeq);
							} else {
								config.onStaleRound?.();
								// A round can start and end between two status ticks,
								// so no status change is left to start the next one.
								// Pending until then: a synced status cancels it.
								if (pendingTimer === null) {
									pendingTimer = setTimeout(() => {
										pendingTimer = null;
										executeSnapshotPush(currentAuthoritative);
									}, DIVERGE_DELAY_MS);
								}
							}
							return;
						}
						throw reverseErr;
					}
				} else {
					throw pushErr;
				}
			}
			clearTimeout(timeoutTimer);
			retryCount = 0; // reset on success
			doubleStaleCount = 0;
			pushSuccesses++;
			const warnings = result.warnings ?? [];
			config.onRecoveryComplete?.(warnings);

			// Partial apply detection: if warnings present, check for remaining divergence.
			// Not after an operator push: a forced stale apply always warns, and a
			// follow-up would run in the reconciler's direction, not the operator's.
			if (warnings.length > 0 && !overrideStale) {
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
			if (err instanceof EngineStartingError) {
				config.onEngineStarting?.(err.engine);
				if (overrideStale) {
					// An operator push keeps the operator's direction, and no
					// status change or auto push replaces it meanwhile.
					operatorRetryPending = true;
					if (retryTimer !== null) clearTimeout(retryTimer);
					const retryTarget = target;
					retryTimer = setTimeout(() => {
						retryTimer = null;
						operatorRetryPending = false;
						lastPushTimes.delete(retryTarget);
						executeSnapshotPush(authoritative, true);
					}, ENGINE_STARTING_RETRY_MS);
				} else if (pendingTimer === null) {
					pendingTimer = setTimeout(() => {
						pendingTimer = null;
						executeSnapshotPush(currentAuthoritative);
					}, ENGINE_STARTING_RETRY_MS);
				}
				return;
			}
			pushErrors++;
			lastError = toError(err).message;
			config.onRecoveryError?.(toError(err));
			// Retry once after 2s + jitter
			if (retryCount < 1) {
				retryCount++;
				const delay = RETRY_BASE_DELAY_MS + Math.random() * RETRY_JITTER_MS;
				const retryTarget = target;
				if (overrideStale) operatorRetryPending = true;
				retryTimer = setTimeout(() => {
					retryTimer = null;
					operatorRetryPending = false;
					// Clear cooldown for the target so retry can proceed
					lastPushTimes.delete(retryTarget);
					// An operator push retries in the direction the operator chose.
					executeSnapshotPush(overrideStale ? authoritative : currentAuthoritative, overrideStale);
				}, delay);
			}
		} finally {
			if (lockRefreshInterval) {
				clearInterval(lockRefreshInterval);
			}
			pushInFlight = false;
			lastPushTimes.set(target, Date.now());
			if (config.releaseLock) {
				if (secondLockId) {
					config.releaseLock(second, secondLockId).catch(() => {});
				}
				if (firstLockId) {
					config.releaseLock(first, firstLockId).catch(() => {});
				}
			}
		}
	}

	function onSyncStatusChange(status: SyncStatus, authoritative: EngineId) {
		if (destroyed) return;

		// Always track the latest authoritative engine
		currentAuthoritative = authoritative;
		if (operatorRetryPending) return;

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

	function clearRetry() {
		if (retryTimer !== null) clearTimeout(retryTimer);
		retryTimer = null;
		operatorRetryPending = false;
	}

	function destroy() {
		destroyed = true;
		clearRetry();
		clearPendingTimer();
	}

	/** Immediate push that bypasses the 2s divergence timer and per-target
	 *  cooldown. Used on engine discovery so the newly discovered engine
	 *  catches up on commands it missed during the pre-discovery window,
	 *  and for an operator's manual push, which overrides the target's
	 *  newer state because the operator chose to overwrite it.
	 *  Returns false when it could not start because a push is already running. */
	function forceSnapshotPush(authoritative: EngineId, opts: { overrideStale?: boolean } = {}): boolean {
		if (destroyed) return false;
		clearPendingTimer();
		if (pushInFlight) return false;
		currentAuthoritative = authoritative;
		// A newer push supersedes a pending retry of an older one.
		clearRetry();
		// A manual push gets its own retry, even after auto pushes used theirs.
		if (opts.overrideStale) retryCount = 0;
		lastPushTimes.delete(otherEngine(authoritative));
		executeSnapshotPush(authoritative, opts.overrideStale);
		return true;
	}

	return {
		onSyncStatusChange,
		forceSnapshotPush,
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
