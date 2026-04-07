import type { ControlRoomState, SourceInfo, RecordingStatus, SRTOutputConfig, SRTOutputStatus, Preset, RecallPresetResponse, GraphicsState, GraphicsLayerState, EQBand, CompressorSettings, GateSettings, Macro, KeyConfig, ReplayState, ReplayBufferInfo, OperatorRole, OperatorInfo, DestinationConfig, DestinationStatus, EasingConfig, PipelineFormatInfo, EncoderState, SCTE35CueRequest, SCTE35State, SCTE35Event, SCTE35Rule, CaptionState, CaptionMode, ClipPlayerState, ClipInfo, RecordingFileInfo, EngineRecordingGroup, CreateSRTSourceConfig, SRTSourceStats, CommsState, STMapState, STMapInfo, STMapGeneratorInfo, STMapGenerateRequest, AISegmentConfig, ColorGradeState, CorrectorState, PlayoutChannelState, Playlist, PlaylistItem, CacheStatus, PodInfo, PodConfig, PodTemplate, AsRunEvent, ChannelHealth, SampleClip, PlayoutClip, ASRState, DVEPresetInfo, WipePattern, WipeConfig } from './types';
import { notify } from '$lib/state/notifications.svelte';
import { resolveApiUrl, getEngineUrls, type EngineEndpoint } from './base-url';

export class SwitchApiError extends Error {
	constructor(
		public status: number,
		message: string,
	) {
		super(message);
		this.name = 'SwitchApiError';
	}
}

function getAuthToken(): string | null {
	if (typeof localStorage === 'undefined') return null;
	return localStorage.getItem('switchframe_operator_token');
}

export function setAuthToken(token: string): void {
	localStorage.setItem('switchframe_operator_token', token);
}

/**
 * Check URL fragment for token handoff from control plane.
 * If #token=xxx is present, store it and clear the fragment.
 */
export function checkFragmentToken(): void {
	if (typeof window === 'undefined') return;
	const hash = window.location.hash;
	if (!hash.startsWith('#token=')) return;

	const token = hash.slice('#token='.length);
	if (token) {
		setAuthToken(token);
		// Clear fragment from URL bar without triggering navigation
		history.replaceState(null, '', window.location.pathname + window.location.search);
	}
}

export function authHeaders(): Record<string, string> {
	const token = getAuthToken();
	const headers: Record<string, string> = {};
	if (token) {
		headers['Authorization'] = `Bearer ${token}`;
	}
	return headers;
}

// ── Circuit breaker: per-engine failure tracking ──

interface CircuitState {
	failures: number;
	firstFailure: number;
	openUntil: number; // 0 = closed
}
const circuitBreakers = new Map<string, CircuitState>();

const CIRCUIT_FAILURE_THRESHOLD = 5;
const CIRCUIT_FAILURE_WINDOW_MS = 30_000;
const CIRCUIT_OPEN_DURATION_MS = 30_000;

function isCircuitOpen(engineUrl: string): boolean {
	const state = circuitBreakers.get(engineUrl);
	if (!state) return false;
	if (state.openUntil > 0) {
		if (Date.now() < state.openUntil) return true;
		// Half-open: allow one probe
		state.openUntil = 0;
		state.failures = 0;
		state.firstFailure = 0;
	}
	return false;
}

function recordFailure(engineUrl: string): void {
	let state = circuitBreakers.get(engineUrl);
	if (!state) {
		state = { failures: 0, firstFailure: Date.now(), openUntil: 0 };
		circuitBreakers.set(engineUrl, state);
	}
	// Reset window if expired
	if (Date.now() - state.firstFailure > CIRCUIT_FAILURE_WINDOW_MS) {
		state.failures = 0;
		state.firstFailure = Date.now();
	}
	state.failures++;
	if (state.failures >= CIRCUIT_FAILURE_THRESHOLD) {
		state.openUntil = Date.now() + CIRCUIT_OPEN_DURATION_MS;
	}
}

function recordSuccess(engineUrl: string): void {
	circuitBreakers.delete(engineUrl);
}

// ── Dual-engine PTP timestamp injection ──

let dualTimestampProvider: (() => { execute_at: number; command_seq: number }) | null = null;

export function setDualTimestampProvider(provider: (() => { execute_at: number; command_seq: number }) | null): void {
	dualTimestampProvider = provider;
}

const TIMED_PATH_EXACT = new Set([
	'/api/switch/cut', '/api/switch/preview', '/api/switch/transition', '/api/switch/ftb',
	'/api/replay/play', '/api/replay/stop',
	'/api/scte35/cue', '/api/scte35/return', '/api/scte35/cancel',
	'/api/presets/recall',
]);

const TIMED_PATH_PATTERNS = [
	/^\/api\/graphics\/\d+\/(on|off)$/,
	/^\/api\/presets\/[^/]+\/recall$/,
	/^\/api\/dve\/slots\/\d+\/(on|off)$/,
	/^\/api\/clips\/players\/\d+\/(play|pause|stop)$/,
	/^\/api\/playout\/channels\/\d+\/(start|stop|next|previous)$/,
	/^\/api\/playout\/channels\/\d+\/pods\/[^/]+\/(fire|dump|hold|extend)$/,
];

/**
 * Returns true for endpoints that require PTP execute_at timestamps
 * for frame-accurate synchronization across engines.
 */
function isTimedPath(path: string): boolean {
	if (TIMED_PATH_EXACT.has(path)) return true;
	return TIMED_PATH_PATTERNS.some(re => re.test(path));
}

// ── Dual-engine fan-out ──
//
// ┌─────────────────────────────────────────────────────────────┐
// │                  Dual-Engine Request Flow                   │
// │                                                             │
// │  253 exported API functions                                 │
// │       │                                                     │
// │       ├─ JSON APIs ──► request() ──► dualRequest()          │
// │       │                   │              │                  │
// │       │                   │         GET/HEAD? ──► primaryWithFallback()
// │       │                   │              │        (try A, fall back to B)
// │       │                   │              │                  │
// │       │                   │         POST/PUT/DELETE? ──► allSettled fan-out
// │       │                   │              (both engines, PTP timestamps  │
// │       │                   │               for timed paths)  │
// │       │                   │                                 │
// │       │              single engine? ──► normal fetch        │
// │       │                                                     │
// │       └─ Raw/FormData ──► dualFetch()                      │
// │                              │                              │
// │                         Same GET vs POST logic              │
// │                         Returns raw Response                │
// │                         (for blobs, streams, FormData)      │
// │                                                             │
// │  request()  = JSON parse + error handling (most API calls)  │
// │  dualFetch() = raw Response (uploads, downloads, blobs)     │
// └─────────────────────────────────────────────────────────────┘

/**
 * Sequential engine fallback for read-only requests.
 * Tries each engine in order, returns first successful response.
 */
async function primaryWithFallback<T>(url: string, options: RequestInit | undefined, engines: EngineEndpoint[]): Promise<T> {
	const opts: RequestInit = { ...options, headers: { ...authHeaders(), ...options?.headers } };

	// Filter out engines with open circuits
	let availableEngines = engines.filter(e => !isCircuitOpen(e.url));
	if (availableEngines.length === 0) {
		// All circuits open — try all engines anyway (half-open probe)
		availableEngines = [...engines];
	}

	let lastHttpError: { status: number; body: Record<string, string> } | null = null;

	for (const engine of availableEngines) {
		try {
			const res = await fetch(`${engine.url}${url}`, opts);
			if (res.ok) {
				recordSuccess(engine.url);
				if (res.status === 202 || res.status === 204 || res.headers?.get('content-length') === '0') {
					return undefined as T;
				}
				return res.json();
			}
			// HTTP error — record it and try next engine
			recordFailure(engine.url);
			const body = await res.json().catch(() => ({ error: 'unknown error' }));
			lastHttpError = { status: res.status, body };
		} catch {
			// Network error — try next engine
			recordFailure(engine.url);
		}
	}

	// All engines failed
	if (lastHttpError) {
		throw new SwitchApiError(lastHttpError.status, lastHttpError.body.error || `HTTP ${lastHttpError.status}`);
	}
	throw new Error(`All engines failed for GET ${url}`);
}

