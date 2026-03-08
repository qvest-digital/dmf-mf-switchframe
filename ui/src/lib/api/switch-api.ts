import type { ControlRoomState, SourceInfo, RecordingStatus, SRTOutputConfig, SRTOutputStatus, Preset, RecallPresetResponse, GraphicsState, EQBand, CompressorSettings, Macro, KeyConfig, ReplayState, ReplayBufferInfo, OperatorRole, OperatorInfo, DestinationConfig, DestinationStatus, EasingConfig, PipelineFormatInfo } from './types';
import { notify } from '$lib/state/notifications.svelte';
import { resolveApiUrl } from './base-url';

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
	if (typeof sessionStorage === 'undefined') return null;
	return sessionStorage.getItem('switchframe_operator_token');
}

export function setAuthToken(token: string): void {
	sessionStorage.setItem('switchframe_operator_token', token);
}

function authHeaders(): Record<string, string> {
	const token = getAuthToken();
	const headers: Record<string, string> = {};
	if (token) {
		headers['Authorization'] = `Bearer ${token}`;
	}
	return headers;
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
	const opts: RequestInit = {
		...options,
		headers: { ...authHeaders(), ...options?.headers },
	};
	const res = await fetch(resolveApiUrl(url), opts);
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
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

export function setMasterLevel(level: number): Promise<ControlRoomState> {
	return post('/api/audio/master', { level });
}

export function startTransition(source: string, type: string, durationMs: number, wipeDirection?: string, stingerName?: string, easing?: EasingConfig): Promise<ControlRoomState> {
	const body: Record<string, unknown> = { source, type, durationMs };
	if (wipeDirection) {
		body.wipeDirection = wipeDirection;
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

export function recallPreset(id: string): Promise<RecallPresetResponse> {
	return post(`/api/presets/${encodeURIComponent(id)}/recall`, {});
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
	const response = await fetch(resolveApiUrl(`/api/stinger/${encodeURIComponent(name)}/upload`), {
		method: 'POST',
		body: await file.arrayBuffer(),
	});
	if (!response.ok) {
		const data = await response.json();
		throw new Error(data.error || 'Upload failed');
	}
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

// --- Graphics Overlay API ---

export function graphicsOn(): Promise<GraphicsState> {
	return post('/api/graphics/on', {});
}

export function graphicsOff(): Promise<GraphicsState> {
	return post('/api/graphics/off', {});
}

export function graphicsAutoOn(): Promise<GraphicsState> {
	return post('/api/graphics/auto-on', {});
}

export function graphicsAutoOff(): Promise<GraphicsState> {
	return post('/api/graphics/auto-off', {});
}

export function getGraphicsStatus(): Promise<GraphicsState> {
	return request('/api/graphics/status');
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

// --- Operator API ---

export interface OperatorRegistration {
	id: string;
	name: string;
	role: OperatorRole;
	token: string;
}

export function operatorRegister(name: string, role: OperatorRole): Promise<OperatorRegistration> {
	return post('/api/operator/register', { name, role });
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
