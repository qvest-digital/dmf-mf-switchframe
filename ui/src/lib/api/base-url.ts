import { untrack } from 'svelte';

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
	'/api/switch/transition/position',
	'/api/replay/play', '/api/replay/stop',
	'/api/replay/mark-in', '/api/replay/mark-out',
	'/api/scte35/cue', '/api/scte35/return', '/api/scte35/cancel',
	'/api/presets/recall',
	'/api/format',
	'/api/encoder',
	'/api/stingers',
	'/api/color-grade/lut',
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
	/^\/api\/captions\/(text|newline|clear|mode)$/,
	/^\/api\/graphics\/\d+\/image$/,
	/^\/api\/graphics\/\d+\/sequence$/,
	/^\/api\/stmaps\/[^/]+\/upload$/,
	/^\/api\/sources\/[^/]+\/ai-segment$/,
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
	/^\/api\/playout\/channels\/\d+\/pods\/[^/]+\/(fire|dump|hold|extend)$/,
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

/**
 * The engine the reconciler holds authoritative, read without subscribing:
 * every request reads it, and the reconciler's health changes on each state
 * snapshot, so a request issued from an effect would otherwise re-run that
 * effect, and issue the request again, on every snapshot.
 */
export function getAuthoritativeEngine(): string | null {
	return untrack(() => authoritativeEngineProvider?.() ?? null);
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
		for (const engine of newEngines) {
			onNewEngineDiscovered?.(engine.id);
		}
	}
}

export function getEngineUrls(): EngineEndpoint[] | null {
	return engineUrls;
}

// ── Emergency fallback URL for Engine B ──
// Set before full dual-engine wiring is established.
// Used by request()/dualFetch() to send commands when the primary engine is unreachable.

let emergencyEngineBUrl: string | null = null;

export function setEmergencyEngineBUrl(url: string | null): void {
	emergencyEngineBUrl = url?.replace(/\/$/, '') ?? null;
}

export function getEmergencyEngineBUrl(): string | null {
	return emergencyEngineBUrl;
}
