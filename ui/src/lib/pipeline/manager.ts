import type { MediaPipeline } from '$lib/transport/media-pipeline';

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
 * - Canvas management: attaches multiview tile, program, and preview canvases
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

	/** Pending program canvas waiting for program-raw catalog to arrive. */
	private pendingProgramCanvasEl: HTMLCanvasElement | null = null;
	/** Fallback timer: if program-raw catalog doesn't arrive within 500ms, attach H.264 program. */
	private programFallbackTimer: ReturnType<typeof setTimeout> | null = null;

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
				this.pipeline.connectSource(key);
				this.connectedSources.add(key);

				// For replay, also connect the raw YUV variant (mirrors program/program-raw).
				if (key === 'replay') {
					this.pipeline.addSource('replay-raw');
					this.pipeline.connectSource('replay-raw');
				}
			}
		}

		// Remove stale sources
		for (const key of this.connectedSources) {
			if (!(key in sources)) {
				this.pipeline.removeSource(key);
				this.connectedSources.delete(key);
				this.attachedCanvases.delete(key);

				// Clean up the raw variant when replay is removed.
				if (key === 'replay') {
					this.pipeline.removeSource('replay-raw');
				}
			}
		}

		// Attach multiview tile canvases
		for (const key of stateSourceKeys) {
			if (!this.attachedCanvases.has(key)) {
				// Prefer raw YUV replay stream when available (bypasses H.264 decode).
				const renderKey = key === 'replay' && this.pipeline.isRawYUVSource('replay-raw')
					? 'replay-raw'
					: key;
				const canvas = document.getElementById(`tile-${key}`) as HTMLCanvasElement | null;
				if (canvas) {
					this.pipeline.attachCanvas(renderKey, `tile-${key}`, canvas);
					this.attachedCanvases.add(key);
				}
			}
		}

		// Retry any pending preview attachment that failed because the source
		// wasn't in the pipeline yet (race between effects and source sync).
		if (this.pendingPreview) {
			const { source, canvas } = this.pendingPreview;
			const attached = this.pipeline.attachCanvas(source, 'preview', canvas);
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
		// Prefer low-bitrate program-preview (3 Mbps) over full-quality program
		// (10 Mbps) once its MoQ catalog has arrived (audio decoder configured).
		// Fall back to program-raw (raw YUV) or full-quality program.
		const previewReady = this.pipeline.getAudioDecoder('program-preview') !== null;
		const programKey = previewReady
			? 'program-preview'
			: this.pipeline.isRawYUVSource('program-raw') ? 'program-raw' : 'program';

		// If program-raw exists but catalog hasn't arrived yet, defer PROGRAM
		// canvas attachment to avoid creating a 2D context that blocks WebGL.
		// Preview canvas is unaffected — process it normally below.
		const deferProgram = this.pipeline.hasSource('program-raw') &&
			!this.pipeline.isRawYUVSource('program-raw') &&
			!this.currentProgramCanvas;

		if (deferProgram) {
			// Store pending canvas ref for when catalog arrives or fallback fires
			this.pendingProgramCanvasEl = programCanvasEl ?? null;
			if (!this.programFallbackTimer) {
				this.programFallbackTimer = setTimeout(() => {
					this.programFallbackTimer = null;
					// Raw catalog didn't arrive — fall back to H.264
					if (this.pendingProgramCanvasEl && !this.currentProgramCanvas) {
						this.pipeline.attachCanvas('program', 'program', this.pendingProgramCanvasEl);
						this.currentProgramCanvas = 'program';
						this.currentProgramCanvasEl = this.pendingProgramCanvasEl;
						this.pendingProgramCanvasEl = null;
					}
				}, 500);
			}
		} else {
			// Program canvas: render the program MoQ stream (shows transitions).
			// Re-attach if the canvas element changed (HMR, layout switch) even if
			// the source key hasn't changed.
			const needsProgramAttach =
				this.currentProgramCanvas !== programKey ||
				(programCanvasEl && programCanvasEl !== this.currentProgramCanvasEl);

			if (needsProgramAttach) {
				if (this.currentProgramCanvas) {
					this.pipeline.detachCanvas(this.currentProgramCanvas, 'program');
				}
				if (programCanvasEl) {
					this.pipeline.attachCanvas(programKey, 'program', programCanvasEl);
					this.currentProgramCanvas = programKey;
					this.currentProgramCanvasEl = programCanvasEl;
					// Disconnect and mute full-quality program when preview is active.
					// Stops wasting ~10 Mbps bandwidth and prevents the program
					// AudioContext from outputting silence (which can interfere
					// with program-preview's audio on some systems).
					if (programKey === 'program-preview') {
						this.pipeline.disconnectSource('program');
						this.pipeline.setSourceMuted('program', true);
					}
				} else {
					this.currentProgramCanvas = null;
					this.currentProgramCanvasEl = null;
				}
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
				this.pipeline.detachCanvas(this.currentPreviewCanvas, 'preview');
			}
			if (previewCanvasEl && previewSource) {
				const attached = this.pipeline.attachCanvas(previewSource, 'preview', previewCanvasEl);
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
		this.pipeline.resetRendererSync('program');
	}

	/**
	 * Reset program canvas tracking so the next syncProgramPreviewCanvases
	 * call re-evaluates which source (program vs program-raw) to use and
	 * re-attaches the canvas. Called when a raw YUV source becomes ready.
	 */
	resetProgramCanvas(): void {
		// Cancel fallback timer — raw YUV is ready
		if (this.programFallbackTimer) {
			clearTimeout(this.programFallbackTimer);
			this.programFallbackTimer = null;
		}
		if (this.currentProgramCanvas) {
			this.pipeline.detachCanvas(this.currentProgramCanvas, 'program');
		}
		this.currentProgramCanvas = null;
		this.currentProgramCanvasEl = null;
		// If we had a pending canvas waiting for catalog, clear it so the
		// next syncProgramPreviewCanvases picks up the now-ready program-raw.
		this.pendingProgramCanvasEl = null;
	}

	/**
	 * Detach all canvases. Called before DOM replacement (e.g. layout mode
	 * switch) so renderers don't reference destroyed canvas elements.
	 */
	onLayoutChange(): void {
		// Detach all tile canvases
		for (const key of this.attachedCanvases) {
			this.pipeline.detachCanvas(key, `tile-${key}`);
		}
		// Detach program canvas
		if (this.currentProgramCanvas) {
			this.pipeline.detachCanvas(this.currentProgramCanvas, 'program');
		}
		// Detach preview canvas
		if (this.currentPreviewCanvas) {
			this.pipeline.detachCanvas(this.currentPreviewCanvas, 'preview');
		}
		// Reset tracking
		if (this.programFallbackTimer) {
			clearTimeout(this.programFallbackTimer);
			this.programFallbackTimer = null;
		}
		this.attachedCanvases = new Set<string>();
		this.currentProgramCanvas = null;
		this.currentProgramCanvasEl = null;
		this.currentPreviewCanvas = null;
		this.currentPreviewCanvasEl = null;
		this.pendingPreview = null;
		this.pendingProgramCanvasEl = null;
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

	/** Clean up all state. */
	destroy(): void {
		this.stopMetering();
		if (this.programFallbackTimer) {
			clearTimeout(this.programFallbackTimer);
			this.programFallbackTimer = null;
		}
		this.connectedSources = new Set<string>();
		this.attachedCanvases = new Set<string>();
		this.currentProgramCanvas = null;
		this.currentProgramCanvasEl = null;
		this.currentPreviewCanvas = null;
		this.currentPreviewCanvasEl = null;
		this.pendingPreview = null;
		this.pendingProgramCanvasEl = null;
		this.sourceLevels = {};
		this.programLevels = { peakL: 0, peakR: 0 };
	}

	/** rAF callback that samples audio decoder levels at display refresh rate. */
	private meterLoop = (_timestamp: DOMHighResTimeStamp): void => {
		const levels: Record<string, PeakLevels> = {};
		for (const key of this.getSourceKeys()) {
			const decoder = this.pipeline.getAudioDecoder(key);
			if (decoder) {
				const l = decoder.getLevels();
				levels[key] = { peakL: l.peak[0] ?? 0, peakR: l.peak[1] ?? 0 };
			}
		}
		this.sourceLevels = levels;

		// Sample program output peak from the active program audio decoder.
		// Prefer program-preview (low-bitrate) over program (full-quality).
		const programDecoder = this.pipeline.getAudioDecoder('program-preview')
			?? this.pipeline.getAudioDecoder('program');
		if (programDecoder) {
			const pl = programDecoder.getLevels();
			this.programLevels = { peakL: pl.peak[0] ?? 0, peakR: pl.peak[1] ?? 0 };
		}

		// Push levels to subscriber on each rAF tick (no polling delay)
		if (this.onLevelsUpdate) {
			this.onLevelsUpdate(this.sourceLevels, this.programLevels);
		}

		this.meterRafId = requestAnimationFrame(this.meterLoop);
	};
}