// ── Engine-scoped output routing ──

const ENGINE_SCOPED_PATHS = new Set([
	'/api/output/destinations',
	'/api/output/recording/start',
	'/api/output/recording/stop',
]);

function isEngineScopedPath(path: string): boolean {
	if (ENGINE_SCOPED_PATHS.has(path)) return true;
	// Match /api/output/destinations/{id}/start, /stop, /engine
	return /^\/api\/output\/destinations\/[^/]+\/(start|stop|engine)$/.test(path);
}

/**
 * Send a request to a specific engine by ID.
 */
async function targetEngine<T>(url: string, options: RequestInit | undefined, engines: EngineEndpoint[], engineId: string): Promise<T> {
	const engine = engines.find(e => e.id.toLowerCase() === engineId.toLowerCase());
	if (!engine) {
		throw new Error(`Engine ${engineId} not found`);
	}
	const opts: RequestInit = { ...options, headers: { ...authHeaders(), ...options?.headers } };
	const res = await fetch(`${engine.url}${url}`, opts);
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	if (res.status === 202 || res.status === 204 || res.headers?.get('content-length') === '0') {
		return undefined as T;
	}
	return res.json();
}

/**
 * JSON API fan-out. Called by request() when dual-engine is active.
 * Routes GET/HEAD to primaryWithFallback, mutations to allSettled fan-out.
 * Engine-scoped output paths are routed to a specific engine when the
 * request body contains an engine field.
 */
async function dualRequest<T>(url: string, options: RequestInit | undefined, engines: EngineEndpoint[]): Promise<T> {
	const method = (options?.method ?? 'GET').toUpperCase();

	// Read-only requests: primary engine with fallback, not parallel fan-out.
	if (method === 'GET' || method === 'HEAD') {
		return primaryWithFallback<T>(url, options, engines);
	}

	// Engine-scoped paths: route to specific engine if body contains engine field
	if (isEngineScopedPath(url) && options?.body && typeof options.body === 'string') {
		try {
			const parsed = JSON.parse(options.body);
			if (parsed.engine && engines.length > 1) {
				return targetEngine<T>(url, options, engines, parsed.engine);
			}
		} catch { /* fall through to fan-out */ }
	}

	// Mutations: fan out to all engines in parallel
	const opts: RequestInit = { ...options, headers: { ...authHeaders(), ...options?.headers } };

	// Filter out engines with open circuits for mutations
	let availableEngines = engines.filter(e => !isCircuitOpen(e.url));
	if (availableEngines.length === 0) {
		// All circuits open — try all engines anyway (half-open probe)
		availableEngines = [...engines];
	}

	// Add PTP timestamps for timed commands
	if (isTimedPath(url) && dualTimestampProvider && opts.body && typeof opts.body === 'string') {
		try {
			const ts = dualTimestampProvider();
			const parsed = JSON.parse(opts.body);
			opts.body = JSON.stringify({ ...parsed, ...ts });
		} catch {
			// Clock sync not ready or body parse failed — send without timestamps
		}
	}

	// Clone FormData/Blob if needed (each can only be consumed once by fetch)
	const bodies: (BodyInit | null | undefined)[] = availableEngines.map(() => {
		if (opts.body instanceof FormData) {
			const clone = new FormData();
			for (const [key, value] of (opts.body as FormData).entries()) {
				clone.append(key, value);
			}
			return clone;
		}
		if (opts.body instanceof Blob) {
			return opts.body.slice();
		}
		return opts.body;
	});

	const results = await Promise.allSettled(
		availableEngines.map((engine, i) =>
			fetch(`${engine.url}${url}`, { ...opts, body: bodies[i] })
		)
	);

	// Track circuit breaker state for each engine
	results.forEach((result, i) => {
		const engineUrl = availableEngines[i]?.url;
		if (!engineUrl) return;
		if (result.status === 'fulfilled' && result.value.ok) {
			recordSuccess(engineUrl);
		} else {
			recordFailure(engineUrl);
		}
	});

	// Find successful responses
	const fulfilled = results
		.filter((r): r is PromiseFulfilledResult<Response> => r.status === 'fulfilled');
	const successful = fulfilled.filter(r => r.value.ok);

	if (successful.length > 0) {
		// Return first successful response
		const res = successful[0].value;
		if (res.status === 202 || res.status === 204 || res.headers?.get('content-length') === '0') {
			return undefined as T;
		}
		return res.json();
	}

	// All failed
	if (fulfilled.length > 0) {
		const res = fulfilled[0].value;
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}

	// All rejected (network errors)
	const reasons = results
		.filter((r): r is PromiseRejectedResult => r.status === 'rejected')
		.map(r => r.reason?.message ?? String(r.reason));
	throw new Error(`All engines unreachable: ${reasons.join('; ')}`);
}

/**
 * Raw Response fan-out for non-JSON APIs (uploads, downloads).
 * Same GET-vs-mutation routing as dualRequest but returns Response
 * instead of parsed JSON.
 */
async function dualFetch(url: string, options: RequestInit): Promise<Response> {
	const engines = getEngineUrls();
	if (engines && engines.length > 1) {
		const method = (options.method ?? 'GET').toUpperCase();
		const opts: RequestInit = { ...options, headers: { ...authHeaders(), ...options.headers } };

		// Read-only requests: primary with fallback
		if (method === 'GET' || method === 'HEAD') {
			let lastResponse: Response | null = null;
			for (const engine of engines) {
				try {
					const res = await fetch(`${engine.url}${url}`, opts);
					if (res.ok) return res;
					lastResponse = res;
				} catch {
					// Network error — try next engine
				}
			}
			// Return the last non-OK response so callers can inspect status,
			// rather than throwing a generic Error that loses HTTP context.
			if (lastResponse) return lastResponse;
			throw new Error(`All engines unreachable for ${method} ${url}`);
		}

		// Mutations: fan out to all engines in parallel
		const bodies = engines.map(() => {
			if (options.body instanceof FormData) {
				const clone = new FormData();
				for (const [key, value] of (options.body as FormData).entries()) {
					clone.append(key, value);
				}
				return clone;
			}
			if (options.body instanceof Blob) {
				return options.body.slice();
			}
			return options.body;
		});
		const results = await Promise.allSettled(
			engines.map((engine, i) =>
				fetch(`${engine.url}${url}`, { ...opts, body: bodies[i] })
			)
		);
		const fulfilled = results.filter((r): r is PromiseFulfilledResult<Response> => r.status === 'fulfilled');
		const successful = fulfilled.filter(r => r.value.ok);
		if (successful.length > 0) return successful[0].value;
		if (fulfilled.length > 0) return fulfilled[0].value;
		throw new Error('All engines unreachable');
	}
	return fetch(resolveApiUrl(url), { ...options, headers: { ...authHeaders(), ...options.headers } });
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
	const engines = getEngineUrls();
	if (engines && engines.length > 1) {
		return dualRequest<T>(url, options, engines);
	}
	// Single-engine: existing behavior unchanged
	const opts: RequestInit = {
		...options,
		headers: { ...authHeaders(), ...options?.headers },
	};
	const res = await fetch(resolveApiUrl(url), opts);
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	if (res.status === 202 || res.status === 204 || res.headers?.get('content-length') === '0') {
		return undefined as T;
	}
	return res.json();
}

function post<T>(url: string, body: unknown): Promise<T> {
	return request<T>(url, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body),
	});
}

function patch<T>(url: string, body: unknown): Promise<T> {
	return request<T>(url, {
		method: 'PATCH',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body),
	});
}

