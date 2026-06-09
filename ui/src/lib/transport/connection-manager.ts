import { createPrismConnection } from '$lib/transport/connection';
import { getAuthHeaders, SwitchApiError } from '$lib/api/switch-api';
import type { ControlRoomState } from '$lib/api/types';

export type ConnectionStatus = 'webtransport' | 'polling' | 'disconnected';

export interface ConnectionManagerConfig {
	url: string;
	onStateUpdate: (state: ControlRoomState | Uint8Array) => void;
	onConnectionStateChange: (state: ConnectionStatus) => void;
	onInitialLoadComplete: () => void;
	onInitialLoadError: (error: string, rawError?: unknown) => void;
}

export class ConnectionManager {
	private connectionState: ConnectionStatus = 'disconnected';
	private pollInterval: ReturnType<typeof setTimeout> | undefined;
	private retryTimer: ReturnType<typeof setTimeout> | undefined;
	private readonly config: ConnectionManagerConfig;
	private readonly connection: ReturnType<typeof createPrismConnection>;
	/** True when MoQ control data has been received via the media pipeline. */
	private hasMoQPipeline = false;
	/** True after the first successful state fetch (initial or polling). */
	private initialLoadDone = false;
	/** True after stop(); guards in-flight fetches from resurrecting the
	 * retry/poll loops or pushing state into torn-down consumers. */
	private stopped = false;
	private pollDelay = 500;
	private readonly POLL_MIN = 500;
	private readonly POLL_MAX = 5000;

	constructor(config: ConnectionManagerConfig) {
		this.config = config;
		this.connection = createPrismConnection({
			url: config.url,
			onControlState: (data) => {
				config.onStateUpdate(data);
				this.hasMoQPipeline = true;
				this.stopPolling();
				this.setConnectionState('webtransport');
			},
			onConnectionChange: (connState) => {
				if (connState === 'connected') {
					this.setConnectionState('webtransport');
				} else if (connState === 'disconnected' || connState === 'error') {
					// Don't downgrade to polling if MoQ data is flowing
					// via the media pipeline (the standalone connection.ts
					// WebTransport always fails in dev mode).
					if (!this.hasMoQPipeline) {
						this.startPolling();
						this.setConnectionState('polling');
					}
				}
			},
		});
	}

	async start(): Promise<void> {
		this.stopped = false;

		// Initial state fetch via REST (with retry on failure)
		await this.fetchInitialState();

		// stop() may have raced the fetch above — don't resurrect polling or
		// open a WebTransport on a torn-down manager.
		if (this.stopped) return;

		// Start REST polling as immediate fallback
		this.startPolling();

		// Attempt WebTransport connection (will replace polling on success)
		this.connection.connect();
	}

	stop(): void {
		this.stopped = true;
		if (this.retryTimer !== undefined) {
			clearTimeout(this.retryTimer);
			this.retryTimer = undefined;
		}
		this.stopPolling();
		this.connection.disconnect();
		this.hasMoQPipeline = false;
		this.initialLoadDone = false;
	}

	get transport(): WebTransport | null {
		return this.connection.transport;
	}

	getConnectionState(): ConnectionStatus {
		return this.connectionState;
	}

	/** Called when MoQ control track delivers state data via the media pipeline. */
	handleControlData(data: Uint8Array): void {
		this.config.onStateUpdate(data);
		this.notifyMoQActive();
	}

	/**
	 * Called when a per-source MoQ transport has connected and subscribed
	 * to its tracks (including the control track). Stops REST polling
	 * since control data will arrive via MoQ when state changes.
	 *
	 * The ControlBroadcaster in Prism has no "latest state replay" for
	 * new subscribers — it only delivers NEW state changes. So we can't
	 * wait for actual control data to arrive; we must trust that the MoQ
	 * connection is alive once the catalog is received.
	 */
	notifyMoQActive(): void {
		if (this.hasMoQPipeline) return;
		this.hasMoQPipeline = true;
		this.stopPolling();
		this.setConnectionState('webtransport');
	}

	private setConnectionState(state: ConnectionStatus): void {
		if (state === this.connectionState) return;
		this.connectionState = state;
		this.config.onConnectionStateChange(state);
	}

	private startPolling(): void {
		if (this.pollInterval) return;
		const poll = async () => {
			try {
				const res = await fetch(`${this.config.url}/api/switch/state`, {
					headers: getAuthHeaders(),
				});
				if (!res.ok) throw new Error(`Poll failed: ${res.status}`);
				const state = await res.json() as ControlRoomState;
				if (this.stopped) return; // stop() raced the in-flight poll
				this.config.onStateUpdate(state);
				this.pollDelay = this.POLL_MIN; // reset on success
				// Clear loading overlay if initial fetch failed but polling succeeds.
				if (!this.initialLoadDone) {
					this.initialLoadDone = true;
					this.config.onInitialLoadComplete();
				}
			} catch {
				// Backoff on failure
				this.pollDelay = Math.min(this.pollDelay * 2, this.POLL_MAX);
			}
			if (this.pollInterval) {
				this.pollInterval = setTimeout(poll, this.pollDelay);
			}
		};
		this.pollInterval = setTimeout(poll, this.pollDelay);
		// Reflect polling state (WebTransport callbacks will override to 'webtransport' if it connects)
		if (this.connectionState !== 'webtransport') {
			this.setConnectionState('polling');
		}
	}

	private stopPolling(): void {
		if (this.pollInterval) {
			clearTimeout(this.pollInterval);
			this.pollInterval = undefined;
		}
		this.pollDelay = this.POLL_MIN; // reset
	}

	private async fetchInitialState(): Promise<void> {
		if (this.stopped) return;
		try {
			const res = await fetch(`${this.config.url}/api/switch/state`, {
				headers: getAuthHeaders(),
			});
			if (!res.ok) {
				const body = await res.text().catch(() => '');
				throw new SwitchApiError(res.status, body || `${res.status}`);
			}
			const state = await res.json() as ControlRoomState;
			if (this.stopped) return; // stop() raced the in-flight fetch
			this.config.onStateUpdate(state);
			if (!this.initialLoadDone) {
				this.initialLoadDone = true;
				this.config.onInitialLoadComplete();
			}
		} catch (e) {
			// stop() raced the in-flight fetch (or aborted it) — re-arming the
			// retry timer here would create an unkillable 3s fetch loop that
			// keeps invoking callbacks on torn-down consumers.
			if (this.stopped) return;
			const msg = e instanceof Error ? e.message : String(e);
			this.config.onInitialLoadError(msg, e);
			// Retry every 3 seconds until successful
			this.retryTimer = setTimeout(() => {
				this.retryTimer = undefined;
				this.fetchInitialState();
			}, 3000);
		}
	}
}
