import { describe, it, expect, vi, beforeEach } from 'vitest';
import { cut, setPreview, setLabel, getState, getSources, setLevel, setMute, setAFV, setMasterLevel, startTransition, setTransitionPosition, fadeToBlack, startRecording, stopRecording, getRecordingStatus, startSRTOutput, stopSRTOutput, getSRTOutputStatus, setAuthToken, apiCall, SwitchApiError } from './switch-api';
import * as notifications from '$lib/state/notifications.svelte';
import type { SRTOutputConfig } from './types';

describe('switch-api', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		sessionStorage.clear();
	});

	it('cut sends POST with source', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await cut('cam1');
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/cut', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ source: 'cam1' }),
		});
		expect(result.programSource).toBe('cam1');
	});

	it('setPreview sends POST with source', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ previewSource: 'cam2', seq: 2 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await setPreview('cam2');
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/preview', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ source: 'cam2' }),
		});
		expect(result.previewSource).toBe('cam2');
	});

	it('setLabel sends POST with label', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ sources: { cam1: { label: 'Camera 1' } } }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await setLabel('cam1', 'Camera 1');
		expect(mockFetch).toHaveBeenCalledWith('/api/sources/cam1/label', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ label: 'Camera 1' }),
		});
	});

	it('getState fetches current state', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 5 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await getState();
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/state', { headers: {} });
		expect(result.seq).toBe(5);
	});

	it('getSources fetches source list', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () =>
				Promise.resolve({
					cam1: { key: 'cam1', status: 'healthy', lastFrameTime: 100 },
				}),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await getSources();
		expect(mockFetch).toHaveBeenCalledWith('/api/sources', { headers: {} });
		expect(result.cam1.status).toBe('healthy');
	});

	it('cut throws on error response', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: false,
			status: 404,
			json: () => Promise.resolve({ error: 'source "cam99" not found' }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await expect(cut('cam99')).rejects.toThrow('source "cam99" not found');
	});
});

describe('Audio API', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
	});

	it('should call setLevel endpoint', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ seq: 1, programSource: 'cam1' }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await setLevel('cam1', -6.0);
		expect(mockFetch).toHaveBeenCalledWith('/api/audio/level', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ source: 'cam1', level: -6.0 }),
		});
	});

	it('should call setMute endpoint', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await setMute('cam1', true);
		expect(mockFetch).toHaveBeenCalledWith('/api/audio/mute', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ source: 'cam1', muted: true }),
		});
	});

	it('should call setAFV endpoint', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await setAFV('cam1', true);
		expect(mockFetch).toHaveBeenCalledWith('/api/audio/afv', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ source: 'cam1', afv: true }),
		});
	});

	it('should call setMasterLevel endpoint', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await setMasterLevel(-3.0);
		expect(mockFetch).toHaveBeenCalledWith('/api/audio/master', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ level: -3.0 }),
		});
	});
});

describe('Transition API', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
	});

	it('should call startTransition endpoint', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ seq: 1, programSource: 'cam1', inTransition: true }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await startTransition('cam2', 'mix', 1000);
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/transition', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ source: 'cam2', type: 'mix', durationMs: 1000 }),
		});
	});

	it('should call setTransitionPosition endpoint', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({}),
		});
		vi.stubGlobal('fetch', mockFetch);

		await setTransitionPosition(0.5);
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/transition/position', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ position: 0.5 }),
		});
	});

	it('should call fadeToBlack endpoint', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ seq: 1, ftbActive: true }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await fadeToBlack();
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/ftb', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({}),
		});
	});
});

describe('Recording API', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
	});

	it('should call startRecording with no outputDir', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ active: true, filename: 'rec-2026.ts' }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await startRecording();
		expect(mockFetch).toHaveBeenCalledWith('/api/recording/start', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({}),
		});
		expect(result.active).toBe(true);
		expect(result.filename).toBe('rec-2026.ts');
	});

	it('should call startRecording with outputDir', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ active: true, filename: '/tmp/rec.ts' }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await startRecording({ outputDir: '/tmp' });
		expect(mockFetch).toHaveBeenCalledWith('/api/recording/start', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ outputDir: '/tmp' }),
		});
		expect(result.active).toBe(true);
	});

	it('should call stopRecording', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ active: false, bytesWritten: 1024000, durationSecs: 60 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await stopRecording();
		expect(mockFetch).toHaveBeenCalledWith('/api/recording/stop', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({}),
		});
		expect(result.active).toBe(false);
		expect(result.bytesWritten).toBe(1024000);
	});

	it('should call getRecordingStatus', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ active: true, filename: 'rec.ts', bytesWritten: 512000, durationSecs: 30 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await getRecordingStatus();
		expect(mockFetch).toHaveBeenCalledWith('/api/recording/status', { headers: {} });
		expect(result.active).toBe(true);
		expect(result.durationSecs).toBe(30);
	});
});

