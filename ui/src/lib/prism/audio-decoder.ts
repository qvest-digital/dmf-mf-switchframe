import { AudioRingBuffer } from "./audio-ring-buffer";
import audioWorkletUrl from "./audio-worklet.ts?worker&url";

const MIN_BUFFER_MS = 50;
const RING_BUFFER_SECONDS = 4;
const PEAK_HOLD_SEC = 1.5;
const PEAK_HOLD_DECAY = 0.9;
const BAR_ATTACK = 0.6;
const BAR_RELEASE = 0.92;

/** Comprehensive audio pipeline diagnostics including decode timing, drift, silence, and WebAudio state. */
export interface AudioDiagnostics {
	callbackCount: number;
	callbacksPerSec: number;
	avgCallbackIntervalMs: number;
	minCallbackIntervalMs: number;
	maxCallbackIntervalMs: number;
	scheduleAheadMs: number;
	lastDriftMs: number;
	maxDriftMs: number;
	gapRepairs: number;
	underruns: number;
	totalSilenceMs: number;
	totalScheduled: number;
	decodeErrors: number;
	ptsJumps: number;
	ptsJumpMaxMs: number;
	inputPtsJumps: number;
	inputPtsWraps: number;
	lastInputPTS: number;
	lastOutputPTS: number;
	contextState: string;
	contextSampleRate: number;
	contextCurrentTime: number;
	contextBaseLatency: number;
	contextOutputLatency: number;
	isPlaying: boolean;
	pendingFrames: number;
}

/** Per-channel audio levels for VU meter rendering: instantaneous peak, RMS, and peak-hold values. */
interface AudioLevels {
	peak: number[];
	rms: number[];
	peakHold: number[];
	channels: number;
}

/**
 * Decodes compressed audio using WebCodecs AudioDecoder and plays it
 * through the Web Audio API. Decoded samples are written to a
 * SharedArrayBuffer-backed AudioRingBuffer, consumed by an AudioWorklet
 * on the audio thread for glitch-free playback. Supports per-track
 * muting, metering (for VU meters), and provides the current playback
 * PTS used by the renderer for A/V sync.
 */
export class PrismAudioDecoder {
	private context: AudioContext | null = null;
	private ownsContext = false;
	private decoder: AudioDecoder | null = null;
	private playing = false;
	private starting = false;
	private sampleRate = 0;
	private gainNode: GainNode | null = null;
	private muted = false;
	private numChannels = 0;
	private _suspended = false;

	private metering = false;
	private _peakHold: number[] = [];
	private _peakHoldTime: number[] = [];
	private _smoothedPeak: number[] = [];

	private ringBuffer: AudioRingBuffer | null = null;
	private workletNode: AudioWorkletNode | null = null;
	private firstPTS = -1;
	private totalScheduled = 0;
	private totalSilenceMs = 0;
	private samplesWritten = 0;

	// --- diagnostics ---
	private _diagCallbackCount = 0;
	private _diagDecodeErrors = 0;
	private _diagLastCallbackTime = 0;
	private _diagCallbackIntervalSum = 0;
	private _diagCallbackIntervalMax = 0;
	private _diagCallbackIntervalMin = Infinity;
	private _diagGapRepairs = 0;
	private _diagLastDrift = 0;
	private _diagMaxDrift = 0;
	private _diagUnderruns = 0;
	private _diagFirstCallbackTime = 0;
	private _diagLastPTS = -1;
	private _diagPtsJumps = 0;
	private _diagPtsJumpMaxUs = 0;

	// --- input PTS tracking (before WebCodecs) ---
	private _diagLastInputPTS = -1;
	private _diagInputPtsJumps = 0;
	private _diagInputPtsWraps = 0;
	private _ptsEpochReset = false;
	private _ptsEpochResetTargetPTS = -1; // PTS of the frame that triggered the reset
	private _configuredCodec = "";

	// --- resampling (source rate → context rate) ---
	private _resampleExtractBuf: Float32Array | null = null;
	private _resampleOutBufs: Float32Array[] = [];

