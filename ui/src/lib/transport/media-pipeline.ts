import { MoQTransport, type MoQTransportCallbacks } from '$lib/prism/moq-transport';
import { PrismVideoDecoder } from '$lib/prism/video-decoder';
import { VideoRenderBuffer } from '$lib/prism/video-render-buffer';
import { PrismAudioDecoder } from '$lib/prism/audio-decoder';
import { PrismRenderer } from '$lib/prism/renderer';
import type { TrackInfo } from '$lib/prism/transport';

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
	transport: MoQTransport | null;
	configured: boolean;
	audioConfigured: boolean;
	/** Codec string from catalog, for lazy configure on first frame. */
	videoCodec: string | null;
	videoWidth: number;
	videoHeight: number;
	videoInitData: string | null; // base64 avcC
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
export function createMediaPipeline(): MediaPipeline {
	const sources = new Map<string, SourceMedia>();

	function createSourceMedia(key: string): SourceMedia {
		const videoBuffer = new VideoRenderBuffer();
		const videoDecoder = new PrismVideoDecoder(videoBuffer);

		return {
			key,
			videoBuffer,
			videoDecoder,
			audioDecoder: null,
			renderers: new Map(),
			transport: null,
			configured: false,
			audioConfigured: false,
			videoCodec: null,
			videoWidth: 0,
			videoHeight: 0,
			videoInitData: null,
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
					} else if (track.type === 'audio' && !source.audioConfigured) {
						// Configure audio decoder from catalog info
						const codec = track.codec || 'mp4a.40.2';
						const sampleRate = track.sampleRate || 48000;
						const channels = track.channels || 2;
						const audioDecoder = new PrismAudioDecoder();
						audioDecoder.configure(codec, sampleRate, channels);
						audioDecoder.setMuted(true); // muted by default
						audioDecoder.enableMetering();
						source.audioDecoder = audioDecoder;
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
				feedVideoFrame(key, data, isKeyframe, timestamp, description);
			},

			onAudioFrame(
				data: Uint8Array,
				timestamp: number,
				_groupID: number,
				_trackIndex: number,
			) {
				feedAudioFrame(key, data, timestamp);
			},

			onCaptionFrame() {
				// Captions not used in switcher multiview
			},

			onServerStats() {
				// Stats handled separately
			},

			onControlState(data: Uint8Array) {
				// Control state is handled by the connection manager, not the media pipeline.
				// But if routed here, ignore -- the control-room store handles it.
				void data;
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

	function feedVideoFrame(
		sourceKey: string,
		data: Uint8Array,
		isKeyframe: boolean,
		timestamp: number,
		description: Uint8Array | null,
	): void {
		const source = sources.get(sourceKey);
		if (!source) return;

		// Configure decoder on first frame with description (avcC config record)
		if (description && !source.configured) {
			const codec = source.videoCodec || 'avc1.64001f';
			const width = source.videoWidth || 1920;
			const height = source.videoHeight || 1080;
			source.videoDecoder.configure(
				codec,
				width,
				height,
				description.buffer as ArrayBuffer,
			);
			source.configured = true;
		} else if (description && source.configured) {
			// Reconfigure when description changes (resolution/codec switch)
			const codec = source.videoCodec || 'avc1.64001f';
			const width = source.videoWidth || 1920;
			const height = source.videoHeight || 1080;
			source.videoDecoder.configure(
				codec,
				width,
				height,
				description.buffer as ArrayBuffer,
			);
		}

		const isDisco = isKeyframe; // keyframes mark discontinuity boundaries
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

		// Clean up all renderers
		for (const renderer of source.renderers.values()) {
			renderer.destroy();
		}
		source.renderers.clear();

		// Clean up video pipeline
		source.videoDecoder.reset();
		source.videoBuffer.clear();

		// Clean up audio
		if (source.audioDecoder) {
			source.audioDecoder.reset();
			source.audioDecoder = null;
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
		}

		// Create audio clock from the source's audio decoder (or a no-op clock)
		const audioClock = {
			getPlaybackPTS(): number {
				return source.audioDecoder?.getPlaybackPTS() ?? -1;
			},
		};

		const renderer = new PrismRenderer(canvas, source.videoBuffer, audioClock);
		renderer.freeRunOnly = !source.audioDecoder; // free-run if no audio
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
	}

	function destroy(): void {
		for (const key of Array.from(sources.keys())) {
			removeSource(key);
		}
	}

	return {
		addSource,
		removeSource,
		connectSource,
		disconnectSource,
		getVideoBuffer,
		getAudioDecoder,
		attachCanvas,
		detachCanvas,
		destroy,
		feedVideoFrame,
		feedAudioFrame,
	};
}
