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
	/** Generation id of the current polling chain. Bumped by stopPolling()
	 * and startPolling() so a stale in-flight poll (one that survived a
	 * stop()/start() cycle or a stopPolling() race) can never re-arm itself
	 * alongside — or overwrite the timer of — a newer chain. */
	private pollGen = 0;
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
					//
					// connection.disconnect() inside stop() fires this callback
					// synchronously — never resurrect polling (or report
					// 'polling') on a stopped manager.
					if (!this.hasMoQPipeline && !this.stopped) {
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
		// Set the stopped flag FIRST: connection.disconnect() below
		// synchronously fires onConnectionChange('disconnected'), whose
		// handler would otherwise restart polling we just stopped.
		this.stopped = true;
		if (this.retryTimer !== undefined) {
			clearTimeout(this.retryTimer);
			this.retryTimer = undefined;
		}
		this.stopPolling();
		// Reset before disconnect() so the synchronous disconnect callback
		// never observes stale "MoQ active" state. The callback itself is
		// also guarded by this.stopped — the ordering here is defense in
		// depth, not the only line of protection.
		this.hasMoQPipeline = false;
		this.initialLoadDone = false;
		this.connection.disconnect();
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
		// Never start (or resurrect) polling on a stopped manager.
		if (this.stopped) return;
		if (this.pollInterval) return;
		// Capture this chain's generation: only the current generation may
		// push state or re-arm. stopPolling() bumps pollGen, so an in-flight
		// poll from a previous chain becomes inert the moment polling is
		// stopped or restarted.
		const gen = ++this.pollGen;
		const poll = async () => {
			try {
				const res = await fetch(`${this.config.url}/api/switch/state`, {
					headers: getAuthHeaders(),
				});
				if (!res.ok) throw new Error(`Poll failed: ${res.status}`);
				const state = await res.json() as ControlRoomState;
				// stop() or a stop()/start() restart raced the in-flight poll —
				// don't push stale state into torn-down/replaced consumers.
				if (this.stopped || gen !== this.pollGen) return;
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
			// Single re-arm site covering both success and failure paths:
			// only the current generation may re-arm, and never after stop().
			// A stale chain re-arming here would overwrite the new chain's
			// timer id, leaving an unclearable duplicate loop.
			if (this.stopped || gen !== this.pollGen) return;
			this.pollInterval = setTimeout(poll, this.pollDelay);
		};
		this.pollInterval = setTimeout(poll, this.pollDelay);
		// Reflect polling state (WebTransport callbacks will override to 'webtransport' if it connects)
		if (this.connectionState !== 'webtransport') {
			this.setConnectionState('polling');
		}
	}

	private stopPolling(): void {
		// Invalidate any in-flight poll from the current chain so it can
		// neither push state nor re-arm when its fetch settles.
		this.pollGen++;
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