export function cut(source: string): Promise<ControlRoomState> {
	return post('/api/switch/cut', { source });
}

export function setPreview(source: string): Promise<ControlRoomState> {
	return post('/api/switch/preview', { source });
}

export function setLabel(key: string, label: string): Promise<ControlRoomState> {
	return post(`/api/sources/${encodeURIComponent(key)}/label`, { label });
}

export function setSourceDelay(key: string, delayMs: number): Promise<ControlRoomState> {
	return post(`/api/sources/${encodeURIComponent(key)}/delay`, { delayMs });
}

export function setAudioDelay(source: string, delayMs: number): Promise<ControlRoomState> {
	return request(`/api/audio/${encodeURIComponent(source)}/audio-delay`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ delayMs }),
	});
}

export function getState(): Promise<ControlRoomState> {
	return request('/api/switch/state');
}

export function getSources(): Promise<Record<string, SourceInfo>> {
	return request('/api/sources');
}

export function createSRTSource(config: CreateSRTSourceConfig): Promise<{ key: string }> {
	return request('/api/sources', {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(config),
	});
}

export function deleteSRTSource(key: string): Promise<void> {
	return request(`/api/sources/${encodeURIComponent(key)}`, {
		method: 'DELETE',
	});
}

export function getSRTSourceStats(key: string): Promise<SRTSourceStats> {
	return request(`/api/sources/${encodeURIComponent(key)}/srt/stats`);
}

export function updateSRTLatency(key: string, latencyMs: number): Promise<void> {
	return request(`/api/sources/${encodeURIComponent(key)}/srt`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ latencyMs }),
	});
}

export function setTrim(source: string, trim: number): Promise<ControlRoomState> {
	return post('/api/audio/trim', { source, trim });
}

export function setLevel(source: string, level: number): Promise<ControlRoomState> {
	return post('/api/audio/level', { source, level });
}

export function setMute(source: string, muted: boolean): Promise<ControlRoomState> {
	return post('/api/audio/mute', { source, muted });
}

export function setAFV(source: string, afv: boolean): Promise<ControlRoomState> {
	return post('/api/audio/afv', { source, afv });
}

export function setPhaseInvert(source: string, invert: boolean): Promise<ControlRoomState> {
	return post('/api/audio/phase', { source, invert });
}

export function setBalance(source: string, balance: number): Promise<ControlRoomState> {
	return post('/api/audio/balance', { source, balance });
}

export function setMasterLevel(level: number): Promise<ControlRoomState> {
	return post('/api/audio/master', { level });
}

export function setLimiter(threshold: number, ceiling: number): Promise<ControlRoomState> {
	return request('/api/audio/limiter', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ threshold, ceiling }),
	});
}

export function getLimiter(): Promise<{ threshold: number; ceiling: number; gainReduction: number }> {
	return request('/api/audio/limiter');
}

export function startTransition(source: string, type: string, durationMs: number, wipeDirection?: string, stingerName?: string, easing?: EasingConfig, wipeConfig?: WipeConfig): Promise<ControlRoomState> {
	const body: Record<string, unknown> = { source, type, durationMs };
	if (wipeDirection) {
		body.wipeDirection = wipeDirection;
	}
	if (wipeConfig) {
		body.wipeConfig = wipeConfig;
	}
	if (stingerName) {
		body.stingerName = stingerName;
	}
	if (easing) {
		body.easing = easing;
	}
	return post('/api/switch/transition', body);
}

export function setTransitionPosition(position: number): Promise<void> {
	return post('/api/switch/transition/position', { position });
}

export function fadeToBlack(): Promise<ControlRoomState> {
	return post('/api/switch/ftb', {});
}

export function startRecording(options?: {
	outputDir?: string;
	rotateAfterMins?: number;
	maxFileSizeMB?: number;
}): Promise<RecordingStatus> {
	return post('/api/recording/start', options ?? {});
}

export function stopRecording(): Promise<RecordingStatus> {
	return post('/api/recording/stop', {});
}

export function getRecordingStatus(): Promise<RecordingStatus> {
	return request('/api/recording/status');
}

export function startSRTOutput(config: SRTOutputConfig): Promise<SRTOutputStatus> {
	return post('/api/output/srt/start', config);
}

export function stopSRTOutput(): Promise<SRTOutputStatus> {
	return post('/api/output/srt/stop', {});
}

export function getSRTOutputStatus(): Promise<SRTOutputStatus> {
	return request('/api/output/srt/status');
}

// --- Multi-Destination API ---

export function addDestination(config: DestinationConfig): Promise<DestinationStatus> {
	return request('/api/output/destinations', {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(config),
	});
}

export function listDestinations(): Promise<DestinationStatus[]> {
	return request('/api/output/destinations');
}

export function getDestination(id: string): Promise<DestinationStatus> {
	return request(`/api/output/destinations/${encodeURIComponent(id)}`);
}

export function removeDestination(id: string): Promise<void> {
	return request(`/api/output/destinations/${encodeURIComponent(id)}`, {
		method: 'DELETE',
	});
}

export function startDestination(id: string): Promise<DestinationStatus> {
	return post(`/api/output/destinations/${encodeURIComponent(id)}/start`, {});
}

export function stopDestination(id: string): Promise<DestinationStatus> {
	return post(`/api/output/destinations/${encodeURIComponent(id)}/stop`, {});
}

export function reassignDestinationEngine(id: string, engine: string): Promise<DestinationStatus> {
	return request(`/api/output/destinations/${encodeURIComponent(id)}/engine`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ engine }),
	});
}

// --- Preset API ---

export function listPresets(): Promise<Preset[]> {
	return request('/api/presets');
}

export function createPreset(name: string): Promise<Preset> {
	return post('/api/presets', { name });
}

export function getPreset(id: string): Promise<Preset> {
	return request(`/api/presets/${encodeURIComponent(id)}`);
}

export function updatePreset(id: string, name: string): Promise<Preset> {
	return request(`/api/presets/${encodeURIComponent(id)}`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ name }),
	});
}

export function deletePreset(id: string): Promise<void> {
	return request(`/api/presets/${encodeURIComponent(id)}`, {
		method: 'DELETE',
	});
}

export function deletePresetByName(name: string): Promise<void> {
	return request(`/api/presets/by-name/${encodeURIComponent(name)}`, {
		method: 'DELETE',
	});
}

export function recallPreset(id: string): Promise<RecallPresetResponse> {
	return post(`/api/presets/${encodeURIComponent(id)}/recall`, {});
}

export function recallPresetByName(name: string): Promise<RecallPresetResponse> {
	return post('/api/presets/recall', { name });
}

// --- Stinger API ---

export function listStingers(): Promise<string[]> {
	return request('/api/stinger/list');
}

export function deleteStinger(name: string): Promise<void> {
	return request(`/api/stinger/${encodeURIComponent(name)}`, {
		method: 'DELETE',
	});
}

export function setStingerCutPoint(name: string, cutPoint: number): Promise<void> {
	return post(`/api/stinger/${encodeURIComponent(name)}/cut-point`, { cutPoint });
}

export async function uploadStinger(name: string, file: File): Promise<void> {
	const res = await dualFetch(`/api/stinger/${encodeURIComponent(name)}/upload`, {
		method: 'POST',
		body: await file.arrayBuffer(),
	});
	if (!res.ok) {
		const data = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, data.error || 'Upload failed');
	}
}

// --- HPF API ---

export function setHPF(source: string, frequency: number, enabled: boolean): Promise<ControlRoomState> {
	return request(`/api/audio/${encodeURIComponent(source)}/hpf`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ frequency, enabled }),
	});
}

// --- EQ & Compressor API ---

export function setEQ(source: string, band: number, frequency: number, gain: number, q: number, enabled: boolean): Promise<ControlRoomState> {
	return request(`/api/audio/${encodeURIComponent(source)}/eq`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ band, frequency, gain, q, enabled }),
	});
}