	async configure(codec: string, sampleRate: number, channels: number, ctx?: AudioContext, destinationNode?: AudioNode): Promise<void> {
		this.reset();
		this.sampleRate = sampleRate;
		this.numChannels = channels;
		this._peakHold = new Array(channels).fill(0);
		this._peakHoldTime = new Array(channels).fill(0);
		this._smoothedPeak = new Array(channels).fill(0);

		if (ctx) {
			this.context = ctx;
			this.ownsContext = false;
		} else {
			this.context = new AudioContext({ sampleRate, latencyHint: "interactive" });
			this.ownsContext = true;
			if (this.context.state === "running") {
				await this.context.suspend();
			}
		}

		this.gainNode = this.context.createGain();
		this.gainNode.gain.value = this.muted ? 0 : 1;
		this.gainNode.connect(destinationNode ?? this.context.destination);

		// Use the AudioContext's actual sample rate for the ring buffer, not the source rate.
		// After resampling (e.g., 44.1kHz → 48kHz), all samples in the ring are at the
		// context rate. Using the source rate would miscalculate queueLengthMs, causing
		// A/V sync drift and buffer underruns for non-48kHz sources.
		const ctxRate = this.context.sampleRate;
		const ringSize = Math.ceil(ctxRate * RING_BUFFER_SECONDS);
		this.ringBuffer = new AudioRingBuffer();
		this.ringBuffer.init(channels, ringSize, ctxRate);

		try {
			await this.context.audioWorklet.addModule(audioWorkletUrl);
		} catch (e) {
			console.error("[AudioDecoder] Failed to load AudioWorklet module", e);
		}

		this.workletNode = new AudioWorkletNode(this.context, "prism-audio-worklet", {
			numberOfInputs: 0,
			numberOfOutputs: 1,
			outputChannelCount: [channels],
		});
		this.workletNode.connect(this.gainNode);

		const shared = this.ringBuffer.getSharedBuffers();
		this.workletNode.port.postMessage({
			type: "init",
			sab: shared.audioBuffers,
			commBuffer: shared.commBuffer,
			numChannels: channels,
			sampleRate,
		});

		this._configuredCodec = codec;
		this.createDecoder(codec, channels, sampleRate);
	}

	enableMetering(): void {
		this.metering = true;
	}

	private createDecoder(codec: string, channels: number, sampleRate: number): void {
		this.decoder = new AudioDecoder({
			output: (frame) => {
				this.onDecodedAudio(frame);
			},
			error: (err) => {
				console.error("[AudioDecoder] error:", err.message);
				this._diagDecodeErrors++;
				this.recoverDecoder();
			},
		});
		this.decoder.configure({ codec, numberOfChannels: channels, sampleRate });
	}

	private recoverDecoder(): void {
		if (!this._configuredCodec || !this.sampleRate || !this.numChannels) return;
		if (this.decoder) {
			try { this.decoder.close(); } catch { /* ignore */ }
			this.decoder = null;
		}
		console.warn("[AudioDecoder] Recovering decoder");
		this.createDecoder(this._configuredCodec, this.numChannels, this.sampleRate);
	}

	disableMetering(): void {
		this.metering = false;
		this._peakHold = new Array(this.numChannels).fill(0);
		this._peakHoldTime = new Array(this.numChannels).fill(0);
	}

	decode(data: Uint8Array, timestamp: number, _isDisco: boolean): void {
		if (!this.decoder || this.decoder.state !== "configured") return;

		if (this._diagLastInputPTS >= 0) {
			const gap = Math.abs(timestamp - this._diagLastInputPTS);
			if (gap > 100_000) this._diagInputPtsJumps++;
			if (timestamp < this._diagLastInputPTS &&
				this._diagLastInputPTS - timestamp > 30_000_000) {
				this._diagInputPtsWraps++;
				this._ptsEpochReset = true;
				this._ptsEpochResetTargetPTS = timestamp;
			} else if (gap > 500_000) {
				// PTS discontinuity >500ms (source cut, mixer gap, backward jump).
				// Re-anchor worklet PTS to prevent clock drift.
				// Record the target PTS so onDecodedAudio only consumes the
				// reset when the correct frame arrives (not an earlier queued frame
				// from the old PTS epoch still in WebCodecs' decode queue).
				//
				// No `this.playing` guard: the PTS jump from the server's mixer
				// temp-counter → SeedPTSFromVideo can arrive before the ring
				// buffer reaches MIN_BUFFER_MS (50ms), i.e. before startPlayback()
				// sets playing=true. Without the reset, firstPTS stays anchored
				// to the temp-counter epoch, causing permanent A/V desync.
				this._ptsEpochReset = true;
				this._ptsEpochResetTargetPTS = timestamp;
			}
		}
		this._diagLastInputPTS = timestamp;

		try {
			this.decoder.decode(new EncodedAudioChunk({ type: "key", timestamp, data }));
		} catch {
			this._diagDecodeErrors++;
		}
	}

