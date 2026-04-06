import type { EngineId } from '$lib/api/types';

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

	/** Late-bind a new engine (e.g., engine B discovered from initial state). */
	addEngine(id: EngineId, url: string): void {
		if (this.config.engines.some((e) => e.id === id)) return;
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

	async getStateSnapshot(engineId: EngineId): Promise<unknown> {
		const engine = this.findEngine(engineId);
		const res = await fetch(`${engine.url}/api/sync/state-snapshot`, {
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
		snapshot: unknown
	): Promise<{ warnings: string[] }> {
		const engine = this.findEngine(engineId);
		const res = await fetch(`${engine.url}/api/sync/state-snapshot`, {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json',
				...this.authHeaders(),
			},
			body: JSON.stringify(snapshot),
		});
		if (!res.ok) {
			const body = await res.text().catch(() => 'unknown error');
			throw new Error(`Failed to push snapshot to engine ${engineId}: ${res.status} ${body}`);
		}
		return res.json();
	}
}
