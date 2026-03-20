import { VideoRenderBuffer } from "./video-render-buffer";

const LIVE_EDGE_TARGET_DEPTH = 10;
const RAF_THROTTLE_THRESHOLD_MS = 50;
const RAF_THROTTLE_COUNT = 3; // consecutive slow frames before switching
const RAF_NORMAL_COUNT = 5; // consecutive normal frames before switching back

/** Provides the current audio playback PTS for A/V sync. Returns -1 when audio is unavailable. */
interface AudioClock {
	getPlaybackPTS(): number;
}

/** Lightweight per-frame stats emitted on each render tick for the HUD and metrics store. */
export interface RendererStats {
	currentVideoPTS: number;
	currentAudioPTS: number;
	videoQueueSize: number;
	videoQueueLengthMs: number;
	videoTotalDiscarded: number;
	liveEdgeSkips: number;
}

/** Comprehensive renderer diagnostics for perf snapshots, covering rAF timing, draw cost, A/V sync, and buffer state. */
export interface RendererDiagnostics {
	rafCount: number;
	framesDrawn: number;
	framesSkipped: number;
	avgRafIntervalMs: number;
	minRafIntervalMs: number;
	maxRafIntervalMs: number;
	avgDrawMs: number;
	maxDrawMs: number;
	avgFrameIntervalMs: number;
	minFrameIntervalMs: number;
	maxFrameIntervalMs: number;
	avSyncMs: number;
	avSyncMin: number;
	avSyncMax: number;
	avSyncAvg: number;
	clockMode: string;
	liveEdgeSkips: number;
	emptyBufferHits: number;
	currentVideoPTS: number;
	currentAudioPTS: number;
	videoQueueSize: number;
	videoQueueMs: number;
	videoTotalDiscarded: number;
	useSetTimeoutFallback: boolean;
}

/**
 * Drives the video render loop using requestAnimationFrame. Pulls decoded
 * VideoFrames from a VideoRenderBuffer and draws them to a canvas, paced
 * either by an audio clock (for A/V sync) or by a wall-clock free-run
 * mode when audio is unavailable. Collects timing diagnostics for the
 * perf overlay.
 */
export class PrismRenderer {
	private canvas: HTMLCanvasElement;
	private ctx: CanvasRenderingContext2D;
	private animationId: number | null = null;

	private videoBuffer: VideoRenderBuffer;
	private audioClock: AudioClock;

	private currentVideoPTS = -1;
	private currentAudioPTS = -1;
	private lastDrawnFrame: VideoFrame | null = null;
	private onStats: ((stats: RendererStats) => void) | null = null;
	private freeRunStart = -1;
	private freeRunBasePTS = -1;
	/** @deprecated freeRunOnly is no longer used; audioClock returns -1 dynamically when unavailable */
	private _freeRunOnly = false;
	private _maxResolution = 0;
	private _externallyDriven = false;
	private lastStatsTime = 0;

	private lastAudioAdvanceTime = 0;
	private audioStallFreeRunStart = -1;
	private audioStallFreeRunBasePTS = -1;

	// --- diagnostics ---
	private _diagRafCount = 0;
	private _diagFramesDrawn = 0;
	private _diagFramesSkipped = 0;
	private _diagLastRafTime = 0;
	private _diagRafIntervalSum = 0;
	private _diagRafIntervalMax = 0;
	private _diagRafIntervalMin = Infinity;
	private _diagDrawTimeSum = 0;
	private _diagDrawTimeMax = 0;
	private _diagLastFrameDrawTime = 0;
	private _diagFrameIntervalSum = 0;
	private _diagFrameIntervalMax = 0;
	private _diagFrameIntervalMin = Infinity;
	private _diagAvSyncSum = 0;
	private _diagAvSyncCount = 0;
	private _diagAvSyncMin = Infinity;
	private _diagAvSyncMax = -Infinity;
	private _diagLastAvSync = 0;
	private _diagEmptyBufferHits = 0;
	private _diagLiveEdgeSkips = 0;

	// --- A/V sync feedback loop ---
	// Tracks the running average of video-ahead offset (μs). Subtracted
	// from targetPTS to gradually eliminate measured A/V desync. Converges
	// in ~1 second. Capped at ±200ms to prevent runaway.
	private _avSyncCorrectionUs = 0;

