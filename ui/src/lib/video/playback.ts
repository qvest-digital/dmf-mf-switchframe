/**
 * Per-source video playback state.
 *
 * In the full implementation, each source will have:
 * - A PrismVideoDecoder (WebCodecs in a Worker) decoding MoQ compressed frames
 * - A VideoRenderBuffer (ring buffer) for timestamp-based frame lookup
 * - An OffscreenCanvas for rendering decoded frames
 *
 * The MoQMultiviewTransport feeds compressed frames in, and the
 * WebGPUCompositor reads decoded frames out for display.
 */
interface SourcePlayback {
	key: string;
	canvas: OffscreenCanvas | null;
	// TODO: In full implementation:
	// decoder: PrismVideoDecoder;
	// renderBuffer: VideoRenderBuffer;
}

/**
 * Creates a video playback manager that orchestrates per-source
 * decoder/buffer pairs and tracks which source is on program/preview.
 *
 * This is the coordination layer between MoQ subscriptions and
 * Prism's video decode/render pipeline. Each source gets its own
 * decoder and render buffer, and the manager tracks which sources
 * are assigned to the program and preview outputs.
 *
 * The full Prism module wiring (MoQMultiviewTransport -> PrismVideoDecoder
 * -> VideoRenderBuffer -> WebGPUCompositor) will be connected when the
 * transport layer is ready. The management API is tested now.
 */
export function createVideoPlaybackManager() {
	const sourceMap = new Map<string, SourcePlayback>();
	let _programSource: string | null = null;
	let _previewSource: string | null = null;

	/** Add a source to be decoded and rendered. No-op if already tracked. */
	function addSource(key: string) {
		if (sourceMap.has(key)) return;

		sourceMap.set(key, {
			key,
			canvas:
				typeof OffscreenCanvas !== 'undefined' ? new OffscreenCanvas(320, 180) : null,
			// TODO: In full implementation:
			// const renderBuffer = new VideoRenderBuffer();
			// const decoder = new PrismVideoDecoder(renderBuffer);
			// decoder.preload();
		});
	}

	/**
	 * Remove a source and clean up its decoder/buffer resources.
	 * Also clears program/preview assignment if it pointed to this source.
	 */
	function removeSource(key: string) {
		const source = sourceMap.get(key);
		if (!source) return;

		// TODO: In full implementation:
		// source.decoder.reset();
		// source.renderBuffer.clear();

		sourceMap.delete(key);

		if (_programSource === key) _programSource = null;
		if (_previewSource === key) _previewSource = null;
	}

	/**
	 * Get the OffscreenCanvas for a source.
	 * Returns null if the source is not tracked or OffscreenCanvas is unavailable.
	 */
	function getCanvas(key: string): OffscreenCanvas | null {
		return sourceMap.get(key)?.canvas ?? null;
	}

	/** Set which source is on program output. */
	function setProgramSource(key: string) {
		_programSource = key;
	}

	/** Set which source is on preview output. */
	function setPreviewSource(key: string) {
		_previewSource = key;
	}

	/** Tear down all sources and release resources. */
	function destroy() {
		for (const key of Array.from(sourceMap.keys())) {
			removeSource(key);
		}
	}

	return {
		/** List of currently tracked source keys. */
		get sources() {
			return Array.from(sourceMap.keys());
		},
		/** The source key currently assigned to program output, or null. */
		get programSource() {
			return _programSource;
		},
		/** The source key currently assigned to preview output, or null. */
		get previewSource() {
			return _previewSource;
		},
		addSource,
		removeSource,
		getCanvas,
		setProgramSource,
		setPreviewSource,
		destroy,
	};
}
