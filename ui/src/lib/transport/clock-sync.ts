import type { EngineId } from '$lib/api/types';

/**
 * Monotonic timestamp in nominal microseconds for RTT measurement.
 *
 * Browser `performance.now()` typically has ~1ms resolution (some browsers
 * offer sub-millisecond), so multiplying by 1000 does not yield true
 * microsecond precision. For cross-region RTTs of 50-100ms, the ~1ms
 * quantization error is only 1-2% — acceptable for our purposes. The EWMA
 * smoothing in `updateRTT()` further reduces the impact of timer quantization
 * across successive samples.
 */
function nowMicros(): number {
	return Math.round(performance.now() * 1000);
}

const MSG_PING = 0xFE;
const MSG_PONG = 0xFF;
const FRAME_DURATION_US = 33_333; // 30fps
const JITTER_MULTIPLIER = 2;
const DEFAULT_MARGIN_US = FRAME_DURATION_US * 4;
const EWMA_ALPHA = 0.125;
const DEFAULT_SYNC_INTERVAL_MS = 10_000;
const STALE_PING_THRESHOLD_US = 30_000_000; // 30s — 3× the sync interval
const MAX_PENDING_PINGS = 100;

const STALE_OFFSET_MS = 20_000;

export class ClockSync {
	private offsets = new Map<string, number>();
	private offsetAge = new Map<string, number>();
	private rtts = new Map<string, number>();
	private jitters = new Map<string, number>();
	private lastRawRTT = new Map<string, number>();
	private pingSentTimes = new Map<number, number>(); // seq -> sentAt µs
	private syncInterval: ReturnType<typeof setInterval> | null = null;
	// Every viewer of an engine's control track receives every pong, so each
	// browser starts at its own seq to match only the pongs to its pings.
	private pingSeq = Math.floor(Math.random() * 2 ** 32);
	private trackedEngines: EngineId[] = [];

	updateRTT(engineId: EngineId, rttUS: number): void {
		const prev = this.rtts.get(engineId);
		const prevRaw = this.lastRawRTT.get(engineId);

		if (prev !== undefined) {
			// EWMA smoothing
			this.rtts.set(engineId, prev * (1 - EWMA_ALPHA) + rttUS * EWMA_ALPHA);
		} else {
			// Bootstrap: first sample becomes initial value
			this.rtts.set(engineId, rttUS);
		}

		if (prevRaw !== undefined) {
			// Jitter: EWMA of absolute difference between consecutive raw samples
			const jitterSample = Math.abs(rttUS - prevRaw);
			const prevJitter = this.jitters.get(engineId) ?? 0;
			this.jitters.set(engineId, prevJitter * (1 - EWMA_ALPHA) + jitterSample * EWMA_ALPHA);
		} else {
			// Bootstrap jitter from RTT: assume ~12.5% initial uncertainty.
			this.jitters.set(engineId, rttUS * EWMA_ALPHA);
		}

		this.lastRawRTT.set(engineId, rttUS);
	}

	updateOffset(engineId: EngineId, offsetUS: number): void {
		this.offsets.set(engineId, offsetUS);
		this.offsetAge.set(engineId, Date.now());
	}

	getRTT(engineId: EngineId): number {
		return this.rtts.get(engineId) ?? 0;
	}

	getOffset(engineId: EngineId): number {
		return this.offsets.get(engineId) ?? 0;
	}

	ptpNowForEngine(engineId: EngineId): number {
		const now = Date.now();
		const localUS = now * 1000;
		const offset = this.offsets.get(engineId);
		const age = this.offsetAge.get(engineId);
		if (offset !== undefined && age !== undefined && now - age <= STALE_OFFSET_MS) {
			return Math.round(localUS + offset);
		}
		return this.ptpNow();
	}

	ptpNow(): number {
		const now = Date.now();
		const localUS = now * 1000;
		const validOffsets: number[] = [];
		for (const [engineId, offset] of this.offsets) {
			const age = this.offsetAge.get(engineId);
			if (age !== undefined && now - age <= STALE_OFFSET_MS) {
				validOffsets.push(offset);
			}
		}
		if (validOffsets.length === 0) return 0;
		const avgOffset = validOffsets.reduce((a, b) => a + b, 0) / validOffsets.length;
		return Math.round(localUS + avgOffset);
	}

	safetyMargin(): number {
		const rttValues = [...this.rtts.values()];
		const jitterValues = [...this.jitters.values()];
		if (rttValues.length === 0) return DEFAULT_MARGIN_US;
		const maxRTT = Math.max(...rttValues);
		const maxJitter = jitterValues.length > 0 ? Math.max(...jitterValues) : 0;
		return maxRTT + JITTER_MULTIPLIER * maxJitter + FRAME_DURATION_US;
	}

	getOffsetDivergence(): number {
		const now = Date.now();
		const validOffsets: number[] = [];
		for (const [engineId, offset] of this.offsets) {
			const age = this.offsetAge.get(engineId);
			if (age !== undefined && now - age <= STALE_OFFSET_MS) {
				validOffsets.push(offset);
			}
		}
		if (validOffsets.length < 2) return 0;
		return Math.max(...validOffsets) - Math.min(...validOffsets);
	}

	isClockDiverged(): boolean {
		const now = Date.now();
		const validCount = [...this.offsetAge.values()].filter(
			age => now - age <= STALE_OFFSET_MS
		).length;
		if (validCount < 2) return false;
		return this.getOffsetDivergence() > this.safetyMargin();
	}

