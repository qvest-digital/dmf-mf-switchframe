import type { ControlRoomState, SourceInfo, RecordingStatus, SRTOutputConfig, SRTOutputStatus, Preset, RecallPresetResponse, GraphicsState, GraphicsLayerState, EQBand, CompressorSettings, Macro, KeyConfig, ReplayState, ReplayBufferInfo, OperatorRole, OperatorInfo, DestinationConfig, DestinationStatus, EasingConfig, PipelineFormatInfo, EncoderState, SCTE35CueRequest, SCTE35State, SCTE35Event, SCTE35Rule, LayoutConfig, CaptionState, CaptionMode, ClipPlayerState, ClipInfo, RecordingFileInfo } from './types';
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

export function authHeaders(): Record<string, string> {
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
	if (res.status === 204 || res.headers?.get('content-length') === '0') {
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
	const res = await fetch(resolveApiUrl(`/api/graphics/${layerId}/image`), {
		method: 'POST',
		headers: authHeaders(),
		body: formData,
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.json();
}

export async function graphicsImageGet(layerId: number): Promise<Blob> {
	const res = await fetch(resolveApiUrl(`/api/graphics/${layerId}/image`), {
		headers: authHeaders(),
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
	return res.blob();
}

export async function graphicsImageDelete(layerId: number): Promise<void> {
	const res = await fetch(resolveApiUrl(`/api/graphics/${layerId}/image`), {
		method: 'DELETE',
		headers: authHeaders(),
	});
	if (!res.ok) {
		const body = await res.json().catch(() => ({ error: 'unknown error' }));
		throw new SwitchApiError(res.status, body.error || `HTTP ${res.status}`);
	}
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

export function scte35DeleteRule(id: string): Promise<ControlRoomState> {
	return request(`/api/scte35/rules/${encodeURIComponent(id)}`, { method: 'DELETE' });
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

// Layout/PIP API

export function getLayout(): Promise<LayoutConfig | null> {
	return request('/api/layout');
}

export function setLayout(config: LayoutConfig): Promise<LayoutConfig> {
	return request('/api/layout', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(config),
	});
}

export function clearLayout(): Promise<void> {
	return request('/api/layout', { method: 'DELETE' });
}

export function layoutSlotOn(slotId: number): Promise<void> {
	return post(`/api/layout/slots/${slotId}/on`, {});
}

export function layoutSlotOff(slotId: number): Promise<void> {
	return post(`/api/layout/slots/${slotId}/off`, {});
}

export function updateLayoutSlot(slotId: number, update: Record<string, unknown>): Promise<void> {
	return request(`/api/layout/slots/${slotId}`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(update),
	});
}

export function setLayoutSlotSource(slotId: number, source: string): Promise<void> {
	return request(`/api/layout/slots/${slotId}/source`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ source }),
	});
}

export function listLayoutPresets(): Promise<string[]> {
	return request('/api/layout/presets');
}

export function saveLayoutPreset(name: string): Promise<void> {
	return post('/api/layout/presets', { name });
}

export function deleteLayoutPreset(name: string): Promise<void> {
	return request(`/api/layout/presets/${encodeURIComponent(name)}`, { method: 'DELETE' });
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

export function uploadClip(file: File, onUploadProgress?: (percent: number) => void): Promise<ClipInfo> {
	return new Promise((resolve, reject) => {
		const formData = new FormData();
		formData.append('file', file);

		const xhr = new XMLHttpRequest();
		xhr.open('POST', resolveApiUrl('/api/clips/upload'));

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

export function pinClip(id: string): Promise<ClipInfo> {
	return post(`/api/clips/${encodeURIComponent(id)}/pin`, {});
}

export function importRecording(path: string): Promise<ClipInfo> {
	return post('/api/clips/from-recording', { path });
}

export function listRecordings(): Promise<RecordingFileInfo[]> {
	return request('/api/clips/recordings');
}

export function listClipPlayers(): Promise<ClipPlayerState[]> {
	return request('/api/clips/players');
}

export function clipPlayerLoad(player: number, clipId: string): Promise<ControlRoomState> {
	return post(`/api/clips/players/${player}/load`, { clipId });
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