	// --- rAF throttle detection ---
	private _slowRafCount = 0;
	private _normalRafCount = 0;
	private _useSetTimeoutFallback = false;
	private _timeoutId: ReturnType<typeof setTimeout> | null = null;

	constructor(
		canvas: HTMLCanvasElement,
		videoBuffer: VideoRenderBuffer,
		audioClock: AudioClock,
		onStats?: (stats: RendererStats) => void,
	) {
		this.canvas = canvas;
		this.ctx = canvas.getContext("2d")!;
		this.videoBuffer = videoBuffer;
		this.audioClock = audioClock;
		this.onStats = onStats ?? null;
	}

	set freeRunOnly(v: boolean) {
		this._freeRunOnly = v;
	}

	set maxResolution(v: number) {
		this._maxResolution = v;
	}

	set externallyDriven(v: boolean) {
		this._externallyDriven = v;
	}

	getVideoBuffer(): VideoRenderBuffer {
		return this.videoBuffer;
	}

	start(): void {
		if (this._externallyDriven) return;
		if (this.animationId !== null || this._timeoutId !== null) return;
		this.scheduleNextTick();
	}

	renderOnce(): void {
		const now = performance.now();
		this.renderTick(now);
	}

	private scheduleNextTick(): void {
		if (this._useSetTimeoutFallback) {
			this._timeoutId = setTimeout(this.fallbackLoop, 16);
		} else {
			this.animationId = requestAnimationFrame(this.renderLoop);
		}
	}

	private renderLoop = (): void => {
		this.animationId = null;
		this.renderTick(performance.now());
		this.scheduleNextTick();
	};

	private fallbackLoop = (): void => {
		this._timeoutId = null;
		this.renderTick(performance.now());
		this.scheduleNextTick();
	};

