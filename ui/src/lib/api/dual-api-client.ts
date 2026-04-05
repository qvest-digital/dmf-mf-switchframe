import type { ClockSync } from '$lib/transport/clock-sync';

export interface EngineConfig {
	id: string;
	url: string;
}

export interface DualApiClientConfig {
	engines: EngineConfig[];
	clockSync: ClockSync;
	/** Returns a monotonically incrementing sequence number for each call. */
	getCommandSeq: () => number;
	authToken?: string;
}

export class DualApiClient {
	constructor(private config: DualApiClientConfig) {}

	private authHeaders(): Record<string, string> {
		const headers: Record<string, string> = {};
		if (this.config.authToken) {
			headers['Authorization'] = `Bearer ${this.config.authToken}`;
		}
		return headers;
	}

	/**
	 * Send a timed command to all engines.
	 * The execute_at timestamp is computed as ptpNow() + safetyMargin() so that
	 * all engines execute the command at the same logical wall-clock instant.
	 */
	async timedCommand(path: string, body: Record<string, unknown>): Promise<void> {
		const executeAt = this.config.clockSync.ptpNow() + this.config.clockSync.safetyMargin();
		const commandSeq = this.config.getCommandSeq();
		const payload = { ...body, execute_at: executeAt, command_seq: commandSeq };

		await Promise.allSettled(
			this.config.engines.map((engine) =>
				fetch(`${engine.url}${path}`, {
					method: 'POST',
					headers: {
						'Content-Type': 'application/json',
						...this.authHeaders(),
					},
					body: JSON.stringify(payload),
				})
			)
		);
	}

	/**
	 * Send an untimed command to all engines (config/label changes, audio levels, etc.).
	 * No execute_at or command_seq is included.
	 */
	async untimedCommand(
		path: string,
		body: Record<string, unknown>,
		method = 'POST'
	): Promise<void> {
		await Promise.allSettled(
			this.config.engines.map((engine) =>
				fetch(`${engine.url}${path}`, {
					method,
					headers: {
						'Content-Type': 'application/json',
						...this.authHeaders(),
					},
					body: JSON.stringify(body),
				})
			)
		);
	}

	// --- Timed switching commands ---

	async cut(source: string): Promise<void> {
		return this.timedCommand('/api/switch/cut', { source });
	}

	async setPreview(source: string): Promise<void> {
		return this.timedCommand('/api/switch/preview', { source });
	}

	// --- Untimed configuration commands ---

	async setLevel(source: string, level: number): Promise<void> {
		return this.untimedCommand(
			`/api/audio/${encodeURIComponent(source)}/level`,
			{ level },
			'PUT'
		);
	}

	async setMasterLevel(level: number): Promise<void> {
		return this.untimedCommand('/api/audio/master', { level }, 'PUT');
	}
}
