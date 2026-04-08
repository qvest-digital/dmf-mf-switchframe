/**
 * API base URL for HTTP/3 commands.
 * Empty string = same-origin (production).
 * Set to QUIC server origin for dev (e.g. "https://localhost:8080").
 */
let apiBaseUrl = '';

export function getApiBaseUrl(): string {
	return apiBaseUrl;
}

export function setApiBaseUrl(url: string): void {
	apiBaseUrl = url.replace(/\/$/, '');
}

/** Prepend the API base URL to a relative path. */
export function resolveApiUrl(path: string): string {
	return apiBaseUrl + path;
}

// ── Dual-engine endpoint management ──

export interface EngineEndpoint {
	id: string;
	url: string;
}

let engineUrls: EngineEndpoint[] | null = null;

/** Whether we expect dual-engine mode (set early, before engine B URL is known). */
let expectDualEngine = false;

export function setExpectDualEngine(value: boolean): void {
	expectDualEngine = value;
	if (!value) {
		pendingForNewEngine.length = 0;
	}
}

export function getExpectDualEngine(): boolean {
	return expectDualEngine;
}

// ── Flush timestamp injection ──
// Callback to inject PTP timestamps into flushed pending commands.
// Set by switch-api.ts to avoid circular imports (switch-api imports base-url).

export type FlushTimestampInjector = (path: string, body: string) => string;

let flushTimestampInjector: FlushTimestampInjector | null = null;

export function setFlushTimestampInjector(injector: FlushTimestampInjector | null): void {
	flushTimestampInjector = injector;
}

// Paths that require PTP execute_at timestamps for frame-accurate sync.
// Duplicated from switch-api.ts to avoid circular imports.
const TIMED_PATH_EXACT = new Set([
	'/api/switch/cut', '/api/switch/preview', '/api/switch/transition', '/api/switch/ftb',
	'/api/replay/play', '/api/replay/stop',
	'/api/scte35/cue', '/api/scte35/return', '/api/scte35/cancel',
	'/api/presets/recall',
	'/api/format',
	'/api/encoder',
]);

const TIMED_PATH_PATTERNS = [
	/^\/api\/graphics\/\d+\/(on|off)$/,
	/^\/api\/graphics\/\d+\/rect$/,
	/^\/api\/graphics\/\d+\/zorder$/,
	/^\/api\/presets\/[^/]+\/recall$/,
	/^\/api\/dve\/slots\/\d+\/(on|off)$/,
	/^\/api\/dve\/slots\/\d+$/,
	/^\/api\/clips\/players\/\d+\/(play|pause|stop)$/,
	/^\/api\/playout\/channels\/\d+\/(start|stop|next|previous)$/,
	/^\/api\/playout\/channels\/\d+\/pods\/[^/]+\/(fire|dump|hold|extend)$/,
	/^\/api\/audio\/.+$/,
	/^\/api\/macros\/[^/]+\/run$/,
	/^\/api\/sources\/[^/]+\/key$/,
	/^\/api\/captions\/(text|newline|clear)$/,
];

export function isTimedPath(path: string): boolean {
	if (TIMED_PATH_EXACT.has(path)) return true;
	return TIMED_PATH_PATTERNS.some(re => re.test(path));
}

/** Paths where retry after partial failure could cause duplicate side effects. */
const NON_IDEMPOTENT_PATHS = new Set([
	'/api/recording/start',
	'/api/recording/stop',
	'/api/output/srt/start',
	'/api/output/srt/stop',
	'/api/output/destinations',
]);

const NON_IDEMPOTENT_PATTERNS = [
	/^\/api\/macros\/[^/]+\/run$/,
	/^\/api\/scte35\/cue$/,
	/^\/api\/playout\/channels\/\d+\/pods\/[^/]+\/fire$/,
];

export function isNonIdempotent(path: string): boolean {
	if (NON_IDEMPOTENT_PATHS.has(path)) return true;
	return NON_IDEMPOTENT_PATTERNS.some(re => re.test(path));
}

// ── Authoritative engine provider for GET routing ──

let authoritativeEngineProvider: (() => string | null) | null = null;

export function setAuthoritativeEngineProvider(provider: (() => string | null) | null): void {
	authoritativeEngineProvider = provider;
}

export function getAuthoritativeEngine(): string | null {
	return authoritativeEngineProvider?.() ?? null;
}

// ── Pending command buffer for engine B discovery race ──

const PENDING_BUFFER_MAX = 10;
const PENDING_MAX_AGE_MS = 5_000;

interface PendingCommand {
	path: string;
	options: RequestInit;
	timestamp: number;
}

let pendingForNewEngine: PendingCommand[] = [];

/**
 * Queue a mutation command for replay when a new engine is discovered.
 * Only buffers when we expect dual-engine mode but only have one engine.
 * FIFO eviction at 10 items.
 */
export function bufferPendingCommand(path: string, options: RequestInit): void {
	if (!expectDualEngine) return;
	if (engineUrls && engineUrls.length > 1) return;

	pendingForNewEngine.push({ path, options, timestamp: Date.now() });

	// FIFO eviction: keep only the last PENDING_BUFFER_MAX items
	if (pendingForNewEngine.length > PENDING_BUFFER_MAX) {
		pendingForNewEngine = pendingForNewEngine.slice(-PENDING_BUFFER_MAX);
	}
}

// ── New engine discovery callback ──
// Called when a new engine is discovered (e.g., engine B URL becomes known).
// More reliable than command buffer replay: triggers a full snapshot push.

let onNewEngineDiscovered: ((engineId: string) => void) | null = null;

export function setOnNewEngineDiscovered(cb: ((engineId: string) => void) | null): void {
	onNewEngineDiscovered = cb;
}

export function setEngineUrls(urls: EngineEndpoint[] | null): void {
	const prevIds = new Set((engineUrls ?? []).map(e => e.id));
	engineUrls = urls?.map(e => ({ ...e, url: e.url.replace(/\/$/, '') })) ?? null;

	if (!engineUrls) return;

	// Find engines that weren't in the previous set.
	// When transitioning from null/single-engine to dual-engine, the
	// "primary" engine (A) was already receiving commands via same-origin
	// fetch, so we only notify for genuinely new engines.
	// When prevIds is empty (null→[A,B]), skip the first engine (A)
	// since it was the implicit single-engine target.
	const newEngines = prevIds.size > 0
		? engineUrls.filter(e => !prevIds.has(e.id))
		: engineUrls.slice(1);

	if (newEngines.length > 0) {
		// Discard pending command buffer — snapshot push is more reliable than replay
		pendingForNewEngine.length = 0;
		for (const engine of newEngines) {
			onNewEngineDiscovered?.(engine.id);
		}
	}
}

export function getEngineUrls(): EngineEndpoint[] | null {
	return engineUrls;
}
