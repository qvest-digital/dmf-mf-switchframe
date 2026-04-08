import type { ControlRoomState, SourceHealthStatus, EngineId } from '$lib/api/types';

/** Default thresholds for audio drift reconciliation (configurable). */
export const AUDIO_DRIFT_DB_THRESHOLD = 3;
export const AUDIO_DRIFT_DURATION_MS = 10_000;

/** Minimum RTT delta (ms) required to switch authority based on RTT alone. Prevents jitter flapping. */
export const RTT_HYSTERESIS_MS = 10;

const EMPTY_STATE: ControlRoomState = {
	programSource: '',
	previewSource: '',
	transitionType: 'cut',
	transitionDurationMs: 0,
	transitionPosition: 0,
	inTransition: false,
	ftbActive: false,
	audioChannels: undefined,
	masterLevel: 0,
	programPeak: [0, 0],
	tallyState: {},
	sources: {},
	seq: 0,
	lastCommandSeq: 0,
	timestamp: 0,
};

export type SyncStatus = 'synced' | 'lagging' | 'diverged' | 'mismatch' | 'recovering' | 'single-engine' | 'disconnected';

export interface Divergence {
	field: string;
	engineA: unknown;
	engineB: unknown;
}

export interface EngineHealthDetail {
	connected: boolean;
	rtt: number;
	clockOffset: number;
	lastSeq: number;
	uptime: number;
	lastSeen: number;
	sources: Record<string, SourceHealthStatus>;
}

export interface EngineHealth {
	lastSeen: number;
	lastCommandSeq: number;
	connected: boolean;
}

export interface SyncHealth {
	status: SyncStatus;
	divergences: Divergence[];
	authoritative: EngineId;
	engineA: EngineHealth;
	engineB: EngineHealth;
	warnings: string[];
	staleEngines: EngineId[];
}

/**
 * Key-order-independent JSON serialization for deep comparison.
 * Object keys are sorted recursively so {a:1, b:2} === {b:2, a:1}.
 * Array element order is preserved (semantically meaningful).
 */
function sortedStringify(obj: unknown): string {
	if (obj === null || obj === undefined) return String(obj);
	if (typeof obj !== 'object') return JSON.stringify(obj);
	if (Array.isArray(obj)) return '[' + (obj as unknown[]).map(sortedStringify).join(',') + ']';
	const sorted = Object.keys(obj as Record<string, unknown>).sort();
	return '{' + sorted.map(k => JSON.stringify(k) + ':' + sortedStringify((obj as Record<string, unknown>)[k])).join(',') + '}';
}

/** Deep-ish equality for field comparison (handles nested objects, key-order-independent). */
export function fieldEquals(a: unknown, b: unknown): boolean {
	if (a === b) return true;
	if (a == null || b == null) return a == b;
	if (typeof a !== typeof b) return false;
	if (typeof a === 'object') return sortedStringify(a) === sortedStringify(b);
	return false;
}

/** Fields considered program-visible for divergence comparison. */
const DIVERGENCE_FIELDS: (keyof ControlRoomState)[] = [
	'programSource',
	'previewSource',
	'inTransition',
	'transitionPosition',
	'masterLevel',
	'ftbActive',
	'transitionType',
	'transitionDurationMs',
];

/** Critical fields: mismatch at same seq = deterministic failure. */
const MISMATCH_FIELDS: (keyof ControlRoomState)[] = [
	'programSource',
	'previewSource',
	'ftbActive',
];

/** Thresholds for time-based status detection. */
const TRANSIENT_THRESHOLD_MS = 500;
const DIVERGED_THRESHOLD_MS = 2000;

/** If no state update for this long, treat engine as stale/disconnected. */
export const STALE_THRESHOLD_MS = 5000;

