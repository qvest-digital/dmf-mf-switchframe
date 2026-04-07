import type { ControlRoomState, SourceHealthStatus, EngineId } from '$lib/api/types';

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
}

/** Fields considered program-visible for divergence comparison. */
const DIVERGENCE_FIELDS: (keyof ControlRoomState)[] = [
	'programSource',
	'previewSource',
	'inTransition',
	'transitionPosition',
	'masterLevel',
	'ftbActive',
];

/** Critical fields: mismatch at same seq = deterministic failure. */
const MISMATCH_FIELDS: (keyof ControlRoomState)[] = [
	'programSource',
];

/** Thresholds for time-based status detection. */
const TRANSIENT_THRESHOLD_MS = 500;
const DIVERGED_THRESHOLD_MS = 2000;

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

	/** Which engine is authoritative: source health → higher lastCommandSeq → RTT tie-break → A wins final ties. */
	const authoritative = $derived.by((): EngineId => {
		if (!stateA && !stateB) return 'A';
		if (!stateA || !connectedA) return 'B';
		if (!stateB || !connectedB) return 'A';

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
		// Equal seq: prefer lower RTT
		if (rttA > 0 && rttB > 0) {
			if (rttB < rttA) return 'B';
			if (rttA < rttB) return 'A';
		}
		// Final tie: A wins
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
			if (a !== b) {
				result.push({ field, engineA: a, engineB: b });
			}
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
			if (stateA[field] !== stateB[field]) return true;
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
	}));

	return {
		ingest,
		tick,
		setRTT,
		setClockOffset,
		disconnect,
		setRecovering,
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