	private renderTick(now: number): void {
		this._diagRafCount++;
		if (this._diagLastRafTime > 0) {
			const interval = now - this._diagLastRafTime;
			this._diagRafIntervalSum += interval;
			if (interval > this._diagRafIntervalMax) this._diagRafIntervalMax = interval;
			if (interval < this._diagRafIntervalMin) this._diagRafIntervalMin = interval;

			// Detect rAF throttling and switch render strategy
			if (interval > RAF_THROTTLE_THRESHOLD_MS) {
				this._slowRafCount++;
				this._normalRafCount = 0;
				if (!this._useSetTimeoutFallback && this._slowRafCount >= RAF_THROTTLE_COUNT) {
					this._useSetTimeoutFallback = true;
				}
			} else {
				this._normalRafCount++;
				this._slowRafCount = 0;
				if (this._useSetTimeoutFallback && this._normalRafCount >= RAF_NORMAL_COUNT) {
					this._useSetTimeoutFallback = false;
				}
			}
		}
		this._diagLastRafTime = now;

		// Live-edge enforcement: if buffer has accumulated beyond target
		// depth, skip directly to the newest frame. This prevents latency
		// buildup from encode jitter, network batching, or decode stalls.
		const preSkipStats = this.videoBuffer.getStats();
		if (preSkipStats.queueSize > LIVE_EDGE_TARGET_DEPTH) {
			const skipResult = this.videoBuffer.takeNewestFrame();
			if (skipResult.frame) {
				this._diagLiveEdgeSkips++;
				if (this.lastDrawnFrame) {
					this.lastDrawnFrame.close();
				}
				this.lastDrawnFrame = skipResult.frame;

				const t0 = performance.now();
				this.drawFrame(skipResult.frame);
				const drawMs = performance.now() - t0;
				this._diagDrawTimeSum += drawMs;
				if (drawMs > this._diagDrawTimeMax) this._diagDrawTimeMax = drawMs;

				this._diagFramesDrawn++;
				if (this._diagLastFrameDrawTime > 0) {
					const fInterval = now - this._diagLastFrameDrawTime;
					this._diagFrameIntervalSum += fInterval;
					if (fInterval > this._diagFrameIntervalMax) this._diagFrameIntervalMax = fInterval;
					if (fInterval < this._diagFrameIntervalMin) this._diagFrameIntervalMin = fInterval;
				}
				this._diagLastFrameDrawTime = now;

				this.currentVideoPTS = skipResult.frame.timestamp;

				// Re-anchor any freerun clocks to the new PTS
				if (this.freeRunStart >= 0) {
					this.freeRunStart = now;
					this.freeRunBasePTS = skipResult.frame.timestamp;
				}
				if (this.audioStallFreeRunStart >= 0) {
					this.audioStallFreeRunStart = now;
					this.audioStallFreeRunBasePTS = skipResult.frame.timestamp;
				}

				this.reportStats(now);
				return;
			}
		}

		let targetPTS: number;
		// Always check the audio clock — it returns -1 dynamically when
		// unavailable (audioDecoder not yet configured, not playing, etc.).
		// This allows the renderer to transition from freerun to audio-driven
		// mode once audio becomes available, rather than being locked out.
		const audioPTS = this.audioClock.getPlaybackPTS();

		const AUDIO_STALE_MS = 200;

		if (audioPTS >= 0) {
			if (this.currentAudioPTS >= 0 && this.currentVideoPTS >= 0 &&
				this.currentAudioPTS - audioPTS > 30_000_000) {
				this.videoBuffer.clear();
				this.currentVideoPTS = -1;
			}

			const audioAdvanced = this.currentAudioPTS < 0 || audioPTS !== this.currentAudioPTS;

			// Detect A/V PTS divergence: video buffer frames are far ahead
			// of audio PTS. Happens after source transitions where the mixer
			// has an audio gap (e.g., clip→live), creating permanent desync.
			// Threshold: 3 seconds (3,000,000 μs).
			const peekDiverge = this.videoBuffer.peekFirstFrame();
			const avDiverged = peekDiverge != null && audioPTS >= 0 &&
				peekDiverge.timestamp - audioPTS > 3_000_000 &&
				this.videoBuffer.getStats().queueSize > 10;

			// Only reset audio-stall tracking when audio advances AND
			// A/V PTS are reasonably aligned. During divergence, keep
			// the wall-clock pacing stable.
			if (audioAdvanced && !avDiverged) {
				this.lastAudioAdvanceTime = now;
				this.audioStallFreeRunStart = -1;
				this.audioStallFreeRunBasePTS = -1;
			}
			this.currentAudioPTS = audioPTS;
			this.freeRunStart = -1;
			this.freeRunBasePTS = -1;

			const audioStale = this.lastAudioAdvanceTime > 0 &&
				(now - this.lastAudioAdvanceTime) > AUDIO_STALE_MS;

			if ((audioStale || avDiverged) && this.videoBuffer.getStats().queueSize > 0) {
				// Audio clock has stalled — pace video using wall clock
				// anchored from when the stall was first detected.
				if (this.audioStallFreeRunStart < 0) {
					this.audioStallFreeRunStart = now;
					this.audioStallFreeRunBasePTS = this.currentVideoPTS >= 0
						? this.currentVideoPTS
						: (this.videoBuffer.peekFirstFrame()?.timestamp ?? -1);
				}
				if (this.audioStallFreeRunBasePTS >= 0) {
					targetPTS = this.audioStallFreeRunBasePTS +
						(now - this.audioStallFreeRunStart) * 1000;
				} else {
					targetPTS = -1;
				}
			} else {
				const avDelta = this.currentVideoPTS >= 0
					? Math.abs(audioPTS - this.currentVideoPTS)
					: 0;

				if (avDelta > 30_000_000 || (this.currentVideoPTS >= 0 && audioPTS - this.currentVideoPTS > 150_000)) {
					// A/V desync: audio clock is too far from video. Switch to
					// wall-clock free-run pacing anchored to the video buffer
					// instead of blind takeNextFrame (which causes frame bursts).
					const peek = this.videoBuffer.peekFirstFrame();
					if (peek) {
						if (this.audioStallFreeRunStart < 0) {
							this.audioStallFreeRunStart = now;
							this.audioStallFreeRunBasePTS = this.currentVideoPTS >= 0
								? this.currentVideoPTS : peek.timestamp;
						}
						targetPTS = this.audioStallFreeRunBasePTS +
							(now - this.audioStallFreeRunStart) * 1000;
					} else {
						targetPTS = -1;
					}
				} else {
					targetPTS = audioPTS - this._avSyncCorrectionUs;
				}
			}
		} else {
			const firstFrame = this.videoBuffer.peekFirstFrame();
			if (!firstFrame) {
				this._diagEmptyBufferHits++;
				// Reset free-run clock so the next burst of frames starts fresh
				// rather than being treated as stale relative to the old clock.
				this.freeRunStart = -1;
				this.freeRunBasePTS = -1;
				this.reportStats(now);
				return;
			}
			if (this.freeRunStart < 0) {
				this.freeRunStart = now;
				this.freeRunBasePTS = firstFrame.timestamp;
			}
			targetPTS = this.freeRunBasePTS + (now - this.freeRunStart) * 1000;
		}

		let frame: VideoFrame | null = null;

		if (targetPTS < 0) {
			// No valid clock reference. Use wall-clock pacing anchored to the
			// first available frame so we don't burn through bursts unpaced.
			const peek = this.videoBuffer.peekFirstFrame();
			if (peek) {
				if (this.freeRunStart < 0) {
					this.freeRunStart = now;
					this.freeRunBasePTS = peek.timestamp;
				}
				targetPTS = this.freeRunBasePTS + (now - this.freeRunStart) * 1000;
			} else {
				this._diagEmptyBufferHits++;
				this.reportStats(now);
				return;
			}
		}

		if (targetPTS >= 0) {
			const result = this.videoBuffer.getFrameByTimestamp(targetPTS);
			frame = result.frame;

			// PTS discontinuity detection for freerun mode: if binary search
			// found nothing but the buffer has frames, all frames are "in the
			// future" — reset the freerun clock to catch up immediately.
			if (!frame && this.freeRunStart >= 0) {
				const peek = this.videoBuffer.peekFirstFrame();
				if (peek && Math.abs(peek.timestamp - targetPTS) > 500_000) {
					this.freeRunStart = now;
					this.freeRunBasePTS = peek.timestamp;
					targetPTS = peek.timestamp;
					const retry = this.videoBuffer.getFrameByTimestamp(targetPTS);
					frame = retry.frame;
				}
			}

			// Look-ahead + PTS discontinuity recovery:
			// 1. If next frame is within 150ms of audio → display (normal jitter)
			// 2. If next frame is >500ms from target → PTS discontinuity (source
			//    cut), re-anchor the clock to recover instead of freezing
			if (!frame) {
				const peek = this.videoBuffer.peekFirstFrame();
				if (peek && peek.timestamp > targetPTS) {
					const gap = peek.timestamp - targetPTS;
					if (gap < 150_000) {
						// Normal jitter — display the slightly-ahead frame
						frame = this.videoBuffer.takeNextFrame();
					} else if (gap > 500_000) {
						// PTS discontinuity (source cut) — re-anchor clock to
						// the new PTS timeline and display the frame.
						if (this.audioStallFreeRunStart >= 0) {
							this.audioStallFreeRunStart = now;
							this.audioStallFreeRunBasePTS = peek.timestamp;
						}
						if (this.freeRunStart >= 0) {
							this.freeRunStart = now;
							this.freeRunBasePTS = peek.timestamp;
						}
						frame = this.videoBuffer.takeNextFrame();
					}
				}
			}
		}

		if (frame) {
			// If the drawn frame's PTS is far from targetPTS, re-anchor the
			// freerun clock so subsequent frames don't drift or freeze.
			if (this.freeRunStart >= 0 && Math.abs(frame.timestamp - targetPTS) > 500_000) {
				this.freeRunStart = now;
				this.freeRunBasePTS = frame.timestamp;
			}

			if (this.lastDrawnFrame) {
				this.lastDrawnFrame.close();
			}
			this.lastDrawnFrame = frame;

			const t0 = performance.now();
			this.drawFrame(frame);
			const drawMs = performance.now() - t0;
			this._diagDrawTimeSum += drawMs;
			if (drawMs > this._diagDrawTimeMax) this._diagDrawTimeMax = drawMs;

			this._diagFramesDrawn++;
			if (this._diagLastFrameDrawTime > 0) {
				const fInterval = now - this._diagLastFrameDrawTime;
				this._diagFrameIntervalSum += fInterval;
				if (fInterval > this._diagFrameIntervalMax) this._diagFrameIntervalMax = fInterval;
				if (fInterval < this._diagFrameIntervalMin) this._diagFrameIntervalMin = fInterval;
			}
			this._diagLastFrameDrawTime = now;

			this.currentVideoPTS = frame.timestamp;

			if (this.currentAudioPTS >= 0 && this.currentVideoPTS >= 0) {
				const delta = Math.abs(this.currentVideoPTS - this.currentAudioPTS);
				if (delta < 30_000_000) {
					const syncMs = (this.currentVideoPTS - this.currentAudioPTS) / 1000;
					this._diagLastAvSync = syncMs;
					this._diagAvSyncSum += syncMs;
					this._diagAvSyncCount++;
					if (syncMs < this._diagAvSyncMin) this._diagAvSyncMin = syncMs;
					if (syncMs > this._diagAvSyncMax) this._diagAvSyncMax = syncMs;

					// A/V sync feedback loop: if video is consistently ahead
					// (positive syncMs), gradually increase the correction to
					// pull targetPTS back. EMA with α=0.03 converges in ~1s.
					const syncUs = syncMs * 1000;
					this._avSyncCorrectionUs = this._avSyncCorrectionUs * 0.97 + syncUs * 0.03;
					// Clamp to ±200ms to prevent runaway
					this._avSyncCorrectionUs = Math.max(-200_000,
						Math.min(200_000, this._avSyncCorrectionUs));
				}
			}
		} else {
			this._diagFramesSkipped++;
		}

		this.reportStats(now);
	}

