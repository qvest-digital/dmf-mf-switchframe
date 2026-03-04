import type { ControlRoomState, SourceInfo } from './types';

export class SwitchApiError extends Error {
	constructor(
		public status: number,
		message: string,
	) {
		super(message);
		this.name = 'SwitchApiError';
	}
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
	const res = options ? await fetch(url, options) : await fetch(url);
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

export function getState(): Promise<ControlRoomState> {
	return request('/api/switch/state');
}

export function getSources(): Promise<Record<string, SourceInfo>> {
	return request('/api/sources');
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

/** Log and swallow errors from fire-and-forget API calls (click handlers, keyboard shortcuts). */
export function fireAndForget(promise: Promise<unknown>): void {
	promise.catch((err) => console.warn('API call failed:', err));
}
