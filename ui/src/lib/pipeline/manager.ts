import type { MediaPipeline } from '$lib/transport/media-pipeline';
import { createMediaPipeline } from '$lib/transport/media-pipeline';

/** Audio peak levels for a single channel (linear 0..1). */
export interface PeakLevels {
	peakL: number;
	peakR: number;
}

/**
 * PipelineManager extracts media pipeline orchestration out of +page.svelte.
 *
 * Responsibilities:
 * - Source sync: adds/removes pipeline sources when control room state changes
 * - Canvas management: attaches source tile, program, and preview canvases
 * - Audio metering: rAF loop that samples per-source and program audio levels
 *
 * Program and preview canvases are passed in via syncProgramPreviewCanvases()
 * to avoid getElementById collisions when multiple components share the same
 * canvas IDs. Tile canvases still use getElementById (IDs are unique per source).
 */
export class PipelineManager {
	private pipeline: MediaPipeline;
	private getSourceKeys: () => string[];
	private onLevelsUpdate?: (sourceLevels: Record<string, PeakLevels>, programLevels: PeakLevels) => void;

	/** Secondary pipeline for Engine B (compare mode / A/B toggle). */
	private pipelineB: MediaPipeline | null = null;

	/** Track the URL that `pipelineB` was built for. Used to detect a
	 * replacement Engine B (control plane re-provisioned at new URL) and
	 * rebuild the WebTransport subscriptions. */
	private engineBUrl: string | null = null;

	/** Track which sources are connected on Engine B (warm standby + compare). */
	private connectedSourcesB = new Set<string>();

	/** Whether warm standby was enabled (proactive Engine B pipeline). */
	private _warmStandbyActive = false;

	/** Last time warm standby health check found a valid decoder (ms since epoch). */
	private _warmStandbyLastHealthy = 0;

	/** Whether a failover moved the tiles and the preview canvas to
	 * Engine B. The A/B toggle moves only the program canvas. */
	private viewsOnB = false;

	/** Track which sources are connected to avoid duplicate work. */
	private connectedSources = new Set<string>();
	/** Track which tile canvases are attached. */
	private attachedCanvases = new Set<string>();
	/** Current source key bound to the program canvas. */
	private currentProgramCanvas: string | null = null;
	/** Current source key bound to the preview canvas. */
	private currentPreviewCanvas: string | null = null;
	/** Track the actual preview DOM element to detect canvas replacement (layout switch). */
	private currentPreviewCanvasEl: HTMLCanvasElement | null = null;

	/** Pending preview attachment that failed because the source wasn't in the pipeline yet. */
	private pendingPreview: { source: string; canvas: HTMLCanvasElement } | null = null;

	/** Per-source audio levels sampled from pipeline decoders. */
	private sourceLevels: Record<string, PeakLevels> = {};
	/** Program output peak levels. */
	private programLevels: PeakLevels = { peakL: 0, peakR: 0 };
	/** rAF handle for metering loop. */
	private meterRafId: number | undefined;

	constructor(
		pipeline: MediaPipeline,
		getSourceKeys: () => string[],
		onLevelsUpdate?: (sourceLevels: Record<string, PeakLevels>, programLevels: PeakLevels) => void,
	) {
		this.pipeline = pipeline;
		this.getSourceKeys = getSourceKeys;
		this.onLevelsUpdate = onLevelsUpdate;
	}

	/**
	 * Sync media pipeline sources with control room state.
	 * Adds new sources, removes stale ones, and attaches tile canvases
	 * once DOM elements are available.
	 *
	 * The caller is responsible for awaiting DOM updates (e.g. Svelte tick())
	 * before calling this method, so canvas elements exist in the DOM.
	 */
	async syncSources(sources: Record<string, unknown>): Promise<void> {
		const stateSourceKeys = Object.keys(sources).sort();

		// Add new sources
		for (const key of stateSourceKeys) {
			if (!this.connectedSources.has(key)) {
				this.pipeline.addSource(key);
				// After a failover nothing shows Engine A; restoring to it
				// connects every source.
				if (!this.viewsOnB) this.pipeline.connectSource(key);
				this.connectedSources.add(key);
			}
		}

		// Remove stale sources
		for (const key of this.connectedSources) {
			if (!(key in sources)) {
				this.pipeline.removeSource(key);
				if (this.viewsOnB && this.connectedSourcesB.delete(key)) this.pipelineB?.removeSource(key);
				this.connectedSources.delete(key);
				this.attachedCanvases.delete(key);
			}
		}

		// Attach source tile canvases
		for (const key of stateSourceKeys) {
			if (!this.attachedCanvases.has(key)) {
				const canvas = document.getElementById(`tile-${key}`) as HTMLCanvasElement | null;
				if (canvas) {
					this.attachView(key, `tile-${key}`, canvas);
					this.attachedCanvases.add(key);
				}
			}
		}

		// Retry any pending preview attachment that failed because the source
		// wasn't in the pipeline yet (race between effects and source sync).
		if (this.pendingPreview) {
			const { source, canvas } = this.pendingPreview;
			const attached = this.attachView(source, 'preview', canvas);
			if (attached) {
				this.currentPreviewCanvas = source;
				this.currentPreviewCanvasEl = canvas;
				this.pendingPreview = null;
			}
		}
	}