	// Cached draw rect — used to avoid redundant fillRect calls for
	// letterbox/pillarbox bars when the layout hasn't changed.
	private lastDraw = { cw: 0, ch: 0, dx: 0, dy: 0, dw: 0, dh: 0 };

	/**
	 * Draw a video frame centered within the canvas, maintaining the
	 * video's aspect ratio. The canvas backing store dimensions are
	 * controlled by setupHiDPICanvas (via ResizeObserver), NOT by the
	 * video frame — this prevents the canvas from fighting CSS layout
	 * and ensures consistent centering across resize.
	 */
	private drawFrame(frame: VideoFrame): void {
		const cw = this.canvas.width;
		const ch = this.canvas.height;
		if (cw === 0 || ch === 0) return;

		const fw = frame.displayWidth;
		const fh = frame.displayHeight;
		const videoAspect = fw / fh;
		const canvasAspect = cw / ch;

		let dx: number, dy: number, dw: number, dh: number;
		if (Math.abs(videoAspect - canvasAspect) < 0.01) {
			// Aspect ratios match — fill entire canvas (common case)
			dx = 0; dy = 0; dw = cw; dh = ch;
		} else if (videoAspect > canvasAspect) {
			// Video wider than canvas — fit to width, bars top/bottom
			dw = cw;
			dh = Math.round(cw / videoAspect);
			dx = 0;
			dy = Math.round((ch - dh) / 2);
		} else {
			// Video taller than canvas — fit to height, bars left/right
			dh = ch;
			dw = Math.round(ch * videoAspect);
			dx = Math.round((cw - dw) / 2);
			dy = 0;
		}

		// Clear letterbox/pillarbox bars only when the draw rect changes
		// (canvas resize or video aspect ratio change). The video area
		// itself is overwritten by drawImage each frame.
		const needsBars = dw !== cw || dh !== ch;
		if (needsBars && (
			cw !== this.lastDraw.cw || ch !== this.lastDraw.ch ||
			dx !== this.lastDraw.dx || dy !== this.lastDraw.dy ||
			dw !== this.lastDraw.dw || dh !== this.lastDraw.dh
		)) {
			this.ctx.fillStyle = '#000';
			this.ctx.fillRect(0, 0, cw, ch);
		}
		this.lastDraw = { cw, ch, dx, dy, dw, dh };

		this.ctx.drawImage(frame, 0, 0, fw, fh, dx, dy, dw, dh);
	}

