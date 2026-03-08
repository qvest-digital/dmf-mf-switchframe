declare class AudioWorkletProcessor {
	readonly port: MessagePort;
	constructor();
}

declare function registerProcessor(name: string, ctor: new () => AudioWorkletProcessor): void;

// IMPORTANT: SharedStates must be kept in sync with audio-ring-buffer.ts SharedStates
const SharedStates = {
	BUFF_START: 0,
	BUFF_END: 1,
	INSERTED_SILENCE_MS: 2,
	IS_PLAYING: 3,
	PTS_HI: 4,
	PTS_LO: 5,
	NUM_CHANNELS: 6,
	SAMPLE_RATE: 7,
	PEAK_BASE: 8,
} as const;

const MAX_CHANNELS = 8;
const RMS_BASE = SharedStates.PEAK_BASE + MAX_CHANNELS;

// Adaptive rate control: when buffer is between LOW and HIGH, consume at 1x.
// Below LOW, slow down consumption; above HIGH, speed up. Drift is
// compensated by skipping or repeating ~3 samples per 128-sample quantum
// (~60µs, perceptually transparent) — no resampling or interpolation.
//
// The ±2% range is chosen to be imperceptible. Broadcast systems use
// ±0.1-0.5%, but we allow slightly more to handle WebTransport jitter.
// At ±2%, pitch shift is <0.3 semitones — well below audibility threshold.
// The previous ±10% caused noticeable pitch changes and garbling.
//
// Buffer depth directly determines A/V sync offset: video displays immediately
// but audio plays through the buffer, so avSyncMs ≈ buffer depth. Targets are
// set to keep steady-state depth at ~80-120ms for <150ms A/V sync.
const TARGET_BUFFER_MS = 100;
const LOW_WATER_MS = 50;
const HIGH_WATER_MS = 200;
const MAX_SPEED_RATIO = 1.02;
const MIN_SPEED_RATIO = 0.98;
// Above this threshold, hard-flush the ring buffer to TARGET_BUFFER_MS.
// Set high enough that normal jitter doesn't trigger it, but delivery
// stalls (network hiccup, tab backgrounded) are caught quickly.
const HARD_FLUSH_MS = 400;

function writePTS(states: Int32Array, pts: number): void {
	const intPart = Math.trunc(pts);
	Atomics.store(states, SharedStates.PTS_HI, Math.trunc(intPart / 1_000_000));
	Atomics.store(states, SharedStates.PTS_LO, intPart % 1_000_000);
}

class PrismAudioWorkletProcessor extends AudioWorkletProcessor {
	private sharedStates: Int32Array | null = null;
	private floatViews: Float32Array[] = [];
	private ringSize = 0;
	private numChannels = 0;

	private basePTS = 0;
	private sampleOffset = 0;
	private samplesConsumed = 0;
	private samplesOutput = 0;
	private localSampleRate = 0;

	private fractionalAccum = 0;

	constructor() {
		super();
		this.port.onmessage = (ev: MessageEvent) => this.handleMessage(ev.data);
	}

	private handleMessage(msg: { type: string; sab?: SharedArrayBuffer[]; commBuffer?: SharedArrayBuffer; numChannels?: number; sampleRate?: number; pts?: number; sampleOffset?: number }): void {
		if (msg.type === "init") {
			this.sharedStates = new Int32Array(msg.commBuffer!);
			this.numChannels = msg.numChannels!;
			this.localSampleRate = msg.sampleRate!;
			this.floatViews = [];
			for (let c = 0; c < this.numChannels; c++) {
				this.floatViews.push(new Float32Array(msg.sab![c]));
			}
			this.ringSize = this.floatViews[0].length;
		} else if (msg.type === "set-pts") {
			this.basePTS = msg.pts!;
			this.sampleOffset = msg.sampleOffset ?? 0;
			this.samplesConsumed = 0;
			this.samplesOutput = 0;
		}
	}