	/**
	 * Update which source is rendered on the program and preview canvases.
	 *
	 * Program canvas: always renders the "program" MoQ stream (authoritative
	 * server output including transition blends). Attached once and stays
	 * until layout change.
	 *
	 * Preview canvas: renders the preview source's individual stream.
	 *
	 * Canvas elements are passed directly from Svelte bind:this to avoid
	 * getElementById collisions when ProgramPreview and SimpleMode share
	 * the same canvas IDs.
	 */
	/** Track the actual DOM element to detect canvas replacement (HMR, layout). */
	private currentProgramCanvasEl: HTMLCanvasElement | null = null;

	syncProgramPreviewCanvases(
		previewSource: string,
		programCanvasEl?: HTMLCanvasElement | null,
		previewCanvasEl?: HTMLCanvasElement | null,
	): void {
		// UI renders from program-preview (low-bitrate, ~3 Mbps). The
		// full-quality `program` MoQ source is reserved for SRT / file
		// recording output and is never subscribed from the browser.
		const programKey = 'program-preview';

		// Program canvas: render the program MoQ stream (shows transitions).
		// Re-attach if the canvas element changed (HMR, layout switch) even if
		// the source key hasn't changed.
		const needsProgramAttach =
			this.currentProgramCanvas !== programKey ||
			(programCanvasEl && programCanvasEl !== this.currentProgramCanvasEl);

		if (needsProgramAttach) {
			if (this.currentProgramCanvas) {
				this.detachEverywhere(this.currentProgramCanvas, 'program');
			}
			if (programCanvasEl) {
				const pgm = this._activeEngine === 'B' && this.pipelineB ? this.pipelineB : this.pipeline;
				pgm.attachCanvas(programKey, 'program', programCanvasEl);
				this.currentProgramCanvas = programKey;
				this.currentProgramCanvasEl = programCanvasEl;
			} else {
				this.currentProgramCanvas = null;
				this.currentProgramCanvasEl = null;
			}
		}

		// Preview canvas: render the preview source's video.
		// Re-attach if the source changed OR the canvas element changed (layout
		// switch creates new DOM elements). Mirrors the program canvas logic.
		const needsPreviewAttach =
			previewSource !== this.currentPreviewCanvas ||
			(previewCanvasEl && previewCanvasEl !== this.currentPreviewCanvasEl);

		if (needsPreviewAttach) {
			// Detach old preview renderer from previous source
			if (this.currentPreviewCanvas) {
				this.detachEverywhere(this.currentPreviewCanvas, 'preview');
			}
			if (previewCanvasEl && previewSource) {
				const attached = this.attachView(previewSource, 'preview', previewCanvasEl);
				if (!attached) {
					// Source not in pipeline yet — store pending attachment
					// so syncSources can retry after adding the source.
					this.pendingPreview = { source: previewSource, canvas: previewCanvasEl };
					this.currentPreviewCanvas = null;
					this.currentPreviewCanvasEl = null;
					return;
				}
				this.pendingPreview = null;
				this.currentPreviewCanvas = previewSource;
				this.currentPreviewCanvasEl = previewCanvasEl;
			} else {
				this.currentPreviewCanvas = null;
				this.currentPreviewCanvasEl = null;
			}
		}
	}

	/**
	 * Notify that the program source has changed (transition completed).
	 * Resets A/V sync tracking on the program renderer so stale PTS from
	 * the previous source doesn't produce transient sync swings.
	 */
	notifyProgramSourceChange(): void {
		const activePipeline = this._activeEngine === 'B' && this.pipelineB
			? this.pipelineB : this.pipeline;
		activePipeline.resetRendererSync('program-preview');
	}

