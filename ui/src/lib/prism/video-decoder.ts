import { CompressedFrameQueue } from "./compressed-frame-queue";
import { VideoRenderBuffer } from "./video-render-buffer";

/** Diagnostic counters and timing statistics from the video decoder worker. */
export interface VideoDecoderDiagnostics {
	inputCount: number;
	outputCount: number;
	keyframeCount: number;
	decodeErrors: number;
	discardedDelta: number;
	discardedBufferFull: number;
	decodeQueueSize: number;
	avgInputIntervalMs: number;
	minInputIntervalMs: number;
	maxInputIntervalMs: number;
	avgOutputIntervalMs: number;
	minOutputIntervalMs: number;
	maxOutputIntervalMs: number;
	inputFps: number;
	outputFps: number;
	ptsJumps: number;
	bufferDropped: number;
	// Lifetime counters (survive reconfigures)
	lifetimeInputCount: number;
	lifetimeOutputCount: number;
	lifetimeKeyframeCount: number;
	lifetimeDecodeErrors: number;
	lifetimeDiscardedDelta: number;
	lifetimeDiscardedBufferFull: number;
	lifetimeConfigureCount: number;
	lifetimeConfigGuardDrops: number;
}

/**
 * Manages a Web Worker that runs the WebCodecs VideoDecoder. Compressed
 * frames are posted to the worker, and decoded VideoFrames are transferred
 * back and inserted into a VideoRenderBuffer for the renderer to consume.
 * The worker isolation prevents decode stalls from blocking the main thread.
 */
export class PrismVideoDecoder {
	private worker: Worker | null = null;
	private renderBuffer: VideoRenderBuffer;
	private onFrameReceived: ((frame: VideoFrame) => void) | null;
	private configured = false;
	private _lastDiag: VideoDecoderDiagnostics | null = null;
	private _diagResolve: ((d: VideoDecoderDiagnostics) => void) | null = null;
	private _bufferDropped = 0;
	private _paused = false;
	private compressedQueue = new CompressedFrameQueue(5_000_000); // 5s max
	private audioClock: { getPlaybackPTS(): number } | null = null;
	private _bootstrapCount = 0;
	private _lastDescription: Uint8Array | undefined;
	private _lastPumpAudioPTS = -1;
	private _audioStallStartMs = 0;

	constructor(renderBuffer: VideoRenderBuffer, onFrameReceived?: (frame: VideoFrame) => void) {
		this.renderBuffer = renderBuffer;
		this.onFrameReceived = onFrameReceived ?? null;
	}

	preload(): void {
		if (this.worker) return;
		this.worker = new Worker(
			new URL("./video-decoder-worker.ts", import.meta.url),
			{ type: "module" },
		);
		this.worker.onmessage = (e) => this.handleWorkerMessage(e);
	}

	setAudioClock(clock: { getPlaybackPTS(): number }): void {
		this.audioClock = clock;
	}

	configure(codec: string, width: number, height: number, description?: ArrayBuffer): void {
		// Reuse the existing worker on reconfigure — terminating the worker
		// kills all queued frames, causing massive frame loss on the program
		// stream during transitions (SPS/PPS changes every transition boundary).
		// The worker handles "configure" internally by closing the old decoder
		// and creating a new one.
		if (!this.worker) {
			this.preload();
		}

		// Don't clear render buffer on reconfigure — old frames are still
		// displayable until new frames arrive. PTS is monotonic on the
		// program stream (server uses tsOffset), so binary search ordering
		// is preserved. Clearing would discard the last displayable frames,
		// causing visible stutter while waiting for the next keyframe.

		this.worker!.postMessage({
			type: "configure",
			codec,
			width,
			height,
			description: description ?? null,
		}, description ? [description] : []);

		this.configured = true;

		if (description) {
			this._lastDescription = new Uint8Array(description);
		}
	}

	pause(): void {
		this._paused = true;
	}

	resume(): void {
		this._paused = false;
		// Tell worker to wait for next keyframe
		if (this.worker) {
			this.worker.postMessage({ type: "flush" });
		}
		// Clear stale frames from all buffers
		this.renderBuffer.clear();
		this.compressedQueue.flush();
		this._bootstrapCount = 0;
		this._lastPumpAudioPTS = -1;
		this._audioStallStartMs = 0;
	}

	decode(data: Uint8Array, isKeyframe: boolean, timestamp: number, isDisco: boolean): void {
		if (!this.worker || !this.configured || this._paused) return;

		this.compressedQueue.push(
			new Uint8Array(data),  // copy — caller may transfer original
			timestamp,
			isKeyframe,
			isKeyframe ? this._lastDescription : undefined,
		);

		// Pump immediately to minimize latency
		this.pumpDecode();
	}