	private reportStats(now: number): void {
		if (!this.onStats) return;
		if (this._externallyDriven && now - this.lastStatsTime < 250) return;
		this.lastStatsTime = now;
		const vStats = this.videoBuffer.getStats();
		this.onStats({
			currentVideoPTS: this.currentVideoPTS,
			currentAudioPTS: this.currentAudioPTS,
			videoQueueSize: vStats.queueSize,
			videoQueueLengthMs: vStats.queueLengthMs,
			videoTotalDiscarded: vStats.totalDiscarded,
			liveEdgeSkips: this._diagLiveEdgeSkips,
		});
	}

	/**
	 * Reset A/V sync tracking state. Call when the underlying source changes
	 * (e.g. program source switch after a transition) so the renderer doesn't
	 * carry stale PTS references from the old source into the new one.
	 */
	resetSync(): void {
		this.currentVideoPTS = -1;
		this.currentAudioPTS = -1;
		this.freeRunStart = -1;
		this.freeRunBasePTS = -1;
		this.lastAudioAdvanceTime = 0;
		this.audioStallFreeRunStart = -1;
		this.audioStallFreeRunBasePTS = -1;
		this._diagAvSyncSum = 0;
		this._diagAvSyncCount = 0;
		this._diagAvSyncMin = Infinity;
		this._diagAvSyncMax = -Infinity;
		this._diagLastAvSync = 0;
		// Reset RAF timing stats so the background gap doesn't pollute them
		this._diagLastRafTime = 0;
		this._diagRafIntervalMax = 0;
		this._diagLastFrameDrawTime = 0;
		this._diagFrameIntervalMax = 0;
	}