	/**
	 * Reset program canvas tracking so the next syncProgramPreviewCanvases
	 * call re-evaluates which source to use and re-attaches the canvas.
	 */
	resetProgramCanvas(): void {
		if (this.currentProgramCanvas) {
			this.pipeline.detachCanvas(this.currentProgramCanvas, 'program');
		}
		this.currentProgramCanvas = null;
		this.currentProgramCanvasEl = null;
	}

	/**
	 * Detach all canvases. Called before DOM replacement (e.g. layout mode
	 * switch) so renderers don't reference destroyed canvas elements.
	 */
	onLayoutChange(): void {
		// Detach all tile canvases
		for (const key of this.attachedCanvases) {
			this.detachEverywhere(key, `tile-${key}`);
		}
		// Detach program canvas
		if (this.currentProgramCanvas) {
			this.detachEverywhere(this.currentProgramCanvas, 'program');
		}
		// Detach preview canvas
		if (this.currentPreviewCanvas) {
			this.detachEverywhere(this.currentPreviewCanvas, 'preview');
		}
		// Reset tracking
		this.attachedCanvases = new Set<string>();
		this.currentProgramCanvas = null;
		this.currentProgramCanvasEl = null;
		this.currentPreviewCanvas = null;
		this.currentPreviewCanvasEl = null;
		this.pendingPreview = null;
	}

	/**
	 * Attach a tile or preview canvas on the engine that shows them: Engine
	 * B after a failover, which subscribes the source there if it has not
	 * yet, else Engine A.
	 */
	private attachView(key: string, canvasId: string, canvas: HTMLCanvasElement): boolean {
		if (this.viewsOnB && this.pipelineB) {
			if (!this.connectedSourcesB.has(key)) {
				this.pipelineB.addSource(key);
				this.pipelineB.connectSource(key);
				this.connectedSourcesB.add(key);
			}
			return this.pipelineB.attachCanvas(key, canvasId, canvas);
		}
		return this.pipeline.attachCanvas(key, canvasId, canvas);
	}

	/** Detach a canvas from both engines: a renderer left on the other one
	 * would go on drawing its source onto the same canvas. */
	private detachEverywhere(key: string, canvasId: string): void {
		this.pipeline.detachCanvas(key, canvasId);
		this.pipelineB?.detachCanvas(key, canvasId);
	}

	/** Start the rAF audio metering loop. */
	startMetering(): void {
		if (this.meterRafId !== undefined) return;
		this.meterRafId = requestAnimationFrame(this.meterLoop);
	}

	/** Stop the rAF audio metering loop. */
	stopMetering(): void {
		if (this.meterRafId !== undefined) {
			cancelAnimationFrame(this.meterRafId);
			this.meterRafId = undefined;
		}
	}

	/** Get current audio levels. */
	getLevels(): { sourceLevels: Record<string, PeakLevels>; programLevels: PeakLevels } {
		return {
			sourceLevels: this.sourceLevels,
			programLevels: this.programLevels,
		};
	}

