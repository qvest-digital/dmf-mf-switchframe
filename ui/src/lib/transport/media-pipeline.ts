import { MoQTransport, type MoQTransportCallbacks } from '$lib/prism/moq-transport';
import { PrismVideoDecoder } from '$lib/prism/video-decoder';
import { VideoRenderBuffer } from '$lib/prism/video-render-buffer';
import { PrismAudioDecoder } from '$lib/prism/audio-decoder';
import { PrismRenderer } from '$lib/prism/renderer';
import type { TrackInfo } from '$lib/prism/transport';
import type { CaptionData } from '$lib/prism/protocol';
import { setSourceStalled, clearSourceStall, clearAllStalls, isSourceStalled } from '$lib/transport/source-errors.svelte';
import { monitorGain, programGain, subscribeMonitor } from '$lib/audio/monitor.svelte';
import { holdReload } from '$lib/util/activity';
/** Configuration for the media pipeline. */
export interface MediaPipelineConfig {
	/** Called when MoQ control track delivers state data. */
	onControlState?: (data: Uint8Array) => void;
	/**
	 * Called when a per-source MoQ transport has successfully connected
	 * and received its catalog. This proves the WebTransport + MoQ
	 * handshake succeeded, so REST polling can be stopped.
	 */
	onMoQActive?: () => void;
	/** Called when a caption frame arrives on the program stream. */
	onProgramCaptionFrame?: (caption: CaptionData, timestamp: number) => void;
	/** Base URL for connecting to a remote engine (e.g. Engine B in multi-region). */
	engineBaseUrl?: string;
	/**
	 * Called when stall detection marks a source as stalled (no frames for STALL_THRESHOLD_MS).
	 * Useful for triggering media failover when program video freezes but the state
	 * transport remains alive (partial failure).
	 */
	onSourceStalled?: (key: string) => void;
}

/** Diagnostics snapshot for a single source. */
export interface SourceDiagnostics {
	renderer: Record<string, unknown> | null;
	videoDecoder: Record<string, unknown> | null;
	audio: Record<string, unknown> | null;
	transport: Record<string, unknown> | null;
}

/**
 * Per-source media state: decoder, buffers, and renderers.
 * Each source stream has its own MoQTransport, video decoder pipeline,
 * and audio decoder for metering/PFL. Multiple renderers are supported
 * per source so the same decoded video can render to both a multiview
 * tile and a program/preview canvas simultaneously.
 */
