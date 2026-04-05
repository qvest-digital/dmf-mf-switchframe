import type { ControlRoomState } from '$lib/api/types';

export type SyncStatus = 'nominal' | 'lagging' | 'diverged' | 'single-engine' | 'disconnected';

export interface Divergence {
	field: string;
	engineA: unknown;
	engineB: unknown;
}

export interface EngineHealth {
	lastSeen: number;
	lastCommandSeq: number;
	connected: boolean;
}

export interface SyncHealth {
	status: SyncStatus;
	divergences: Divergence[];
	authoritative: 'A' | 'B';
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

export function createReconciler() {
	let stateA = $state<ControlRoomState | null>(null);
	let stateB = $state<ControlRoomState | null>(null);
	let lastSeenA = $state(0);
	let lastSeenB = $state(0);

	function ingest(engine: 'A' | 'B', update: ControlRoomState) {
		if (engine === 'A') {
			stateA = update;
			lastSeenA = Date.now();
		} else {
			stateB = update;
			lastSeenB = Date.now();
		}
	}

	/** Which engine is authoritative: higher lastCommandSeq wins; A wins ties. */
	const authoritative = $derived.by((): 'A' | 'B' => {
		if (!stateA && !stateB) return 'A';
		if (!stateA) return 'B';
		if (!stateB) return 'A';
		return stateA.lastCommandSeq >= stateB.lastCommandSeq ? 'A' : 'B';
	});

	/** The merged state from the authoritative engine. */
	const currentState = $derived.by((): ControlRoomState => {
		if (authoritative === 'A' && stateA) return stateA;
		if (authoritative === 'B' && stateB) return stateB;
		// Fallback: return whichever is available
		return (stateA ?? stateB)!;
	});

	/** Divergence detection across program-visible fields. */
	const divergences = $derived.by((): Divergence[] => {
		if (!stateA || !stateB) return [];
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

	/** Overall sync status. */
	const syncStatus = $derived.by((): SyncStatus => {
		const hasA = stateA !== null;
		const hasB = stateB !== null;
		if (!hasA && !hasB) return 'disconnected';
		if (!hasA || !hasB) return 'single-engine';
		if (divergences.length > 0) return 'diverged';
		return 'nominal';
	});

	const health = $derived.by((): SyncHealth => ({
		status: syncStatus,
		divergences,
		authoritative,
		engineA: {
			lastSeen: lastSeenA,
			lastCommandSeq: stateA?.lastCommandSeq ?? 0,
			connected: stateA !== null,
		},
		engineB: {
			lastSeen: lastSeenB,
			lastCommandSeq: stateB?.lastCommandSeq ?? 0,
			connected: stateB !== null,
		},
	}));

	return {
		ingest,
		get state() {
			return currentState;
		},
		get health() {
			return health;
		},
	};
}
