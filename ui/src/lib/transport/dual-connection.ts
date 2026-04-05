import { ConnectionManager, type ConnectionStatus } from './connection-manager';
import { ClockSync } from './clock-sync';
import { createReconciler, type SyncHealth } from '$lib/state/reconciler.svelte';
import type { ControlRoomState } from '$lib/api/types';

export interface DualConnectionConfig {
	engines: Array<{ id: string; url: string }>; // 1 or 2 entries
	onStateUpdate: (state: ControlRoomState) => void;
	onConnectionStateChange: (state: ConnectionStatus) => void;
	onInitialLoadComplete: () => void;
	onInitialLoadError: (error: string, rawError?: unknown) => void;
}

export class DualConnectionManager {
	private managers: Map<string, ConnectionManager> = new Map();
	private reconciler = createReconciler();
	private clockSync = new ClockSync();
	private _commandSeq = 0;
	readonly isDualEngine: boolean;

	constructor(private config: DualConnectionConfig) {
		this.isDualEngine = config.engines.length > 1;

		for (const engine of config.engines) {
			const mgr = new ConnectionManager({
				url: engine.url,
				onStateUpdate: (update) => {
					let state: ControlRoomState;
					const isBytes = ArrayBuffer.isView(update) || update instanceof ArrayBuffer;
					if (isBytes) {
						state = JSON.parse(new TextDecoder().decode(update as Uint8Array));
					} else {
						state = update as ControlRoomState;
					}

					if (this.isDualEngine) {
						this.reconciler.ingest(engine.id as 'A' | 'B', state);
						config.onStateUpdate(this.reconciler.state);
					} else {
						config.onStateUpdate(update as ControlRoomState);
					}
				},
				onConnectionStateChange: (connState) => {
					if (this.isDualEngine) {
						// Report the best available connection status across all engines
						const states = [...this.managers.values()].map((m) => m.getConnectionState());
						if (states.includes('webtransport')) {
							config.onConnectionStateChange('webtransport');
						} else if (states.includes('polling')) {
							config.onConnectionStateChange('polling');
						} else {
							config.onConnectionStateChange('disconnected');
						}
					} else {
						config.onConnectionStateChange(connState);
					}
				},
				onInitialLoadComplete: config.onInitialLoadComplete,
				onInitialLoadError: config.onInitialLoadError,
			});
			this.managers.set(engine.id, mgr);
		}
	}

	async start(): Promise<void> {
		const promises = [...this.managers.values()].map((m) => m.start());
		await Promise.allSettled(promises);
	}

	stop(): void {
		for (const mgr of this.managers.values()) {
			mgr.stop();
		}
	}

	get state(): ControlRoomState {
		return this.reconciler.state;
	}

	get health(): SyncHealth {
		return this.reconciler.health;
	}

	get clock(): ClockSync {
		return this.clockSync;
	}

	nextCommandSeq(): number {
		return ++this._commandSeq;
	}
}
