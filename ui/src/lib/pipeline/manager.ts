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
			}
		}

		// Remove stale sources
		for (const key of this.connectedSources) {
			if (!(key in sources)) {
				this.pipeline.removeSource(key);
				this.connectedSources.delete(key);
				this.attachedCanvases.delete(key);
			}
		}

		// Attach multiview tile canvases
		for (const key of stateSourceKeys) {
			if (!this.attachedCanvases.has(key)) {
				const canvas = document.getElementById(`tile-${key}`) as HTMLCanvasElement | null;
				if (canvas) {
					this.pipeline.attachCanvas(key, `tile-${key}`, canvas);
					this.attachedCanvases.add(key);
				}
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
		// Program canvas: render the "program" MoQ stream (shows transitions).
		// Re-attach if the canvas element changed (HMR, layout switch) even if
		// the source key is already 'program'.
		const needsProgramAttach =
			this.currentProgramCanvas !== 'program' ||
			(programCanvasEl && programCanvasEl !== this.currentProgramCanvasEl);

		if (needsProgramAttach) {
			if (this.currentProgramCanvas) {
				this.pipeline.detachCanvas(this.currentProgramCanvas, 'program');
			}
			if (programCanvasEl) {
				this.pipeline.attachCanvas('program', 'program', programCanvasEl);
				this.currentProgramCanvas = 'program';
				this.currentProgramCanvasEl = programCanvasEl;
			} else {
				this.currentProgramCanvas = null;
				this.currentProgramCanvasEl = null;
			}
		}

		// Preview canvas: render the preview source's video
		if (previewSource !== this.currentPreviewCanvas) {
			// Detach old preview renderer from previous source
			if (this.currentPreviewCanvas) {
				this.pipeline.detachCanvas(this.currentPreviewCanvas, 'preview');
			}
			if (previewCanvasEl && previewSource) {
				this.pipeline.attachCanvas(previewSource, 'preview', previewCanvasEl);
			}
			this.currentPreviewCanvas = previewSource;
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
		this.attachedCanvases = new Set<string>();
		this.currentProgramCanvas = null;
		this.currentProgramCanvasEl = null;
		this.currentPreviewCanvas = null;
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
		this.connectedSources = new Set<string>();
		this.attachedCanvases = new Set<string>();
		this.currentProgramCanvas = null;
		this.currentPreviewCanvas = null;
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

		// Sample program output peak from program audio decoder
		const programDecoder = this.pipeline.getAudioDecoder('program');
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
