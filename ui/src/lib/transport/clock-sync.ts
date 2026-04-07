import type { EngineId } from '$lib/api/types';

const MSG_PING = 0xFE;
const MSG_PONG = 0xFF;
const FRAME_DURATION_US = 33_333; // 30fps
const JITTER_MULTIPLIER = 2;
const DEFAULT_MARGIN_US = FRAME_DURATION_US * 4;
const EWMA_ALPHA = 0.125;
const DEFAULT_SYNC_INTERVAL_MS = 10_000;

export class ClockSync {
	private offsets = new Map<string, number>();
	private rtts = new Map<string, number>();
	private jitters = new Map<string, number>();
	private lastRawRTT = new Map<string, number>();
	private lastPingSentAt = new Map<string, number>(); // engineId -> µs timestamp
	private offsetAge = new Map<string, number>(); // engineId -> last update time (Date.now())
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
	}

	getRTT(engineId: EngineId): number {
		return this.rtts.get(engineId) ?? 0;
	}

	getOffset(engineId: EngineId): number {
		return this.offsets.get(engineId) ?? 0;
	}

	handlePong(engineId: EngineId, data: Uint8Array): void {
		const serverTimestamp = this.parsePong(data);
		const now = Date.now() * 1000;
		const sentAt = this.lastPingSentAt.get(engineId);
		if (sentAt === undefined) return; // no pending ping

		const rtt = now - sentAt;
		const offset = serverTimestamp - (sentAt + rtt / 2);

		this.updateRTT(engineId, rtt);
		this.updateOffset(engineId, offset);
		this.offsetAge.set(engineId, Date.now());
	}

	ptpNow(): number {
		const localUS = Date.now() * 1000;
		const now = Date.now();
		const validOffsets: number[] = [];
		for (const [engineId, offset] of this.offsets) {
			const age = this.offsetAge.get(engineId);
			if (age !== undefined && now - age <= 60_000) {
				validOffsets.push(offset);
			}
		}
		if (validOffsets.length === 0) return localUS;
		const avgOffset = validOffsets.reduce((a, b) => a + b, 0) / validOffsets.length;
		return Math.round(localUS + avgOffset);
	}

	getOffsetDivergence(): number {
		const offsets = [...this.offsets.values()];
		if (offsets.length < 2) return 0;
		return Math.abs(offsets[0] - offsets[1]);
	}

	isClockDiverged(): boolean {
		const div = this.getOffsetDivergence();
		if (div === 0) return false; // No divergence data
		return div > this.safetyMargin();
	}

	safetyMargin(): number {
		const rttValues = [...this.rtts.values()];
		const jitterValues = [...this.jitters.values()];
		if (rttValues.length === 0) return DEFAULT_MARGIN_US;
		const maxRTT = Math.max(...rttValues);
		const maxJitter = jitterValues.length > 0 ? Math.max(...jitterValues) : 0;
		return maxRTT + JITTER_MULTIPLIER * maxJitter + FRAME_DURATION_US;
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
			this.lastPingSentAt.set(engineId, Date.now() * 1000); // µs
			sendPing(engineId, this.buildPing(this.pingSeq++));
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

	parsePong(data: Uint8Array): number {
		if (data.byteLength < 9) {
			throw new Error(`parsePong: expected >= 9 bytes, got ${data.byteLength}`);
		}
		if (data[0] !== MSG_PONG) {
			throw new Error(`parsePong: expected type 0x${MSG_PONG.toString(16)}, got 0x${data[0].toString(16)}`);
		}
		const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
		const hi = view.getUint32(1);
		const lo = view.getUint32(5);
		return hi * 2 ** 32 + lo;
	}
}