export function createReconciler() {
	let stateA = $state<ControlRoomState | null>(null);
	let stateB = $state<ControlRoomState | null>(null);
	let lastSeenA = $state(0);
	let lastSeenB = $state(0);
	let connectedA = $state(false);
	let connectedB = $state(false);
	let rttA = $state(0);
	let rttB = $state(0);
	let clockOffsetA = $state(0);
	let clockOffsetB = $state(0);
	let firstSeenA = $state(0);
	let firstSeenB = $state(0);
	let everSeenA = $state(false);
	let everSeenB = $state(false);
	let recovering = $state(false);
	let clockDiverged = $state(false);
	let transportAliveA = $state(false);
	let transportAliveB = $state(false);

	// Track when seq delta first appeared for time-based detection
	let seqDeltaSince = $state(0);

	// Tick-updated status (time-based)
	let tickedStatus = $state<SyncStatus>('disconnected');

	// Updated every tick (~200ms) to drive smooth uptime display in engineHealth
	let lastTickTime = $state(Date.now());

	function ingest(engine: EngineId, update: ControlRoomState) {
		const now = Date.now();
		if (engine === 'A') {
			stateA = update;
			lastSeenA = now;
			connectedA = true;
			if (!everSeenA) {
				everSeenA = true;
				firstSeenA = now;
			}
		} else {
			stateB = update;
			lastSeenB = now;
			connectedB = true;
			if (!everSeenB) {
				everSeenB = true;
				firstSeenB = now;
			}
		}
		// Re-evaluate seq delta tracking
		updateSeqDeltaTracking(now);
		// Recompute status immediately so health reflects the new state
		tickedStatus = computeStatus(now);
	}

	function updateSeqDeltaTracking(now: number) {
		if (!stateA || !stateB || !connectedA || !connectedB) {
			seqDeltaSince = 0;
			return;
		}
		const seqA = stateA.lastCommandSeq ?? 0;
		const seqB = stateB.lastCommandSeq ?? 0;
		if (seqA === seqB) {
			seqDeltaSince = 0;
		} else if (seqDeltaSince === 0) {
			seqDeltaSince = now;
		}
	}

	/** Count sources that are not offline or no_signal. */
	function countHealthySources(state: ControlRoomState): number {
		if (!state.sources) return 0;
		return Object.values(state.sources).filter(
			s => s.status !== 'offline' && s.status !== 'no_signal'
		).length;
	}

	/** Which engine is authoritative: stale → source health → higher lastCommandSeq → RTT tie-break → A wins final ties. */
	const authoritative = $derived.by((): EngineId => {
		if (!stateA && !stateB) return 'A';
		// Treat stale engines as effectively disconnected for authority selection
		const now = lastTickTime;
		const effectiveA = stateA !== null && connectedA && !(lastSeenA > 0 && now - lastSeenA > STALE_THRESHOLD_MS);
		const effectiveB = stateB !== null && connectedB && !(lastSeenB > 0 && now - lastSeenB > STALE_THRESHOLD_MS);
		if (!effectiveA && !effectiveB) return 'A';
		if (!effectiveA) return 'B';
		if (!effectiveB) return 'A';

		// Source health scoring: if one engine has >50% sources degraded and other doesn't, healthier wins
		const healthyA = countHealthySources(stateA);
		const healthyB = countHealthySources(stateB);
		const totalA = Object.keys(stateA.sources ?? {}).length;
		const totalB = Object.keys(stateB.sources ?? {}).length;
		const degradedA = totalA > 0 && healthyA / totalA < 0.5;
		const degradedB = totalB > 0 && healthyB / totalB < 0.5;
		if (degradedA && !degradedB) return 'B';
		if (degradedB && !degradedA) return 'A';

		// Fall back to seq-based logic
		const seqA = stateA.lastCommandSeq ?? 0;
		const seqB = stateB.lastCommandSeq ?? 0;
		if (seqA > seqB) return 'A';
		if (seqB > seqA) return 'B';
		// Equal seq: prefer lower RTT only if the delta clearly exceeds hysteresis.
		// Small fluctuations (< RTT_HYSTERESIS_MS) keep A as authority to prevent jitter flapping.
		if (rttA > 0 && rttB > 0) {
			const delta = rttA - rttB;
			if (Math.abs(delta) > RTT_HYSTERESIS_MS) {
				return delta > 0 ? 'B' : 'A'; // prefer whichever has clearly lower RTT
			}
		}
		// Final tie (and within hysteresis): A wins
		return 'A';
	});

	/** The merged state from the authoritative engine. */
	const currentState = $derived.by((): ControlRoomState => {
		if (authoritative === 'A' && stateA) return stateA;
		if (authoritative === 'B' && stateB) return stateB;
		return stateA ?? stateB ?? EMPTY_STATE;
	});

	/** Divergence detection across program-visible fields. */
	const divergences = $derived.by((): Divergence[] => {
		if (!stateA || !stateB || !connectedA || !connectedB) return [];
		const result: Divergence[] = [];
		for (const field of DIVERGENCE_FIELDS) {
			const a = stateA[field];
			const b = stateB[field];
			if (!fieldEquals(a, b)) {
				result.push({ field, engineA: a, engineB: b });
			}
		}
		// Check recording state divergence (nested object, not in DIVERGENCE_FIELDS)
		if (stateA.recording?.active !== stateB.recording?.active) {
			result.push({ field: 'recording', engineA: stateA.recording, engineB: stateB.recording });
		}
		// Graphics layer health divergence (active state or html5Status differs)
		const layersA = stateA.graphics?.layers ?? [];
		const layersB = stateB.graphics?.layers ?? [];
		const maxLayers = Math.max(layersA.length, layersB.length);
		for (let i = 0; i < maxLayers; i++) {
			const lA = layersA[i];
			const lB = layersB[i];
			if (!lA || !lB) continue;
			if (lA.active !== lB.active || lA.html5Status !== lB.html5Status) {
				result.push({ field: 'graphics.layers', engineA: lA, engineB: lB });
				break; // one divergence entry is sufficient
			}
		}
		// Layout preset divergence
		if (!fieldEquals(stateA.layout?.activePreset, stateB.layout?.activePreset)) {
			result.push({
				field: 'layout.activePreset',
				engineA: stateA.layout?.activePreset,
				engineB: stateB.layout?.activePreset,
			});
		}
		return result;
	});

	/** Check if critical fields mismatch at same lastCommandSeq. */
	function hasCriticalMismatch(): boolean {
		if (!stateA || !stateB || !connectedA || !connectedB) return false;
		const seqA = stateA.lastCommandSeq ?? 0;
		const seqB = stateB.lastCommandSeq ?? 0;
		if (seqA !== seqB) return false;

		// Check programSource and other mismatch fields
		for (const field of MISMATCH_FIELDS) {
			if (!fieldEquals(stateA[field], stateB[field])) return true;
		}

		// Check playout channel divergence (currentItemId, itemIndex, currentTitle)
		const playoutA = stateA?.playout;
		const playoutB = stateB?.playout;
		if (playoutA?.channels?.length && playoutB?.channels?.length) {
			const channelsA = playoutA.channels;
			const channelsB = playoutB.channels;
			const maxLen = Math.max(channelsA.length, channelsB.length);
			for (let i = 0; i < maxLen; i++) {
				const chA = channelsA[i];
				const chB = channelsB[i];
				if (!chA || !chB) continue;
				if (chA.currentItemId !== undefined && chB.currentItemId !== undefined && chA.currentItemId !== chB.currentItemId) return true;
				if (chA.itemIndex !== chB.itemIndex) return true;
				if (chA.currentTitle !== undefined && chB.currentTitle !== undefined && chA.currentTitle !== chB.currentTitle) return true;
			}
		}

		return false;
	}

	/** Compute sync status based on current state + elapsed time. */
	function computeStatus(now: number): SyncStatus {
		const hasA = stateA !== null && connectedA;
		const hasB = stateB !== null && connectedB;

		if (!stateA && !stateB) return 'disconnected';
		// Disconnected trumps recovering: can't recover if both engines are offline
		if (!hasA && !hasB) return 'disconnected';

		// Stale engine detection: no state update for STALE_THRESHOLD_MS.
		// Don't mark stale if transport is alive (WebTransport connected but engine may just be idle).
		const staleA = hasA && !transportAliveA && (now - lastSeenA > STALE_THRESHOLD_MS);
		const staleB = hasB && !transportAliveB && (now - lastSeenB > STALE_THRESHOLD_MS);

		if (staleA && staleB) return 'disconnected';
		if (staleA && !hasB) return 'disconnected';
		if (staleB && !hasA) return 'disconnected';
		if (staleA) return 'single-engine';
		if (staleB) return 'single-engine';

		if (!hasA || !hasB) return 'single-engine';

		// Check critical mismatch before recovering — mismatches must surface
		// even during recovery so auto-reconcile can re-push if needed.
		if (hasCriticalMismatch()) return 'mismatch';
		if (recovering) return 'recovering';

		const seqA = stateA!.lastCommandSeq ?? 0;
		const seqB = stateB!.lastCommandSeq ?? 0;

		if (seqA === seqB) {
			// Seqs match, check divergence on visible fields
			if (divergences.length > 0) return 'diverged';
			return 'synced';
		}

		// Seqs differ: check field-level divergence for immediate diverged
		if (divergences.length > 0) return 'diverged';

		// Time-based: how long has seq delta persisted?
		if (seqDeltaSince === 0) return 'synced';
		const elapsed = now - seqDeltaSince;
		if (elapsed >= DIVERGED_THRESHOLD_MS) return 'diverged';
		if (elapsed >= TRANSIENT_THRESHOLD_MS) return 'lagging';
		return 'synced';
	}

	/** Called periodically (~200ms) to re-evaluate time-based statuses. */
	function tick() {
		const now = Date.now();
		lastTickTime = now;
		tickedStatus = computeStatus(now);
	}

	/**
	 * Request an immediate status recheck, e.g. after a partial command failure.
	 * Skips the normal transient grace period so divergence surfaces faster.
	 */
	function requestCheck() {
		// Jump the seq-delta clock backward so elapsed time exceeds the
		// diverged threshold immediately on the next tick.
		if (seqDeltaSince > 0) {
			seqDeltaSince = Date.now() - DIVERGED_THRESHOLD_MS - 1;
		}
		tick();
	}

	function setRTT(engine: EngineId, ms: number) {
		if (engine === 'A') rttA = ms;
		else rttB = ms;
	}

	function setClockOffset(engine: EngineId, us: number) {
		if (engine === 'A') clockOffsetA = us;
		else clockOffsetB = us;
	}

	function disconnect(engine: EngineId) {
		if (engine === 'A') {
			connectedA = false;
		} else {
			connectedB = false;
		}
		// Re-evaluate status
		tickedStatus = computeStatus(Date.now());
	}

	function setRecovering(value: boolean) {
		recovering = value;
	}

	function setClockDiverged(diverged: boolean) {
		clockDiverged = diverged;
	}

	function setTransportAlive(engine: EngineId, alive: boolean) {
		if (engine === 'A') transportAliveA = alive;
		else transportAliveB = alive;
	}

	const engineHealthDerived = $derived.by((): { A: EngineHealthDetail; B: EngineHealthDetail } => {
		// Use lastTickTime (updated every ~200ms by tick()) instead of Date.now()
		// so uptime display updates smoothly on each tick rather than only when
		// other reactive deps change.
		const now = lastTickTime;
		return {
			A: {
				connected: connectedA,
				rtt: rttA,
				clockOffset: clockOffsetA,
				lastSeq: stateA?.lastCommandSeq ?? 0,
				uptime: firstSeenA > 0 ? now - firstSeenA : 0,
				lastSeen: lastSeenA,
				sources: extractSourceHealth(stateA),
			},
			B: {
				connected: connectedB,
				rtt: rttB,
				clockOffset: clockOffsetB,
				lastSeq: stateB?.lastCommandSeq ?? 0,
				uptime: firstSeenB > 0 ? now - firstSeenB : 0,
				lastSeen: lastSeenB,
				sources: extractSourceHealth(stateB),
			},
		};
	});

	function extractSourceHealth(state: ControlRoomState | null): Record<string, SourceHealthStatus> {
		if (!state?.sources) return {};
		const result: Record<string, SourceHealthStatus> = {};
		for (const [key, info] of Object.entries(state.sources)) {
			result[key] = info.status;
		}
		return result;
	}

	const modeDerived = $derived.by((): 'dual' | 'single' => {
		return everSeenA && everSeenB ? 'dual' : 'single';
	});

	const warnings = $derived.by((): string[] => {
		const w: string[] = [];
		if (clockDiverged) w.push('clock_diverged');
		return w;
	});

	const staleEngines = $derived.by((): EngineId[] => {
		const now = lastTickTime;
		const result: EngineId[] = [];
		if (connectedA && !transportAliveA && lastSeenA > 0 && (now - lastSeenA > STALE_THRESHOLD_MS)) result.push('A');
		if (connectedB && !transportAliveB && lastSeenB > 0 && (now - lastSeenB > STALE_THRESHOLD_MS)) result.push('B');
		return result;
	});

	const health = $derived.by((): SyncHealth => ({
		status: tickedStatus,
		divergences,
		authoritative,
		engineA: {
			lastSeen: lastSeenA,
			lastCommandSeq: stateA?.lastCommandSeq ?? 0,
			connected: connectedA,
		},
		engineB: {
			lastSeen: lastSeenB,
			lastCommandSeq: stateB?.lastCommandSeq ?? 0,
			connected: connectedB,
		},
		warnings,
		staleEngines,
	}));

	return {
		ingest,
		tick,
		requestCheck,
		setRTT,
		setClockOffset,
		disconnect,
		setRecovering,
		setClockDiverged,
		setTransportAlive,
		get state() {
			return currentState;
		},
		get health() {
			return health;
		},
		get engineHealth() {
			return engineHealthDerived;
		},
		get mode() {
			return modeDerived;
		},
		/** Raw per-engine states for components that need individual engine data. */
		get rawStates() {
			return { A: stateA, B: stateB };
		},
	};
}