	setSuspended(suspended: boolean): void {
		this._suspended = suspended;
	}

	setMuted(muted: boolean): void {
		if (this.muted === muted) return;
		this.muted = muted;
		if (this.gainNode) {
			this.gainNode.gain.value = muted ? 0 : 1;
		}
	}

	isMuted(): boolean {
		return this.muted;
	}

	getAudioDebug(): { muted: boolean; suspended: boolean; gain: number; playing: boolean; contextState: string } {
		return {
			muted: this.muted,
			suspended: this._suspended,
			gain: this.gainNode?.gain.value ?? -1,
			playing: this.playing,
			contextState: this.context?.state ?? "null",
		};
	}

	isMetering(): boolean {
		return this.metering;
	}

	getPlaybackPTS(): number {
		if (!this.ringBuffer || !this.playing) return -1;
		const pts = this.ringBuffer.readPTS();
		if (pts <= 0) return pts;
		return pts;
	}

	getLevels(): AudioLevels {
		if (!this.metering || !this.ringBuffer) {
			return { peak: [], rms: [], peakHold: [], channels: this.numChannels };
		}

		const raw = this.ringBuffer.readLevels();
		const now = performance.now() / 1000;
		const peak: number[] = [];
		const rms: number[] = [];

		for (let c = 0; c < this.numChannels; c++) {
			const maxAbs = raw.peak[c] ?? 0;
			const rmsVal = raw.rms[c] ?? 0;

			if (c < this._smoothedPeak.length) {
				if (maxAbs > this._smoothedPeak[c]) {
					this._smoothedPeak[c] += (maxAbs - this._smoothedPeak[c]) * BAR_ATTACK;
				} else {
					this._smoothedPeak[c] *= BAR_RELEASE;
					if (this._smoothedPeak[c] < 0.0001) this._smoothedPeak[c] = 0;
				}
			}

			peak.push(this._smoothedPeak[c] ?? maxAbs);
			rms.push(rmsVal);

			if (c < this._peakHold.length) {
				if (maxAbs >= this._peakHold[c]) {
					this._peakHold[c] = maxAbs;
					this._peakHoldTime[c] = now;
				} else if (now - this._peakHoldTime[c] > PEAK_HOLD_SEC) {
					this._peakHold[c] *= PEAK_HOLD_DECAY;
					if (this._peakHold[c] < 0.001) this._peakHold[c] = 0;
				}
			}
		}

		return { peak, rms, peakHold: this._peakHold, channels: this.numChannels };
	}

	getStats(): { queueLengthMs: number; totalSilenceInsertedMs: number; isPlaying: boolean } {
		if (this.ringBuffer) {
			const stats = this.ringBuffer.getStats();
			return {
				queueLengthMs: stats.queueLengthMs,
				totalSilenceInsertedMs: stats.totalSilenceInsertedMs,
				isPlaying: this.playing,
			};
		}
		return {
			queueLengthMs: 0,
			totalSilenceInsertedMs: Math.floor(this.totalSilenceMs),
			isPlaying: this.playing,
		};
	}

