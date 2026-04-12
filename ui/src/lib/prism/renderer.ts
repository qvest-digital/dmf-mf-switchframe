import { VideoRenderBuffer } from "./video-render-buffer";

// Allow up to 9 frames (~300ms at 30fps) before live-edge skip.
// Lower threshold catches up faster on high-RTT connections (5G, etc.)
// where bursty delivery causes frame accumulation. Group-level skip
// in media-pipeline handles keyframe-aware stall recovery.
const LIVE_EDGE_TARGET_DEPTH = 9;
const RAF_THROTTLE_THRESHOLD_MS = 50;
const RAF_THROTTLE_COUNT = 3; // consecutive slow frames before switching
const RAF_NORMAL_COUNT = 5; // consecutive normal frames before switching back

/** Provides the current audio playback PTS for A/V sync. Returns -1 when audio is unavailable. */
export interface AudioClock {
	getPlaybackPTS(): number;
}

/** Sync configuration knobs for A/V sync timing. All fields are optional; missing fields keep current values. */
export interface SyncConfig {
	audioStaleMs?: number;
	driftThresholdUs?: number;
	driftCorrectionFactor?: number;
	freeRunSkipThreshold?: number;
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
	avSyncCorrectionMs: number;
}

/**
 * Drives the video render loop using requestAnimationFrame. Pulls decoded
 * VideoFrames from a VideoRenderBuffer and draws them to a canvas, paced
 * either by an audio clock (for A/V sync) or by a wall-clock free-run
 * mode when audio is unavailable. Collects timing diagnostics for the
 * perf overlay.
 *
 * Uses wall-clock pacing with gentle drift correction toward audio PTS,
 * rather than directly chasing audio PTS. This produces smooth, regular
 * frame intervals regardless of audio PTS quantization jitter.
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
	private afterDraw: ((ctx: CanvasRenderingContext2D, frame: VideoFrame) => void) | null = null;
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

	// Wall-clock sync: paces video at wall-clock rate with gentle drift correction toward audio PTS
	private wallClockSyncStart = -1;
	private wallClockSyncBasePTS = -1;
	private smoothedDrift = 0;

	// --- sync config (runtime-updatable) ---
	private _audioStaleMs = 200;
	private _driftThresholdUs = 500_000;
	private _driftCorrectionFactor = 0.05;
	private _freeRunSkipThreshold = 9;

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

	updateSyncConfig(config: SyncConfig): void {
		if (config.audioStaleMs != null) this._audioStaleMs = config.audioStaleMs;
		if (config.driftThresholdUs != null) this._driftThresholdUs = config.driftThresholdUs;
		if (config.driftCorrectionFactor != null) this._driftCorrectionFactor = config.driftCorrectionFactor;
		if (config.freeRunSkipThreshold != null) this._freeRunSkipThreshold = config.freeRunSkipThreshold;
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

	setAudioClock(clock: AudioClock): void {
		this.audioClock = clock;
		// Reset free-run state so audio sync takes over
		this.freeRunStart = -1;
		this.freeRunBasePTS = -1;
		this.audioStallFreeRunStart = -1;
		this.audioStallFreeRunBasePTS = -1;
		this.lastAudioAdvanceTime = 0;
		this.wallClockSyncStart = -1;
		this.wallClockSyncBasePTS = -1;
		this.smoothedDrift = 0;
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

				// Sample audio clock so A/V sync diagnostics can measure the gap.
				const skipAudioPTS = this.audioClock.getPlaybackPTS();
				if (skipAudioPTS >= 0) {
					this.currentAudioPTS = skipAudioPTS;
				}

				// Re-anchor any freerun/wall-clock clocks to the new PTS
				if (this.freeRunStart >= 0) {
					this.freeRunStart = now;
					this.freeRunBasePTS = skipResult.frame.timestamp;
				}
				if (this.audioStallFreeRunStart >= 0) {
					this.audioStallFreeRunStart = now;
					this.audioStallFreeRunBasePTS = skipResult.frame.timestamp;
				}
				if (this.wallClockSyncStart >= 0) {
					this.wallClockSyncStart = now;
					this.wallClockSyncBasePTS = skipResult.frame.timestamp;
					this.smoothedDrift = 0;
				}

				this.updateAvSyncDiagnostics();

				this.reportStats(now);
				return;
			}
		}

		let targetPTS: number;
		const audioPTS = this._freeRunOnly ? -1 : this.audioClock.getPlaybackPTS();

		if (audioPTS >= 0) {
			// Audio PTS jumped backward >30s — stream restart. Clear everything.
			if (this.currentAudioPTS >= 0 && this.currentVideoPTS >= 0 &&
				this.currentAudioPTS - audioPTS > 30_000_000) {
				this.videoBuffer.clear();
				this.currentVideoPTS = -1;
				this.wallClockSyncStart = -1;
				this.wallClockSyncBasePTS = -1;
				this.smoothedDrift = 0;
			}

			const audioAdvanced = this.currentAudioPTS < 0 || audioPTS !== this.currentAudioPTS;
			if (audioAdvanced) {
				this.lastAudioAdvanceTime = now;
				this.audioStallFreeRunStart = -1;
				this.audioStallFreeRunBasePTS = -1;
			}
			this.currentAudioPTS = audioPTS;
			this.freeRunStart = -1;
			this.freeRunBasePTS = -1;

			const audioStale = this.lastAudioAdvanceTime > 0 &&
				(now - this.lastAudioAdvanceTime) > this._audioStaleMs;

			if (audioStale && this.videoBuffer.getStats().queueSize > 0) {
				// Audio clock has stalled — pace video using wall clock
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
				// Wall-clock-paced rendering with gentle drift correction toward audio PTS.
				// Video frames arrive at ~1x real-time from MoQ, but the audio worklet's
				// adaptive speed ratio (0.98x–1.02x) causes audio PTS to drift from wall
				// clock. Using audio PTS directly causes visible frame skips; instead we
				// pace at wall-clock rate and smoothly correct toward the audio timeline.
				if (this.wallClockSyncStart < 0) {
					this.wallClockSyncStart = now;
					this.wallClockSyncBasePTS = audioPTS;
					this.smoothedDrift = 0;
				}

				const wallTarget = this.wallClockSyncBasePTS + (now - this.wallClockSyncStart) * 1000;
				const rawDrift = audioPTS - wallTarget;

				if (Math.abs(rawDrift) > this._driftThresholdUs) {
					// >500ms drift: hard resync (reconnect, stream restart, etc.)
					this.wallClockSyncStart = now;
					this.wallClockSyncBasePTS = audioPTS;
					this.smoothedDrift = 0;
					targetPTS = audioPTS;
				} else {
					this.smoothedDrift += (rawDrift - this.smoothedDrift) * this._driftCorrectionFactor;
					targetPTS = wallTarget + this.smoothedDrift;
				}
			}
		} else {
			const firstFrame = this.videoBuffer.peekFirstFrame();
			if (!firstFrame) {
				this._diagEmptyBufferHits++;
				this.freeRunStart = -1;
				this.freeRunBasePTS = -1;
				this.reportStats(now);
				return;
			}
			if (this.freeRunStart < 0) {
				this.freeRunStart = now;

				const stats = this.videoBuffer.getStats();
				if (stats.queueSize > this._freeRunSkipThreshold) {
					const skip = this.videoBuffer.getFrameByTimestamp(Infinity);
					if (skip.frame) {
						if (this.lastDrawnFrame) this.lastDrawnFrame.close();
						this.lastDrawnFrame = skip.frame;
						this.currentVideoPTS = skip.frame.timestamp;
						this.freeRunBasePTS = skip.frame.timestamp;
						this.drawFrame(skip.frame);
						this.reportStats(now);
						return;
					}
				}

				this.freeRunBasePTS = firstFrame.timestamp;
			}
			targetPTS = this.freeRunBasePTS + (now - this.freeRunStart) * 1000;
		}

		let frame: VideoFrame | null = null;

		if (targetPTS < 0) {
			frame = this.videoBuffer.takeNextFrame();
		} else {
			const result = this.videoBuffer.getFrameByTimestamp(targetPTS);
			frame = result.frame;

			// PTS discontinuity recovery: if binary search found nothing but
			// buffer has frames far ahead of target, re-anchor and draw.
			// Handles source transitions where video PTS jumps.
			if (!frame) {
				const peek = this.videoBuffer.peekFirstFrame();
				if (peek && peek.timestamp - targetPTS > 500_000) {
					if (this.wallClockSyncStart >= 0) {
						this.wallClockSyncStart = now;
						this.wallClockSyncBasePTS = peek.timestamp;
						this.smoothedDrift = 0;
					}
					if (this.freeRunStart >= 0) {
						this.freeRunStart = now;
						this.freeRunBasePTS = peek.timestamp;
					}
					frame = this.videoBuffer.takeNextFrame();
				}
			}
		}

		if (frame) {
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

			this.updateAvSyncDiagnostics();
		} else {
			this._diagFramesSkipped++;
		}

		this.reportStats(now);
	}

	/** Update A/V sync diagnostics (measurement only, no feedback correction). */
	private updateAvSyncDiagnostics(): void {
		if (this.currentAudioPTS >= 0 && this.currentVideoPTS >= 0) {
			const delta = Math.abs(this.currentVideoPTS - this.currentAudioPTS);
			if (delta < 30_000_000) {
				const syncMs = (this.currentVideoPTS - this.currentAudioPTS) / 1000;
				this._diagLastAvSync = syncMs;
				this._diagAvSyncSum += syncMs;
				this._diagAvSyncCount++;
				if (syncMs < this._diagAvSyncMin) this._diagAvSyncMin = syncMs;
				if (syncMs > this._diagAvSyncMax) this._diagAvSyncMax = syncMs;
			}
		}
	}

	private cachedCanvasW = 0;
	private cachedCanvasH = 0;

	/**
	 * Draw a video frame to the canvas. Sets the canvas backing store to
	 * match the video frame dimensions (or maxResolution), then draws at
	 * 1:1. CSS handles display scaling — this avoids per-frame GPU scaling
	 * inside drawImage and lets the browser compositor handle it efficiently.
	 */
	private drawFrame(frame: VideoFrame): void {
		let targetW = frame.displayWidth;
		let targetH = frame.displayHeight;
		if (this._maxResolution > 0) {
			const scale = Math.min(1, this._maxResolution / Math.max(targetW, targetH));
			targetW = Math.round(targetW * scale);
			targetH = Math.round(targetH * scale);
		}
		if (this.cachedCanvasW !== targetW || this.cachedCanvasH !== targetH) {
			this.canvas.width = targetW;
			this.canvas.height = targetH;
			this.cachedCanvasW = targetW;
			this.cachedCanvasH = targetH;
		}
		this.ctx.drawImage(frame, 0, 0, targetW, targetH);

		if (this.afterDraw) {
			this.afterDraw(this.ctx, frame);
		}
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
		this.wallClockSyncStart = -1;
		this.wallClockSyncBasePTS = -1;
		this.smoothedDrift = 0;
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
			clockMode: (this.freeRunStart >= 0 || this._freeRunOnly) ? "freerun"
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
			avSyncCorrectionMs: this.smoothedDrift / 1000,
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
