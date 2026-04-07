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

export function setEngineUrls(urls: EngineEndpoint[] | null): void {
	const prevIds = new Set((engineUrls ?? []).map(e => e.id));
	engineUrls = urls?.map(e => ({ ...e, url: e.url.replace(/\/$/, '') })) ?? null;

	if (!engineUrls || pendingForNewEngine.length === 0) return;

	// Find engines that weren't in the previous set.
	// When transitioning from null/single-engine to dual-engine, the
	// "primary" engine (A) was already receiving commands via same-origin
	// fetch, so we only replay to genuinely new engines.
	// When prevIds is empty (null→[A,B]), skip the first engine (A)
	// since it was the implicit single-engine target.
	const newEngines = prevIds.size > 0
		? engineUrls.filter(e => !prevIds.has(e.id))
		: engineUrls.slice(1);

	if (newEngines.length > 0) {
		flushPendingCommands(newEngines);
	}
}

function flushPendingCommands(newEngines: EngineEndpoint[]): void {
	const commands = pendingForNewEngine.splice(0);
	const now = Date.now();

	for (const cmd of commands) {
		// Drop stale commands (older than 5 seconds)
		if (now - cmd.timestamp > PENDING_MAX_AGE_MS) continue;

		for (const engine of newEngines) {
			const fullUrl = `${engine.url}${cmd.path}`;
			fetch(fullUrl, cmd.options).catch(() => {});
		}
	}
}

export function getEngineUrls(): EngineEndpoint[] | null {
	return engineUrls;
}