	/** Create Engine B media pipeline for compare mode / A/B toggle.
	 * If pipelineB exists and URL matches, no-op. If pipelineB exists but the
	 * URL has changed (Engine B re-provisioned), destroys the old pipeline,
	 * rebuilds against the new URL, and — if we were currently failed over
	 * to Engine B — re-attaches tile/program/preview canvases to the new
	 * pipeline so the program canvas doesn't go permanently black. */
	createEngineBPipeline(engineBUrl: string, options?: { onControlState?: (data: Uint8Array) => void }): void {
		if (this.pipelineB && this.engineBUrl === engineBUrl) return;
		const wasFailedOver = this.pipelineB !== null && this._activeEngine === 'B';
		if (this.pipelineB) {
			this.pipelineB.destroy();
			this.pipelineB = null;
			this.connectedSourcesB = new Set<string>();
		}
		this.pipelineB = createMediaPipeline({
			engineBaseUrl: engineBUrl,
			...(options?.onControlState ? { onControlState: options.onControlState } : {}),
		});
		this.engineBUrl = engineBUrl;

		// Mid-failover URL replacement: the destroyed pipeline had canvases
		// attached. The DOM canvas elements survive, but their renderers are
		// gone. Re-attach them to the fresh pipelineB so the operator gets
		// video back once media flows. Sources will be re-subscribed by the
		// enableWarmStandby / failover caller.
		if (wasFailedOver) {
			for (const key of this.attachedCanvases) {
				const canvas = document.getElementById(`tile-${key}`) as HTMLCanvasElement | null;
				if (canvas) {
					this.pipelineB.addSource(key);
					this.pipelineB.connectSource(key);
					this.connectedSourcesB.add(key);
					this.pipelineB.attachCanvas(key, `tile-${key}`, canvas);
				}
			}
			if (this.currentProgramCanvasEl) {
				// UI subscribes only to program-preview (low-bitrate copy).
				// Full-quality `program` is for SRT/recording output, never
				// for browser delivery.
				this.pipelineB.addSource('program-preview');
				this.pipelineB.connectSource('program-preview');
				this.connectedSourcesB.add('program-preview');
				this.pipelineB.attachCanvas('program-preview', 'program', this.currentProgramCanvasEl);
			}
			if (this.currentPreviewCanvas && this.currentPreviewCanvasEl) {
				// Preview binds to one of the tile sources, but don't assume —
				// defensively subscribe before attaching so a future preview
				// source outside `attachedCanvases` doesn't silently fail
				// (attachCanvas returns false on an unknown source).
				if (!this.connectedSourcesB.has(this.currentPreviewCanvas)) {
					this.pipelineB.addSource(this.currentPreviewCanvas);
					this.pipelineB.connectSource(this.currentPreviewCanvas);
					this.connectedSourcesB.add(this.currentPreviewCanvas);
				}
				this.pipelineB.attachCanvas(this.currentPreviewCanvas, 'preview', this.currentPreviewCanvasEl);
			}
		}
	}

	/** Destroy Engine B pipeline. Canvases it shows move back to Engine A
	 * first, which reconnects Engine A's sources: nothing else would. */
	destroyEngineBPipeline(): void {
		if (this.pipelineB && this._activeEngine === 'B') {
			this.switchProgramToEngineA();
		}
		if (this.pipelineB && this.viewsOnB) {
			this.swapTileAndPreviewCanvases(this.pipelineB, this.pipeline);
		}
		this.pipelineB?.destroy();
		this.pipelineB = null;
		this.engineBUrl = null;
		this.connectedSourcesB = new Set<string>();
		this._warmStandbyActive = false;
		this._warmStandbyLastHealthy = 0;
		this.viewsOnB = false;
	}

	/**
	 * Proactively create and maintain a warm Engine B pipeline.
	 * Connects all sources plus 'program-preview' on B,
	 * but does NOT attach any canvases. MoQ transports stay running so
	 * failover only needs to swap canvases (instant, no 3s wait).
	 *
	 * Safe to call repeatedly. If the engine B URL changes (re-provisioning),
	 * the existing pipelineB is torn down and re-created against the new URL
	 * via createEngineBPipeline — connectedSourcesB is cleared as part of
	 * that teardown, so sources are re-subscribed on the fresh pipeline.
	 */
	enableWarmStandby(engineBUrl: string, sourceKeys: string[], options?: { onControlState?: (data: Uint8Array) => void }): void {
		// Fast path: URL unchanged and warm standby already running. Steady-
		// state source churn is driven by `syncWarmStandby` on its own
		// $effect. Returning early here prevents an O(N_sources) sweep on
		// every state tick when a caller (the warm-standby $effect) re-runs
		// because `store.state.sources` identity changed.
		if (this.pipelineB && this.engineBUrl === engineBUrl && this._warmStandbyActive) return;

		// createEngineBPipeline handles both first-time creation and the URL-
		// changed rebuild case. After it returns, connectedSourcesB reflects
		// which sources are subscribed on the current pipelineB.
		this.createEngineBPipeline(engineBUrl, options);
		if (!this.pipelineB) return;

		this._warmStandbyActive = true;

		// Connect all source keys plus program-preview. Full-quality `program`
		// is never subscribed from the browser — it's for SRT / record output.
		const allKeys = [...sourceKeys, 'program-preview'];
		for (const key of allKeys) {
			if (!this.connectedSourcesB.has(key)) {
				this.pipelineB.addSource(key);
				this.pipelineB.connectSource(key);
				this.connectedSourcesB.add(key);
			}
		}
	}

