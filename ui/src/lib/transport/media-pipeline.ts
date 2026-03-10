import { MoQTransport, type MoQTransportCallbacks } from '$lib/prism/moq-transport';
import { PrismVideoDecoder } from '$lib/prism/video-decoder';
import { VideoRenderBuffer } from '$lib/prism/video-render-buffer';
import { PrismAudioDecoder } from '$lib/prism/audio-decoder';
import { PrismRenderer } from '$lib/prism/renderer';
import type { TrackInfo } from '$lib/prism/transport';
import { createYUVRenderer, type YUVRenderer } from '$lib/video/yuv-renderer';

/** Configuration for the media pipeline. */
export interface MediaPipelineConfig {
	/** Called when MoQ control track delivers state data. */
	onControlState?: (data: Uint8Array) => void;
	/** Called when a source is identified as raw YUV (after catalog arrives). */
	onRawSourceReady?: (sourceKey: string) => void;
}

/** Diagnostics snapshot for a single source. */
export interface SourceDiagnostics {
	renderer: Record<string, unknown> | null;
	videoDecoder: Record<string, unknown> | null;
	audio: Record<string, unknown> | null;
	transport: Record<string, unknown> | null;
}

/**
 * Per-source media state: decoder, buffer, and renderers.
 * Each source stream has its own MoQTransport, video decoder pipeline,
 * and audio decoder for metering/PFL. Multiple renderers are supported
 * per source so the same decoded video can render to both a multiview
 * tile and a program/preview canvas simultaneously.
 */
interface SourceMedia {
	key: string;
	videoBuffer: VideoRenderBuffer;
	videoDecoder: PrismVideoDecoder;
	audioDecoder: PrismAudioDecoder | null;
	renderers: Map<string, PrismRenderer>;
	/** Per-renderer secondary buffers for multi-canvas sources (program/preview). */
	secondaryBuffers: Map<string, VideoRenderBuffer>;
	transport: MoQTransport | null;
	configured: boolean;
	audioConfigured: boolean;
	/** Last avcC description bytes, for change detection. */
	lastDescription: Uint8Array | null;
	/** Codec string from catalog, for lazy configure on first frame. */
	videoCodec: string | null;
	videoWidth: number;
	videoHeight: number;
	videoInitData: string | null; // base64 avcC
	/** WebGL YUV420 renderer for raw YUV sources (no H.264 decode needed). */
	yuvRenderer: YUVRenderer | null;
	/** True if this source uses raw/yuv420 codec instead of H.264. */
	isRawYUV: boolean;
	/** Count of raw YUV frames dropped because yuvRenderer was null. */
	rawDropCount: number;
}

/** Orchestrates MoQ media subscriptions and per-source decode pipelines. */
export interface MediaPipeline {
	/** Add a source and optionally connect its MoQ transport. */
	addSource(key: string): void;
	/** Remove a source and destroy its decode resources. */
	removeSource(key: string): void;
	/** Connect a source to its Prism MoQ stream. */
	connectSource(key: string): void;
	/** Disconnect a source's MoQ transport without removing decode state. */
	disconnectSource(key: string): void;
	/** Get the video render buffer for a source (for external renderers). */
	getVideoBuffer(sourceKey: string): VideoRenderBuffer | null;
	/** Get the audio decoder for a source (for PFL/metering). */
	getAudioDecoder(sourceKey: string): PrismAudioDecoder | null;
	/** Attach a canvas to render a source's decoded video. canvasId identifies this renderer instance. */
	attachCanvas(sourceKey: string, canvasId: string, canvas: HTMLCanvasElement): void;
	/** Detach and destroy a specific renderer for a source. */
	detachCanvas(sourceKey: string, canvasId: string): void;
	/** Destroy all sources and transports. */
	destroy(): void;
	/** Feed a video frame directly (for testing or alternative transports). */
	feedVideoFrame(
		sourceKey: string,
		data: Uint8Array,
		isKeyframe: boolean,
		timestamp: number,
		description: Uint8Array | null,
	): void;
	/** Feed an audio frame directly (for testing or alternative transports). */
	feedAudioFrame(
		sourceKey: string,
		data: Uint8Array,
		timestamp: number,
	): void;
	/** Set muted state for a source's audio decoder. */
	setSourceMuted(sourceKey: string, muted: boolean): void;
	/** Resume all AudioContexts. Must be called from a user gesture handler. */
	resumeAllAudio(): Promise<void>;
	/** Reset A/V sync tracking on all renderers for a source (e.g. after program source change). */
	resetRendererSync(sourceKey: string): void;
	/** Check if a source uses raw YUV420. */
	isRawYUVSource(sourceKey: string): boolean;
	/** Get diagnostics from all active sources for debug snapshot. */
	getAllDiagnostics(): Promise<Record<string, SourceDiagnostics>>;
}