	getDiagnostics(): AudioDiagnostics {
		const ringStats = this.ringBuffer?.getStats();
		const avgInterval = this._diagCallbackCount > 1
			? this._diagCallbackIntervalSum / (this._diagCallbackCount - 1)
			: 0;
		return {
			callbackCount: this._diagCallbackCount,
			callbacksPerSec: this._diagFirstCallbackTime > 0
				? this._diagCallbackCount / ((performance.now() - this._diagFirstCallbackTime) / 1000)
				: 0,
			avgCallbackIntervalMs: avgInterval,
			minCallbackIntervalMs: this._diagCallbackIntervalMin === Infinity ? 0 : this._diagCallbackIntervalMin,
			maxCallbackIntervalMs: this._diagCallbackIntervalMax,
			scheduleAheadMs: ringStats?.queueLengthMs ?? 0,
			lastDriftMs: this._diagLastDrift * 1000,
			maxDriftMs: this._diagMaxDrift * 1000,
			gapRepairs: this._diagGapRepairs,
			underruns: this._diagUnderruns,
			totalSilenceMs: ringStats?.totalSilenceInsertedMs ?? this.totalSilenceMs,
			totalScheduled: this.totalScheduled,
			decodeErrors: this._diagDecodeErrors,
			ptsJumps: this._diagPtsJumps,
			ptsJumpMaxMs: this._diagPtsJumpMaxUs / 1000,
			inputPtsJumps: this._diagInputPtsJumps,
			inputPtsWraps: this._diagInputPtsWraps,
			lastInputPTS: this._diagLastInputPTS,
			lastOutputPTS: this.ringBuffer?.readPTS() ?? this._diagLastPTS,
			contextState: this.context?.state ?? "closed",
			contextSampleRate: this.context?.sampleRate ?? 0,
			contextCurrentTime: this.context?.currentTime ?? 0,
			contextBaseLatency: (this.context as AudioContext & { baseLatency?: number })?.baseLatency ?? 0,
			contextOutputLatency: (this.context as AudioContext & { outputLatency?: number })?.outputLatency ?? 0,
			isPlaying: this.playing,
			pendingFrames: 0,
		};
	}

	resetDiagnostics(): void {
		this._diagCallbackCount = 0;
		this._diagDecodeErrors = 0;
		this._diagLastCallbackTime = 0;
		this._diagCallbackIntervalSum = 0;
		this._diagCallbackIntervalMax = 0;
		this._diagCallbackIntervalMin = Infinity;
		this._diagGapRepairs = 0;
		this._diagLastDrift = 0;
		this._diagMaxDrift = 0;
		this._diagUnderruns = 0;
		this._diagFirstCallbackTime = 0;
		this._diagLastPTS = -1;
		this._diagPtsJumps = 0;
		this._diagPtsJumpMaxUs = 0;
		this._diagLastInputPTS = -1;
		this._diagInputPtsJumps = 0;
		this._diagInputPtsWraps = 0;
		this._ptsEpochReset = false;
		this._ptsEpochResetTargetPTS = -1;
	}

	reset(): void {
		if (this.decoder) {
			try { this.decoder.close(); } catch { /* ignore */ }
			this.decoder = null;
		}
		if (this.workletNode) {
			this.workletNode.disconnect();
			this.workletNode = null;
		}
		if (this.ringBuffer) {
			this.ringBuffer.destroy();
			this.ringBuffer = null;
		}
		if (this.gainNode) {
			this.gainNode.disconnect();
			this.gainNode = null;
		}
		if (this.context && this.ownsContext) {
			this.context.close();
		}
		this.context = null;
		this.ownsContext = false;
		this.playing = false;
		this.starting = false;
		this.firstPTS = -1;
		this._ptsEpochReset = false;
		this._ptsEpochResetTargetPTS = -1;
		this.totalScheduled = 0;
		this.totalSilenceMs = 0;
		this.samplesWritten = 0;
		this.numChannels = 0;
		this._peakHold = [];
		this._peakHoldTime = [];
		this._smoothedPeak = [];
		this._suspended = false;
		this._resampleExtractBuf = null;
		this._resampleOutBufs = [];
		this.resetDiagnostics();
	}

