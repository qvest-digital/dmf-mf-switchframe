import type { ControlRoomState, SourceHealthStatus, EngineId } from '$lib/api/types';

/** Minimum RTT delta (ms) required to switch authority based on RTT alone. Prevents jitter flapping. */
export const RTT_HYSTERESIS_MS = 100;

/** Minimum hold-time (ms) before authority can switch when both engines are effective.
 * Prevents jitter-driven A→B→A→B flapping during transient network glitches. */
export const AUTHORITY_HOLD_MS = 500;

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
export function sortedStringify(obj: unknown): string {
	if (obj === null || obj === undefined) return String(obj);
	if (obj instanceof Date) return JSON.stringify(obj.toISOString());
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
	'transitionEasing',
	'encoder',
	'captions',
	'scte35',
	'macro',
	'colorGrade',
	'stmap',
	'limiter',
	'pipelineFormat',
	'cbr',
	'aiSegmentation',
];

/** Audio channel fields that are settings (not real-time metering). */
const AUDIO_SETTING_KEYS: string[] = [
	'level', 'trim', 'muted', 'afv', 'phaseInvert', 'balance',
	'hpfFrequency', 'hpfEnabled', 'eq', 'compressor', 'gate',
	'gateEnabled', 'audioDelayMs',
];

/** Compare audio channels excluding metering fields (peakL, peakR, gainReduction, gateReduction). */
function audioChannelsSettingsEqual(
	a: Record<string, any> | undefined,
	b: Record<string, any> | undefined,
): boolean {
	if (a === b) return true;
	if (!a || !b) return a == b;
	const keysA = Object.keys(a).sort();
	const keysB = Object.keys(b).sort();
	if (keysA.length !== keysB.length || keysA.some((k, i) => k !== keysB[i])) return false;
	for (const key of keysA) {
		const chA = a[key];
		const chB = b[key];
		for (const field of AUDIO_SETTING_KEYS) {
			if (!fieldEquals(chA[field], chB[field])) return false;
		}
	}
	return true;
}

/** Critical fields: mismatch at same seq = deterministic failure. */
const MISMATCH_FIELDS: (keyof ControlRoomState)[] = [
	'programSource',
	'previewSource',
	'ftbActive',
];