	/**
	 * Keep Engine B's source list in sync as sources come and go.
	 * Only operates when warm standby is active and Engine A is primary.
	 */
	syncWarmStandby(sourceKeys: string[]): void {
		if (!this.pipelineB || !this._warmStandbyActive || this._activeEngine !== 'A') return;

		const sourceKeySet = new Set(sourceKeys);

		// Add new sources
		for (const key of sourceKeys) {
			if (!this.connectedSourcesB.has(key)) {
				this.pipelineB.addSource(key);
				this.pipelineB.connectSource(key);
				this.connectedSourcesB.add(key);
			}
		}

		// Remove stale sources (keep program-preview)
		for (const key of this.connectedSourcesB) {
			if (key === 'program-preview') continue;
			if (!sourceKeySet.has(key)) {
				this.pipelineB.removeSource(key);
				this.connectedSourcesB.delete(key);
			}
		}
	}

	/** Connect a source on Engine B's pipeline. */
	connectSourceB(key: string): void {
		if (!this.pipelineB) return;
		this.pipelineB.addSource(key);
		this.pipelineB.connectSource(key);
	}

	/** Disconnect a source on Engine B. */
	disconnectSourceB(key: string): void {
		if (!this.pipelineB) return;
		this.pipelineB.removeSource(key);
	}

	/** Attach a canvas to Engine B's source. */
	attachCanvasB(sourceKey: string, canvasId: string, canvas: HTMLCanvasElement): boolean {
		return this.pipelineB?.attachCanvas(sourceKey, canvasId, canvas) ?? false;
	}

	/** Detach canvas from Engine B's source. */
	detachCanvasB(sourceKey: string, canvasId: string): void {
		this.pipelineB?.detachCanvas(sourceKey, canvasId);
	}

	/** Check if Engine B pipeline exists. */
	get hasEngineBPipeline(): boolean {
		return this.pipelineB !== null;
	}

	/** Track which engine is currently rendering the program canvas. */
	private _activeEngine: 'A' | 'B' = 'A';

	/** Guard against concurrent failover/restore calls. */
	private _failoverInProgress = false;

	/** Which engine currently owns the program canvas. */
	get activeEngine(): 'A' | 'B' {
		return this._activeEngine;
	}

	/**
	 * Move the program canvas from Engine A to Engine B.
	 * Requires an active Engine B pipeline and a program canvas element.
	 *
	 * Also disconnects Engine A's program feed, which nothing views any
	 * more; tiles and preview stay on Engine A.
	 */
	switchProgramToEngineB(): void {
		if (!this.pipelineB || !this.currentProgramCanvasEl) return;

		// Detach from Engine A (whichever source key is currently showing)
		if (this.currentProgramCanvas) {
			this.pipeline.detachCanvas(this.currentProgramCanvas, 'program');
		}

		// Disconnect Engine A's program feed, which nothing shows any more.
		// Tiles and preview stay on Engine A and keep their transports; a
		// failover moves them and disconnects the rest. disconnectSource,
		// not removeSource, keeps decode state for a quick switch back.
		this.pipeline.disconnectSource('program-preview');

		// Attach to Engine B's program-preview (UI never subscribes to
		// full-quality `program`).
		this.pipelineB.attachCanvas('program-preview', 'program', this.currentProgramCanvasEl);
		this.currentProgramCanvas = 'program-preview';
		this._activeEngine = 'B';
	}

	/**
	 * Move the program canvas back from Engine B to Engine A. Reconnects
	 * Engine A's MoQ transports that were paused during failover.
	 */
	switchProgramToEngineA(): void {
		if (!this.pipelineB) return;

		// Detach from Engine B
		this.pipelineB.detachCanvas('program-preview', 'program');

		// Reconnect Engine A's MoQ transports (paused during switchProgramToEngineB)
		for (const key of this.connectedSources) {
			this.pipeline.connectSource(key);
		}
		this.pipeline.connectSource('program-preview');

		// Re-attach to Engine A's program-preview.
		if (this.currentProgramCanvasEl) {
			this.pipeline.attachCanvas('program-preview', 'program', this.currentProgramCanvasEl);
			this.currentProgramCanvas = 'program-preview';
		}
		this._activeEngine = 'A';
	}