	process(_inputs: Float32Array[][], outputs: Float32Array[][], _parameters: Record<string, Float32Array>): boolean {
		if (!this.sharedStates || this.floatViews.length === 0) return true;

		const isPlaying = Atomics.load(this.sharedStates, SharedStates.IS_PLAYING);
		if (!isPlaying) {
			this.outputSilence(outputs);
			return true;
		}

		const output = outputs[0];
		const framesToFill = output[0].length;
		let start = Atomics.load(this.sharedStates, SharedStates.BUFF_START);
		const end = Atomics.load(this.sharedStates, SharedStates.BUFF_END);

		let available: number;
		if (start === end) {
			available = 0;
		} else if (end > start) {
			available = end - start;
		} else {
			available = (this.ringSize - start) + end;
		}

		if (available === 0) {
			this.outputSilence(outputs);
			Atomics.add(this.sharedStates, SharedStates.INSERTED_SILENCE_MS,
				Math.round((framesToFill / this.localSampleRate) * 1000));
			// Do NOT advance samplesConsumed or PTS during underruns.
			// The renderer detects stalled audio (200ms) and falls back to
			// wall-clock free-run for video. When real audio resumes, PTS
			// will accurately reflect the media timeline position.
			return true;
		}

		let bufferMs = (available / this.localSampleRate) * 1000;

		// Hard flush: if buffer exceeds threshold (extreme stall recovery),
		// skip forward to TARGET_BUFFER_MS. Adjust sampleOffset so PTS
		// jumps to match the new ring position.
		if (bufferMs > HARD_FLUSH_MS) {
			const targetSamples = Math.round((TARGET_BUFFER_MS / 1000) * this.localSampleRate);
			const skipSamples = available - targetSamples;
			if (skipSamples > 0) {
				start = (start + skipSamples) % this.ringSize;
				Atomics.store(this.sharedStates, SharedStates.BUFF_START, start);
				this.sampleOffset += skipSamples;
				available = targetSamples;
				bufferMs = TARGET_BUFFER_MS;
			}
		}

		const speedRatio = this.computeSpeedRatio(bufferMs);

		const channelsToFill = Math.min(output.length, this.numChannels);

		// Drift compensation via pointer-rate adjustment:
		// Always copy clean source samples to output (no interpolation/resampling).
		// Compensate by advancing the ring read pointer at a slightly different
		// rate than the output frame size. At ±10%, this means ~13 samples of
		// overlap (slow) or skip (fast) per 128-sample quantum — ~270µs, inaudible.
		const exactAdvance = framesToFill * speedRatio + this.fractionalAccum;
		const toAdvance = Math.min(Math.floor(exactAdvance), available);
		this.fractionalAccum = exactAdvance - Math.floor(exactAdvance);

		const toRead = Math.min(framesToFill, available);
		this.readFromRing(start, toRead, output, channelsToFill);

		if (toRead < framesToFill) {
			for (let c = 0; c < channelsToFill; c++) {
				output[c].fill(0, toRead);
			}
		}

		for (let c = channelsToFill; c < output.length; c++) {
			output[c].fill(0);
		}

		const newStart = (start + toAdvance) % this.ringSize;
		Atomics.store(this.sharedStates, SharedStates.BUFF_START, newStart);
		this.samplesConsumed += toAdvance;
		this.samplesOutput += toRead;

		// PTS tracks samples actually output to speakers (toRead), not the
		// speed-adjusted ring pointer advance (toAdvance). When adaptive rate
		// control drains excess buffer at >1.0x speed, the ring pointer jumps
		// ahead (skipping samples), but PTS should reflect wall-clock playback
		// position. Using samplesConsumed would cause PTS to overshoot by
		// ~50ms/sec at 1.05x, accumulating seconds of AV sync drift over time.
		const currentPTS = this.basePTS +
			((this.sampleOffset + this.samplesOutput) / this.localSampleRate) * 1_000_000;
		writePTS(this.sharedStates, currentPTS);

		this.computeLevels(output, channelsToFill, framesToFill);

		return true;
	}

	private computeSpeedRatio(bufferMs: number): number {
		if (bufferMs < LOW_WATER_MS) {
			// Buffer is getting dangerously low -- slow down consumption
			const t = bufferMs / LOW_WATER_MS;
			return MIN_SPEED_RATIO + t * (1.0 - MIN_SPEED_RATIO);
		} else if (bufferMs > HIGH_WATER_MS) {
			// Buffer is too full -- speed up consumption to drain
			const excess = bufferMs - HIGH_WATER_MS;
			const range = TARGET_BUFFER_MS;
			const t = Math.min(excess / range, 1.0);
			return 1.0 + t * (MAX_SPEED_RATIO - 1.0);
		}
		return 1.0;
	}

	private readFromRing(start: number, count: number, output: Float32Array[], channels: number): void {
		for (let c = 0; c < channels; c++) {
			const dst = output[c];
			const src = this.floatViews[c];
			const firstPart = Math.min(count, this.ringSize - start);
			dst.set(src.subarray(start, start + firstPart), 0);
			if (count > firstPart) {
				dst.set(src.subarray(0, count - firstPart), firstPart);
			}
		}
	}

	private outputSilence(outputs: Float32Array[][]): void {
		const output = outputs[0];
		for (let c = 0; c < output.length; c++) {
			output[c].fill(0);
		}
	}

	private computeLevels(output: Float32Array[], channels: number, samples: number): void {
		if (!this.sharedStates) return;

		for (let c = 0; c < channels; c++) {
			const data = output[c];
			let maxAbs = 0;
			let sumSq = 0;
			for (let i = 0; i < samples; i++) {
				const s = data[i];
				const abs = s < 0 ? -s : s;
				if (abs > maxAbs) maxAbs = abs;
				sumSq += s * s;
			}
			const rmsVal = Math.sqrt(sumSq / samples);

			Atomics.store(this.sharedStates, SharedStates.PEAK_BASE + c,
				Math.round(maxAbs * 1_000_000));
			Atomics.store(this.sharedStates, RMS_BASE + c,
				Math.round(rmsVal * 1_000_000));
		}
	}
}

registerProcessor("prism-audio-worklet", PrismAudioWorkletProcessor);