	getDiagnostics(): RendererDiagnostics {
		const vStats = this.videoBuffer.getStats();
		return {
			rafCount: this._diagRafCount,
			framesDrawn: this._diagFramesDrawn,
			framesSkipped: this._diagFramesSkipped,
			avgRafIntervalMs: this._diagRafCount > 1 ? this._diagRafIntervalSum / (this._diagRafCount - 1) : 0,
			minRafIntervalMs: this._diagRafIntervalMin === Infinity ? 0 : this._diagRafIntervalMin,
			maxRafIntervalMs: this._diagRafIntervalMax,
			avgDrawMs: this._diagFramesDrawn > 0 ? this._diagDrawTimeSum / this._diagFramesDrawn : 0,
			maxDrawMs: this._diagDrawTimeMax,
			avgFrameIntervalMs: this._diagFramesDrawn > 1 ? this._diagFrameIntervalSum / (this._diagFramesDrawn - 1) : 0,
			minFrameIntervalMs: this._diagFrameIntervalMin === Infinity ? 0 : this._diagFrameIntervalMin,
			maxFrameIntervalMs: this._diagFrameIntervalMax,
			avSyncMs: this._diagLastAvSync,
			avSyncMin: this._diagAvSyncMin === Infinity ? 0 : this._diagAvSyncMin,
			avSyncMax: this._diagAvSyncMax === -Infinity ? 0 : this._diagAvSyncMax,
			avSyncAvg: this._diagAvSyncCount > 0 ? this._diagAvSyncSum / this._diagAvSyncCount : 0,
			clockMode: this.freeRunStart >= 0 ? "freerun"
				: this.audioStallFreeRunStart >= 0 ? "audio-stall-freerun"
				: "audio",
			liveEdgeSkips: this._diagLiveEdgeSkips,
			emptyBufferHits: this._diagEmptyBufferHits,
			currentVideoPTS: this.currentVideoPTS,
			currentAudioPTS: this.currentAudioPTS,
			videoQueueSize: vStats.queueSize,
			videoQueueMs: vStats.queueLengthMs,
			videoTotalDiscarded: vStats.totalDiscarded,
			useSetTimeoutFallback: this._useSetTimeoutFallback,
		};
	}

	destroy(): void {
		if (this.animationId !== null) {
			cancelAnimationFrame(this.animationId);
			this.animationId = null;
		}
		if (this._timeoutId !== null) {
			clearTimeout(this._timeoutId);
			this._timeoutId = null;
		}
		if (this.lastDrawnFrame) {
			this.lastDrawnFrame.close();
			this.lastDrawnFrame = null;
		}
	}
}