	/** Release compressed frames to the VideoDecoder worker based on audio clock position. */
	pumpDecode(): void {
		if (!this.worker || !this.configured) return;

		const LOOKAHEAD_US = 200_000; // 200ms decode lead time
		const BOOTSTRAP_FRAMES = 3;   // frames to decode before audio starts

		const audioPTS = this.audioClock?.getPlaybackPTS() ?? -1;

		if (audioPTS < 0) {
			// No audio yet — bootstrap: decode a few frames so renderer shows something
			if (this._bootstrapCount >= BOOTSTRAP_FRAMES) return;

			const available = this.compressedQueue.size();
			if (available === 0) return;

			// Drain up to remaining bootstrap count
			const toDrain = Math.min(available, BOOTSTRAP_FRAMES - this._bootstrapCount);
			const frames = this.compressedQueue.drain(Infinity, 0);
			const toSend = frames.slice(0, toDrain);
			// Re-push extras back to queue (in reverse to preserve order)
			for (let i = frames.length - 1; i >= toDrain; i--) {
				const f = frames[i];
				this.compressedQueue.push(f.data, f.timestamp, f.isKeyframe, f.description);
			}
			for (const f of toSend) {
				this.sendToWorker(f.data, f.isKeyframe, f.timestamp);
			}
			this._bootstrapCount += toSend.length;
			return;
		}

		// Audio stall detection: if audio hasn't advanced for 500ms, drain
		// one frame per pump tick to prevent compressed queue from growing
		// unbounded. The renderer handles wall-clock pacing for display.
		if (audioPTS === this._lastPumpAudioPTS && this._lastPumpAudioPTS >= 0) {
			if (this._audioStallStartMs === 0) {
				this._audioStallStartMs = performance.now();
			}
			if (performance.now() - this._audioStallStartMs > 500 && this.compressedQueue.size() > 0) {
				// Stalled — drain one frame at a time (wall-clock pacing)
				const staleFrames = this.compressedQueue.drain(this.compressedQueue.oldestPTS(), 0);
				for (const f of staleFrames) {
					this.sendToWorker(f.data, f.isKeyframe, f.timestamp);
				}
				return;
			}
		} else {
			this._audioStallStartMs = 0;
			this._lastPumpAudioPTS = audioPTS;
		}

		// Audio is active — drain frames up to audioPTS + lookahead
		this._bootstrapCount = BOOTSTRAP_FRAMES; // mark bootstrap done
		let frames = this.compressedQueue.drain(audioPTS, LOOKAHEAD_US);
		for (const f of frames) {
			this.sendToWorker(f.data, f.isKeyframe, f.timestamp);
		}

		// PTS discontinuity: if oldest remaining frame is far ahead of audio,
		// a source cut happened. Drain everything so the renderer can re-anchor.
		if (frames.length === 0 && this.compressedQueue.size() > 0) {
			const oldest = this.compressedQueue.oldestPTS();
			if (oldest - audioPTS > LOOKAHEAD_US + 500_000) {
				frames = this.compressedQueue.drain(Infinity, 0);
				for (const f of frames) {
					this.sendToWorker(f.data, f.isKeyframe, f.timestamp);
				}
			}
		}
	}

	private sendToWorker(data: Uint8Array, isKeyframe: boolean, timestamp: number): void {
		if (!this.worker) return;
		this.worker.postMessage(
			{
				type: "decode",
				data: data.buffer,
				isKeyframe,
				timestamp,
				isDisco: false,
			},
			[data.buffer],
		);
	}

	reset(): void {
		if (this.worker) {
			this.worker.postMessage({ type: "stop" });
			this.worker.terminate();
			this.worker = null;
		}
		this.renderBuffer.clear();
		this.compressedQueue.flush();
		this._bootstrapCount = 0;
		this._lastPumpAudioPTS = -1;
		this._audioStallStartMs = 0;
		this.configured = false;
	}

	async getDiagnostics(): Promise<VideoDecoderDiagnostics> {
		if (!this.worker) {
			return this.emptyDiag();
		}
		return new Promise<VideoDecoderDiagnostics>((resolve) => {
			this._diagResolve = resolve;
			this.worker!.postMessage({ type: "getDiagnostics" });
			setTimeout(() => {
				if (this._diagResolve) {
					this._diagResolve(this._lastDiag ?? this.emptyDiag());
					this._diagResolve = null;
				}
			}, 200);
		});
	}

	private emptyDiag(): VideoDecoderDiagnostics {
		return {
			inputCount: 0, outputCount: 0, keyframeCount: 0, decodeErrors: 0,
			discardedDelta: 0, discardedBufferFull: 0, decodeQueueSize: 0,
			avgInputIntervalMs: 0, minInputIntervalMs: 0, maxInputIntervalMs: 0,
			avgOutputIntervalMs: 0, minOutputIntervalMs: 0, maxOutputIntervalMs: 0,
			inputFps: 0, outputFps: 0, ptsJumps: 0, bufferDropped: 0,
			lifetimeInputCount: 0, lifetimeOutputCount: 0, lifetimeKeyframeCount: 0,
			lifetimeDecodeErrors: 0, lifetimeDiscardedDelta: 0, lifetimeDiscardedBufferFull: 0,
			lifetimeConfigureCount: 0, lifetimeConfigGuardDrops: 0,
		};
	}

	private handleWorkerMessage(e: MessageEvent): void {
		const msg = e.data;

		if (msg.type === "frame") {
			const frame: VideoFrame = msg.frame;
			if (this.onFrameReceived) {
				this.onFrameReceived(frame);
			}
			this.renderBuffer.addFrame(frame);
		} else if (msg.type === "diagnostics") {
			const d: VideoDecoderDiagnostics = { ...msg.data, bufferDropped: this._bufferDropped };
			this._lastDiag = d;
			if (this._diagResolve) {
				this._diagResolve(d);
				this._diagResolve = null;
			}
		} else if (msg.type === "error") {
			console.error("[VideoDecoder] worker error:", msg.message);
		} else if (msg.type === "warning") {
			console.warn("[VideoDecoder]", msg.message);
		}
	}
}