/**
 * Creates a media pipeline that manages per-source MoQ subscriptions,
 * video decoders, render buffers, audio decoders, and canvas renderers.
 *
 * Each source in Switchframe corresponds to a separate Prism stream.
 * The pipeline creates a MoQTransport per source when connectSource()
 * is called, subscribes to its video/audio tracks, and routes decoded
 * frames through PrismVideoDecoder -> VideoRenderBuffer -> PrismRenderer.
 */
export function createMediaPipeline(config?: MediaPipelineConfig): MediaPipeline {
	const sources = new Map<string, SourceMedia>();

	function createSourceMedia(key: string): SourceMedia {
		const videoBuffer = new VideoRenderBuffer();
		const secondaryBuffers = new Map<string, VideoRenderBuffer>();

		// Clone decoded frames into secondary buffers so multiple renderers
		// (tile + program/preview) each get their own copy without contention.
		const videoDecoder = new PrismVideoDecoder(videoBuffer, (frame: VideoFrame) => {
			for (const buf of secondaryBuffers.values()) {
				buf.addFrame(frame.clone());
			}
		});

		return {
			key,
			videoBuffer,
			videoDecoder,
			audioDecoder: null,
			renderers: new Map(),
			secondaryBuffers,
			transport: null,
			configured: false,
			audioConfigured: false,
			lastDescription: null,
			videoCodec: null,
			videoWidth: 0,
			videoHeight: 0,
			videoInitData: null,
			yuvRenderer: null,
			isRawYUV: false,
			rawDropCount: 0,
		};
	}

	function makeCallbacks(key: string): MoQTransportCallbacks {
		return {
			onTrackInfo(tracks: TrackInfo[]) {
				const source = sources.get(key);
				if (!source) return;

				// Store codec info from catalog for lazy configure
				for (const track of tracks) {
					if (track.type === 'video') {
						source.videoCodec = track.codec;
						source.videoWidth = track.width;
						source.videoHeight = track.height;
						source.videoInitData = track.initData ?? null;
						if (track.codec === 'raw/yuv420') {
							source.isRawYUV = true;
							// If a PrismRenderer was already attached (before we knew
							// this was raw YUV), detach it so the pipeline manager
							// re-attaches with a YUVRenderer on the next sync call.
							for (const [canvasId, renderer] of source.renderers) {
								renderer.destroy();
							}
							source.renderers.clear();
							for (const buf of source.secondaryBuffers.values()) {
								buf.clear();
							}
							source.secondaryBuffers.clear();
							// Notify so the pipeline manager can re-attach canvases.
							config?.onRawSourceReady?.(key);
						}
					} else if (track.type === 'audio' && !source.audioConfigured) {
						// Configure audio decoder from catalog info
						const codec = track.codec || 'mp4a.40.2';
						const sampleRate = track.sampleRate || 48000;
						const channels = track.channels || 2;
						const audioDecoder = new PrismAudioDecoder();
						audioDecoder.configure(codec, sampleRate, channels).then(() => {
							audioDecoder.setMuted(!unmutedSources.has(key));
							audioDecoder.enableMetering();
							source.audioDecoder = audioDecoder;
						}).catch((err) => {
							console.warn(`[MediaPipeline] Audio decoder failed for "${key}":`, err);
						});
						source.audioConfigured = true;
					}
				}
			},

			onVideoFrame(
				data: Uint8Array,
				isKeyframe: boolean,
				timestamp: number,
				_groupID: number,
				description: Uint8Array | null,
			) {
					// MoQ timestamps are in 90kHz (MPEG-TS clock); WebCodecs expects microseconds.
				const timestampUs = Math.round(timestamp * 1_000_000 / 90_000);
				feedVideoFrame(key, data, isKeyframe, timestampUs, description);
			},

			onAudioFrame(
				data: Uint8Array,
				timestamp: number,
				_groupID: number,
				_trackIndex: number,
			) {
				const timestampUs = Math.round(timestamp * 1_000_000 / 90_000);
				feedAudioFrame(key, data, timestampUs);
			},

			onCaptionFrame() {
				// Captions not used in switcher multiview
			},

			onServerStats() {
				// Stats handled separately
			},

			onControlState(data: Uint8Array) {
				if (config?.onControlState) {
					config.onControlState(data);
				}
			},

			onClose() {
				// Transport closed -- could reconnect
				const source = sources.get(key);
				if (source) {
					source.transport = null;
				}
			},

			onError(err: string) {
				console.warn(`[MediaPipeline] source "${key}" error:`, err);
			},
		};
	}

	/** Compare two avcC descriptions for byte equality. */
	function descriptionEqual(a: Uint8Array | null, b: Uint8Array | null): boolean {
		if (a === null || b === null) return a === b;
		if (a.byteLength !== b.byteLength) return false;
		for (let i = 0; i < a.byteLength; i++) {
			if (a[i] !== b[i]) return false;
		}
		return true;
	}

	function feedVideoFrame(
		sourceKey: string,
		data: Uint8Array,
		isKeyframe: boolean,
		timestamp: number,
		description: Uint8Array | null,
	): void {
		const source = sources.get(sourceKey);
		if (!source) return;

		// Raw YUV path: bypass WebCodecs, render directly via WebGL
		if (source.isRawYUV) {
			if (source.yuvRenderer) {
				source.yuvRenderer.render(data);
				source.rawDropCount = 0;
			} else {
				// No renderer — track drops and disconnect after 1s (~30 frames)
				// to stop wasting bandwidth on data we can't render.
				source.rawDropCount++;
				if (source.rawDropCount >= 30) {
					console.warn(`[MediaPipeline] Disconnecting "${sourceKey}": no YUV renderer after ${source.rawDropCount} frames`);
					disconnectSource(sourceKey);
				}
			}
			return;
		}

		// Configure decoder on first frame with description (avcC config record).
		// description is often a subarray (view into a larger extensions buffer),
		// so we must slice() to get an owned copy — .buffer would return the
		// entire parent ArrayBuffer which contains other extension data.
		// We need TWO copies: one to transfer to the worker (gets detached),
		// and one to retain for change detection on subsequent keyframes.
		if (description && !source.configured) {
			const codec = source.videoCodec || 'avc1.64001f';
			const width = source.videoWidth || 1920;
			const height = source.videoHeight || 1080;
			source.lastDescription = description.slice(); // retained copy
			source.videoDecoder.configure(codec, width, height, description.slice().buffer as ArrayBuffer);
			source.configured = true;
		} else if (description && source.configured && !descriptionEqual(source.lastDescription, description)) {
			// Reconfigure only when description actually changes (resolution/codec switch)
			const codec = source.videoCodec || 'avc1.64001f';
			const width = source.videoWidth || 1920;
			const height = source.videoHeight || 1080;
			source.lastDescription = description.slice(); // retained copy
			source.videoDecoder.configure(codec, width, height, description.slice().buffer as ArrayBuffer);
		}

		const isDisco = false; // continuous stream — keyframes are not discontinuities
		source.videoDecoder.decode(data, isKeyframe, timestamp, isDisco);
	}

	function feedAudioFrame(
		sourceKey: string,
		data: Uint8Array,
		timestamp: number,
	): void {
		const source = sources.get(sourceKey);
		if (!source || !source.audioDecoder) return;

		source.audioDecoder.decode(data, timestamp, false);
	}

	function addSource(key: string): void {
		if (sources.has(key)) return;
		const media = createSourceMedia(key);
		sources.set(key, media);
	}

	function removeSource(key: string): void {
		const source = sources.get(key);
		if (!source) return;

		// Clean up transport
		if (source.transport) {
			source.transport.close();
			source.transport = null;
		}

		// Clean up all renderers and secondary buffers
		for (const renderer of source.renderers.values()) {
			renderer.destroy();
		}
		source.renderers.clear();
		for (const buf of source.secondaryBuffers.values()) {
			buf.clear();
		}
		source.secondaryBuffers.clear();

		// Clean up video pipeline
		source.videoDecoder.reset();
		source.videoBuffer.clear();

		// Clean up audio
		if (source.audioDecoder) {
			source.audioDecoder.reset();
			source.audioDecoder = null;
		}

		// Clean up YUV renderer
		if (source.yuvRenderer) {
			source.yuvRenderer.destroy();
			source.yuvRenderer = null;
		}

		sources.delete(key);
	}

	function connectSource(key: string): void {
		const source = sources.get(key);
		if (!source) return;
		if (source.transport) return; // already connected

		const callbacks = makeCallbacks(key);
		const transport = new MoQTransport(key, callbacks);
		source.transport = transport;

		// Connect asynchronously
		transport.connect().catch((err) => {
			console.warn(`[MediaPipeline] Failed to connect source "${key}":`, err);
		});
	}

	function disconnectSource(key: string): void {
		const source = sources.get(key);
		if (!source || !source.transport) return;

		source.transport.close();
		source.transport = null;
	}

	function getVideoBuffer(sourceKey: string): VideoRenderBuffer | null {
		return sources.get(sourceKey)?.videoBuffer ?? null;
	}

	function getAudioDecoder(sourceKey: string): PrismAudioDecoder | null {
		return sources.get(sourceKey)?.audioDecoder ?? null;
	}

	function attachCanvas(sourceKey: string, canvasId: string, canvas: HTMLCanvasElement): void {
		const source = sources.get(sourceKey);
		if (!source) return;

		// Destroy existing renderer for this canvasId if any
		const existing = source.renderers.get(canvasId);
		if (existing) {
			existing.destroy();
			source.secondaryBuffers.delete(canvasId);
		}

		// Raw YUV sources use WebGL renderer instead of PrismRenderer.
		// WebGL and Canvas2D contexts are mutually exclusive, so if a
		// PrismRenderer previously used this canvas (2D context), we must
		// create a fresh canvas element for the WebGL YUV renderer.
		if (source.isRawYUV) {
			let glCanvas = canvas;
			const testCtx = canvas.getContext('webgl2') || canvas.getContext('webgl');
			if (!testCtx) {
				// Canvas already has a 2D context — swap it with a fresh one.
				glCanvas = document.createElement('canvas');
				glCanvas.width = canvas.width;
				glCanvas.height = canvas.height;
				glCanvas.style.cssText = canvas.style.cssText;
				canvas.parentElement?.replaceChild(glCanvas, canvas);
			}
			const yuvR = createYUVRenderer(glCanvas);
			if (yuvR) {
				source.yuvRenderer = yuvR;
			}
			return;
		}

		// Create audio clock from the source's audio decoder (or a no-op clock)
		const audioClock = {
			getPlaybackPTS(): number {
				return source.audioDecoder?.getPlaybackPTS() ?? -1;
			},
		};

		// First renderer uses the primary buffer; additional renderers get
		// their own buffer with cloned frames to avoid contention.
		let buffer: VideoRenderBuffer;
		if (source.renderers.size === 0) {
			buffer = source.videoBuffer;
		} else {
			buffer = new VideoRenderBuffer();
			source.secondaryBuffers.set(canvasId, buffer);
		}

		const renderer = new PrismRenderer(canvas, buffer, audioClock);
		// No freeRunOnly flag — audioClock dynamically returns -1 when audio
		// isn't ready yet, so the renderer naturally uses freerun mode and
		// transitions to audio-driven once the decoder finishes configuring.
		source.renderers.set(canvasId, renderer);
		renderer.start();
	}

	function detachCanvas(sourceKey: string, canvasId: string): void {
		const source = sources.get(sourceKey);
		if (!source) return;

		const renderer = source.renderers.get(canvasId);
		if (!renderer) return;

		renderer.destroy();
		source.renderers.delete(canvasId);

		const secBuf = source.secondaryBuffers.get(canvasId);
		if (secBuf) {
			secBuf.clear();
			source.secondaryBuffers.delete(canvasId);
		}
	}

	function destroy(): void {
		document.removeEventListener("visibilitychange", handleVisibilityChange);
		for (const key of Array.from(sources.keys())) {
			removeSource(key);
		}
	}

	function handleVisibilityChange(): void {
		const hidden = document.hidden;
		for (const source of sources.values()) {
			if (hidden) {
				source.videoDecoder.pause();
			} else {
				source.videoDecoder.resume();
				// Clear secondary buffers (program/preview canvases)
				for (const buf of source.secondaryBuffers.values()) {
					buf.clear();
				}
				// Reset renderer sync so stale PTS doesn't corrupt timing
				for (const renderer of source.renderers.values()) {
					renderer.resetSync();
				}
			}
		}
	}

	document.addEventListener("visibilitychange", handleVisibilityChange);

	/** Track which sources should be unmuted (set before audio decoder is ready). */
	const unmutedSources = new Set<string>();

	function setSourceMuted(sourceKey: string, muted: boolean): void {
		if (muted) {
			unmutedSources.delete(sourceKey);
		} else {
			unmutedSources.add(sourceKey);
		}
		const source = sources.get(sourceKey);
		if (source?.audioDecoder) {
			source.audioDecoder.setMuted(muted);
		}
	}

	async function resumeAllAudio(): Promise<void> {
		const promises: Promise<void>[] = [];
		for (const source of sources.values()) {
			if (source.audioDecoder) {
				promises.push(source.audioDecoder.resumeContext());
			}
		}
		await Promise.all(promises);
	}

	function resetRendererSync(sourceKey: string): void {
		const source = sources.get(sourceKey);
		if (!source) return;
		for (const renderer of source.renderers.values()) {
			renderer.resetSync();
		}
	}

	function isRawYUVSource(sourceKey: string): boolean {
		return sources.get(sourceKey)?.isRawYUV === true;
	}

	async function getAllDiagnostics(): Promise<Record<string, SourceDiagnostics>> {
		const result: Record<string, SourceDiagnostics> = {};
		for (const [key, source] of sources) {
			// Get diagnostics from first renderer (tile renderer)
			let rendererDiag: Record<string, unknown> | null = null;
			for (const renderer of source.renderers.values()) {
				rendererDiag = renderer.getDiagnostics() as unknown as Record<string, unknown>;
				break;
			}

			result[key] = {
				renderer: rendererDiag,
				videoDecoder: await source.videoDecoder.getDiagnostics() as unknown as Record<string, unknown>,
				audio: (source.audioDecoder?.getDiagnostics() ?? null) as Record<string, unknown> | null,
				transport: (source.transport?.getDiagnostics() ?? null) as Record<string, unknown> | null,
			};
		}
		return result;
	}

	return {
		addSource,
		removeSource,
		connectSource,
		disconnectSource,
		getVideoBuffer,
		getAudioDecoder,
		setSourceMuted,
		resumeAllAudio,
		attachCanvas,
		detachCanvas,
		destroy,
		feedVideoFrame,
		feedAudioFrame,
		resetRendererSync,
		isRawYUVSource,
		getAllDiagnostics,
	};
}
