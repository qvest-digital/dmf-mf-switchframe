import { ConnectionManager, type ConnectionStatus } from './connection-manager';
import { ClockSync } from './clock-sync';
import { createReconciler, type SyncHealth, type EngineHealthDetail } from '$lib/state/reconciler.svelte';
import type { ControlRoomState, EngineId } from '$lib/api/types';

export interface DualConnectionConfig {
	engines: Array<{ id: EngineId; url: string }>; // 1 or 2 entries
	onStateUpdate: (state: ControlRoomState) => void;
	onConnectionStateChange: (state: ConnectionStatus) => void;
	onInitialLoadComplete: () => void;
	onInitialLoadError: (error: string, rawError?: unknown) => void;
}

export class DualConnectionManager {
	private managers: Map<string, ConnectionManager> = new Map();
	private engineStates: Map<string, ConnectionStatus> = new Map();
	private reconciler = createReconciler();
	private clockSync = new ClockSync();
	private _commandSeq = 0;
	private _isDualEngine: boolean;
	private _started = false;
	private _initialLoadDone = false;
	private _initialLoadErrors = 0;
	private _pendingEngineStarts: string[] = [];

	constructor(private config: DualConnectionConfig) {
		this._isDualEngine = config.engines.length > 1;

		for (const engine of config.engines) {
			this.createEngineManager(engine.id, engine.url);
		}
	}

	get isDualEngine(): boolean {
		return this._isDualEngine;
	}

	/** Get the connection status for a specific engine. */
	engineConnectionState(id: EngineId): ConnectionStatus {
		return this.engineStates.get(id) ?? 'disconnected';
	}

	/** Report RTT measurement for an engine. Feeds both reconciler and ClockSync. */
	reportRTT(engine: EngineId, ms: number): void {
		this.reconciler.setRTT(engine, ms);
		this.clockSync.updateRTT(engine, ms * 1000); // ms -> us
	}

	/** Reconciler mode: 'dual' when both engines have been seen, 'single' otherwise. */
	get mode(): 'dual' | 'single' {
		return this.reconciler.mode;
	}

	/** Per-engine health detail from the reconciler. */
	get engineHealth(): { A: EngineHealthDetail; B: EngineHealthDetail } {
		return this.reconciler.engineHealth;
	}

	/** Get the WebTransport session for a specific engine (if connected). */
	getTransport(engineId: EngineId): WebTransport | null {
		const mgr = this.managers.get(engineId);
		return mgr?.transport ?? null;
	}

	/** Late-bind a new engine (e.g., engine B discovered from initial state). */
	addEngine(id: EngineId, url: string): void {
		if (this.managers.has(id)) return; // no duplicates
		this.createEngineManager(id, url);
		this._isDualEngine = this.managers.size > 1;

		// Note: There is a brief window (~200ms) between setting _isDualEngine = true
		// and the next Engine A state update where the reconciler has no state.
		// This is harmless because the reconciler returns EMPTY_STATE as a fallback,
		// and the UI already handles empty state gracefully (loading overlay).
		// The next MoQ control track update from Engine A will flow through the
		// reconciler and populate it.

		// If we've already started, start the new manager — but only after the
		// initial load completes to prevent Engine B from briefly becoming
		// authoritative before Engine A's state has been ingested.
		if (this._started) {
			if (this._initialLoadDone) {
				// Safe to start immediately — initial load is complete
				const mgr = this.managers.get(id);
				mgr?.start();
			} else {
				// Defer until initial load completes
				this._pendingEngineStarts.push(id);
			}
		}

		this.startClockSync();
	}

	async start(): Promise<void> {
		this._started = true;
		const promises = [...this.managers.values()].map((m) => m.start());
		await Promise.allSettled(promises);
		this.startClockSync();
	}

