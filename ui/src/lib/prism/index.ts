// Vendored Prism TypeScript modules for Switchframe UI consumption.
// These files are copied from /Users/zsiec/dev/prism/web/src/ with minimal
// changes (only import fixes for verbatimModuleSyntax compatibility).

// --- MoQ Transport ---
export { MoQTransport } from "./moq-transport";
export type { MoQTransportCallbacks } from "./moq-transport";
export { MoQMultiviewTransport } from "./moq-multiview-transport";
export type { MoQMultiviewCallbacks } from "./moq-multiview-transport";

// --- MoQ Protocol Constants ---
export {
	MOQ_VERSION,
	readVarint,
	writeVarint,
} from "./moq-constants";
export type { LOCExtensions } from "./moq-constants";

// --- Transport Types ---
export type {
	TrackInfo,
	ServerAudioTrackStats,
	ServerViewerStats,
	ServerSCTE35Event,
	ServerStats,
} from "./transport";
export type { ServerInfo } from "./transport-utils";
export { fetchServerInfo, wtBaseURL, connectWebTransport } from "./transport-utils";
export { StreamBuffer } from "./stream-buffer";

// --- Protocol (captions) ---
export { parseCaptionData } from "./protocol";
export type {
	CaptionSpan,
	CaptionRow,
	CaptionRegion,
	CaptionData,
	ProtocolDiagnostics,
} from "./protocol";

// --- Video Decode ---
export { PrismVideoDecoder } from "./video-decoder";
export type { VideoDecoderDiagnostics } from "./video-decoder";

// --- Audio Decode ---
export { PrismAudioDecoder } from "./audio-decoder";
export type { AudioDiagnostics } from "./audio-decoder";

// --- Rendering ---
export { PrismRenderer } from "./renderer";
export type { RendererStats, RendererDiagnostics } from "./renderer";
export { VideoRenderBuffer } from "./video-render-buffer";
export { WebGPUCompositor } from "./webgpu-compositor";

// --- Player ---
export { PrismPlayer } from "./player";
export type { TilePerfStats } from "./player";

// --- Metrics ---
export { MetricsStore } from "./metrics-store";
export type {
	FrameEvent,
	VideoMetrics,
	AudioMetrics,
	SyncMetrics,
	TransportMetrics,
	CaptionMetrics,
	HealthStatus,
	StreamInfo,
	ErrorCounters,
} from "./metrics-store";

// --- Multiview Types ---
export type {
	MuxStreamEntry,
	MuxStreamCallbacks,
	MuxViewerStats,
} from "./multiview-types";

// --- Capabilities ---
export { checkCapabilities } from "./capabilities";