	private onDecodedAudio(audioData: AudioData): void {
		if (!this.context || !this.ringBuffer) {
			audioData.close();
			return;
		}

		const numFrames = audioData.numberOfFrames;
		const durationSec = numFrames / audioData.sampleRate;
		const pts = audioData.timestamp;

		const cbNow = performance.now();
		this._diagCallbackCount++;
		if (this._diagFirstCallbackTime === 0) this._diagFirstCallbackTime = cbNow;
		if (this._diagLastCallbackTime > 0) {
			const interval = cbNow - this._diagLastCallbackTime;
			this._diagCallbackIntervalSum += interval;
			if (interval > this._diagCallbackIntervalMax) this._diagCallbackIntervalMax = interval;
			if (interval < this._diagCallbackIntervalMin) this._diagCallbackIntervalMin = interval;
		}
		this._diagLastCallbackTime = cbNow;

		if (this._diagLastPTS >= 0) {
			const expectedDurationUs = durationSec * 1_000_000;
			const gap = Math.abs(pts - this._diagLastPTS - expectedDurationUs);
			if (gap > expectedDurationUs * 0.5) {
				this._diagPtsJumps++;
				if (gap > this._diagPtsJumpMaxUs) this._diagPtsJumpMaxUs = gap;
			}
		}
		this._diagLastPTS = pts;

		if (this.firstPTS < 0) {
			this.firstPTS = pts;
		}

		if (this._ptsEpochReset) {
			// Only consume the reset when the decoded frame matches the PTS
			// that triggered it. WebCodecs buffers frames internally, so
			// onDecodedAudio may fire for older frames (from the previous
			// PTS epoch) before the discontinuity frame arrives. Consuming
			// the flag on the wrong frame would re-anchor the worklet to
			// the old epoch's PTS, causing permanent A/V desync.
			const isTargetFrame = this._ptsEpochResetTargetPTS < 0 ||
				Math.abs(pts - this._ptsEpochResetTargetPTS) < 100_000; // 100ms tolerance
			if (isTargetFrame) {
				this._ptsEpochReset = false;
				this._ptsEpochResetTargetPTS = -1;
				// Update firstPTS so startPlayback() uses the corrected epoch
				// if it hasn't fired yet (PTS jump arrived during initial buffering).
				this.firstPTS = pts;
				// Clear the ring buffer before re-anchoring. The ring may
				// contain samples from the OLD PTS epoch whose timeline
				// doesn't match the new epoch. Using -bufferedSamples with
				// mixed-epoch content causes PTS to be offset by the gap
				// between epochs (typically seconds). Clearing also prevents
				// the worklet's hard-flush from racing with this set-pts
				// message — an empty ring means no flush, so the worklet
				// processes set-pts cleanly.
				if (this.workletNode && this.ringBuffer) {
					this.ringBuffer.clear();
					this.workletNode.port.postMessage({
						type: "set-pts",
						pts: pts,
						sampleOffset: 0,
					});
				}
			}
		}

		const contextRate = this.context.sampleRate;
		const sourceRate = audioData.sampleRate;

		let written: number;
		if (sourceRate !== contextRate && sourceRate > 0 && contextRate > 0) {
			// Source sample rate differs from AudioContext rate (e.g. 44100Hz
			// source played through 48kHz context). Resample via linear
			// interpolation before writing to the ring buffer.
			written = this.writeResampled(audioData, sourceRate, contextRate);
		} else {
			written = this.ringBuffer.write(audioData);
		}
		audioData.close();

		if (written > 0) {
			this.samplesWritten += written;
			this.totalScheduled++;
		}

		if (!this.playing && !this.starting) {
			const bufferedMs = this.ringBuffer.getStats().queueLengthMs;
			if (bufferedMs >= MIN_BUFFER_MS) {
				this.startPlayback();
			}
		}
	}