export function getEQ(source: string): Promise<EQBand[]> {
	return request(`/api/audio/${encodeURIComponent(source)}/eq`);
}

export function setCompressor(source: string, threshold: number, ratio: number, attack: number, release: number, makeupGain: number): Promise<ControlRoomState> {
	return request(`/api/audio/${encodeURIComponent(source)}/compressor`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ threshold, ratio, attack, release, makeupGain }),
	});
}

export interface CompressorResponse extends CompressorSettings {
	gainReduction: number;
}

export function getCompressor(source: string): Promise<CompressorResponse> {
	return request(`/api/audio/${encodeURIComponent(source)}/compressor`);
}

export function setGate(source: string, threshold: number, range: number, attack: number, release: number, hold: number, enabled: boolean): Promise<ControlRoomState> {
	return request(`/api/audio/${encodeURIComponent(source)}/gate`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ threshold, range, attack, release, hold, enabled }),
	});
}

export interface GateResponse extends GateSettings {
	enabled: boolean;
	gainReduction: number;
}

export function getGate(source: string): Promise<GateResponse> {
	return request(`/api/audio/${encodeURIComponent(source)}/gate`);
}

// --- Graphics Layer API ---

export function graphicsAddLayer(): Promise<{ id: number }> {
	return request('/api/graphics', { method: 'POST' });
}

export function graphicsRemoveLayer(layerId: number): Promise<void> {
	return request(`/api/graphics/${layerId}`, { method: 'DELETE' });
}

export function graphicsOn(layerId: number): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/on`, {});
}

export function graphicsOff(layerId: number): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/off`, {});
}

export function graphicsAutoOn(layerId: number): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/auto-on`, {});
}

export function graphicsAutoOff(layerId: number): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/auto-off`, {});
}

export function getGraphicsStatus(): Promise<GraphicsState> {
	return request('/api/graphics');
}

export interface GraphicsAnimateConfig {
	mode: string;
	minAlpha?: number;
	maxAlpha?: number;
	speedHz?: number;
	toRect?: { x: number; y: number; width: number; height: number };
	toAlpha?: number;
	durationMs?: number;
	easing?: string;
}

export function graphicsAnimate(layerId: number, config: GraphicsAnimateConfig): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/animate`, config);
}

export function graphicsAnimateStop(layerId: number): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/animate/stop`, {});
}

export function graphicsSetRect(layerId: number, rect: { x: number; y: number; width: number; height: number }): Promise<GraphicsState> {
	return request(`/api/graphics/${layerId}/rect`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(rect),
	});
}

export function graphicsSetZOrder(layerId: number, zOrder: number): Promise<GraphicsState> {
	return request(`/api/graphics/${layerId}/zorder`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ zOrder }),
	});
}

export function graphicsUploadFrame(
	layerId: number,
	rgba: Uint8Array,
	width: number,
	height: number,
	template: string,
): Promise<GraphicsState> {
	// Encode as base64 to match Go's encoding/json []byte decoding
	// and avoid 3-4x payload bloat from JSON number arrays.
	let binary = '';
	for (let i = 0; i < rgba.length; i++) {
		binary += String.fromCharCode(rgba[i]);
	}
	const base64 = btoa(binary);
	return post(`/api/graphics/${layerId}/frame`, {
		width,
		height,
		template,
		rgba: base64,
	});
}

export function graphicsFlyIn(layerId: number, direction: string, durationMs = 500): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/fly-in`, { direction, durationMs });
}

export function graphicsFlyOn(layerId: number, direction: string, durationMs = 500): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/fly-on`, { direction, durationMs });
}

export function graphicsFlyOut(layerId: number, direction: string, durationMs = 500): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/fly-out`, { direction, durationMs });
}

export function graphicsSlide(
	layerId: number,
	rect: { x: number; y: number; width: number; height: number },
	durationMs = 500,
): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/slide`, { ...rect, durationMs });
}

// --- Graphics Image API ---

export async function graphicsImageUpload(layerId: number, file: File): Promise<GraphicsState> {
	const formData = new FormData();
	formData.append('image', file);
	const res = await dualFetch(`/api/graphics/${layerId}/image`, {
		method: 'POST',
		body: formData,
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.json();
}

export async function graphicsImageGet(layerId: number): Promise<Blob> {
	const res = await dualFetch(`/api/graphics/${layerId}/image`, {});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.blob();
}

export async function graphicsImageDelete(layerId: number): Promise<void> {
	const res = await dualFetch(`/api/graphics/${layerId}/image`, {
		method: 'DELETE',
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
}

// --- Sequence API ---

export async function graphicsSequenceUpload(layerId: number, file: File, fps: number): Promise<void> {
	const res = await dualFetch(`/api/graphics/${layerId}/sequence?fps=${fps}`, {
		method: 'POST',
		body: file,
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	// 202 Accepted — processing happens async, state broadcast will update UI
}

export async function graphicsSequenceDelete(layerId: number): Promise<GraphicsState> {
	const res = await dualFetch(`/api/graphics/${layerId}/sequence`, {
		method: 'DELETE',
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.json();
}

export async function graphicsSequenceSetSpeed(layerId: number, speed: number): Promise<GraphicsState> {
	const res = await dualFetch(`/api/graphics/${layerId}/sequence/speed`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ speed }),
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.json();
}

// --- Text Animation API ---

export function graphicsTextAnimStart(layerId: number, config: {
	mode: string;
	text: string;
	fontSize?: number;
	bold?: boolean;
	charsPerSec?: number;
	wordDelayMs?: number;
	fadeDurationMs?: number;
}): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/text-animate`, config);
}

export function graphicsTextAnimStop(layerId: number): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/text-animate/stop`, {});
}

// --- Ticker API ---

export function graphicsTickerStart(layerId: number, config: {
	text: string;
	fontSize?: number;
	speed?: number;
	bold?: boolean;
	loop?: boolean;
	height?: number;
}): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/ticker`, config);
}

export function graphicsTickerStop(layerId: number): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/ticker/stop`, {});
}

export function graphicsTickerUpdateText(layerId: number, text: string): Promise<GraphicsState> {
	return request(`/api/graphics/${layerId}/ticker/text`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ text }),
	});
}

// --- HTML5 Graphics API ---

export interface HTML5TemplateInfo {
	id: string;
	name: string;
	description?: string;
	format: 'ograf' | 'casparcg';
	url: string;
	dataSchema?: Record<string, unknown>;
}

export function graphicsListHTML5Templates(): Promise<HTML5TemplateInfo[]> {
	return request('/api/graphics/html5/templates');
}

export function graphicsLoadHTML5(
	layerId: number,
	url: string,
	data?: Record<string, unknown>,
): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/html5`, { url, data });
}

export function graphicsUpdateHTML5Data(
	layerId: number,
	data: Record<string, unknown>,
): Promise<GraphicsState> {
	return request(`/api/graphics/${layerId}/html5/data`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ data }),
	});
}

export function graphicsHTML5Play(layerId: number): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/html5/play`, {});
}

export function graphicsHTML5Stop(layerId: number): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/html5/stop`, {});
}

export function graphicsHTML5Next(layerId: number): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/html5/next`, {});
}

export function graphicsHTML5Custom(
	layerId: number,
	action: string,
	payload: unknown,
): Promise<GraphicsState> {
	return post(`/api/graphics/${layerId}/html5/custom`, { action, payload });
}

export function graphicsUnloadHTML5(layerId: number): Promise<void> {
	return request(`/api/graphics/${layerId}/html5`, { method: 'DELETE' });
}

// --- Macro API ---

export function listMacros(): Promise<Macro[]> {
	return request('/api/macros');
}

export function getMacro(name: string): Promise<Macro> {
	return request(`/api/macros/${encodeURIComponent(name)}`);
}

