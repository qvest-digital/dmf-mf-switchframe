import type { EngineId } from '$lib/api/types';
import { SnapshotStaleError } from '$lib/state/auto-reconcile';

export interface EngineConfig {
	id: EngineId;
	url: string;
}

export interface DualApiClientConfig {
	engines: EngineConfig[];
}

/**
 * Reduced client for engine-targeted snapshot operations only.
 *
 * All regular API commands (cut, preview, transition, audio, etc.) now go
 * through switch-api.ts whose request() interceptor handles dual-engine
 * fan-out automatically. This class exists solely because snapshot
 * get/push must target a SPECIFIC engine by ID, which the generic
 * fan-out cannot express.
 */
export class DualApiClient {
	constructor(private config: DualApiClientConfig) {}

	/** Late-bind a new engine (e.g., engine B discovered from initial state).
	 * If the engine id already exists and the URL differs, updates the target
	 * URL in place — used when an engine is re-provisioned at a new URL after
	 * crash. Identical URL for an existing id is a no-op. */
	addEngine(id: EngineId, url: string): void {
		const existing = this.config.engines.find((e) => e.id === id);
		if (existing) {
			if (existing.url !== url) {
				existing.url = url;
			}
			return;
		}
		this.config.engines.push({ id, url });
	}

	private authHeaders(): Record<string, string> {
		const token = typeof localStorage !== 'undefined'
			? localStorage.getItem('switchframe_operator_token')
			: null;
		const headers: Record<string, string> = {};
		if (token) {
			headers['Authorization'] = `Bearer ${token}`;
		}
		return headers;
	}

	private findEngine(engineId: EngineId): EngineConfig {
		const engine = this.config.engines.find((e) => e.id === engineId);
		if (!engine) {
			throw new Error(`Unknown engine: ${engineId}`);
		}
		return engine;
	}

	// =============================================
	// State snapshot methods (target specific engine)
	// =============================================

	async getStateSnapshot(engineId: EngineId, lockId?: string): Promise<unknown> {
		const engine = this.findEngine(engineId);
		const url = new URL(`${engine.url}/api/sync/state-snapshot`);
		if (lockId) {
			url.searchParams.set('lockId', lockId);
		}
		const res = await fetch(url.toString(), {
			method: 'GET',
			headers: {
				...this.authHeaders(),
			},
		});
		if (!res.ok) {
			const body = await res.text().catch(() => 'unknown error');
			throw new Error(`Failed to get snapshot from engine ${engineId}: ${res.status} ${body}`);
		}
		return res.json();
	}

	async pushStateSnapshot(
		engineId: EngineId,
		snapshot: unknown,
		opts?: { force: true }
	): Promise<{ warnings: string[] }> {
		const engine = this.findEngine(engineId);
		const query = opts?.force ? '?force=true' : '';
		const res = await fetch(`${engine.url}/api/sync/state-snapshot${query}`, {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json',
				...this.authHeaders(),
			},
			body: JSON.stringify(snapshot),
		});
		if (res.status === 409) {
			// Stale snapshot: engine has advanced past our captured seq.
			const body = await res.json().catch(() => null) as
				{ error?: string; snapshot_seq?: number; current_seq?: number } | null;
			throw new SnapshotStaleError(body?.snapshot_seq ?? 0, body?.current_seq ?? 0);
		}
		if (!res.ok) {
			const body = await res.text().catch(() => 'unknown error');
			throw new Error(`Failed to push snapshot to engine ${engineId}: ${res.status} ${body}`);
		}
		return res.json();
	}

	// =============================================
	// Reconcile lock methods (target specific engine)
	// =============================================

	async acquireReconcileLock(
		engineId: EngineId
	): Promise<{ acquired: boolean; lockId?: string; holder?: string }> {
		const engine = this.findEngine(engineId);
		const res = await fetch(`${engine.url}/api/sync/reconcile-lock`, {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json',
				...this.authHeaders(),
			},
			body: JSON.stringify({ holder: `browser-${Date.now()}` }),
		});
		if (!res.ok) return { acquired: false };
		return res.json();
	}

	async releaseReconcileLock(engineId: EngineId, lockId: string): Promise<void> {
		const engine = this.findEngine(engineId);
		await fetch(`${engine.url}/api/sync/reconcile-lock/${lockId}`, {
			method: 'DELETE',
			headers: this.authHeaders(),
		}).catch(() => {}); // best-effort
	}
}
