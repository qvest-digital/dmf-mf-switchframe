/// <reference lib="webworker" />
declare const self: DedicatedWorkerGlobalScope;
export {};

// Bound the decode backlog by time, not by count: chunks arrive in bursts
// whenever the main thread or the network holds them back, and a burst of
// a few hundred milliseconds is 17 or more chunks at 50 fps. A chunk is
// dropped, and the decoder waits for the next keyframe, only when it is
// more than MAX_DECODE_LAG_US ahead of the last frame the decoder put out,
// or when MAX_QUEUED_CHUNKS are queued whatever their timestamps. The
// renderer's live-edge skip keeps what a burst decodes from adding latency.
const MAX_DECODE_LAG_US = 1_000_000;
const MAX_QUEUED_CHUNKS = 120;

let videoDecoder: VideoDecoder | null = null;
let waitForKeyframe = true;
/** Timestamp of the last frame the decoder put out, -1 before the first. */
let lastOutputTs = -1;
/** Timestamp of the last chunk handed to the worker, -1 before the first. */
let lastInputTs = -1;
let discardedDelta = 0;
let discardedBufferFull = 0;

let diagFrameCount = 0;
let diagDecodeCount = 0;
let diagKeyframeCount = 0;
let diagDecodeErrors = 0;
let diagTotalDiscardedDelta = 0;
let diagTotalDiscardedFull = 0;
let diagLastOutputTime = 0;
let diagOutputIntervalSum = 0;
let diagOutputIntervalMax = 0;
let diagOutputIntervalMin = Infinity;
let diagLastInputTime = 0;
let diagInputIntervalSum = 0;
let diagInputIntervalMax = 0;
let diagInputIntervalMin = Infinity;
let diagFirstInputTime = 0;
let diagLastPTS = -1;
let diagPtsJumps = 0;

// Lifetime counters — survive reconfigures for session-wide visibility.
let lifetimeInputCount = 0;
let lifetimeOutputCount = 0;
let lifetimeKeyframeCount = 0;
let lifetimeDecodeErrors = 0;
let lifetimeDiscardedDelta = 0;
let lifetimeDiscardedBufferFull = 0;
let lifetimeConfigureCount = 0;
let lifetimeConfigGuardDrops = 0;

let lastConfigCodec = "";
let lastConfigWidth = 0;
let lastConfigHeight = 0;
let lastDescription: ArrayBuffer | null = null;

function createDecoder(): VideoDecoder {
	const decoder = new VideoDecoder({
		output: (frame) => processVideoFrame(frame),
		error: (err) => {
			diagDecodeErrors++;
			lifetimeDecodeErrors++;
			waitForKeyframe = true;
			self.postMessage({ type: "error", message: err.message });
			recoverDecoder();
		},
	});
	const config: VideoDecoderConfig = {
		codec: lastConfigCodec,
		codedWidth: lastConfigWidth,
		codedHeight: lastConfigHeight,
		optimizeForLatency: true,
	};
	if (lastDescription) {
		config.description = lastDescription;
	}
	decoder.configure(config);
	return decoder;
}

let recovering = false;

function recoverDecoder(): void {
	if (!lastConfigCodec || recovering) return;
	recovering = true;
	// The reset decoder has put out nothing yet: its lag counts from its
	// first frame, not from one the old decoder put out.
	lastOutputTs = -1;
	try {
		// Prefer reset() over close()+new — it's faster because it reuses the
		// underlying hardware decoder context instead of tearing it down.
		if (videoDecoder && videoDecoder.state !== "closed") {
			videoDecoder.reset();
			const config: VideoDecoderConfig = {
				codec: lastConfigCodec,
				codedWidth: lastConfigWidth,
				codedHeight: lastConfigHeight,
				hardwareAcceleration: "prefer-software",
				optimizeForLatency: true,
			};
			if (lastDescription) config.description = lastDescription;
			videoDecoder.configure(config);
		} else {
			videoDecoder = createDecoder();
		}
	} catch {
		// reset() failed — fall back to full recreation
		try { if (videoDecoder) videoDecoder.close(); } catch { /* */ }
		videoDecoder = createDecoder();
	}
	recovering = false;
}