	stop(): void {
		this._started = false;
		this.clockSync.stopPeriodicSync();
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

	/** Raw per-engine states for components that need individual engine data. */
	get rawStates(): { A: ControlRoomState | null; B: ControlRoomState | null } {
		return this.reconciler.rawStates;
	}

	get clock(): ClockSync {
		return this.clockSync;
	}

	/** Mark the reconciler as recovering (or clear it). */
	setRecovering(value: boolean): void {
		this.reconciler.setRecovering(value);
	}

	/** Tick the reconciler's time-based status detection. Call every ~200ms. */
	tick(): void {
		this.reconciler.tick();
	}

	/** Request immediate reconciler check (e.g. after partial command failure). */
	requestCheck(): void {
		this.reconciler.requestCheck();
	}

	nextCommandSeq(): number {
		return ++this._commandSeq;
	}

	/** Forward MoQ control data to the appropriate engine's connection manager. */
	handleControlData(engineId: EngineId, data: Uint8Array): void {
		// Intercept clock sync pong messages
		if (data.length >= 13 && data[0] === 0xFF) {
			this.clockSync.handlePong(engineId, data);
			// Check if clock offset divergence exceeds safety margin
			this.reconciler.setClockDiverged(this.clockSync.isClockDiverged());
			// Record pong as proof-of-life for stale detection
			this.reconciler.recordPong(engineId);
			return; // don't forward pong to connection manager
		}

		const mgr = this.managers.get(engineId);
		if (mgr) {
			mgr.handleControlData(data);
		} else {
			console.warn(`[DualConnectionManager] unknown engineId "${engineId}", falling back to first manager`);
			// Fallback: forward to first manager (single-engine compatibility)
			const first = this.managers.values().next().value;
			if (first) first.handleControlData(data);
		}
	}

	/** Notify that MoQ is active (WebTransport connected, catalog received). */
	notifyMoQActive(engineId?: EngineId): void {
		if (engineId) {
			const mgr = this.managers.get(engineId);
			if (mgr) mgr.notifyMoQActive();
		} else {
			// Notify all managers (backward compat)
			for (const mgr of this.managers.values()) {
				mgr.notifyMoQActive();
			}
		}
	}

	/** Start periodic clock sync pings if in dual-engine mode. */
	private startClockSync(): void {
		if (!this._isDualEngine) return;
		const engineIds = [...this.managers.keys()] as EngineId[];
		this.clockSync.startPeriodicSync(
			engineIds,
			(engineId, data) => {
				const transport = this.getTransport(engineId);
				if (transport) {
					const writer = transport.datagrams.writable.getWriter();
					writer.write(data).catch(() => {}).finally(() => writer.releaseLock());
				}
			}
		);
	}

	/** Create a ConnectionManager for an engine and wire up callbacks. */
	private createEngineManager(engineId: EngineId, url: string): void {
		const mgr = new ConnectionManager({
			url,
			onStateUpdate: (update) => {
				let state: ControlRoomState;
				const isBytes = ArrayBuffer.isView(update) || update instanceof ArrayBuffer;
				if (isBytes) {
					state = JSON.parse(new TextDecoder().decode(update as Uint8Array));
				} else {
					state = update as ControlRoomState;
				}

				if (this._isDualEngine) {
					this.reconciler.ingest(engineId, state);
					this.config.onStateUpdate(this.reconciler.state);
				} else {
					this.config.onStateUpdate(state);
				}
			},
			onConnectionStateChange: (connState) => {
				// Track per-engine state
				this.engineStates.set(engineId, connState);

				if (this._isDualEngine) {
					// Feed disconnect/reconnect to reconciler
					if (connState === 'disconnected') {
						this.reconciler.disconnect(engineId);
					}

					// Feed transport-alive state to reconciler for hung-engine detection
					if (connState === 'webtransport') {
						this.reconciler.setTransportAlive(engineId, true);
					} else {
						this.reconciler.setTransportAlive(engineId, false);
					}

					// Report the best available connection status across all engines
					const states = [...this.managers.values()].map((m) => m.getConnectionState());
					if (states.includes('webtransport')) {
						this.config.onConnectionStateChange('webtransport');
					} else if (states.includes('polling')) {
						this.config.onConnectionStateChange('polling');
					} else {
						this.config.onConnectionStateChange('disconnected');
					}
				} else {
					this.config.onConnectionStateChange(connState);
				}
			},
			onInitialLoadComplete: () => {
				if (this._initialLoadDone) return;
				this._initialLoadDone = true;
				this.config.onInitialLoadComplete();
				// Flush any engines that were deferred during initial load
				for (const pendingId of this._pendingEngineStarts) {
					const pendingMgr = this.managers.get(pendingId);
					pendingMgr?.start();
				}
				this._pendingEngineStarts = [];
			},
			onInitialLoadError: (error, rawError) => {
				this._initialLoadErrors++;
				if (this._initialLoadErrors >= this.managers.size && !this._initialLoadDone) {
					this.config.onInitialLoadError(error, rawError);
				}
			},
		});
		this.managers.set(engineId, mgr);
		this.engineStates.set(engineId, 'disconnected');
	}
}