	/**
	 * Extract, resample, and write audio data when source rate differs from
	 * context rate. Uses linear interpolation which is sufficient for the
	 * common 44100→48000 upsampling case.
	 */
	private writeResampled(audioData: AudioData, srcRate: number, dstRate: number): number {
		if (!this.ringBuffer) return 0;

		const srcFrames = audioData.numberOfFrames;
		const outCh = this.numChannels;
		const srcCh = audioData.numberOfChannels;
		const dstFrames = Math.ceil(srcFrames * dstRate / srcRate);

		// Ensure extract buffer is large enough for source frames.
		if (!this._resampleExtractBuf || this._resampleExtractBuf.length < srcFrames) {
			this._resampleExtractBuf = new Float32Array(srcFrames);
		}

		// Ensure output buffers exist for each output channel (grow incrementally).
		while (this._resampleOutBufs.length < outCh) {
			this._resampleOutBufs.push(null as unknown as Float32Array);
		}
		for (let c = 0; c < outCh; c++) {
			if (!this._resampleOutBufs[c] || this._resampleOutBufs[c].length < dstFrames) {
				this._resampleOutBufs[c] = new Float32Array(dstFrames);
			}
		}

		const ratio = srcRate / dstRate;

		for (let c = 0; c < outCh; c++) {
			// Extract from source channel, upmixing mono by duplicating channel 0.
			const planeIndex = c < srcCh ? c : 0;
			audioData.copyTo(this._resampleExtractBuf, { planeIndex, format: "f32-planar" });
			const src = this._resampleExtractBuf;
			const dst = this._resampleOutBufs[c];

			for (let i = 0; i < dstFrames; i++) {
				const srcPos = i * ratio;
				const idx = Math.floor(srcPos);
				const frac = srcPos - idx;

				if (idx + 1 < srcFrames) {
					dst[i] = src[idx] * (1 - frac) + src[idx + 1] * frac;
				} else {
					dst[i] = src[Math.min(idx, srcFrames - 1)];
				}
			}
		}

		return this.ringBuffer.writeBuffers(this._resampleOutBufs, dstFrames);
	}

	/** Resume the AudioContext. Must be called from a user gesture handler. */
	async resumeContext(): Promise<void> {
		if (this.context && this.context.state === "suspended") {
			// Flush stale audio accumulated during autoplay suspension.
			// When the AudioContext is suspended, the ring buffer fills up
			// (worklet isn't consuming) and eventually overflows. _diagLastPTS
			// tracks decoded frames even when ring writes fail, so it diverges
			// from the ring buffer's actual content. Using _diagLastPTS with
			// -bufferedSamples to re-anchor PTS creates a permanent A/V sync
			// offset equal to the overflow duration (typically 800-1100ms).
			//
			// Fix: clear the stale ring buffer, set an approximate PTS for
			// the brief transition, and re-anchor precisely from the next
			// decoded frame which represents current server time.
			if (this.ringBuffer) {
				this.ringBuffer.clear();
			}
			if (this.workletNode && this.playing) {
				// Set approximate PTS so the renderer doesn't see stale values
				// during the brief gap before the epoch reset fires.
				const approxPTS = this._diagLastInputPTS >= 0 ? this._diagLastInputPTS :
					(this._diagLastPTS >= 0 ? this._diagLastPTS : this.firstPTS);
				this.workletNode.port.postMessage({
					type: "set-pts",
					pts: approxPTS,
					sampleOffset: 0,
				});
			}
			// Do NOT set _ptsEpochReset here. After resume, the WebCodecs
			// AudioDecoder queue bursts all decoded frames accumulated during
			// suspension. These flood the ring buffer, triggering hard flushes
			// that accumulate sampleOffset (each flush adds skipSamples to
			// sampleOffset, advancing PTS by seconds). An epoch reset with
			// targetPTS=-1 fires mid-burst on a stale frame, but the continued
			// burst still causes hard-flush accumulation afterward.
			//
			// If there's already a pending epoch reset from a PTS jump in
			// decode() (e.g., temp-counter → SeedPTSFromVideo), let it fire
			// on its correct target frame — don't override its targetPTS.
			// The approximate set-pts above is sufficient (~18ms accuracy).
			await this.context.resume();
		}
	}

	private startPlayback(): void {
		if (!this.context || this.playing || this.starting || !this.workletNode || !this.ringBuffer) return;

		if (this._suspended) {
			return;
		}

		// Don't attempt to resume a suspended context here — it will fail
		// without a user gesture (Chrome autoplay policy). The context will
		// be resumed by the gesture handler in +page.svelte, which calls
		// resumeContext(). Once resumed, the worklet will start pulling
		// samples from the ring buffer automatically.
		if (this.context.state === "suspended") {
			// Buffer data so playback starts immediately when context resumes.
			this.ringBuffer.play();
			this.playing = true;
			return;
		}

		this.starting = true;

		if (this.workletNode) {
			this.workletNode.port.postMessage({
				type: "set-pts",
				pts: this.firstPTS,
				sampleOffset: 0,
			});
		}

		this.ringBuffer.play();

		this.context.resume().then(() => {
			this.playing = true;
			this.starting = false;
		});
	}
}