	/** True when all tracked engines have valid (non-stale) RTT and offset data. */
	isConverged(): boolean {
		if (this.trackedEngines.length < 2) return false;
		const now = Date.now();
		for (const id of this.trackedEngines) {
			const rtt = this.rtts.get(id);
			const offset = this.offsets.get(id);
			const age = this.offsetAge.get(id);
			if (rtt === undefined || offset === undefined) return false;
			if (age !== undefined && now - age > STALE_OFFSET_MS) return false;
		}
		return true;
	}

	addEngine(engineId: EngineId): void {
		if (!this.trackedEngines.includes(engineId)) {
			this.trackedEngines.push(engineId);
		}
	}

	startPeriodicSync(
		engineIds: EngineId[],
		sendPing: (engineId: EngineId, data: Uint8Array) => void,
		intervalMs: number = DEFAULT_SYNC_INTERVAL_MS
	): void {
		this.stopPeriodicSync();
		// Seed the tracked engine list from the provided IDs
		for (const id of engineIds) {
			this.addEngine(id);
		}
		// Fire immediately, then on interval
		this.doPingRound(sendPing);
		this.syncInterval = setInterval(() => this.doPingRound(sendPing), intervalMs);
	}

	private doPingRound(sendPing: (engineId: EngineId, data: Uint8Array) => void): void {
		for (const engineId of this.trackedEngines) {
			const seq = this.pingSeq;
			this.pingSeq = (this.pingSeq + 1) >>> 0; // the ping carries a uint32
			this.pingSentTimes.set(seq, nowMicros());
			sendPing(engineId, this.buildPing(seq));
		}
		// Prune stale entries by time (pongs that will never arrive)
		const staleThreshold = nowMicros() - STALE_PING_THRESHOLD_US;
		for (const [seq, sentAt] of this.pingSentTimes) {
			if (sentAt < staleThreshold) {
				this.pingSentTimes.delete(seq);
			}
		}
		// Secondary safety net: keep last MAX_PENDING_PINGS entries
		if (this.pingSentTimes.size > MAX_PENDING_PINGS) {
			const entries = [...this.pingSentTimes.entries()].sort((a, b) => a[1] - b[1]); // oldest first
			for (const [key] of entries.slice(0, entries.length - MAX_PENDING_PINGS)) {
				this.pingSentTimes.delete(key);
			}
		}
	}

	stopPeriodicSync(): void {
		if (this.syncInterval !== null) {
			clearInterval(this.syncInterval);
			this.syncInterval = null;
		}
	}

	buildPing(seq: number): Uint8Array {
		const buf = new Uint8Array(5);
		buf[0] = MSG_PING;
		const view = new DataView(buf.buffer);
		view.setUint32(1, seq);
		return buf;
	}

	parsePong(data: Uint8Array): { seq: number; serverTimestamp: number } {
		if (data.byteLength < 13) {
			throw new Error(`parsePong: expected >= 13 bytes, got ${data.byteLength}`);
		}
		if (data[0] !== MSG_PONG) {
			throw new Error(`parsePong: expected type 0x${MSG_PONG.toString(16)}, got 0x${data[0].toString(16)}`);
		}
		const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
		const seq = view.getUint32(1);
		const hi = view.getUint32(5);
		const lo = view.getUint32(9);
		return { seq, serverTimestamp: hi * 2 ** 32 + lo };
	}

	/**
	 * Process a pong response and update RTT + clock offset for this engine.
	 *
	 * The offset calculation (`serverTimestamp - (sentAt + rtt / 2)`) assumes
	 * symmetric network paths — i.e., that the one-way latency is exactly half
	 * the round-trip time. GCP cross-region links can exhibit ±20-30% asymmetry,
	 * meaning if the true one-way delays are A (client→server) and B
	 * (server→client), the measured RTT is A+B but we estimate A = B = RTT/2.
	 * An asymmetry of X ms causes an offset error of X/2 ms.
	 *
	 * The `safetyMargin()` compensates partially by adding RTT + 2*jitter +
	 * one frame duration, which absorbs moderate asymmetry but does not
	 * eliminate the bias in the offset estimate itself.
	 *
	 * Future improvement: track min-RTT samples per engine to detect persistent
	 * asymmetry (the minimum RTT is least affected by queuing and gives the
	 * tightest bound on true one-way delay).
	 */
	handlePong(engineId: EngineId, data: Uint8Array): void {
		const { seq, serverTimestamp } = this.parsePong(data);
		const now = nowMicros();
		const sentAt = this.pingSentTimes.get(seq);
		if (sentAt === undefined) return;
		this.pingSentTimes.delete(seq);

		const rtt = now - sentAt;

		// RTT is measured on the monotonic `performance.now()` clock (immune to
		// wall-clock jumps), but the offset MUST be expressed against the same
		// epoch as `serverTimestamp` (server wall-clock, `time.Now().UnixMicro()`)
		// and as `ptpNow()` (which adds the offset to `Date.now()*1000`).
		// `nowMicros()` lives in the `performance.now()` epoch (≈ms since page
		// load), so subtracting it from `serverTimestamp` would yield a bogus
		// ~navigationStart-sized offset. Convert the send time into the
		// wall-clock domain via the just-measured RTT: the request left the
		// client `rtt` µs ago in monotonic time, so its wall-clock send time is
		// `Date.now()*1000 - rtt`. Then
		//   offset = serverTimestamp - (wallSentAt + rtt/2)
		//          = serverTimestamp - (Date.now()*1000 - rtt/2).
		const wallNowUS = Date.now() * 1000;
		const offset = serverTimestamp - (wallNowUS - rtt / 2);
		this.updateRTT(engineId, rtt);
		this.updateOffset(engineId, offset);
		this.offsetAge.set(engineId, Date.now());
	}
}
