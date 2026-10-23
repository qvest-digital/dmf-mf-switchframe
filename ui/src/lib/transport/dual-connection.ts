import { ConnectionManager, type ConnectionStatus } from './connection-manager';
import { ClockSync } from './clock-sync';
import { createReconciler, STALE_THRESHOLD_MS, type SyncHealth, type EngineHealthDetail } from '$lib/state/reconciler.svelte';
import type { ControlRoomState, EngineId } from '$lib/api/types';

export interface DualConnectionConfig {
	engines: Array<{ id: EngineId; url: string }>; // 1 or 2 entries
	onStateUpdate: (state: ControlRoomState) => void;
	onConnectionStateChange: (state: ConnectionStatus) => void;
	onInitialLoadComplete: () => void;
	onInitialLoadError: (error: string, rawError?: unknown) => void;
	onDualEngineReady?: () => void;
}

export class DualConnectionManager {
	private managers: Map<string, ConnectionManager> = new Map();
	private engineUrls: Map<string, string> = new Map();
	private engineStates: Map<string, ConnectionStatus> = new Map();
	/** The last state each engine sent and when, kept so an engine added
	 * later can seed the reconciler with the others' state rather than wait
	 * for them to change. */
	private lastStates: Map<EngineId, { state: ControlRoomState; at: number }> = new Map();
	private reconciler = createReconciler();
	private clockSync = new ClockSync();
	private _commandSeq = 0;
	private _isDualEngine: boolean;
	private _started = false;
	private _initialLoadDone = false;
	/** Engines whose initial load has failed at least once (and not yet
	 * succeeded). Tracked per engine — ConnectionManager fires
	 * onInitialLoadError on every 3s retry attempt, so a raw counter would
	 * inflate from a single unreachable engine. */
	private _initialLoadFailedEngines = new Set<string>();
	private _pendingEngineStarts: string[] = [];
	private _engineALoaded = false;
	private _engineBLoaded = false;
	private _dualReadyFired = false;
	private _datagramSenders = new Map<string, (data: Uint8Array) => void>();
	private _initialLoadTimeout: ReturnType<typeof setTimeout> | undefined;

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

	/** Late-bind a new engine (e.g., engine B discovered from initial state).
	 * If the engine id already exists and the URL differs, tears down the old
	 * ConnectionManager and creates a fresh subscription at the new URL. Used
	 * when an engine is re-provisioned at a new URL after crash (control-plane
	 * re-issue). Identical URL for an existing id is a no-op. */
	addEngine(id: EngineId, url: string): void {
		const existingUrl = this.engineUrls.get(id);
		if (existingUrl !== undefined) {
			if (existingUrl === url) return; // identical — no-op
			// URL changed — tear down the old subscription and re-subscribe.
			// This is how we recover when the control plane provisions a
			// replacement engine at a new address.
			this.replaceEngineManager(id, url);
			return;
		}
		this.createEngineManager(id, url);
		const wasDual = this._isDualEngine;
		this._isDualEngine = this.managers.size > 1;

		// The engines that were already running delivered their state before
		// the reconciler was in use. Without it, the engine added now is the
		// only one the reconciler has seen, reads as authoritative, and the
		// UI fails video over to it on load.
		// A state older than the stale threshold would read as fresh once
		// ingested, so it is left out.
		if (this._isDualEngine && !wasDual) {
			const now = Date.now();
			let seeded = false;
			for (const [engineId, { state, at }] of this.lastStates) {
				if (now - at > STALE_THRESHOLD_MS) continue;
				this.reconciler.ingest(engineId, state);
				seeded = true;
			}
			if (seeded) this.config.onStateUpdate(this.reconciler.state);
		}

		// If we've already started, start the new manager — but only after the
		// initial load completes to prevent Engine B from briefly becoming
		// authoritative before Engine A's state has been ingested.
		if (this._started) {
			if (this._initialLoadDone) {
				// Safe to start immediately — initial load is complete
				const mgr = this.managers.get(id);
				mgr?.start();
			} else if (this._initialLoadFailedEngines.size > 0 && this._initialLoadFailedEngines.size >= this.startedEngineCount(id)) {
				// Every engine we actually started has failed initial load.
				// Start the new engine immediately — it's our best hope.
				// (Compare against STARTED engines, not managers.size:
				// deferred engines never start, so they can never fail.)
				const mgr = this.managers.get(id);
				mgr?.start();
			} else {
				// Defer until initial load completes
				this.deferEngineStart(id);
			}
		}

		this.startClockSync();
	}