describe('SRT Output API', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
	});

	it('should call startSRTOutput with config', async () => {
		const config: SRTOutputConfig = {
			mode: 'caller',
			address: '192.168.1.100',
			port: 9000,
			latency: 200,
		};
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ active: true, mode: 'caller', address: '192.168.1.100', port: 9000 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await startSRTOutput(config);
		expect(mockFetch).toHaveBeenCalledWith('/api/output/srt/start', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(config),
		});
		expect(result.active).toBe(true);
		expect(result.mode).toBe('caller');
	});

	it('should call startSRTOutput in listener mode', async () => {
		const config: SRTOutputConfig = {
			mode: 'listener',
			port: 9001,
			streamID: 'program',
		};
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ active: true, mode: 'listener', port: 9001, connections: 0 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await startSRTOutput(config);
		expect(mockFetch).toHaveBeenCalledWith('/api/output/srt/start', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(config),
		});
		expect(result.active).toBe(true);
		expect(result.mode).toBe('listener');
	});

	it('should call stopSRTOutput', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ active: false }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await stopSRTOutput();
		expect(mockFetch).toHaveBeenCalledWith('/api/output/srt/stop', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({}),
		});
		expect(result.active).toBe(false);
	});

	it('should call getSRTOutputStatus', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ active: true, mode: 'listener', port: 9001, connections: 3, bytesWritten: 2048000 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await getSRTOutputStatus();
		expect(mockFetch).toHaveBeenCalledWith('/api/output/srt/status', { headers: {} });
		expect(result.active).toBe(true);
		expect(result.connections).toBe(3);
	});
});

describe('apiCall', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
	});

	it('shows error toast on API failure', async () => {
		const notifySpy = vi.spyOn(notifications, 'notify');
		const err = new SwitchApiError(404, 'source "cam99" not found');
		const promise = Promise.reject(err);

		apiCall(promise, 'Cut failed');

		// Wait for the microtask (catch handler) to run
		await new Promise((r) => setTimeout(r, 0));

		expect(notifySpy).toHaveBeenCalledWith('error', 'Cut failed: source "cam99" not found');
	});

	it('includes context prefix in error message', async () => {
		const notifySpy = vi.spyOn(notifications, 'notify');
		const err = new SwitchApiError(500, 'internal error');
		const promise = Promise.reject(err);

		apiCall(promise, 'Preview failed');

		await new Promise((r) => setTimeout(r, 0));

		expect(notifySpy).toHaveBeenCalledWith('error', 'Preview failed: internal error');
	});

	it('shows "Network error" for non-SwitchApiError', async () => {
		const notifySpy = vi.spyOn(notifications, 'notify');
		const promise = Promise.reject(new TypeError('Failed to fetch'));

		apiCall(promise, 'Cut failed');

		await new Promise((r) => setTimeout(r, 0));

		expect(notifySpy).toHaveBeenCalledWith('error', 'Cut failed: Network error');
	});

	it('does not show toast on success', async () => {
		const notifySpy = vi.spyOn(notifications, 'notify');
		const promise = Promise.resolve({ programSource: 'cam1' });

		apiCall(promise, 'Cut failed');

		await new Promise((r) => setTimeout(r, 0));

		expect(notifySpy).not.toHaveBeenCalled();
	});

	it('shows raw error message when no context given', async () => {
		const notifySpy = vi.spyOn(notifications, 'notify');
		const err = new SwitchApiError(404, 'not found');
		const promise = Promise.reject(err);

		apiCall(promise);

		await new Promise((r) => setTimeout(r, 0));

		expect(notifySpy).toHaveBeenCalledWith('error', 'not found');
	});
});

describe('Auth token', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		sessionStorage.clear();
	});

	it('includes Authorization header when token is set', async () => {
		setAuthToken('my-secret-token');
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await getState();
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/state', {
			headers: { Authorization: 'Bearer my-secret-token' },
		});
	});

	it('includes Authorization header in POST requests', async () => {
		setAuthToken('my-secret-token');
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await cut('cam1');
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/cut', {
			method: 'POST',
			headers: { Authorization: 'Bearer my-secret-token', 'Content-Type': 'application/json' },
			body: JSON.stringify({ source: 'cam1' }),
		});
	});

	it('does not include Authorization header when no token is set', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await getState();
		expect(mockFetch).toHaveBeenCalledWith('/api/switch/state', {
			headers: {},
		});
	});
});