interface SourceMedia {
	key: string;
	videoDecoder: PrismVideoDecoder;
	/** Whether the video decoder is paused: the tab is hidden or no canvas shows the source. */
	videoPaused: boolean;
	audioDecoder: PrismAudioDecoder | null;
	renderers: Map<string, PrismRenderer>;
	/** Render buffer of each renderer, by canvasId. */
	buffers: Map<string, VideoRenderBuffer>;
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
	/** Newest MoQ group ID seen — used for skip-to-latest on stall recovery. */
	latestGroupID: number;
	/** Count of group-level skips (diagnostic). */
	groupSkips: number;
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
	/** Get the audio decoder for a source (for PFL/metering). */
	getAudioDecoder(sourceKey: string): PrismAudioDecoder | null;
	/** Get the underlying WebTransport for a source (for datagrams). */
	getSourceTransport(sourceKey: string): WebTransport | null;
	/** Attach a canvas to render a source's decoded video. canvasId identifies this renderer instance.
	 *  Returns true if the source exists and the canvas was attached, false otherwise. */
	attachCanvas(sourceKey: string, canvasId: string, canvas: HTMLCanvasElement): boolean;
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
	/** Set the linear gain a source's audio plays at while unmuted (AFL). */
	setSourceGain(sourceKey: string, gain: number): void;
	/** Resume all AudioContexts. Must be called from a user gesture handler. */
	resumeAllAudio(): Promise<void>;
	/** Reset A/V sync tracking on all renderers for a source (e.g. after program source change). */
	resetRendererSync(sourceKey: string): void;
	/** Check if a source exists in the pipeline (added but not necessarily catalog-ready). */
	hasSource(sourceKey: string): boolean;
	/** Get diagnostics from all active sources for debug snapshot. */
	getAllDiagnostics(): Promise<Record<string, SourceDiagnostics>>;
	/** Start periodic frame stall detection (checks every 500ms, stall after 1.5s without frames). */
	startStallDetection(): void;
	/** Stop the stall detection interval. */
	stopStallDetection(): void;
	/** Get the last video frame arrival time (performance.now()) for a source, or 0 if never seen. */
	getLastFrameTime(sourceKey: string): number;
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
/** How long (ms) without a video frame before a source is considered stalled. */
export const STALL_THRESHOLD_MS = 1500;
/** How often (ms) to check for stalled sources. */
const STALL_CHECK_INTERVAL_MS = 500;
/** Extra time a reconnected source has for its first frame: certificate
 * lookup, QUIC and MoQ handshakes and the subscriptions come before it. */
const RECONNECT_GRACE_MS = 3000;
/** Delay before reconnecting a source whose transport closed or failed to
 * connect; it doubles with each failed attempt up to RECONNECT_MAX_MS. */
export const RECONNECT_BASE_MS = 500;
export const RECONNECT_MAX_MS = 30_000;
/** How long a transport has to deliver its catalog. A dial made while the
 * network is down can hang in the QUIC handshake for longer than the
 * outage lasts; it is abandoned and dialled again. */
export const CONNECT_TIMEOUT_MS = 4000;
/** How long a transport has to stay up before its loss starts the
 * backoff afresh. An engine that accepts a session and drops it at once
 * would otherwise be dialled at the base delay for as long as it flaps. */
export const RECONNECT_STABLE_MS = 10_000;
/** Least time between two early dials of the sources waiting to
 * reconnect, set off by another source of the pipeline coming up. */
export const DIAL_WAITING_MIN_MS = 1000;
/** How long a transport that delivered video may go without a frame
 * before it is replaced. An open session that stopped carrying frames is
 * otherwise never noticed: nothing closes it. A transport that has not
 * delivered a frame is left open, so a source wired to nothing or idle
 * keeps one session, which the engine closes once its catalog changes. */
export const STALL_REDIAL_MS = 3000;
/** WebTransport close code of a session whose catalog went stale: the
 * engine's wtErrCatalogChanged. The source has media again, so it is
 * dialled at once whatever backoff it built up. */
export const CLOSE_CATALOG_CHANGED = 6;

/**
 * Whether the page has had a user gesture, which a browser requires before
 * an AudioContext may run. navigator.userActivation knows it; where it is
 * missing, a pointer or key press seen since this module loaded stands in.
 * Kept per page, not per pipeline: a pipeline created after the gesture,
 * such as engine B's on a failover, must know of it too.
 */
let gestureSeen = false;
const markGesture = (): void => {
	gestureSeen = true;
	window.removeEventListener('pointerdown', markGesture, { capture: true });
	window.removeEventListener('keydown', markGesture, { capture: true });
};
if (typeof window !== 'undefined') {
	window.addEventListener('pointerdown', markGesture, { capture: true });
	window.addEventListener('keydown', markGesture, { capture: true });
}
function audioMayStart(): boolean {
	return gestureSeen || (navigator as Navigator & { userActivation?: { hasBeenActive: boolean } }).userActivation?.hasBeenActive === true;
}

export function createMediaPipeline(config?: MediaPipelineConfig): MediaPipeline {
	const sources = new Map<string, SourceMedia>();
	/** Per-source last video frame arrival time (performance.now()). */
	const lastFrameTime = new Map<string, number>();
	/** Sources that delivered frames before disconnectSource; their
	 * reconnect re-arms stall detection. */
	const disconnectedWithFrames = new Set<string>();
	/** Interval handle for stall detection. */
	let stallCheckInterval: ReturnType<typeof setInterval> | null = null;
	/** Sources whose transport should be up: connected and not since
	 * disconnected or removed. A transport of one that closes, or fails to
	 * connect, is reconnected; nothing else would. */
	const wanted = new Set<string>();
	/** Pending reconnect of each source, and the attempts made since its
	 * last transport delivered a catalog. */
	const reconnects = new Map<string, { timer: ReturnType<typeof setTimeout> | null; attempts: number }>();
	/** When each source's current transport delivered its catalog. */
	const upSince = new Map<string, number>();
	/** When waiting sources were last dialled early. */
	let lastDialWaiting = -Infinity;
	/** Last video frame of each source's current transport
	 * (performance.now()); none until it delivers one. */
	const lastMedia = new Map<string, number>();
	/** First video frame of each source's current transport. */
	const firstMedia = new Map<string, number>();
	/** Sources whose current transport delivered video over more than
	 * STALL_REDIAL_MS. A relay replays its cached GOP to every new
	 * subscriber in one burst, so a transport that only received that
	 * burst is not live and its silence is no stall. */
	const liveMedia = new Set<string>();
	/** Pending replacement of each source's stalled transport. */
	const stallRedials = new Map<string, ReturnType<typeof setTimeout>>();
	let stallRedialInterval: ReturnType<typeof setInterval> | null = null;
	/** Deadline of each source's transport that has not delivered its
	 * catalog yet. */
	const connectDeadlines = new Map<string, ReturnType<typeof setTimeout>>();
	function clearConnectDeadline(key: string): void {
		const t = connectDeadlines.get(key);
		if (t) clearTimeout(t);
		connectDeadlines.delete(key);
	}

	function createSourceMedia(key: string): SourceMedia {
		const buffers = new Map<string, VideoRenderBuffer>();

		// Every renderer (tile, program/preview) reads its own buffer: one
		// takes the decoded frame, the others a clone. A frame nothing
		// renders is closed at once; an open VideoFrame holds decoder
		// memory, and a decoder whose frames are held stops producing.
		const sink = {
			addFrame(frame: VideoFrame): void {
				let owner: VideoRenderBuffer | null = null;
				for (const buf of buffers.values()) {
					if (owner) buf.addFrame(frame.clone());
					else owner = buf;
				}
				if (owner) owner.addFrame(frame);
				else frame.close();
			},
			clear(): void {
				for (const buf of buffers.values()) buf.clear();
			},
		};
		const videoDecoder = new PrismVideoDecoder(sink);
		// Nothing shows the source until a canvas is attached.
		videoDecoder.pause();

		return {
			key,
			videoDecoder,
			videoPaused: true,
			audioDecoder: null,
			renderers: new Map(),
			buffers,
			transport: null,
			configured: false,
			audioConfigured: false,
			lastDescription: null,
			videoCodec: null,
			videoWidth: 0,
			videoHeight: 0,
			videoInitData: null,
			latestGroupID: 0,
			groupSkips: 0,
		};
	}

	/** Set once after the first MoQ catalog is received. */
	let moqActiveNotified = false;

	/**
	 * One AudioContext for the audio of every source. Each context is an
	 * audio output stream and an audio rendering thread of its own, and a
	 * context per source made one tab hold one per source and engine.
	 * Created before the page's first user gesture, it is suspended, as the
	 * browser would not let it run; resumeAllAudio resumes it on that
	 * gesture. Created after it, it runs.
	 */
	let audioContext: AudioContext | null = null;
	/** Set by destroy: nothing may create a context it would not close. */
	let destroyed = false;
	function sharedAudioContext(): AudioContext {
		if (destroyed) throw new Error('media pipeline destroyed');
		if (!audioContext) {
			audioContext = new AudioContext({ sampleRate: 48000, latencyHint: 'interactive' });
			if (audioContext.state === 'running' && !audioMayStart()) void audioContext.suspend();
			monitorBus = audioContext.createGain();
			monitorBus.connect(audioContext.destination);
			programBus = audioContext.createGain();
			programBus.connect(monitorBus);
			applyMonitor(true);
			unsubscribeMonitor = subscribeMonitor(() => { applyMonitor(false); updateAudioHold(); });
			audioContext.onstatechange = updateAudioHold;
			updateAudioHold();
		}
		return audioContext;
	}

	/**
	 * The local monitor (see $lib/audio/monitor.svelte): program audio plays
	 * through programBus, every other source straight into monitorBus, and
	 * monitorBus into the speakers. Only this browser's output changes.
	 */
	let monitorBus: GainNode | null = null;
	let programBus: GainNode | null = null;
	let unsubscribeMonitor: (() => void) | null = null;
	/** Held while this browser plays audio someone may be listening to. */
	let releaseAudioHold: (() => void) | null = null;
	function updateAudioHold(): void {
		const audible = audioContext?.state === 'running' && monitorGain() > 0;
		if (audible && !releaseAudioHold) releaseAudioHold = holdReload('audio monitor');
		else if (!audible && releaseAudioHold) {
			releaseAudioHold();
			releaseAudioHold = null;
		}
	}
	/** Time constant of a monitor gain change: quick, but without a click. */
	const MONITOR_RAMP_S = 0.015;

	function applyMonitor(immediate: boolean): void {
		if (!audioContext || !monitorBus || !programBus) return;
		const now = audioContext.currentTime;
		for (const [bus, gain] of [[monitorBus, monitorGain()], [programBus, programGain()]] as const) {
			if (immediate) {
				bus.gain.value = gain;
			} else {
				bus.gain.cancelScheduledValues(now);
				bus.gain.setTargetAtTime(gain, now, MONITOR_RAMP_S);
			}
		}
	}

	function isProgramKey(key: string): boolean {
		return key === 'program' || key === 'program-preview';
	}

	/** Callbacks of one transport of a source. `current` tells whether that
	 * transport is still the source's: a replaced or closed transport can
	 * still deliver, and its frames would interleave with the new one's in
	 * the same decoder. */
	function makeCallbacks(key: string, current: () => boolean): MoQTransportCallbacks {
		return {
			onTrackInfo(tracks: TrackInfo[]) {
				const source = sources.get(key);
				if (!source || destroyed || !current()) return;
				// The transport is up, and the engine is reachable again, so
				// every source still waiting dials now rather than at the end
				// of its backoff. Its own backoff starts afresh only once it
				// has stayed up: see transportLost.
				clearConnectDeadline(key);
				upSince.set(key, Date.now());
				dialWaiting();

				// Notify once that MoQ is active (catalog received = WebTransport
				// + MoQ handshake succeeded, control data will flow on state changes).
				// Fire only for program-preview (the browser-visible program feed);
				// full-quality `program` is never subscribed from the browser. A tile
				// connecting first must not suppress REST polling before the program
				// feed is confirmed working.
				if (!moqActiveNotified && key === 'program-preview') {
					moqActiveNotified = true;
					config?.onMoQActive?.();
				}

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
						const ctx = sharedAudioContext();
						const bus = isProgramKey(key) ? programBus : monitorBus;
						audioDecoder.configure(codec, sampleRate, channels, ctx, bus ?? undefined).then(() => {
							// The source was removed, or the pipeline destroyed,
							// while the decoder was configuring: nothing would
							// ever reset it.
							if (sources.get(key) !== source) {
								audioDecoder.reset();
								return;
							}
							audioDecoder.setGain(sourceGains.get(key) ?? 1);
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
				groupID: number,
				description: Uint8Array | null,
			) {
				const source = sources.get(key);
				if (!source || !current()) return;

				// Group-level skip-to-latest: when a keyframe arrives and we've
				// SKIPPED groups (gap > 1), flush stale decoder state and jump
				// forward. Normal sequential groups (gap == 1) just advance the
				// tracker without flushing — the decoder pipeline continues
				// smoothly. Only a gap indicates a transport stall where we
				// fell behind and should discard stale state.
				if (groupID > 0 && groupID > source.latestGroupID) {
					if (isKeyframe) {
						const gap = groupID - source.latestGroupID;
						if (gap > 1 && source.latestGroupID > 0) {
							// Skipped group(s) — transport stall, flush and jump forward
							source.videoDecoder.flush();
							source.groupSkips++;
						}
						source.latestGroupID = groupID;
					} else {
						// Non-keyframe from a group newer than what we've decoded —
						// drop it, we need the keyframe first.
						return;
					}
				} else if (groupID > 0 && groupID < source.latestGroupID) {
					// Stale frame from an older group — drop silently.
					return;
				}

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
				if (!current()) return;
				const timestampUs = Math.round(timestamp * 1_000_000 / 90_000);
				feedAudioFrame(key, data, timestampUs);
			},

			onCaptionFrame(caption: CaptionData, timestamp: number) {
				if (!current()) return;
				if (key === 'program-preview' && config?.onProgramCaptionFrame) {
					config.onProgramCaptionFrame(caption, timestamp);
				}
			},

			onServerStats() {
				// Stats handled separately
			},

			onControlState(data: Uint8Array) {
				if (!current()) return;
				if (config?.onControlState) {
					config.onControlState(data);
				}
			},

			onClose(closeCode?: number) {
				if (current()) transportLost(key, closeCode === CLOSE_CATALOG_CHANGED);
			},

			onError(err: string) {
				if (!current()) return;
				console.warn(`[MediaPipeline] source "${key}" error:`, err);
				transportLost(key);
			},
		};
	}

	/**
	 * The current transport of a source closed or failed. Drops it and
	 * everything it delivered: the decoder waits for a keyframe of the next
	 * transport rather than decoding its deltas against old references, and
	 * no backlog of the old stream is left in the render buffers. A
	 * reconnect of the new relay session starts its group IDs afresh.
	 * A source that has media again, or whose transport stayed up, starts
	 * its backoff afresh.
	 */
	function transportLost(key: string, mediaAgain = false): void {
		if (!sources.get(key)?.transport) return;
		const up = dropTransport(key);
		if (mediaAgain || (up !== undefined && Date.now() - up >= RECONNECT_STABLE_MS)) {
			const r = reconnects.get(key);
			if (r) r.attempts = 0;
		}
		scheduleReconnect(key);
	}

	/** Close a source's transport and drop what it delivered. Returns when
	 * the transport delivered its catalog, if it did. */
	function dropTransport(key: string): number | undefined {
		const source = sources.get(key);
		if (!source?.transport) return undefined;
		const transport = source.transport;
		source.transport = null;
		clearConnectDeadline(key);
		clearStallRedial(key);
		forgetMedia(key);
		try { transport.close(); } catch { /* already closed */ }
		source.latestGroupID = 0;
		try { source.videoDecoder.flush(); } catch { /* decoder may be in error state */ }
		for (const buf of source.buffers.values()) buf.clear();
		const up = upSince.get(key);
		upSince.delete(key);
		return up;
	}

	/** The next reconnect delay of a source; each call backs off further. */
	function nextBackoff(key: string): number {
		let r = reconnects.get(key);
		if (!r) {
			r = { timer: null, attempts: 0 };
			reconnects.set(key, r);
		}
		const backoff = Math.min(RECONNECT_BASE_MS * 2 ** r.attempts, RECONNECT_MAX_MS);
		r.attempts++;
		// Jitter, so every source of a page does not dial at the same moment.
		return backoff * (0.75 + Math.random() * 0.5);
	}

	function scheduleReconnect(key: string): void {
		if (destroyed || !wanted.has(key)) return;
		if (reconnects.get(key)?.timer) return;
		const delay = nextBackoff(key);
		const r = reconnects.get(key)!;
		r.timer = setTimeout(() => {
			r.timer = null;
			const source = sources.get(key);
			if (destroyed || !source || !wanted.has(key) || source.transport) return;
			openTransport(key, source);
		}, delay);
	}

	function cancelReconnect(key: string): void {
		const r = reconnects.get(key);
		if (r?.timer) clearTimeout(r.timer);
		reconnects.delete(key);
		clearStallRedial(key);
	}

	function clearStallRedial(key: string): void {
		const t = stallRedials.get(key);
		if (t) clearTimeout(t);
		stallRedials.delete(key);
	}

	/** Watch for stalled transports while sources are wanted and the tab
	 * is visible; a hidden tab's timers are throttled and nobody sees its
	 * previews. */
	function startStallRedials(): void {
		if (stallRedialInterval === null && !document.hidden) {
			stallRedialInterval = setInterval(checkStalledTransports, STALL_CHECK_INTERVAL_MS);
		}
	}

	function stopStallRedials(): void {
		if (stallRedialInterval !== null) clearInterval(stallRedialInterval);
		stallRedialInterval = null;
		for (const key of Array.from(stallRedials.keys())) clearStallRedial(key);
	}

	/** Record a video frame on a source's current transport. */
	function noteMedia(key: string): void {
		const now = performance.now();
		const first = firstMedia.get(key);
		if (first === undefined) firstMedia.set(key, now);
		else if (now - first > STALL_REDIAL_MS) liveMedia.add(key);
		lastMedia.set(key, now);
	}

	/** Forget the video seen on a source's current transport. */
	function forgetMedia(key: string): void {
		lastMedia.delete(key);
		firstMedia.delete(key);
		liveMedia.delete(key);
	}

	/** Replace, after the reconnect backoff, the transport of every source
	 * that delivered video over more than STALL_REDIAL_MS and then none for
	 * STALL_REDIAL_MS. A
	 * source delivering frames on a transport that has been up for
	 * RECONNECT_STABLE_MS starts its backoff afresh. */
	function checkStalledTransports(): void {
		if (wanted.size === 0) {
			stopStallRedials();
			return;
		}
		const now = performance.now();
		for (const key of wanted) {
			const source = sources.get(key);
			const up = upSince.get(key);
			const last = lastMedia.get(key);
			if (!source?.transport || up === undefined || last === undefined) continue;
			if (now - last <= STALL_REDIAL_MS) {
				if (Date.now() - up >= RECONNECT_STABLE_MS) {
					const r = reconnects.get(key);
					if (r && !r.timer) r.attempts = 0;
				}
				continue;
			}
			if (!liveMedia.has(key) || stallRedials.has(key)) continue;
			stallRedials.set(key, setTimeout(() => {
				stallRedials.delete(key);
				const s = sources.get(key);
				if (destroyed || !s?.transport || !wanted.has(key)) return;
				const seen = lastMedia.get(key);
				if (seen !== undefined && performance.now() - seen <= STALL_REDIAL_MS) return;
				console.warn(`[MediaPipeline] source "${key}" delivered no video for ${STALL_REDIAL_MS} ms, reconnecting`);
				dropTransport(key);
				openTransport(key, s);
			}, nextBackoff(key)));
		}
	}

	/** Dial at once every source waiting for a reconnect: the browser
	 * reports the network back, or a transport of this pipeline came up. */
	function dialWaiting(): void {
		const now = Date.now();
		if (now - lastDialWaiting < DIAL_WAITING_MIN_MS) return;
		lastDialWaiting = now;
		for (const [key, r] of reconnects) {
			if (!r.timer) continue;
			clearTimeout(r.timer);
			r.timer = null;
			const source = sources.get(key);
			if (source && wanted.has(key) && !source.transport) openTransport(key, source);
		}
	}

	/** The browser reports the network back: every waiting source dials
	 * now, its backoff started afresh. */
	function handleOnline(): void {
		for (const r of reconnects.values()) r.attempts = 0;
		lastDialWaiting = -Infinity;
		dialWaiting();
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

	/**
	 * Extract H.264 codec string from avcC configuration record (ISO 14496-15).
	 * Returns "avc1.PPCCLL" where PP=profile_idc, CC=constraint_flags, LL=level_idc.
	 * These bytes are at avcC[1..3], matching SPS[1..3].
	 */
	function codecStringFromAvcC(avcC: Uint8Array): string | null {
		if (avcC.length < 4) return null;
		const p = avcC[1].toString(16).padStart(2, '0');
		const c = avcC[2].toString(16).padStart(2, '0');
		const l = avcC[3].toString(16).padStart(2, '0');
		return `avc1.${p}${c}${l}`;
	}

	function fallbackCodecString(width: number, height: number): string {
		if (height >= 1080) return 'avc1.640028'; // High Profile L4.0 (1080p)
		if (height >= 720) return 'avc1.64001f';   // High Profile L3.1 (720p)
		return 'avc1.640015';                       // High Profile L2.1 (480p/lower)
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

		// Track frame arrival for stall detection
		lastFrameTime.set(sourceKey, performance.now());
		if (source.transport) noteMedia(sourceKey);
		// If this source was previously marked as stalled, clear it
		clearSourceStall(sourceKey);

		// Configure decoder on first frame with description (avcC config record).
		// description is often a subarray (view into a larger extensions buffer),
		// so we must slice() to get an owned copy — .buffer would return the
		// entire parent ArrayBuffer which contains other extension data.
		// We need TWO copies: one to transfer to the worker (gets detached),
		// and one to retain for change detection on subsequent keyframes.
		//
		// Codec string and dimensions are derived from the avcC bytes when
		// available, rather than relying on the cached MoQ catalog values.
		// The catalog is only received once per connection; subsequent
		// SetVideoInfo calls on the server update the relay but don't push
		// a new catalog to existing subscribers. Without this, transitions
		// between sources of different resolutions/profiles (e.g., clip →
		// live) would reconfigure the decoder with a stale codec string,
		// causing WebCodecs to silently drop frames.
		if (description && !source.configured) {
			const codec = codecStringFromAvcC(description) || source.videoCodec || fallbackCodecString(source.videoWidth, source.videoHeight);
			const width = source.videoWidth || 1920;
			const height = source.videoHeight || 1080;
			source.lastDescription = description.slice(); // retained copy
			source.videoDecoder.configure(codec, width, height, description.slice().buffer as ArrayBuffer);
			source.configured = true;
		} else if (description && source.configured && !descriptionEqual(source.lastDescription, description)) {
			// Reconfigure: derive codec string from the NEW avcC, not stale catalog.
			const codec = codecStringFromAvcC(description) || source.videoCodec || fallbackCodecString(source.videoWidth, source.videoHeight);
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

		wanted.delete(key);
		cancelReconnect(key);
		clearConnectDeadline(key);
		upSince.delete(key);
		forgetMedia(key);

		// Clean up stall tracking
		lastFrameTime.delete(key);
		disconnectedWithFrames.delete(key);
		clearSourceStall(key);

		// Clean up transport
		if (source.transport) {
			source.transport.close();
			source.transport = null;
		}

		// Clean up all renderers and their buffers
		for (const renderer of source.renderers.values()) {
			renderer.destroy();
		}
		source.renderers.clear();
		for (const buf of source.buffers.values()) {
			buf.clear();
		}
		source.buffers.clear();

		// Clean up video pipeline
		source.videoDecoder.reset();

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
		wanted.add(key);
		if (source.transport) return; // already connected
		cancelReconnect(key);
		// A source that delivered frames before it was disconnected must
		// deliver again: count its stall window from the reconnect, after
		// the time the reconnect itself takes.
		if (disconnectedWithFrames.delete(key)) lastFrameTime.set(key, performance.now() + RECONNECT_GRACE_MS);
		openTransport(key, source);
	}

	function openTransport(key: string, source: SourceMedia): void {
		startStallRedials();
		const current = () => sources.get(key) === source && source.transport === transport;
		// program-preview gets high priority (16) so QUIC schedules it
		// ahead of multiview tiles (96) during bandwidth contention.
		const videoPriority = key === "program-preview" ? 16 : 96;
		const transport = new MoQTransport(key, makeCallbacks(key, current), videoPriority, config?.engineBaseUrl);
		source.transport = transport;
		clearConnectDeadline(key);
		connectDeadlines.set(key, setTimeout(() => {
			connectDeadlines.delete(key);
			if (!current()) return;
			console.warn(`[MediaPipeline] source "${key}" did not connect within ${CONNECT_TIMEOUT_MS} ms`);
			transportLost(key);
		}, CONNECT_TIMEOUT_MS));

		// Connect asynchronously. A transport closed on purpose meanwhile,
		// by disconnectSource or removeSource, rejects too; that is no
		// failure.
		transport.connect().catch((err) => {
			if (!current()) return;
			console.warn(`[MediaPipeline] Failed to connect source "${key}":`, err);
			transportLost(key);
		});
	}

	function disconnectSource(key: string): void {
		const source = sources.get(key);
		if (!source) return;
		wanted.delete(key);
		cancelReconnect(key);
		clearConnectDeadline(key);
		upSince.delete(key);
		forgetMedia(key);
		// A disconnected source receives no frames, so it must not read as
		// stalled; connectSource re-arms it.
		if (lastFrameTime.delete(key)) disconnectedWithFrames.add(key);
		if (!source.transport) return;

		source.transport.close();
		source.transport = null;

		// Flush decoder to prevent stale frames during failover
		if (source.videoDecoder) {
			try { source.videoDecoder.flush(); } catch { /* decoder may be in error state */ }
		}
	}

	function getAudioDecoder(sourceKey: string): PrismAudioDecoder | null {
		return sources.get(sourceKey)?.audioDecoder ?? null;
	}

	function attachCanvas(sourceKey: string, canvasId: string, canvas: HTMLCanvasElement): boolean {
		const source = sources.get(sourceKey);
		if (!source) return false;

		// Destroy existing renderer for this canvasId if any
		removeRenderer(source, canvasId);

		// Create audio clock from the source's audio decoder (or a no-op clock)
		const audioClock = {
			getPlaybackPTS(): number {
				return source.audioDecoder?.getPlaybackPTS() ?? -1;
			},
		};

		const buffer = new VideoRenderBuffer();
		source.buffers.set(canvasId, buffer);

		const renderer = new PrismRenderer(canvas, buffer, audioClock);
		// No freeRunOnly flag — audioClock dynamically returns -1 when audio
		// isn't ready yet, so the renderer naturally uses freerun mode and
		// transitions to audio-driven once the decoder finishes configuring.
		source.renderers.set(canvasId, renderer);
		renderer.start();
		updateVideoDecode(source);
		return true;
	}

	function detachCanvas(sourceKey: string, canvasId: string): void {
		const source = sources.get(sourceKey);
		if (!source) return;
		if (!removeRenderer(source, canvasId)) return;
		updateVideoDecode(source);
	}

	/** Destroy a renderer and close the frames its buffer holds. */
	function removeRenderer(source: SourceMedia, canvasId: string): boolean {
		const renderer = source.renderers.get(canvasId);
		if (!renderer) return false;
		renderer.destroy();
		source.renderers.delete(canvasId);
		source.buffers.get(canvasId)?.clear();
		source.buffers.delete(canvasId);
		return true;
	}

	/**
	 * Decode video only while it can be seen: the tab is visible and a
	 * canvas shows the source. A paused decoder drops compressed frames and
	 * resumes on the next keyframe, so a hidden tab or a source kept
	 * connected without a canvas (warm standby) neither decodes nor holds
	 * frames.
	 */
	function updateVideoDecode(source: SourceMedia): void {
		const paused = document.hidden || source.renderers.size === 0;
		if (paused === source.videoPaused) return;
		source.videoPaused = paused;
		if (paused) {
			source.videoDecoder.pause();
		} else {
			source.videoDecoder.resume();
		}
	}

	function startStallDetection(): void {
		if (stallCheckInterval !== null) return;
		stallCheckInterval = setInterval(() => {
			const now = performance.now();
			for (const [key, lastTime] of lastFrameTime) {
				// Only check sources that are still in the pipeline
				if (!sources.has(key)) continue;
				const elapsed = now - lastTime;
				if (elapsed > STALL_THRESHOLD_MS) {
					// Fire the callback only on the transition to stalled (not on every check)
					const wasAlreadyStalled = isSourceStalled(key);
					setSourceStalled(key);
					if (!wasAlreadyStalled) {
						config?.onSourceStalled?.(key);
					}
				}
			}
		}, STALL_CHECK_INTERVAL_MS);
	}

	function stopStallDetection(): void {
		if (stallCheckInterval !== null) {
			clearInterval(stallCheckInterval);
			stallCheckInterval = null;
		}
		clearAllStalls();
	}

	function destroy(): void {
		destroyed = true;
		stopStallDetection();
		document.removeEventListener("visibilitychange", handleVisibilityChange);
		window.removeEventListener("focus", handleWindowFocus);
		window.removeEventListener("online", handleOnline);
		for (const key of Array.from(reconnects.keys())) cancelReconnect(key);
		for (const key of Array.from(connectDeadlines.keys())) clearConnectDeadline(key);
		stopStallRedials();
		upSince.clear();
		lastMedia.clear();
		firstMedia.clear();
		liveMedia.clear();
		wanted.clear();
		for (const key of Array.from(sources.keys())) {
			removeSource(key);
		}
		unsubscribeMonitor?.();
		unsubscribeMonitor = null;
		releaseAudioHold?.();
		releaseAudioHold = null;
		monitorBus = null;
		programBus = null;
		void audioContext?.close();
		audioContext = null;
		lastFrameTime.clear();
		disconnectedWithFrames.clear();
	}

	function handleVisibilityChange(): void {
		const hidden = document.hidden;
		for (const source of sources.values()) {
			// Resuming flushes the decoder and clears every render buffer.
			updateVideoDecode(source);
			if (!hidden) {
				// Reset renderer sync so stale PTS doesn't corrupt timing
				for (const renderer of source.renderers.values()) {
					renderer.resetSync();
				}
			}
		}
		if (hidden) {
			// Pause stall detection while tab is hidden — no frames arrive
			// when decoders are paused, so stall detection would false-positive.
			if (stallCheckInterval !== null) {
				clearInterval(stallCheckInterval);
				stallCheckInterval = null;
			}
			stopStallRedials();
		} else {
			// Reset frame arrival times so resumed sources aren't immediately stalled
			const now = performance.now();
			for (const key of lastFrameTime.keys()) {
				lastFrameTime.set(key, now);
			}
			for (const key of lastMedia.keys()) lastMedia.set(key, now);
			if (wanted.size > 0) startStallRedials();
			// Clear any stalls that were set just before the tab was hidden
			clearAllStalls();
			// Restart stall detection
			startStallDetection();
		}
	}

	function handleWindowFocus(): void {
		// Snap all video buffers to live edge — discard stale frames
		// accumulated while the window was unfocused
		for (const source of sources.values()) {
			for (const buf of source.buffers.values()) {
				buf.takeNewestFrame().frame?.close();
			}
			// Reset renderer sync so PTS tracking starts fresh
			for (const renderer of source.renderers.values()) {
				renderer.resetSync();
			}
		}
		// Resume audio contexts that may have been deprioritized
		resumeAllAudio();
	}

	document.addEventListener("visibilitychange", handleVisibilityChange);
	window.addEventListener("focus", handleWindowFocus);
	window.addEventListener("online", handleOnline);

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

	/** Gain per source while unmuted, set before or after its decoder exists. */
	const sourceGains = new Map<string, number>();

	function setSourceGain(sourceKey: string, gain: number): void {
		sourceGains.set(sourceKey, gain);
		sources.get(sourceKey)?.audioDecoder?.setGain(gain);
	}

	/**
	 * Resume the shared AudioContext once the page has had a user gesture;
	 * before it, a resume only earns a browser warning. Each decoder flushes
	 * the audio it buffered while suspended as it resumes, and a context no
	 * decoder has attached to yet, created before the gesture, is resumed
	 * directly: no later call would reach it.
	 */
	async function resumeAllAudio(): Promise<void> {
		if (!audioMayStart()) return;
		const promises: Promise<void>[] = [];
		for (const source of sources.values()) {
			if (source.audioDecoder) {
				promises.push(source.audioDecoder.resumeContext());
			}
		}
		// One decoder failing to resume must not keep the context suspended.
		await Promise.allSettled(promises);
		if (audioContext && audioContext.state === 'suspended') await audioContext.resume();
	}

	function resetRendererSync(sourceKey: string): void {
		const source = sources.get(sourceKey);
		if (!source) return;
		for (const renderer of source.renderers.values()) {
			renderer.resetSync();
		}
	}

	function hasSource(sourceKey: string): boolean {
		return sources.has(sourceKey);
	}

	async function getAllDiagnostics(): Promise<Record<string, SourceDiagnostics>> {
		const result: Record<string, SourceDiagnostics> = {};
		for (const [key, source] of sources) {
			// Get diagnostics from first PrismRenderer (tile renderer)
			let rendererDiag: Record<string, unknown> | null = null;
			for (const renderer of source.renderers.values()) {
				rendererDiag = renderer.getDiagnostics() as unknown as Record<string, unknown>;
				break;
			}

			const transportDiag = (source.transport?.getDiagnostics() ?? null) as Record<string, unknown> | null;
			if (transportDiag) {
				transportDiag.groupSkips = source.groupSkips;
				transportDiag.latestGroupID = source.latestGroupID;
			}
			result[key] = {
				renderer: rendererDiag,
				videoDecoder: await source.videoDecoder.getDiagnostics() as unknown as Record<string, unknown>,
				audio: (source.audioDecoder?.getDiagnostics() ?? null) as Record<string, unknown> | null,
				transport: transportDiag,
			};
		}
		return result;
	}

	function getSourceTransport(sourceKey: string): WebTransport | null {
		const source = sources.get(sourceKey);
		return source?.transport?.getTransport() ?? null;
	}

	function getLastFrameTime(sourceKey: string): number {
		return lastFrameTime.get(sourceKey) ?? 0;
	}

	return {
		addSource,
		removeSource,
		connectSource,
		disconnectSource,
		getAudioDecoder,
		getSourceTransport,
		setSourceMuted,
		setSourceGain,
		resumeAllAudio,
		attachCanvas,
		detachCanvas,
		destroy,
		feedVideoFrame,
		feedAudioFrame,
		resetRendererSync,
		hasSource,
		getAllDiagnostics,
		startStallDetection,
		stopStallDetection,
		getLastFrameTime,
	};
}