/** Thresholds for time-based status detection. */
const TRANSIENT_THRESHOLD_MS = 500;
const DIVERGED_THRESHOLD_MS = 2000;
export const STALE_THRESHOLD_MS = 5_000;
const STALE_TRANSPORT_ALIVE_MS = 15_000;
/** Pong freshness window: 30s = 3x the 10s ping interval. */
export const PONG_FRESH_MS = 30_000;
/** Max time without a state update before engine is stale, even if pongs are fresh. */
export const STATE_UPDATE_STALE_MS = 12_000;

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
	let warmStandbyDegraded = $state(false);
	let transportAliveA = $state(false);
	let transportAliveB = $state(false);
	let lastPongA = $state(0);
	let lastPongB = $state(0);

	// Lock authority to the engine that was authoritative when a transition started,
	// to prevent mid-transition UI jumps if authority would otherwise flip.
	let transitionLockEngine = $state<EngineId | null>(null);

	// Hold-time tracking: prevents authority from flapping during transient glitches.
	// Only switch authority if the new candidate has been the winner for AUTHORITY_HOLD_MS.
	// These are plain variables (not $state) because they are updated imperatively in
	// tick()/ingest(), not reactively. The $state heldAuthority drives the $derived.
	let lastAuthorityCandidate: EngineId = 'A';
	let lastAuthorityCandidateSince = 0;
	let authorityEstablished = false;
	let heldAuthority = $state<EngineId>('A');

	// Track when seq delta first appeared for time-based detection
	let seqDeltaSince = $state(0);

	// Tick-updated status (time-based)
	let tickedStatus = $state<SyncStatus>('disconnected');

	// Updated every tick (~200ms) to drive smooth uptime display in engineHealth
	let lastTickTime = $state(Date.now());

	function ingest(engine: EngineId, update: ControlRoomState) {
		const now = Date.now();
		// Capture previous inTransition state for transition lock tracking
		const prevInTransition = (engine === 'A' ? stateA : stateB)?.inTransition ?? false;
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
		// Track transition lock: lock authority when transition starts, release when it ends.
		// Compute authority inline using Date.now() instead of reading the $derived `authoritative`
		// which depends on lastTickTime (updated every 200ms). Between ticks, the derived could
		// use a stale timestamp for staleness checks.
		if (update.inTransition && !prevInTransition && transitionLockEngine === null) {
			const lockNow = Date.now();
			const effectiveA = stateA !== null && connectedA && !(lastSeenA > 0 && lockNow - lastSeenA > STALE_THRESHOLD_MS);
			const effectiveB = stateB !== null && connectedB && !(lastSeenB > 0 && lockNow - lastSeenB > STALE_THRESHOLD_MS);
			if (effectiveA && !effectiveB) transitionLockEngine = 'A';
			else if (effectiveB && !effectiveA) transitionLockEngine = 'B';
			else {
				const seqA = stateA?.lastCommandSeq ?? 0;
				const seqB = stateB?.lastCommandSeq ?? 0;
				transitionLockEngine = seqA >= seqB ? 'A' : 'B';
			}
		}
		if (!update.inTransition && prevInTransition) {
			// Only release lock when the locked engine finishes AND the other engine
			// also reports inTransition=false (or isn't connected).
			if (transitionLockEngine === engine) {
				const otherState = engine === 'A' ? stateB : stateA;
				const otherConnected = engine === 'A' ? connectedB : connectedA;
				if (!otherConnected || !otherState?.inTransition) {
					transitionLockEngine = null;
				}
			} else if (transitionLockEngine !== null) {
				// Non-locked engine finished — check if locked engine also finished
				const lockedState = transitionLockEngine === 'A' ? stateA : stateB;
				if (!lockedState?.inTransition) {
					transitionLockEngine = null;
				}
			}
		}
		// Re-evaluate seq delta tracking
		updateSeqDeltaTracking(now);
		// Update authority with hold-time logic
		updateHeldAuthority(now);
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

	/** Compute the raw authority candidate without hold-time. Used by the derived and by hold-time logic. */
	function computeRawAuthority(now: number): { candidate: EngineId; bothEffective: boolean } {
		if (!stateA && !stateB) return { candidate: 'A', bothEffective: false };
		// Treat stale engines as effectively disconnected for authority selection
		const effectiveA = stateA !== null && connectedA && !(lastSeenA > 0 && now - lastSeenA > STALE_THRESHOLD_MS);
		const effectiveB = stateB !== null && connectedB && !(lastSeenB > 0 && now - lastSeenB > STALE_THRESHOLD_MS);
		if (!effectiveA && !effectiveB) return { candidate: 'A', bothEffective: false };
		if (!effectiveA) return { candidate: 'B', bothEffective: false };
		if (!effectiveB) return { candidate: 'A', bothEffective: false };

		// Lock authority during active transitions to prevent mid-transition jumps
		const aInTransition = stateA?.inTransition ?? false;
		const bInTransition = stateB?.inTransition ?? false;
		if (aInTransition || bInTransition) {
			if (transitionLockEngine !== null) {
				// Verify the locked engine is still reachable
				const lockedEffective = transitionLockEngine === 'A' ? effectiveA : effectiveB;
				if (lockedEffective) return { candidate: transitionLockEngine, bothEffective: true };
			}
		}

		// TypeScript can't narrow through effectiveA/B guards, but we returned above if null
		const sA = stateA!;
		const sB = stateB!;

		// Check program source health: if program is unhealthy on one engine, prefer the other
		const programKey = sA.programSource || sB.programSource;
		if (programKey) {
			const progHealthA = sA.sources?.[programKey]?.status;
			const progHealthB = sB.sources?.[programKey]?.status;
			const progDegradedA = progHealthA === 'offline' || progHealthA === 'no_signal';
			const progDegradedB = progHealthB === 'offline' || progHealthB === 'no_signal';
			if (progDegradedA && !progDegradedB) return { candidate: 'B', bothEffective: true };
			if (progDegradedB && !progDegradedA) return { candidate: 'A', bothEffective: true };
		}

		// Source health scoring: if one engine has >50% sources degraded and other doesn't, healthier wins
		const healthyA = countHealthySources(sA);
		const healthyB = countHealthySources(sB);
		const totalA = Object.keys(sA.sources ?? {}).length;
		const totalB = Object.keys(sB.sources ?? {}).length;
		const degradedA = totalA > 0 && healthyA / totalA < 0.5;
		const degradedB = totalB > 0 && healthyB / totalB < 0.5;
		if (degradedA && !degradedB) return { candidate: 'B', bothEffective: true };
		if (degradedB && !degradedA) return { candidate: 'A', bothEffective: true };

		// Fall back to seq-based logic
		const seqA = sA.lastCommandSeq ?? 0;
		const seqB = sB.lastCommandSeq ?? 0;
		if (seqA > seqB) return { candidate: 'A', bothEffective: true };
		if (seqB > seqA) return { candidate: 'B', bothEffective: true };
		// Equal seq: prefer lower RTT only if the delta clearly exceeds hysteresis.
		// Small fluctuations (< RTT_HYSTERESIS_MS) keep A as authority to prevent jitter flapping.
		if (rttA > 0 && rttB > 0) {
			const delta = rttA - rttB;
			if (Math.abs(delta) > RTT_HYSTERESIS_MS) {
				return { candidate: delta > 0 ? 'B' : 'A', bothEffective: true };
			}
		}
		// Final tie (and within hysteresis): A wins
		return { candidate: 'A', bothEffective: true };
	}

	/** Update heldAuthority with hold-time logic. Called from tick() and ingest().
	 * Hold-time: only switch authority if the new candidate has been the winner
	 * for at least AUTHORITY_HOLD_MS. Prevents jitter-driven A→B→A→B flapping.
	 * Exception: switch immediately if the current authority is stale/disconnected. */
	function updateHeldAuthority(now: number) {
		const { candidate, bothEffective } = computeRawAuthority(now);

		// If only one engine is effective, switch immediately (no hold-time)
		if (!bothEffective) {
			heldAuthority = candidate;
			lastAuthorityCandidate = candidate;
			lastAuthorityCandidateSince = now;
			authorityEstablished = false;
			return;
		}

		// First dual-engine evaluation: apply immediately (no warm-up period)
		if (!authorityEstablished) {
			authorityEstablished = true;
			heldAuthority = candidate;
			lastAuthorityCandidate = candidate;
			lastAuthorityCandidateSince = now;
			return;
		}

		if (candidate === heldAuthority) {
			// No change — reset candidate tracking
			lastAuthorityCandidate = candidate;
			lastAuthorityCandidateSince = now;
			return;
		}

		// Candidate differs from current authority
		if (candidate !== lastAuthorityCandidate) {
			// New candidate just appeared — start timing
			lastAuthorityCandidate = candidate;
			lastAuthorityCandidateSince = now;
			return;
		}

		// Same candidate has been winning — check if hold-time has elapsed
		if (now - lastAuthorityCandidateSince >= AUTHORITY_HOLD_MS) {
			heldAuthority = candidate;
		}
		// Otherwise: still within hold-time — keep current heldAuthority
	}

	/** Which engine is authoritative. Reads the hold-time-stabilized value
	 * computed by updateHeldAuthority() in tick()/ingest(). */
	const authoritative = $derived.by((): EngineId => {
		// Read heldAuthority (the $state variable updated imperatively).
		// Also read lastTickTime to ensure reactivity triggers on tick().
		void lastTickTime;
		return heldAuthority;
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
		// Audio channels: compare settings only (exclude metering)
		if (!audioChannelsSettingsEqual(stateA.audioChannels, stateB.audioChannels)) {
			result.push({ field: 'audioChannels', engineA: 'settings differ', engineB: 'settings differ' });
		}
		// Check recording state divergence (nested object, not in DIVERGENCE_FIELDS)
		if (stateA.recording?.active !== stateB.recording?.active) {
			result.push({ field: 'recording', engineA: stateA.recording, engineB: stateB.recording });
		}
		// Graphics layer health divergence (layer count or active state/html5Status differs)
		const layersA = stateA.graphics?.layers ?? [];
		const layersB = stateB.graphics?.layers ?? [];
		if (layersA.length !== layersB.length) {
			result.push({ field: 'graphics.layers', engineA: `${layersA.length} layers`, engineB: `${layersB.length} layers` });
		} else {
			for (let i = 0; i < layersA.length; i++) {
				if (layersA[i].active !== layersB[i].active || layersA[i].html5Status !== layersB[i].html5Status) {
					result.push({ field: 'graphics.layers', engineA: layersA[i], engineB: layersB[i] });
					break; // one divergence entry is sufficient
				}
			}
		}
		// Destination divergence: compare count of active (non-error, non-empty state) destinations
		const destsA = stateA.destinations ?? [];
		const destsB = stateB.destinations ?? [];
		const activeDestsA = destsA.filter((d) => d.state === 'running' || d.state === 'connected').length;
		const activeDestsB = destsB.filter((d) => d.state === 'running' || d.state === 'connected').length;
		if (activeDestsA !== activeDestsB) {
			result.push({
				field: 'destinations',
				engineA: `${activeDestsA} active`,
				engineB: `${activeDestsB} active`,
			});
		}
		// SRT output divergence: compare active state
		if (stateA.srtOutput?.active !== stateB.srtOutput?.active) {
			result.push({ field: 'srtOutput', engineA: stateA.srtOutput, engineB: stateB.srtOutput });
		}
		// Layout preset divergence
		if (!fieldEquals(stateA.layout?.activePreset, stateB.layout?.activePreset)) {
			result.push({
				field: 'layout.activePreset',
				engineA: stateA.layout?.activePreset,
				engineB: stateB.layout?.activePreset,
			});
		}
		// Clip player state divergence
		const clipsA = stateA.clipPlayers ?? [];
		const clipsB = stateB.clipPlayers ?? [];
		if (clipsA.length !== clipsB.length) {
			result.push({ field: 'clipPlayers', engineA: `${clipsA.length} slots`, engineB: `${clipsB.length} slots` });
		} else {
			for (let i = 0; i < clipsA.length; i++) {
				if (clipsA[i].state !== clipsB[i].state || clipsA[i].clipName !== clipsB[i].clipName) {
					result.push({ field: 'clipPlayers', engineA: `${clipsA[i].clipName}:${clipsA[i].state}`, engineB: `${clipsB[i].clipName}:${clipsB[i].state}` });
					break;
				}
			}
		}
		// Replay divergence: state, source, and speed
		if (stateA.replay && stateB.replay) {
			if (stateA.replay.state !== stateB.replay.state) {
				result.push({ field: 'replay.state', engineA: stateA.replay.state, engineB: stateB.replay.state });
			}
			if (stateA.replay.source !== stateB.replay.source) {
				result.push({ field: 'replay.source', engineA: stateA.replay.source, engineB: stateB.replay.source });
			}
			if (stateA.replay.speed !== stateB.replay.speed) {
				result.push({ field: 'replay.speed', engineA: stateA.replay.speed, engineB: stateB.replay.speed });
			}
		} else if (stateA.replay?.state !== stateB.replay?.state) {
			result.push({ field: 'replay', engineA: stateA.replay, engineB: stateB.replay });
		}
		// DVE layout divergence (full object comparison, not just activePreset)
		if (!fieldEquals(stateA.layout, stateB.layout)) {
			result.push({ field: 'layout', engineA: stateA.layout, engineB: stateB.layout });
		}
		// Upstream key config divergence
		if (!fieldEquals(stateA.keyConfig, stateB.keyConfig)) {
			result.push({ field: 'keyConfig', engineA: stateA.keyConfig, engineB: stateB.keyConfig });
		}
		// Comms participant muted state divergence (targeted check, not full object
		// comparison — avoids noise from transient fields like participant audio levels)
		const commsA = stateA.comms;
		const commsB = stateB.comms;
		if (commsA?.active !== commsB?.active) {
			result.push({ field: 'comms.active', engineA: commsA?.active, engineB: commsB?.active });
		}
		return result;
	});

	/** Check if audio mute/afv state differs between engines (union of both channel sets). */
	function hasAudioMismatch(): boolean {
		if (!stateA?.audioChannels || !stateB?.audioChannels) return false;
		const allKeys = new Set([
			...Object.keys(stateA.audioChannels),
			...Object.keys(stateB.audioChannels),
		]);
		for (const key of allKeys) {
			const chA = stateA.audioChannels[key];
			const chB = stateB.audioChannels[key];
			if (!chA || !chB) continue;
			if (chA.muted !== chB.muted || chA.afv !== chB.afv) return true;
		}
		return false;
	}

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

		// Audio mute/afv mismatch is critical — one engine muted while other isn't
		if (hasAudioMismatch()) return true;

		// Recording mismatch is critical — one engine recording while other isn't
		if (stateA.recording?.active !== stateB.recording?.active) return true;

		// Check playout channel divergence (itemIndex with drift tolerance, currentTitle)
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
				// Tolerate <=1 item index difference (natural scheduler drift)
				const indexDelta = Math.abs((chA.itemIndex ?? 0) - (chB.itemIndex ?? 0));
				if (indexDelta > 1) return true;
				// Only flag title mismatch if index delta is 0 (same item, different title = real mismatch)
				if (indexDelta === 0 && chA.currentTitle !== undefined && chB.currentTitle !== undefined && chA.currentTitle !== chB.currentTitle) return true;
			}
		}

		return false;
	}

	/** Compute sync status based on current state + elapsed time. */
	function computeStatus(now: number): SyncStatus {
		let hasA = stateA !== null && connectedA;
		let hasB = stateB !== null && connectedB;

		// Stale detection: mark engine as single-engine if no state updates received.
		// If transport is alive AND pongs are fresh (within 30s = 3x the 10s ping interval),
		// the engine is provably alive — but if state updates stopped for STATE_UPDATE_STALE_MS,
		// the application is hung and the engine should still be marked stale.
		if (hasA && lastSeenA > 0) {
			const pongFreshA = transportAliveA && lastPongA > 0 && (now - lastPongA < PONG_FRESH_MS);
			const stateUpdateFreshA = now - lastSeenA < STATE_UPDATE_STALE_MS;
			if (pongFreshA && !stateUpdateFreshA) {
				// Pongs arrive but state updates stopped — engine application is hung
				hasA = false;
			} else if (!pongFreshA) {
				const thresholdA = transportAliveA ? STALE_TRANSPORT_ALIVE_MS : STALE_THRESHOLD_MS;
				if (now - lastSeenA > thresholdA) hasA = false;
			}
		}
		if (hasB && lastSeenB > 0) {
			const pongFreshB = transportAliveB && lastPongB > 0 && (now - lastPongB < PONG_FRESH_MS);
			const stateUpdateFreshB = now - lastSeenB < STATE_UPDATE_STALE_MS;
			if (pongFreshB && !stateUpdateFreshB) {
				// Pongs arrive but state updates stopped — engine application is hung
				hasB = false;
			} else if (!pongFreshB) {
				const thresholdB = transportAliveB ? STALE_TRANSPORT_ALIVE_MS : STALE_THRESHOLD_MS;
				if (now - lastSeenB > thresholdB) hasB = false;
			}
		}

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

		// Clear orphaned transition lock if locked engine is stale or disconnected
		if (transitionLockEngine !== null) {
			const lockedLastSeen = transitionLockEngine === 'A' ? lastSeenA : lastSeenB;
			const lockedConnected = transitionLockEngine === 'A' ? connectedA : connectedB;
			if (!lockedConnected || (lockedLastSeen > 0 && now - lockedLastSeen > STALE_THRESHOLD_MS)) {
				transitionLockEngine = null;
			}
		}

		// Re-evaluate authority with hold-time (may promote a pending candidate)
		updateHeldAuthority(now);

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
		// Re-evaluate authority since RTT affects tie-breaking
		updateHeldAuthority(Date.now());
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
		// Clear transition lock if disconnected engine holds it
		if (transitionLockEngine === engine) {
			transitionLockEngine = null;
		}
		// Re-evaluate authority (disconnection bypasses hold-time)
		const now = Date.now();
		updateHeldAuthority(now);
		// Re-evaluate status
		tickedStatus = computeStatus(now);
	}

	function setRecovering(value: boolean) {
		recovering = value;
	}

	function setClockDiverged(diverged: boolean) {
		clockDiverged = diverged;
	}

	function setWarmStandbyDegraded(degraded: boolean) {
		warmStandbyDegraded = degraded;
	}

	function setTransportAlive(engine: EngineId, alive: boolean) {
		if (engine === 'A') transportAliveA = alive;
		else transportAliveB = alive;
	}

	function recordPong(engine: EngineId): void {
		if (engine === 'A') lastPongA = Date.now();
		else lastPongB = Date.now();
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
		// Surface per-engine command dispatch errors
		const errA = stateA?.lastExecutedErr;
		const errB = stateB?.lastExecutedErr;
		if (errA && connectedA) w.push(`engine_a_cmd_error: ${errA}`);
		if (errB && connectedB) w.push(`engine_b_cmd_error: ${errB}`);
		if (errA && errB && errA !== errB) w.push('cmd_error_divergence');
		if (warmStandbyDegraded) w.push('warm_standby_degraded');
		// Surface late commands (arrived after their PTP target time)
		const lateA = stateA?.lateCommands;
		const lateB = stateB?.lateCommands;
		if (lateA && lateA > 0 && connectedA) w.push(`${lateA} commands arrived late on Engine A`);
		if (lateB && lateB > 0 && connectedB) w.push(`${lateB} commands arrived late on Engine B`);
		return w;
	});

	const staleEngines = $derived.by((): EngineId[] => {
		const now = lastTickTime;
		const result: EngineId[] = [];
		const pongFreshA = transportAliveA && lastPongA > 0 && (now - lastPongA < PONG_FRESH_MS);
		const pongFreshB = transportAliveB && lastPongB > 0 && (now - lastPongB < PONG_FRESH_MS);
		// Stale if pongs are fresh but state updates stopped (hung application)
		if (connectedA && pongFreshA && lastSeenA > 0 && (now - lastSeenA >= STATE_UPDATE_STALE_MS)) result.push('A');
		if (connectedB && pongFreshB && lastSeenB > 0 && (now - lastSeenB >= STATE_UPDATE_STALE_MS)) result.push('B');
		// Stale if pongs are not fresh: use transport-aware threshold (matches computeStatus logic)
		if (connectedA && !pongFreshA && lastSeenA > 0) {
			const thresholdA = transportAliveA ? STALE_TRANSPORT_ALIVE_MS : STALE_THRESHOLD_MS;
			if (now - lastSeenA > thresholdA) result.push('A');
		}
		if (connectedB && !pongFreshB && lastSeenB > 0) {
			const thresholdB = transportAliveB ? STALE_TRANSPORT_ALIVE_MS : STALE_THRESHOLD_MS;
			if (now - lastSeenB > thresholdB) result.push('B');
		}
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
		setWarmStandbyDegraded,
		setTransportAlive,
		recordPong,
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