export function saveMacro(m: Macro): Promise<Macro> {
	return request(`/api/macros/${encodeURIComponent(m.name)}`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(m),
	});
}

export function deleteMacro(name: string): Promise<void> {
	return request(`/api/macros/${encodeURIComponent(name)}`, {
		method: 'DELETE',
	});
}

export function runMacro(name: string): Promise<{ status: string }> {
	return post(`/api/macros/${encodeURIComponent(name)}/run`, {});
}

export function cancelMacro(): Promise<void> {
	return post('/api/macros/execution/cancel', {});
}

export function dismissMacro(): Promise<void> {
	return request('/api/macros/execution', { method: 'DELETE' });
}

// --- Upstream Key API ---

export function setSourceKey(source: string, config: KeyConfig): Promise<KeyConfig> {
	return request(`/api/sources/${encodeURIComponent(source)}/key`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(config),
	});
}

export function getSourceKey(source: string): Promise<KeyConfig> {
	return request(`/api/sources/${encodeURIComponent(source)}/key`);
}

export function deleteSourceKey(source: string): Promise<void> {
	return request(`/api/sources/${encodeURIComponent(source)}/key`, {
		method: 'DELETE',
	});
}

// --- Replay ---

export function replayMarkIn(source: string): Promise<ControlRoomState> {
	return post('/api/replay/mark-in', { source });
}

export function replayMarkOut(source: string): Promise<ControlRoomState> {
	return post('/api/replay/mark-out', { source });
}

export function replayPlay(source: string, speed: number, loop: boolean): Promise<ControlRoomState> {
	return post('/api/replay/play', { source, speed, loop });
}

export function replayStop(): Promise<ControlRoomState> {
	return post('/api/replay/stop', {});
}

export function replayStatus(): Promise<ReplayState> {
	return request('/api/replay/status');
}

export function replaySources(): Promise<ReplayBufferInfo[]> {
	return request('/api/replay/sources');
}

export function replayQuick(seconds: number, speed: number = 0.5, source: string = ''): Promise<ControlRoomState> {
	return post('/api/replay/quick', { seconds, speed, source });
}

export function replayPause(): Promise<ControlRoomState> {
	return post('/api/replay/pause', {});
}

export function replayResume(): Promise<ControlRoomState> {
	return post('/api/replay/resume', {});
}

export function replaySeek(position: number): Promise<ControlRoomState> {
	return patch('/api/replay/seek', { position });
}

export function replaySetSpeed(speed: number): Promise<ControlRoomState> {
	return patch('/api/replay/speed', { speed });
}

export function replaySetMarks(markIn?: number, markOut?: number): Promise<ControlRoomState> {
	return patch('/api/replay/marks', { markIn, markOut });
}

// --- Operator API ---

export interface OperatorRegistration {
	id: string;
	name: string;
	role: OperatorRole;
	token: string;
}

export function operatorRegister(name: string, role: OperatorRole, id?: string): Promise<OperatorRegistration> {
	return post('/api/operator/register', { id, name, role });
}

export function operatorReconnect(): Promise<{ id: string; name: string; role: OperatorRole }> {
	return post('/api/operator/reconnect', {});
}

export function operatorHeartbeat(): Promise<{ ok: boolean }> {
	return post('/api/operator/heartbeat', {});
}

export function operatorList(): Promise<OperatorInfo[]> {
	return request('/api/operator/list');
}

export function operatorLock(subsystem: string): Promise<{ ok: boolean }> {
	return post('/api/operator/lock', { subsystem });
}

export function operatorUnlock(subsystem: string): Promise<{ ok: boolean }> {
	return post('/api/operator/unlock', { subsystem });
}

export function operatorForceUnlock(subsystem: string): Promise<{ ok: boolean }> {
	return post('/api/operator/force-unlock', { subsystem });
}

export function operatorDelete(id: string): Promise<{ ok: boolean }> {
	return request(`/api/operator/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export function operatorInviteTokens(): Promise<Record<string, string>> {
	return request('/api/operator/invite-tokens');
}

// --- Pipeline Format API ---

export function getFormat(): Promise<{ format: PipelineFormatInfo; presets: string[] }> {
	return request('/api/format');
}

export function setFormat(format: string): Promise<ControlRoomState>;
export function setFormat(opts: { width: number; height: number; fpsNum: number; fpsDen: number }): Promise<ControlRoomState>;
export function setFormat(arg: string | { width: number; height: number; fpsNum: number; fpsDen: number }): Promise<ControlRoomState> {
	const body = typeof arg === 'string' ? { format: arg } : arg;
	return request('/api/format', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body),
	});
}

// SCTE-35 operations

export function scte35Cue(req: SCTE35CueRequest): Promise<{ eventId: number; state: ControlRoomState }> {
	return post('/api/scte35/cue', req);
}

export function scte35Return(eventId?: number): Promise<ControlRoomState> {
	if (eventId !== undefined) return post(`/api/scte35/return/${eventId}`, {});
	return post('/api/scte35/return', {});
}

export function scte35Cancel(eventId: number): Promise<ControlRoomState> {
	return post(`/api/scte35/cancel/${eventId}`, {});
}

export function scte35Hold(eventId: number): Promise<ControlRoomState> {
	return post(`/api/scte35/hold/${eventId}`, {});
}

export function scte35Extend(eventId: number, durationMs: number): Promise<ControlRoomState> {
	return post(`/api/scte35/extend/${eventId}`, { durationMs });
}

export function scte35Status(): Promise<SCTE35State> {
	return request('/api/scte35/status');
}

export function scte35Log(limit?: number, offset?: number): Promise<SCTE35Event[]> {
	const params = new URLSearchParams();
	if (limit !== undefined) params.set('limit', String(limit));
	if (offset !== undefined) params.set('offset', String(offset));
	const qs = params.toString();
	return request(`/api/scte35/log${qs ? '?' + qs : ''}`);
}

export function scte35ListRules(): Promise<SCTE35Rule[]> {
	return request('/api/scte35/rules');
}

export function scte35CreateRule(rule: Omit<SCTE35Rule, 'id'>): Promise<SCTE35Rule> {
	return post('/api/scte35/rules', rule);
}

export function scte35UpdateRule(id: string, rule: Partial<SCTE35Rule>): Promise<ControlRoomState> {
	return request(`/api/scte35/rules/${encodeURIComponent(id)}`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(rule),
	});
}

export function scte35UpdateRuleByName(name: string, rule: Partial<SCTE35Rule>): Promise<ControlRoomState> {
	return request(`/api/scte35/rules/by-name/${encodeURIComponent(name)}`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(rule),
	});
}

export function scte35DeleteRule(id: string): Promise<ControlRoomState> {
	return request(`/api/scte35/rules/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export function scte35DeleteRuleByName(name: string): Promise<ControlRoomState> {
	return request(`/api/scte35/rules/by-name/${encodeURIComponent(name)}`, { method: 'DELETE' });
}

export function scte35SetDefaultAction(action: 'pass' | 'delete'): Promise<ControlRoomState> {
	return request('/api/scte35/rules/default', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ action }),
	});
}

export function scte35ReorderRules(ids: string[]): Promise<ControlRoomState> {
	return post('/api/scte35/rules/reorder', { ids });
}

export function scte35Templates(): Promise<SCTE35Rule[]> {
	return request('/api/scte35/rules/templates');
}

export function scte35CreateFromTemplate(name: string): Promise<SCTE35Rule> {
	return post('/api/scte35/rules/from-template', { name });
}

// ── Captions ──

export function setCaptionMode(mode: CaptionMode): Promise<CaptionState> {
	return post('/api/captions/mode', { mode });
}

export function sendCaptionText(text: string): Promise<CaptionState> {
	return post('/api/captions/text', { text });
}

export function sendCaptionNewline(): Promise<CaptionState> {
	return post('/api/captions/text', { newline: true });
}