function processVideoFrame(frame: VideoFrame): void {
	lastOutputTs = frame.timestamp;
	diagFrameCount++;
	lifetimeOutputCount++;
	const now = performance.now();
	if (diagLastOutputTime > 0) {
		const interval = now - diagLastOutputTime;
		diagOutputIntervalSum += interval;
		if (interval > diagOutputIntervalMax) diagOutputIntervalMax = interval;
		if (interval < diagOutputIntervalMin) diagOutputIntervalMin = interval;
	}
	diagLastOutputTime = now;
	self.postMessage({ type: "frame", frame }, { transfer: [frame] });
}

self.onmessage = async (e: MessageEvent) => {
	const msg = e.data;

	if (msg.type === "configure") {
		// Close synchronously — no flush. We don't need in-flight frames
		// from the old codec config (SPS/PPS changed), and flush() is async
		// which yields to the event loop, allowing queued decode messages
		// to race with the reconfigure.
		if (videoDecoder && videoDecoder.state !== "closed") {
			try { videoDecoder.close(); } catch { /* already closed */ }
		}

		lastConfigCodec = msg.codec;
		lastConfigWidth = msg.width;
		lastConfigHeight = msg.height;
		lastDescription = msg.description ?? null;

		videoDecoder = createDecoder();
		lifetimeConfigureCount++;
		waitForKeyframe = true;
		lastOutputTs = -1;
		discardedDelta = 0;
		discardedBufferFull = 0;
		diagFrameCount = 0;
		diagDecodeCount = 0;
		diagKeyframeCount = 0;
		diagDecodeErrors = 0;
		diagTotalDiscardedDelta = 0;
		diagTotalDiscardedFull = 0;
		diagLastOutputTime = 0;
		diagOutputIntervalSum = 0;
		diagOutputIntervalMax = 0;
		diagOutputIntervalMin = Infinity;
		diagLastInputTime = 0;
		diagInputIntervalSum = 0;
		diagInputIntervalMax = 0;
		diagInputIntervalMin = Infinity;
		diagFirstInputTime = 0;
		diagLastPTS = -1;
		diagPtsJumps = 0;

		self.postMessage({ type: "configured" });
	} else if (msg.type === "decode") {
		if (!videoDecoder || videoDecoder.state !== "configured") {
			lifetimeConfigGuardDrops++;
			return;
		}

		const inputNow = performance.now();
		if (diagFirstInputTime === 0) diagFirstInputTime = inputNow;
		diagDecodeCount++;
		lifetimeInputCount++;
		if (diagLastInputTime > 0) {
			const interval = inputNow - diagLastInputTime;
			diagInputIntervalSum += interval;
			if (interval > diagInputIntervalMax) diagInputIntervalMax = interval;
			if (interval < diagInputIntervalMin) diagInputIntervalMin = interval;
		}
		diagLastInputTime = inputNow;

		// A discontinuity starts a new timeline: a timestamp on it says
		// nothing about lag against the last frame of the old one.
		if (msg.isDisco) {
			waitForKeyframe = true;
			lastOutputTs = -1;
		}
		// Neither does a stream resuming after a gap, as an input that was
		// off or moved to another flow does: its timestamps run ahead by the
		// gap, with nothing of the old stream left queued to lag behind.
		if (lastInputTs >= 0 && msg.timestamp - lastInputTs > MAX_DECODE_LAG_US) {
			lastOutputTs = -1;
		}
		lastInputTs = msg.timestamp;

		const lagging = lastOutputTs >= 0 && videoDecoder.decodeQueueSize > 0 &&
			msg.timestamp - lastOutputTs > MAX_DECODE_LAG_US;
		if (lagging || videoDecoder.decodeQueueSize >= MAX_QUEUED_CHUNKS) {
			discardedBufferFull++;
			diagTotalDiscardedFull++;
			lifetimeDiscardedBufferFull++;
			// Subsequent deltas would reference this dropped frame and cause
			// decode errors, so skip ahead to the next keyframe immediately
			// rather than triggering an expensive error → recovery cycle.
			waitForKeyframe = true;
			return;
		}

		if (discardedBufferFull > 0) {
			self.postMessage({ type: "warning", message: `Discarded ${discardedBufferFull} video chunks (buffer full)` });
			discardedBufferFull = 0;
		}

		const isKeyframe: boolean = msg.isKeyframe;
		if (waitForKeyframe && !isKeyframe) {
			discardedDelta++;
			diagTotalDiscardedDelta++;
			lifetimeDiscardedDelta++;
			return;
		}

		if (discardedDelta > 0) {
			self.postMessage({ type: "warning", message: `Discarded ${discardedDelta} delta frames before key` });
			discardedDelta = 0;
		}
		// The keyframe waited for starts afresh: the last frame put out lies
		// before the chunks dropped, a GOP or more back, and the deltas that
		// follow the keyframe before it is put out would all read as lagging
		// against it, leaving one picture per keyframe for good.
		if (waitForKeyframe) lastOutputTs = -1;
		waitForKeyframe = false;
		if (isKeyframe) {
			diagKeyframeCount++;
			lifetimeKeyframeCount++;
		}

		const ts: number = msg.timestamp;
		if (diagLastPTS >= 0) {
			const gap = Math.abs(ts - diagLastPTS);
			// 500ms threshold: high enough to ignore B-frame reordering
			// (~66ms gaps at 30fps) but catches real discontinuities.
			// Tracked for diagnostics only — keyframes reset decoder state
			// naturally, so no flush or waitForKeyframe needed here.
			if (gap > 500_000) diagPtsJumps++;
		}
		diagLastPTS = ts;

		try {
			videoDecoder.decode(new EncodedVideoChunk({
				type: isKeyframe ? "key" : "delta",
				timestamp: ts,
				data: msg.data,
			}));
		} catch {
			// A sync throw often precedes an async error if we continue feeding
			// frames from the same damaged sequence. Skip to the next keyframe
			// to avoid cascading into a more expensive async recovery.
			waitForKeyframe = true;
			diagDecodeErrors++;
			lifetimeDecodeErrors++;
		}
	} else if (msg.type === "getDiagnostics") {
		const avgInputInterval = diagDecodeCount > 1
			? diagInputIntervalSum / (diagDecodeCount - 1) : 0;
		const avgOutputInterval = diagFrameCount > 1
			? diagOutputIntervalSum / (diagFrameCount - 1) : 0;
		self.postMessage({
			type: "diagnostics",
			data: {
				inputCount: diagDecodeCount,
				outputCount: diagFrameCount,
				keyframeCount: diagKeyframeCount,
				decodeErrors: diagDecodeErrors,
				discardedDelta: diagTotalDiscardedDelta,
				discardedBufferFull: diagTotalDiscardedFull,
				decodeQueueSize: videoDecoder?.decodeQueueSize ?? 0,
				avgInputIntervalMs: avgInputInterval,
				minInputIntervalMs: diagInputIntervalMin === Infinity ? 0 : diagInputIntervalMin,
				maxInputIntervalMs: diagInputIntervalMax,
				avgOutputIntervalMs: avgOutputInterval,
				minOutputIntervalMs: diagOutputIntervalMin === Infinity ? 0 : diagOutputIntervalMin,
				maxOutputIntervalMs: diagOutputIntervalMax,
				inputFps: diagFirstInputTime > 0
					? diagDecodeCount / ((performance.now() - diagFirstInputTime) / 1000) : 0,
				outputFps: diagLastOutputTime > 0 && diagFrameCount > 1
					? (diagFrameCount - 1) / (diagOutputIntervalSum / 1000) : 0,
				ptsJumps: diagPtsJumps,
				lifetimeInputCount,
				lifetimeOutputCount,
				lifetimeKeyframeCount,
				lifetimeDecodeErrors,
				lifetimeDiscardedDelta,
				lifetimeDiscardedBufferFull,
				lifetimeConfigureCount,
				lifetimeConfigGuardDrops,
			},
		});
	} else if (msg.type === "flush") {
		waitForKeyframe = true;
		lastOutputTs = -1;
		// Reset the decoder's internal state so stale reference frames
		// from before the pause don't cause artifacts.
		if (videoDecoder && videoDecoder.state === "configured") {
			try {
				await videoDecoder.flush();
			} catch { /* ignore */ }
		}
	} else if (msg.type === "stop") {
		if (videoDecoder) {
			try {
				await videoDecoder.flush();
				videoDecoder.close();
			} catch {
				// ignore
			}
			videoDecoder = null;
		}
		waitForKeyframe = true;
		discardedDelta = 0;
		discardedBufferFull = 0;
	}
};
