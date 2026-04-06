import type { SyncStatus } from './reconciler.svelte';
import type { EngineId } from '$lib/api/types';

export interface AutoReconcileConfig {
	getSnapshot: (engine: EngineId) => Promise<unknown>;
	pushSnapshot: (engine: EngineId, snapshot: unknown) => Promise<{ warnings: string[] }>;
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
	let lastPushTime = 0;
	// Track the latest authoritative engine so timer callbacks use the
	// current value, not the one captured when the timer was created.
	let currentAuthoritative: EngineId = 'A';

	function clearPendingTimer() {
		if (pendingTimer !== null) {
			clearTimeout(pendingTimer);
			pendingTimer = null;
		}
	}

	async function executeSnapshotPush(authoritative: EngineId) {
		if (pushInFlight || destroyed) return;
		const remaining = COOLDOWN_MS - (Date.now() - lastPushTime);
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
		pushInFlight = true;

		try {
			config.onRecoveryStart?.();
			const snapshot = await config.getSnapshot(authoritative);
			const target = otherEngine(authoritative);
			const result = await config.pushSnapshot(target, snapshot);
			config.onRecoveryComplete?.(result.warnings);
		} catch (err) {
			config.onRecoveryError?.(toError(err));
		} finally {
			pushInFlight = false;
			lastPushTime = Date.now();
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
