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
	}

	decode(data: Uint8Array, isKeyframe: boolean, timestamp: number, isDisco: boolean): void {
		if (!this.worker || !this.configured || this._paused) return;

		this.worker.postMessage(
			{
				type: "decode",
				data: data.buffer,
				isKeyframe,
				timestamp,
				isDisco,
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