	/**
	 * Full pipeline failover: move all canvases (tiles, program, preview)
	 * from Engine A to Engine B. Creates Engine B pipeline if needed,
	 * connects all sources, waits for readiness, then swaps every canvas.
	 *
	 * Called automatically when Engine A disconnects in dual-engine mode.
	 */
	async failoverToEngineB(engineBUrl: string, sourceKeys: string[], options?: { onControlState?: (data: Uint8Array) => void }): Promise<void> {
		if (this._activeEngine === 'B' || this._failoverInProgress) return;
		this._failoverInProgress = true;
		try {
			if (this.pipelineB && this._warmStandbyActive) {
				// Warm standby path: pipeline B already exists with sources
				// connected — skip creation, source connection, AND the 3s wait.
				// Go straight to canvas swapping below.
			} else if (this.pipelineB && this.pipelineB.getAudioDecoder('program-preview')) {
				// Pipeline B exists and has media flowing — treat as warm.
				// This covers the gap between enableWarmStandby() being called
				// and _warmStandbyActive propagating (200-500ms window).
			} else {
				// Cold path: create Engine B pipeline on-demand.
				this.createEngineBPipeline(engineBUrl, options);
				if (!this.pipelineB) return;

				// Connect source tiles + program-preview on Engine B. Full-
				// quality `program` is never subscribed from the browser.
				for (const key of sourceKeys) {
					this.pipelineB.addSource(key);
					this.pipelineB.connectSource(key);
					this.connectedSourcesB.add(key);
				}
				this.pipelineB.addSource('program-preview');
				this.pipelineB.connectSource('program-preview');
				this.connectedSourcesB.add('program-preview');

				// Wait for Engine B to have media (up to 1.5s)
				await this.waitForPipelineReady(this.pipelineB, 1500);
			}

			// Swap program canvas
			this.switchProgramToEngineB();

			// Swap tile + preview canvases from A → B
			this.swapTileAndPreviewCanvases(this.pipeline, this.pipelineB!);
			this.viewsOnB = true;

			// Nothing shows Engine A any more: stop decoding it headless.
			for (const key of this.connectedSources) {
				this.pipeline.disconnectSource(key);
			}

			this._activeEngine = 'B';

			// Resume audio contexts on the new pipeline — browser autoplay policy
			// suspends AudioContexts that weren't created from a user gesture.
			// Without this, audio goes mute after automatic failover.
			this.pipelineB!.resumeAllAudio().catch(() => {});
		} finally {
			this._failoverInProgress = false;
		}
	}

	/**
	 * Reverse failover: move all canvases back from Engine B to Engine A
	 * and destroy the Engine B pipeline.
	 *
	 * Called when Engine A reconnects and sync is restored.
	 */
	async restoreToEngineA(sourceKeys: string[]): Promise<void> {
		if (this._activeEngine === 'A' || this._failoverInProgress) return;
		this._failoverInProgress = true;
		try {
			if (!this.pipelineB) return;

			// Swap program canvas back to A
			this.switchProgramToEngineA();

			// Swap tile + preview canvases from B → A
			this.swapTileAndPreviewCanvases(this.pipelineB, this.pipeline);
			this.viewsOnB = false;

			this._activeEngine = 'A';

			// Resume audio contexts on Engine A — may have been suspended during failover.
			this.pipeline.resumeAllAudio().catch(() => {});

			// Clean up Engine B pipeline
			this.destroyEngineBPipeline();
		} finally {
			this._failoverInProgress = false;
		}
	}

	/** Swap tile and preview canvases between two pipelines. */
	private swapTileAndPreviewCanvases(from: MediaPipeline, to: MediaPipeline): void {
		for (const key of this.attachedCanvases) {
			const canvas = document.getElementById(`tile-${key}`) as HTMLCanvasElement | null;
			if (canvas) {
				from.detachCanvas(key, `tile-${key}`);
				to.attachCanvas(key, `tile-${key}`, canvas);
			}
		}
		if (this.currentPreviewCanvas && this.currentPreviewCanvasEl) {
			from.detachCanvas(this.currentPreviewCanvas, 'preview');
			to.attachCanvas(this.currentPreviewCanvas, 'preview', this.currentPreviewCanvasEl);
		}
	}

	/**
	 * Poll until the pipeline has a configured audio decoder for
	 * 'program-preview' (indicating MoQ catalog arrival), or timeout expires.
	 */
	private waitForPipelineReady(pipeline: MediaPipeline, timeoutMs: number): Promise<void> {
		return new Promise((resolve) => {
			const start = Date.now();
			const check = () => {
				if (pipeline.getAudioDecoder('program-preview')) {
					resolve();
					return;
				}
				if (Date.now() - start > timeoutMs) {
					resolve(); // timeout — proceed anyway with whatever we have
					return;
				}
				setTimeout(check, 200);
			};
			check();
		});
	}

