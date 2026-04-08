import type { EngineId } from '$lib/api/types';

/** Microsecond-precision monotonic timestamp for RTT measurement. */
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

const STALE_OFFSET_MS = 60_000;

export class ClockSync {
	private offsets = new Map<string, number>();
	private offsetAge = new Map<string, number>();
	private rtts = new Map<string, number>();
	private jitters = new Map<string, number>();
	private lastRawRTT = new Map<string, number>();
	private pingSentTimes = new Map<number, number>(); // seq -> sentAt µs
	private syncInterval: ReturnType<typeof setInterval> | null = null;
	private pingSeq = 0;
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
		return Math.abs(validOffsets[0] - validOffsets[1]);
	}

	isClockDiverged(): boolean {
		const now = Date.now();
		const validCount = [...this.offsetAge.values()].filter(
			age => now - age <= STALE_OFFSET_MS
		).length;
		if (validCount < 2) return false;
		return this.getOffsetDivergence() > this.safetyMargin();
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
			const seq = this.pingSeq++;
			this.pingSentTimes.set(seq, nowMicros());
			sendPing(engineId, this.buildPing(seq));
		}
		// Prune old entries (keep last 100)
		if (this.pingSentTimes.size > 100) {
			const entries = [...this.pingSentTimes.entries()].sort((a, b) => a[0] - b[0]);
			for (const [key] of entries.slice(0, entries.length - 100)) {
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

	handlePong(engineId: EngineId, data: Uint8Array): void {
		const { seq, serverTimestamp } = this.parsePong(data);
		const now = nowMicros();
		const sentAt = this.pingSentTimes.get(seq);
		if (sentAt === undefined) return;
		this.pingSentTimes.delete(seq);

		const rtt = now - sentAt;
		const offset = serverTimestamp - (sentAt + rtt / 2);
		this.updateRTT(engineId, rtt);
		this.updateOffset(engineId, offset);
		this.offsetAge.set(engineId, Date.now());
	}
}