export function clearCaptions(): Promise<CaptionState> {
	return post('/api/captions/text', { clear: true });
}

export function getCaptionState(): Promise<CaptionState> {
	return request('/api/captions/state');
}

// ── Clips ──

export function listClips(): Promise<ClipInfo[]> {
	return request('/api/clips');
}

export function getClip(id: string): Promise<ClipInfo> {
	return request(`/api/clips/${encodeURIComponent(id)}`);
}

export function updateClip(id: string, updates: { name?: string; loop?: boolean }): Promise<ClipInfo> {
	return request(`/api/clips/${encodeURIComponent(id)}`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(updates),
	});
}

export function deleteClip(id: string): Promise<void> {
	return request(`/api/clips/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

function uploadClipToEngine(baseUrl: string, file: File, onUploadProgress?: (percent: number) => void): Promise<ClipInfo> {
	return new Promise((resolve, reject) => {
		const formData = new FormData();
		formData.append('file', file);

		const xhr = new XMLHttpRequest();
		xhr.open('POST', `${baseUrl}/api/clips/upload`);

		// Set auth headers.
		const headers = authHeaders();
		for (const [key, value] of Object.entries(headers)) {
			xhr.setRequestHeader(key, value);
		}

		// Track upload byte progress.
		if (onUploadProgress) {
			xhr.upload.onprogress = (e) => {
				if (e.lengthComputable) {
					onUploadProgress(Math.round((e.loaded / e.total) * 100));
				}
			};
		}

		xhr.onload = () => {
			if (xhr.status >= 200 && xhr.status < 300) {
				try {
					resolve(JSON.parse(xhr.responseText));
				} catch {
					reject(new SwitchApiError(xhr.status, 'invalid response'));
				}
			} else {
				try {
					const body = JSON.parse(xhr.responseText);
					reject(new SwitchApiError(xhr.status, body.error || `HTTP ${xhr.status}`));
				} catch {
					reject(new SwitchApiError(xhr.status, `HTTP ${xhr.status}`));
				}
			}
		};

		xhr.onerror = () => reject(new SwitchApiError(0, 'network error'));
		xhr.onabort = () => reject(new SwitchApiError(0, 'upload aborted'));
		xhr.send(formData);
	});
}

export async function uploadClip(file: File, onUploadProgress?: (percent: number) => void): Promise<ClipInfo> {
	const engines = getEngineUrls();
	const primaryUrl = engines && engines.length > 0
		? engines[0].url
		: resolveApiUrl('');

	// Upload to primary engine with progress tracking
	const result = await uploadClipToEngine(primaryUrl, file, onUploadProgress);

	// Upload to secondary engine(s) in background (no progress tracking)
	if (engines && engines.length > 1) {
		for (let i = 1; i < engines.length; i++) {
			const engine = engines[i];
			uploadClipToEngine(engine.url, file).catch(err => {
				console.warn(`Clip upload to engine ${engine.id} failed:`, err);
			});
		}
	}

	return result;
}

export function pinClip(id: string): Promise<ClipInfo> {
	return post(`/api/clips/${encodeURIComponent(id)}/pin`, {});
}

export function importRecording(path: string): Promise<ClipInfo> {
	return post('/api/clips/from-recording', { path });
}

export function listRecordings(): Promise<RecordingFileInfo[]> {
	return request('/api/clips/recordings');
}

/** Fetch recordings from a specific engine URL. Returns [] on failure. */
export async function listRecordingsFromEngine(engineUrl: string): Promise<RecordingFileInfo[]> {
	try {
		const res = await fetch(`${engineUrl}/api/clips/recordings`, {
			headers: authHeaders(),
		});
		if (!res.ok) return [];
		return res.json();
	} catch {
		return [];
	}
}

/** Fetch recordings from all engines, grouped by engine. Single-engine falls back to standard list. */
export async function listRecordingsDualEngine(): Promise<EngineRecordingGroup[]> {
	const engines = getEngineUrls();
	if (!engines || engines.length <= 1) {
		const recs = await listRecordings();
		return [{ engineId: 'A', engineUrl: resolveApiUrl(''), recordings: recs }];
	}

	const results = await Promise.allSettled(
		engines.map(async (eng) => ({
			engineId: eng.id,
			engineUrl: eng.url,
			recordings: await listRecordingsFromEngine(eng.url),
		}))
	);

	return results
		.filter((r): r is PromiseFulfilledResult<EngineRecordingGroup> => r.status === 'fulfilled')
		.map(r => r.value);
}

/** Import a recording from a specific engine. The file path is local to that engine. */
export async function importRecordingFromEngine(engineUrl: string, path: string): Promise<ClipInfo> {
	const res = await fetch(`${engineUrl}/api/clips/from-recording`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json', ...authHeaders() },
		body: JSON.stringify({ path }),
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.json();
}

export function listClipPlayers(): Promise<ClipPlayerState[]> {
	return request('/api/clips/players');
}

export function clipPlayerLoad(player: number, clipId: string): Promise<ControlRoomState> {
	return post(`/api/clips/players/${player}/load`, { clipId });
}

export function clipPlayerLoadByName(player: number, clipName: string): Promise<ControlRoomState> {
	return post(`/api/clips/players/${player}/load`, { clipName });
}

export function clipPlayerEject(player: number): Promise<ControlRoomState> {
	return post(`/api/clips/players/${player}/eject`, {});
}

export function clipPlayerPlay(player: number, speed?: number, loop?: boolean): Promise<ControlRoomState> {
	const body: Record<string, unknown> = {};
	if (speed !== undefined) body.speed = speed;
	if (loop !== undefined) body.loop = loop;
	return post(`/api/clips/players/${player}/play`, body);
}

export function clipPlayerPause(player: number): Promise<ControlRoomState> {
	return post(`/api/clips/players/${player}/pause`, {});
}

export function clipPlayerStop(player: number): Promise<ControlRoomState> {
	return post(`/api/clips/players/${player}/stop`, {});
}

export function clipPlayerSeek(player: number, position: number): Promise<ControlRoomState> {
	return post(`/api/clips/players/${player}/seek`, { position });
}

export function clipPlayerSpeed(player: number, speed: number): Promise<ControlRoomState> {
	return post(`/api/clips/players/${player}/speed`, { speed });
}

export function clipPlayerLoop(player: number, loop: boolean): Promise<ControlRoomState> {
	return post(`/api/clips/players/${player}/loop`, { loop });
}

/**
 * Fire-and-forget API call with toast notification on error.
 * Callers needing programmatic error handling should await the promise
 * directly instead of using this wrapper.
 */
export function apiCall(promise: Promise<unknown>, context?: string): void {
	promise.catch((err) => {
		const msg = err instanceof SwitchApiError ? err.message : 'Network error';
		notify('error', context ? `${context}: ${msg}` : msg);
		console.warn('API call failed:', err);
	});
}

// --- Encoder ---

export function getEncoder(): Promise<EncoderState> {
	return request<EncoderState>('/api/encoder');
}

export function setEncoderBackend(name: string): Promise<ControlRoomState> {
	return request<ControlRoomState>('/api/encoder', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ encoder: name }),
	});
}

// --- Comms ---

export function commsJoin(operatorId: string, name: string): Promise<ControlRoomState> {
	return post('/api/comms/join', { operatorId, name });
}

export function commsLeave(operatorId: string): Promise<ControlRoomState> {
	return post('/api/comms/leave', { operatorId });
}

export function commsMute(operatorId: string, muted: boolean): Promise<ControlRoomState> {
	return request('/api/comms/mute', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ operatorId, muted }),
	});
}

export function commsStatus(): Promise<CommsState> {
	return request('/api/comms/status');
}

// ─── ST Map ─────────────────────────────────────────

export function stmapList(): Promise<{ maps: string[] }> {
	return request('/api/stmap');
}

export function stmapState(): Promise<STMapState> {
	return request('/api/stmap/state');
}

export function stmapGenerators(): Promise<{ generators: STMapGeneratorInfo[] }> {
	return request('/api/stmap/generators');
}

export function stmapGenerate(req: STMapGenerateRequest): Promise<STMapInfo> {
	return post('/api/stmap/generate', req);
}

export async function stmapUpload(name: string, file: File): Promise<STMapInfo> {
	const res = await dualFetch(`/api/stmap/upload/${encodeURIComponent(name)}`, {
		method: 'POST',
		body: file,
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.json();
}

export function stmapGet(name: string): Promise<STMapInfo> {
	return request(`/api/stmap/${encodeURIComponent(name)}`);
}

export function stmapDelete(name: string): Promise<void> {
	return request(`/api/stmap/${encodeURIComponent(name)}`, { method: 'DELETE' });
}

export async function stmapDownload(name: string): Promise<ArrayBuffer> {
	const res = await dualFetch(`/api/stmap/${encodeURIComponent(name)}/download`, {});
	if (!res.ok) throw new SwitchApiError(res.status, `download failed: ${res.status}`);
	return res.arrayBuffer();
}

export function stmapAssignSource(sourceKey: string, mapName: string): Promise<STMapState> {
	return request(`/api/stmap/source/${encodeURIComponent(sourceKey)}`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ map: mapName }),
	});
}

export function stmapRemoveSource(sourceKey: string): Promise<void> {
	return request(`/api/stmap/source/${encodeURIComponent(sourceKey)}`, { method: 'DELETE' });
}

export function stmapAssignProgram(mapName: string): Promise<STMapState> {
	return request('/api/stmap/program', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ map: mapName }),
	});
}

export function stmapRemoveProgram(): Promise<void> {
	return request('/api/stmap/program', { method: 'DELETE' });
}

// ── Color Grading ──

export function getColorGrade(): Promise<ColorGradeState> {
	return request('/api/colorgrade');
}

export function setColorGrade(config: { lookName?: string; lookGroup?: string; intensity?: number; enabled?: boolean }): Promise<ControlRoomState> {
	return request('/api/colorgrade', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(config),
	});
}

export async function uploadColorGradeLUT(name: string, file: File): Promise<ColorGradeState> {
	const fd = new FormData();
	fd.append('name', name);
	fd.append('file', file);
	const res = await dualFetch('/api/colorgrade/upload', {
		method: 'POST',
		body: fd,
	});
	if (!res.ok) {
		const text = await res.text();
		throw new SwitchApiError(res.status, text);
	}
	return res.json();
}

export function deleteColorGradeLUT(name: string): Promise<void> {
	return request(`/api/colorgrade/${encodeURIComponent(name)}`, { method: 'DELETE' });
}

export function getCorrector(): Promise<CorrectorState> {
	return request('/api/colorgrade/corrector');
}

export function setCorrector(state: CorrectorState): Promise<ControlRoomState> {
	return request('/api/colorgrade/corrector', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(state),
	});
}

export function resetCorrector(): Promise<ControlRoomState> {
	return request('/api/colorgrade/corrector', { method: 'DELETE' });
}

// ── Per-Source Corrector ──

export function getSourceCorrector(key: string): Promise<CorrectorState> {
	return request(`/api/sources/${encodeURIComponent(key)}/corrector`);
}

export function setSourceCorrector(key: string, state: CorrectorState): Promise<ControlRoomState> {
	return request(`/api/sources/${encodeURIComponent(key)}/corrector`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(state),
	});
}

export function resetSourceCorrector(key: string): Promise<ControlRoomState> {
	return request(`/api/sources/${encodeURIComponent(key)}/corrector`, { method: 'DELETE' });
}

export function saveCustomLook(name: string): Promise<ColorGradeState> {
	return post('/api/colorgrade/save', { name });
}

export async function getLookSettings(name: string): Promise<CorrectorState | null> {
	const res = await dualFetch(`/api/colorgrade/looks/${encodeURIComponent(name)}/settings`, {});
	if (res.status === 404) return null;
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.json();
}

// ── ASR (Automatic Speech Recognition) ──

export function getASRStatus(): Promise<ASRState> {
	return request<ASRState>('/api/asr/status');
}

export function setASRConfig(config: {
	active?: boolean;
	backend?: string;
	model?: string;
	confidenceFilter?: string;
	speakerDetection?: string;
}): Promise<ASRState> {
	return request<ASRState>('/api/asr/config', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(config),
	});
}

export function getASRModels(): Promise<string[]> {
	return request<string[]>('/api/asr/models');
}

export function getASRBackendConfig(): Promise<Record<string, unknown>> {
	return request<Record<string, unknown>>('/api/asr/backend/config');
}

export function setASRBackendConfig(config: Record<string, unknown>): Promise<void> {
	return request('/api/asr/backend/config', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(config),
	});
}

// ── AI Background Segmentation ──

export function setAISegment(source: string, config: Partial<AISegmentConfig>): Promise<Response> {
	return dualFetch(`/api/sources/${encodeURIComponent(source)}/ai-segment`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(config),
	});
}

export function getAISegment(source: string): Promise<AISegmentConfig> {
	return request<AISegmentConfig>(`/api/sources/${encodeURIComponent(source)}/ai-segment`);
}

export function deleteAISegment(source: string): Promise<Response> {
	return dualFetch(`/api/sources/${encodeURIComponent(source)}/ai-segment`, {
		method: 'DELETE',
	});
}

// --- Playout ---

export function playoutChannels(): Promise<PlayoutChannelState[]> {
	return request<PlayoutChannelState[]>('/api/playout/channels');
}

export function playoutLoadPlaylist(channelId: number, playlist: Playlist): Promise<void> {
	return post(`/api/playout/channels/${channelId}/load`, playlist);
}

export function playoutStart(channelId: number): Promise<void> {
	return post(`/api/playout/channels/${channelId}/start`, {});
}

export function playoutStop(channelId: number): Promise<void> {
	return post(`/api/playout/channels/${channelId}/stop`, {});
}

export function playoutNext(channelId: number): Promise<void> {
	return post(`/api/playout/channels/${channelId}/next`, {});
}

export function playoutPrevious(channelId: number): Promise<void> {
	return post(`/api/playout/channels/${channelId}/previous`, {});
}

export function playoutTakeManual(channelId: number): Promise<void> {
	return post(`/api/playout/channels/${channelId}/manual`, {});
}

export function playoutReturnToAuto(channelId: number): Promise<void> {
	return post(`/api/playout/channels/${channelId}/auto`, {});
}

export function playoutCacheStatus(): Promise<CacheStatus> {
	return request<CacheStatus>('/api/playout/cache/status');
}

/**
 * Fetch cache status from a specific engine (for dual-engine cache verification).
 * Falls back to the default single-engine path if engineUrls aren't configured
 * or the requested engine isn't found.
 */
export async function playoutEngineCacheStatus(engineId: 'A' | 'B'): Promise<CacheStatus> {
	const engines = getEngineUrls();
	const eng = engines?.find(e => e.id === engineId);
	const url = eng ? `${eng.url}/api/playout/cache/status` : resolveApiUrl('/api/playout/cache/status');
	const res = await fetch(url, { headers: authHeaders() });
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.json();
}

export function playoutLookupCachedAsset(assetUri: string): Promise<void> {
	return post('/api/playout/cache/lookup', { asset_uri: assetUri });
}

export function playoutEvictCachedAsset(assetUri: string): Promise<void> {
	return post('/api/playout/cache/evict', { asset_uri: assetUri });
}

// --- Playout Pods ---

export function podList(channelId: number): Promise<PodInfo[]> {
	return request<PodInfo[]>(`/api/playout/channels/${channelId}/pods`);
}

export function podCreate(channelId: number, body: { template?: string; config?: PodConfig }): Promise<PodInfo> {
	return post(`/api/playout/channels/${channelId}/pods`, body);
}

export function podArm(channelId: number, podId: string): Promise<void> {
	return post(`/api/playout/channels/${channelId}/pods/${podId}/arm`, {});
}

export function podDisarm(channelId: number, podId: string): Promise<void> {
	return post(`/api/playout/channels/${channelId}/pods/${podId}/disarm`, {});
}

export function podFire(channelId: number, podId: string): Promise<void> {
	return post(`/api/playout/channels/${channelId}/pods/${podId}/fire`, {});
}

export function podDump(channelId: number, podId: string): Promise<void> {
	return post(`/api/playout/channels/${channelId}/pods/${podId}/dump`, {});
}

export function podHold(channelId: number, podId: string): Promise<void> {
	return post(`/api/playout/channels/${channelId}/pods/${podId}/hold`, {});
}

export function podExtend(channelId: number, podId: string, additionalMs: number): Promise<void> {
	return post(`/api/playout/channels/${channelId}/pods/${podId}/extend`, { additional_ms: additionalMs });
}

export function podDelete(channelId: number, podId: string): Promise<void> {
	return request(`/api/playout/channels/${channelId}/pods/${podId}`, {
		method: 'DELETE',
	});
}

export function podTemplates(): Promise<PodTemplate[]> {
	return request<PodTemplate[]>('/api/playout/pods/templates');
}

// --- Playout: Jump, Recover, Health, As-Run, BXF Import, Playlist ---

export function playoutJump(channelId: number, itemId: string): Promise<void> {
	return post(`/api/playout/channels/${channelId}/jump`, { itemId });
}

export function playoutRecover(channelId: number): Promise<void> {
	return post(`/api/playout/channels/${channelId}/recover`, {});
}

export function playoutHealth(): Promise<ChannelHealth[]> {
	return request<ChannelHealth[]>('/api/playout/health');
}

export function playoutAsRun(
	channelId: number,
	params?: { type?: string; since?: string; until?: string; limit?: number },
): Promise<AsRunEvent[]> {
	const query = new URLSearchParams();
	if (params?.type) query.set('type', params.type);
	if (params?.since) query.set('since', params.since);
	if (params?.until) query.set('until', params.until);
	if (params?.limit) query.set('limit', String(params.limit));
	const qs = query.toString();
	return request<AsRunEvent[]>(`/api/playout/channels/${channelId}/asrun${qs ? `?${qs}` : ''}`);
}

export function playoutAsRunExport(channelId: number, format: 'json' | 'bxf'): Promise<Response> {
	return dualFetch(`/api/playout/channels/${channelId}/asrun/export?format=${format}`, {});
}

export function playoutAsRunReset(channelId: number): Promise<void> {
	return request<void>(`/api/playout/channels/${channelId}/asrun/reset`, { method: 'POST' });
}

export async function playoutImportBxf(
	bxfXml: string,
	channel?: number,
	name?: string,
): Promise<PlayoutChannelState> {
	const query = new URLSearchParams();
	if (channel != null) query.set('channel', String(channel));
	if (name) query.set('name', name);
	const qs = query.toString();
	const res = await dualFetch(`/api/playout/import/bxf${qs ? `?${qs}` : ''}`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/xml' },
		body: bxfXml,
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.json();
}

export function playoutPlaylist(channelId: number): Promise<PlaylistItem[]> {
	return request<PlaylistItem[]>(`/api/playout/channels/${channelId}/playlist`);
}

export function playoutSamples(): Promise<SampleClip[]> {
	return request<SampleClip[]>('/api/playout/samples');
}

export function playoutClips(): Promise<PlayoutClip[]> {
	return request<PlayoutClip[]>('/api/playout/clips');
}

export function playoutTemplateNames(): Promise<string[]> {
	return request<string[]>('/api/playout/templates');
}

export function playoutTemplate(name: string): Promise<Playlist> {
	return request<Playlist>(`/api/playout/templates/${encodeURIComponent(name)}`);
}

export function playoutSavedList(): Promise<string[]> {
	return request<string[]>('/api/playout/saved');
}

export function playoutSave(playlist: Playlist): Promise<void> {
	return post('/api/playout/saved', playlist);
}

export function playoutSavedGet(name: string): Promise<Playlist> {
	return request<Playlist>(`/api/playout/saved/${encodeURIComponent(name)}`);
}

export function playoutSavedDelete(name: string): Promise<void> {
	return request(`/api/playout/saved/${encodeURIComponent(name)}`, { method: 'DELETE' }) as Promise<void>;
}

// ── DVE API ──

export function getDVELayout(): Promise<any> {
	return request('/api/dve');
}

export function setDVELayout(layout: { preset?: string; name?: string; slots?: any[] }): Promise<any> {
	return request('/api/dve', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(layout),
	});
}

export function deleteDVELayout(): Promise<void> {
	return request('/api/dve', { method: 'DELETE' });
}

export function updateDVESlot(id: number, update: Record<string, unknown>): Promise<void> {
	return request(`/api/dve/slots/${id}`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(update),
	});
}

export function dveSlotOn(id: number): Promise<void> {
	return post(`/api/dve/slots/${id}/on`, {});
}

export function dveSlotOff(id: number): Promise<void> {
	return post(`/api/dve/slots/${id}/off`, {});
}

export function listDVEPresets(): Promise<DVEPresetInfo[]> {
	return request<DVEPresetInfo[]>('/api/dve/presets');
}

export function saveDVEPreset(name: string): Promise<void> {
	return post('/api/dve/presets', { name });
}

export function recallDVEPreset(id: string, durationMs?: number, easing?: string): Promise<void> {
	return request(`/api/dve/presets/${encodeURIComponent(id)}/recall`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ durationMs, easing }),
	});
}

export function deleteDVEPreset(id: string): Promise<void> {
	return request(`/api/dve/presets/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

// ── Wipe Pattern API ──

export function wipeListPatterns(): Promise<{ categories: { category: string; patterns: WipePattern[] }[]; total: number }> {
	return request('/api/wipes/patterns');
}

export function wipeGetPattern(id: string): Promise<WipePattern> {
	return request(`/api/wipes/patterns/${encodeURIComponent(id)}`);
}

export async function wipeGetPreview(id: string): Promise<Blob> {
	const res = await dualFetch(`/api/wipes/patterns/${encodeURIComponent(id)}/preview`, {});
	if (!res.ok) {
		throw new SwitchApiError(res.status, `Preview fetch failed: HTTP ${res.status}`);
	}
	return res.blob();
}

export async function wipeUploadCustom(file: File, name?: string): Promise<WipePattern> {
	const formData = new FormData();
	formData.append('file', file);
	formData.append('name', name ?? file.name.replace(/\.\w+$/, ''));
	const res = await dualFetch('/api/wipes/custom', {
		method: 'POST',
		body: formData,
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.json();
}

export function wipeDeleteCustom(id: string): Promise<void> {
	return request(`/api/wipes/custom/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

// --- Debug / Perf ---

export function getDebugSnapshot(opts?: { signal?: AbortSignal }): Promise<Record<string, unknown>> {
	return request('/api/debug/snapshot', opts);
}

export function getPerfData(opts?: { baseline?: string; signal?: AbortSignal }): Promise<Record<string, unknown>> {
	const url = opts?.baseline
		? `/api/perf?baseline=${encodeURIComponent(opts.baseline)}`
		: '/api/perf';
	return request(url, { signal: opts?.signal });
}

export function savePerfBaseline(name: string): Promise<void> {
	return post('/api/perf/baseline', { name });
}

// --- Operator (invite-based registration) ---

export function operatorRegisterWithInvite(name: string, inviteToken: string, id?: string): Promise<OperatorRegistration> {
	return post('/api/operator/register', { id, name, inviteToken });
}