	/** Whether warm standby is active (Engine B pipeline running proactively). */
	get warmStandbyActive(): boolean {
		return this._warmStandbyActive;
	}

	/** Whether warm standby pipeline has an active MoQ connection (receiving media). */
	get warmStandbyHealthy(): boolean {
		if (!this._warmStandbyActive || !this.pipelineB) return false;
		const hasDecoder = this.pipelineB.getAudioDecoder('program-preview') !== null;
		// Also check that video frames have arrived recently (within 3s).
		// Audio decoder presence alone doesn't prove video is flowing —
		// the video subscription could be stalled while audio works fine.
		const now = performance.now();
		const lastPreviewFrame = this.pipelineB.getLastFrameTime('program-preview');
		const hasRecentVideo = lastPreviewFrame > 0 && (now - lastPreviewFrame) < 3000;
		if (hasDecoder && hasRecentVideo) {
			this._warmStandbyLastHealthy = Date.now();
			return true;
		}
		// If was recently healthy (within 5s), still consider healthy
		// (decoder or video may be briefly absent during stream renegotiation)
		return Date.now() - this._warmStandbyLastHealthy < 5000;
	}

	/**
	 * Whether Engine B's program feed delivered a frame within the last
	 * withinMs. A program stall seen on both engines is this browser or its
	 * network, not Engine A, and a failover would only move it.
	 */
	engineBDelivering(withinMs: number): boolean {
		const last = this.pipelineB?.getLastFrameTime('program-preview') ?? 0;
		return last > 0 && performance.now() - last <= withinMs;
	}

	/** Pre-warm audio contexts on both pipelines.
	 *  Call from a user gesture handler so AudioContext.resume() works
	 *  during automatic failover without requiring another gesture. */
	preWarmAudioContexts(): void {
		this.pipeline.resumeAllAudio().catch(() => {});
		if (this.pipelineB) {
			this.pipelineB.resumeAllAudio().catch(() => {});
		}
	}

	/** Resume audio on whichever pipeline is currently active.
	 *  Used after automatic failover when a user gesture is available. */
	resumeActiveAudio(): void {
		const activePipeline = this._activeEngine === 'B' && this.pipelineB
			? this.pipelineB : this.pipeline;
		activePipeline.resumeAllAudio().catch(() => {});
	}

	/** Clean up all state. */
	destroy(): void {
		this.stopMetering();
		// Engine A goes as well: nothing moves back to it first.
		this._activeEngine = 'A';
		this.viewsOnB = false;
		this.destroyEngineBPipeline();
		this.pipeline.destroy();
		this.connectedSources = new Set<string>();
		this.attachedCanvases = new Set<string>();
		this.connectedSourcesB = new Set<string>();
		this._warmStandbyActive = false;
		this._warmStandbyLastHealthy = 0;
		this.currentProgramCanvas = null;
		this.currentProgramCanvasEl = null;
		this.currentPreviewCanvas = null;
		this.currentPreviewCanvasEl = null;
		this.pendingPreview = null;
		this.sourceLevels = {};
		this.programLevels = { peakL: 0, peakR: 0 };
		this._activeEngine = 'A';
	}

	/** rAF callback that samples audio decoder levels at display refresh rate. */
	private meterLoop = (_timestamp: DOMHighResTimeStamp): void => {
		const activePipeline = this._activeEngine === 'B' && this.pipelineB
			? this.pipelineB : this.pipeline;

		const levels: Record<string, PeakLevels> = {};
		for (const key of this.getSourceKeys()) {
			const decoder = activePipeline.getAudioDecoder(key);
			if (decoder) {
				const l = decoder.getLevels();
				levels[key] = { peakL: l.peak[0] ?? 0, peakR: l.peak[1] ?? 0 };
			}
		}
		this.sourceLevels = levels;

		const programDecoder = activePipeline.getAudioDecoder('program-preview');
		if (programDecoder) {
			const pl = programDecoder.getLevels();
			this.programLevels = { peakL: pl.peak[0] ?? 0, peakR: pl.peak[1] ?? 0 };
		}

		if (this.onLevelsUpdate) {
			this.onLevelsUpdate(this.sourceLevels, this.programLevels);
		}

		this.meterRafId = requestAnimationFrame(this.meterLoop);
	};
}