	async start(): Promise<void> {
		this._started = true;
		// start() is the actual restart entry point for the 401 token-resubmit
		// flow (+page.svelte submitToken calls start() WITHOUT stop()). Clear
		// stale failures here as well as in stop() so they can't poison the
		// all-engines-failed escalation or the deferred-start gating; each
		// manager's fetchInitialState will re-report real failures.
		this._initialLoadFailedEngines.clear();
		const promises = [...this.managers.values()].map((m) => m.start());
		await Promise.allSettled(promises);
		this.startClockSync();

		if (!this._initialLoadDone) {
			this._initialLoadTimeout = setTimeout(() => {
				if (this._initialLoadDone) return;
				if (this._engineALoaded || this._engineBLoaded) {
					this._initialLoadDone = true;
					this.config.onInitialLoadComplete();
					for (const pendingId of this._pendingEngineStarts) {
						this.managers.get(pendingId)?.start();
					}
					this._pendingEngineStarts = [];
				} else {
					this.config.onInitialLoadError('All engines timed out after 15 seconds');
				}
			}, 15_000);
		}
	}

	stop(): void {
		this._started = false;
		if (this._initialLoadTimeout) {
			clearTimeout(this._initialLoadTimeout);
			this._initialLoadTimeout = undefined;
		}
		this.clockSync.stopPeriodicSync();
		for (const mgr of this.managers.values()) {
			mgr.stop();
		}
		// Don't let stale failures poison the next start() cycle (e.g. the
		// submitToken() restart after a 401).
		this._initialLoadFailedEngines.clear();
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

	/** Dual-engine telemetry stats from the reconciler. */
	get dualEngineStats() {
		return this.reconciler.dualEngineStats;
	}

	get clock(): ClockSync {
		return this.clockSync;
	}

	/** Mark the reconciler as recovering (or clear it). */
	setRecovering(value: boolean): void {
		this.reconciler.setRecovering(value);
	}

	/** Mark warm standby as degraded (Engine B pipeline not receiving media). */
	setWarmStandbyDegraded(degraded: boolean): void {
		this.reconciler.setWarmStandbyDegraded(degraded);
	}

	/** Tick the reconciler's time-based status detection. Call every ~200ms. */
	tick(): void {
		this.reconciler.tick();
	}

	/** Request immediate reconciler check (e.g. after partial command failure). */
	requestCheck(): void {
		this.reconciler.requestCheck();
	}

	/** A seq no other browser or earlier page load sends: an engine rejects
	 * a seq it has executed before, and keeps that record across restarts.
	 * Wall-clock microseconds, with random low digits for two browsers in the
	 * same millisecond, kept increasing within this page. */
	nextCommandSeq(): number {
		this._commandSeq = Math.max(this._commandSeq + 1, Date.now() * 1000 + Math.floor(Math.random() * 1000));
		return this._commandSeq;
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
				// Route through FastControl's shared writer when available
				// to avoid WritableStream lock conflict.
				const sender = this._datagramSenders.get(engineId);
				if (sender) {
					sender(data);
					return;
				}
				// Fallback: acquire/release writer (only before FastControl is created)
				const transport = this.getTransport(engineId);
				if (transport) {
					const writer = transport.datagrams.writable.getWriter();
					writer.write(data).catch(() => {}).finally(() => writer.releaseLock());
				}
			}
		);
	}

	/** Register a datagram sender for an engine (routes clock sync through FastControl's writer). */
	setDatagramSender(engineId: EngineId, sender: ((data: Uint8Array) => void) | null): void {
		if (sender) {
			this._datagramSenders.set(engineId, sender);
		} else {
			this._datagramSenders.delete(engineId);
		}
	}

	/** Tear down the existing ConnectionManager for an engine id and replace
	 * it with a fresh subscription at the new URL. Preserves started/initial-
	 * load state so the replacement manager starts immediately if the session
	 * was already running. */
	private replaceEngineManager(engineId: EngineId, url: string): void {
		const oldMgr = this.managers.get(engineId);
		if (oldMgr) {
			oldMgr.stop();
		}
		this.engineStates.set(engineId, 'disconnected');
		// Notify the reconciler the old subscription is gone so authority
		// doesn't stay stuck on the dead engine while we reconnect.
		if (this._isDualEngine) {
			this.reconciler.disconnect(engineId);
		}

		this.createEngineManager(engineId, url);

		const newMgr = this.managers.get(engineId);
		if (!newMgr) return;

		// Preserve the started/initial-load state machine: if we had already
		// started the session AND initial load is done (or all other engines
		// failed initial load), start the replacement manager immediately.
		// Otherwise, defer — matching the logic of addEngine for new ids.
		if (this._started) {
			if (this._initialLoadDone) {
				newMgr.start();
			} else if (this._initialLoadFailedEngines.size > 0 && this._initialLoadFailedEngines.size >= this.startedEngineCount(engineId)) {
				newMgr.start();
			} else {
				this.deferEngineStart(engineId);
			}
		}
	}

	/** Number of engines whose ConnectionManager has actually been started —
	 * i.e. not sitting in _pendingEngineStarts — excluding `excludeId` (the
	 * engine currently being added/replaced). Only started engines can fail
	 * their initial load, so "all engines failed" checks must compare against
	 * this count rather than managers.size. */
	private startedEngineCount(excludeId?: string): number {
		let count = 0;
		for (const id of this.managers.keys()) {
			if (id === excludeId) continue;
			if (this._pendingEngineStarts.includes(id)) continue;
			count++;
		}
		return count;
	}

	/** Queue an engine to be started once the initial load settles
	 * (deduplicated — an engine replaced while already deferred must not be
	 * double-counted or double-started). */
	private deferEngineStart(engineId: string): void {
		if (!this._pendingEngineStarts.includes(engineId)) {
			this._pendingEngineStarts.push(engineId);
		}
	}

	/** Create a ConnectionManager for an engine and wire up callbacks. */
	private createEngineManager(engineId: EngineId, url: string): void {
		if (engineId === 'B') this.reconciler.setEngineBUrl(url);
		const mgr = new ConnectionManager({
			url,
			onStateUpdate: (update) => {
				let state: ControlRoomState;
				const isBytes = ArrayBuffer.isView(update) || update instanceof ArrayBuffer;
				if (isBytes) {
					// Control payload from the MoQ control track. A truncated or
					// malformed payload (realistic during reconnect/relay restart)
					// must not throw — that would unwind into handleDataStream's
					// catch and abandon the rest of the subgroup stream's objects.
					// Drop just this payload, matching the 'stats' track handling.
					try {
						state = JSON.parse(new TextDecoder().decode(update as Uint8Array));
					} catch {
						return;
					}
				} else {
					state = update as ControlRoomState;
				}

				this.lastStates.set(engineId, { state, at: Date.now() });
				if (this._isDualEngine) {
					this.reconciler.ingest(engineId, state);
					this.config.onStateUpdate(this.reconciler.state);
				} else {
					this.config.onStateUpdate(state);
				}

				// Track per-engine load state for dual-engine readiness
				if (engineId === 'A') this._engineALoaded = true;
				if (engineId === 'B') this._engineBLoaded = true;
				if (this._isDualEngine && this._engineALoaded && this._engineBLoaded && !this._dualReadyFired) {
					this._dualReadyFired = true;
					this.config.onDualEngineReady?.();
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
				// This engine loaded — any earlier failures of it are stale.
				this._initialLoadFailedEngines.delete(engineId);
				if (this._initialLoadDone) return;
				this._initialLoadDone = true;
				if (this._initialLoadTimeout) {
					clearTimeout(this._initialLoadTimeout);
					this._initialLoadTimeout = undefined;
				}
				this.config.onInitialLoadComplete();
				// Flush any engines that were deferred during initial load
				for (const pendingId of this._pendingEngineStarts) {
					const pendingMgr = this.managers.get(pendingId);
					pendingMgr?.start();
				}
				this._pendingEngineStarts = [];
			},
			onInitialLoadError: (error, rawError) => {
				this._initialLoadFailedEngines.add(engineId);
				if (this._initialLoadDone) return;
				// Only escalate to the page once every STARTED engine has
				// failed — retries of a single unreachable engine must not mask
				// a healthy engine that is still loading. Compare against
				// started engines, not managers.size: an engine deferred in
				// _pendingEngineStarts never starts, so it can never fail, and
				// counting it would make escalation (which drives the 401
				// token prompt) unreachable.
				if (this._initialLoadFailedEngines.size >= this.startedEngineCount()) {
					// Deferral exists to let a started engine's state land
					// before a late-added engine becomes authoritative — moot
					// when no started engine can load. Flush pending engines:
					// they're the best remaining hope of completing the load.
					const pending = this._pendingEngineStarts;
					this._pendingEngineStarts = [];
					for (const pendingId of pending) {
						this.managers.get(pendingId)?.start();
					}
					this.config.onInitialLoadError(error, rawError);
				}
			},
		});
		this.managers.set(engineId, mgr);
		this.engineUrls.set(engineId, url);
		this.engineStates.set(engineId, 'disconnected');
	}
}
