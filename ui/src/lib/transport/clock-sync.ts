const MSG_PING = 0xFE;
const MSG_PONG = 0xFF;
const FRAME_DURATION_US = 33_333; // 30fps
const JITTER_MULTIPLIER = 2;
const DEFAULT_MARGIN_US = FRAME_DURATION_US * 2;

export class ClockSync {
	private offsets = new Map<string, number>();
	private rtts = new Map<string, number>();
	private jitters = new Map<string, number>();

	updateRTT(engineId: string, rttUS: number): void {
		const prev = this.rtts.get(engineId);
		// Track peak RTT for safety-margin conservatism.
		const prevPeak = this.rtts.get(engineId) ?? 0;
		this.rtts.set(engineId, Math.max(rttUS, prevPeak));
		if (prev !== undefined) {
			const jitter = Math.abs(rttUS - prev);
			const prevJitter = this.jitters.get(engineId) ?? 0;
			this.jitters.set(engineId, prevJitter * 0.875 + jitter * 0.125);
		} else {
			// Bootstrap jitter from RTT: assume ~12.5% initial uncertainty.
			this.jitters.set(engineId, rttUS * 0.125);
		}
	}

	updateOffset(engineId: string, offsetUS: number): void {
		this.offsets.set(engineId, offsetUS);
	}

	ptpNow(): number {
		const localUS = Date.now() * 1000;
		const offsets = [...this.offsets.values()];
		if (offsets.length === 0) return localUS;
		const avgOffset = offsets.reduce((a, b) => a + b, 0) / offsets.length;
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

	buildPing(seq: number): Uint8Array {
		const buf = new Uint8Array(5);
		buf[0] = MSG_PING;
		const view = new DataView(buf.buffer);
		view.setUint32(1, seq);
		return buf;
	}

	parsePong(data: Uint8Array): number {
		const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
		const hi = view.getUint32(1);
		const lo = view.getUint32(5);
		return hi * 2 ** 32 + lo;
	}
}
