import { describe, it, expect, vi, beforeEach } from 'vitest';
import { cut, setPreview, setLabel, getState, getSources, setLevel, setMute, setAFV, setMasterLevel, startTransition, setTransitionPosition, fadeToBlack, startRecording, stopRecording, getRecordingStatus, startSRTOutput, stopSRTOutput, getSRTOutputStatus, setAuthToken, checkFragmentToken, getAuthToken, apiCall, SwitchApiError, scte35Cue, scte35Return, scte35Hold, scte35Extend, scte35Cancel, scte35Status, scte35Log, scte35ListRules, scte35CreateRule, scte35DeleteRule, createSRTSource, deleteSRTSource, getSRTSourceStats, updateSRTLatency, setDualTimestampProvider, graphicsOn, graphicsOff, recallPreset, dveSlotOn, clipPlayerPlay, clipPlayerStop, listRecordingsDualEngine, listRecordingsFromEngine, importRecordingFromEngine, graphicsSequenceUpload, runMacro, setSourceKey, setFormat, setEncoderBackend, resetCircuitBreakers } from './switch-api';
import { setEngineUrls } from './base-url';
import * as notifications from '$lib/state/notifications.svelte';
import type { SRTOutputConfig, SCTE35CueRequest, CreateSRTSourceConfig } from './types';

describe('switch-api', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		localStorage.clear();
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
					cam1: { key: 'cam1', type: 'demo', status: 'healthy' },
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
		expect(mockFetch).toHaveBeenCalledWith('/api/recording/start', expect.objectContaining({
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
		}));
		// Body should include auto-generated filenamePrefix
		const body = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(body.filenamePrefix).toMatch(/^rec-\d+$/);
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
		expect(mockFetch).toHaveBeenCalledWith('/api/recording/start', expect.objectContaining({
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
		}));
		const body = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(body.outputDir).toBe('/tmp');
		expect(body.filenamePrefix).toMatch(/^rec-\d+$/);
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

	it('rewrites peer-gated 409 into actionable recovery copy', async () => {
		const notifySpy = vi.spyOn(notifications, 'notify');
		const err = new SwitchApiError(
			409,
			'peer: action gated — local engine is partitioned follower: action=recording.start',
		);
		apiCall(Promise.reject(err), 'Start recording');

		await new Promise((r) => setTimeout(r, 0));

		expect(notifySpy).toHaveBeenCalledTimes(1);
		const [level, message] = notifySpy.mock.calls[0];
		expect(level).toBe('error');
		expect(message).toContain('Start recording');
		expect(message).toContain('partitioned');
		expect(message).toContain('force takeover');
		// Must NOT leak the backend sentinel verbatim into the user-facing toast.
		expect(message).not.toContain('peer: action gated');
	});

	it('leaves non-peer-gated 409s on their original message', async () => {
		const notifySpy = vi.spyOn(notifications, 'notify');
		const err = new SwitchApiError(409, 'session already in progress');
		apiCall(Promise.reject(err), 'Create session');

		await new Promise((r) => setTimeout(r, 0));

		expect(notifySpy).toHaveBeenCalledWith('error', 'Create session: session already in progress');
	});
});

describe('Auth token', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		localStorage.clear();
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

describe('SCTE-35 API', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		localStorage.clear();
	});

	it('scte35Cue sends POST with body', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const req: SCTE35CueRequest = {
			commandType: 'splice_insert',
			isOut: true,
			durationMs: 30000,
			autoReturn: true,
		};
		await scte35Cue(req);
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/cue', expect.objectContaining({ method: 'POST' }));
		const body = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(body).toEqual({ ...req, eventId: expect.any(Number) });
	});

	it('scte35Cue sends time_signal with descriptors', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const req: SCTE35CueRequest = {
			commandType: 'time_signal',
			isOut: true,
			durationMs: 60000,
			descriptors: [{ segmentationType: 48, upidType: 9, upid: 'ABCD0001', durationMs: 60000 }],
		};
		await scte35Cue(req);
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/cue', expect.objectContaining({ method: 'POST' }));
		const body = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(body).toEqual({ ...req, descriptors: [{ ...req.descriptors![0], segEventId: expect.any(Number) }] });
	});

	it('scte35Return sends POST without eventId', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await scte35Return();
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/return', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({}),
		});
	});

	it('scte35Return sends POST with eventId', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await scte35Return(42);
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/return/42', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({}),
		});
	});

	it('scte35Hold sends POST', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await scte35Hold(7);
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/hold/7', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({}),
		});
	});

	it('scte35Extend sends POST with duration', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await scte35Extend(7, 30000);
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/extend/7', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ durationMs: 30000 }),
		});
	});

	it('scte35Cancel sends POST', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam1', seq: 1 }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await scte35Cancel(99);
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/cancel/99', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({}),
		});
	});

	it('scte35Status sends GET', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ enabled: true, activeEvents: {}, eventLog: [], heartbeatOk: true, config: {} }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await scte35Status();
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/status', { headers: {} });
		expect(result.enabled).toBe(true);
	});

	it('scte35Log sends GET without params', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve([]),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await scte35Log();
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/log', { headers: {} });
		expect(result).toEqual([]);
	});

	it('scte35Log sends GET with limit and offset', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve([{ eventId: 1 }]),
		});
		vi.stubGlobal('fetch', mockFetch);

		await scte35Log(10, 5);
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/log?limit=10&offset=5', { headers: {} });
	});

	it('scte35ListRules sends GET', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve([]),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await scte35ListRules();
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/rules', { headers: {} });
		expect(result).toEqual([]);
	});

	it('scte35CreateRule sends POST with body', async () => {
		const rule = {
			name: 'Block Ad Start',
			enabled: true,
			priority: 1,
			conditions: [{ field: 'segmentationType', operator: 'eq', value: '48' }],
			logic: 'and' as const,
			action: 'delete' as const,
		};
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ id: 'rule-1', ...rule }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await scte35CreateRule(rule);
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/rules', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(rule),
		});
		expect(result.id).toBe('rule-1');
	});

	it('scte35DeleteRule sends DELETE', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve(undefined),
		});
		vi.stubGlobal('fetch', mockFetch);

		await scte35DeleteRule('rule-1');
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/rules/rule-1', {
			method: 'DELETE',
			headers: {},
		});
	});

	it('scte35DeleteRule encodes rule ID', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve(undefined),
		});
		vi.stubGlobal('fetch', mockFetch);

		await scte35DeleteRule('rule/special');
		expect(mockFetch).toHaveBeenCalledWith('/api/scte35/rules/rule%2Fspecial', {
			method: 'DELETE',
			headers: {},
		});
	});

});

describe('SRT source API', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		localStorage.clear();
	});

	it('createSRTSource sends POST /api/sources with correct body', async () => {
		const config: CreateSRTSourceConfig = {
			type: 'srt',
			mode: 'caller',
			address: 'srt://192.168.1.50:9000',
			streamID: 'live/camera5',
			label: 'Camera 5',
			latencyMs: 200,
		};
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ key: 'srt:camera5' }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await createSRTSource(config);
		expect(mockFetch).toHaveBeenCalledWith('/api/sources', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(config),
		});
		expect(result.key).toBe('srt:camera5');
	});

	it('deleteSRTSource sends DELETE /api/sources/{key} with URI encoding', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			status: 204,
			headers: { get: () => '0' },
			json: () => Promise.resolve(undefined),
		});
		vi.stubGlobal('fetch', mockFetch);

		await deleteSRTSource('srt:camera5');
		expect(mockFetch).toHaveBeenCalledWith('/api/sources/srt%3Acamera5', {
			method: 'DELETE',
			headers: {},
		});
	});

	it('getSRTSourceStats sends GET /api/sources/{key}/srt/stats', async () => {
		const stats = {
			mode: 'caller',
			streamID: 'live/camera5',
			state: 'connected',
			connected: true,
			uptimeMs: 60000,
			latencyMs: 120,
			negotiatedLatencyMs: 120,
			rttMs: 5.2,
			rttVarMs: 1.1,
			recvRateMbps: 4.5,
			lossRatePct: 0.01,
			packetsReceived: 100000,
			packetsLost: 10,
			packetsDropped: 2,
			packetsRetransmitted: 8,
			packetsBelated: 0,
			recvBufMs: 80,
			recvBufPackets: 50,
			flightSize: 12,
		};
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve(stats),
		});
		vi.stubGlobal('fetch', mockFetch);

		const result = await getSRTSourceStats('srt:camera5');
		expect(mockFetch).toHaveBeenCalledWith('/api/sources/srt%3Acamera5/srt/stats', { headers: {} });
		expect(result.connected).toBe(true);
		expect(result.rttMs).toBe(5.2);
	});

	it('updateSRTLatency sends PUT /api/sources/{key}/srt with latencyMs body', async () => {
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			status: 204,
			headers: { get: () => '0' },
			json: () => Promise.resolve(undefined),
		});
		vi.stubGlobal('fetch', mockFetch);

		await updateSRTLatency('srt:camera5', 250);
		expect(mockFetch).toHaveBeenCalledWith('/api/sources/srt%3Acamera5/srt', {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ latencyMs: 250 }),
		});
	});
});

describe('checkFragmentToken', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		localStorage.clear();
		// Reset hash to empty
		window.location.hash = '';
	});

	it('reads token from #token=xxx, stores in localStorage, and clears hash', () => {
		window.location.hash = '#token=abc123secret';

		checkFragmentToken();

		expect(localStorage.getItem('switchframe_operator_token')).toBe('abc123secret');
		expect(window.location.hash).toBe('');
	});

	it('does nothing when no fragment is present', () => {
		window.location.hash = '';

		checkFragmentToken();

		expect(localStorage.getItem('switchframe_operator_token')).toBeNull();
	});

	it('does nothing for non-token fragments like #section', () => {
		window.location.hash = '#section';

		checkFragmentToken();

		expect(localStorage.getItem('switchframe_operator_token')).toBeNull();
	});

	it('does nothing for empty token value in #token=', () => {
		window.location.hash = '#token=';

		checkFragmentToken();

		expect(localStorage.getItem('switchframe_operator_token')).toBeNull();
	});

	it('handles tokens with special characters', () => {
		window.location.hash = '#token=abc-123_XYZ.456';

		checkFragmentToken();

		expect(localStorage.getItem('switchframe_operator_token')).toBe('abc-123_XYZ.456');
		expect(window.location.hash).toBe('');
	});
});

describe('dual-engine request interceptor', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		localStorage.clear();
		setEngineUrls(null);
		setDualTimestampProvider(null);
		resetCircuitBreakers();
	});

	it('fans out to both engines when engineUrls set', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);
		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ programSource: 'cam2' }), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await cut('cam2');

		expect(mockFetch).toHaveBeenCalledTimes(2);
		expect(mockFetch.mock.calls[0][0]).toContain('engine-a');
		expect(mockFetch.mock.calls[1][0]).toContain('engine-b');
	});

	it('adds PTP timestamps to timed paths', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 1_000_000; // 1s in the future
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 1 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await cut('cam2');

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
		expect(bodyA.command_seq).toBe(1);
		// Original body fields preserved
		expect(bodyA.source).toBe('cam2');
	});

	it('adds PTP timestamps to setPreview (timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 2_000_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 42 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await setPreview('cam3');

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
		expect(bodyA.source).toBe('cam3');
	});

	it('adds PTP timestamps to fadeToBlack (timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 3_000_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 5 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await fadeToBlack();

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
	});

	it('adds PTP timestamps to graphics on/off (regex timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 4_000_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 10 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await graphicsOn(1);

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
	});

	it('does not add timestamps to untimed paths', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		setDualTimestampProvider(() => ({ execute_at: Date.now() * 1000 + 5_000_000, command_seq: 1 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await setLabel('cam1', 'Camera 1');

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBeUndefined();
		expect(bodyA.label).toBe('Camera 1');
	});

	it('does not add timestamps when no provider set', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		// No timestamp provider set

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await cut('cam2');

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBeUndefined();
		expect(bodyA.source).toBe('cam2');
	});

	it('single-engine unchanged when no engineUrls', async () => {
		// Don't set engineUrls
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ programSource: 'cam2' }),
		});
		vi.stubGlobal('fetch', mockFetch);

		await cut('cam2');

		expect(mockFetch).toHaveBeenCalledTimes(1);
		// Should use resolveApiUrl (same-origin), not engine URLs
		expect(mockFetch.mock.calls[0][0]).toBe('/api/switch/cut');
	});

	it('throws when all engines fail with HTTP errors', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ error: 'server error' }), { status: 500 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await expect(cut('cam2')).rejects.toThrow(SwitchApiError);
	});

	it('throws when all engines are unreachable', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));
		vi.stubGlobal('fetch', mockFetch);

		await expect(cut('cam2')).rejects.toThrow('All engines unreachable');
	});

	it('succeeds when one engine fails and other succeeds', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn()
			.mockResolvedValueOnce(
				new Response(JSON.stringify({ error: 'fail' }), { status: 500 }),
			)
			.mockResolvedValueOnce(
				new Response(JSON.stringify({ programSource: 'cam2' }), { status: 200 }),
			);
		vi.stubGlobal('fetch', mockFetch);

		const result = await cut('cam2');
		expect(result).toBeDefined();
		expect(result.programSource).toBe('cam2');
	});

	it('handles 204 No Content response in dual mode', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(null, { status: 204 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		const result = await setLabel('cam1', 'Camera 1');
		expect(result).toBeUndefined();
	});

	it('GET requests use primary-with-fallback, not parallel fan-out', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ programSource: 'cam1', seq: 1 }), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await getState();

		// Only the primary engine should be called when it succeeds
		expect(mockFetch).toHaveBeenCalledTimes(1);
		expect(mockFetch.mock.calls[0][0]).toBe('https://a.example.com/api/switch/state');
	});

	it('GET falls back to second engine on HTTP error from primary', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn()
			.mockResolvedValueOnce(
				new Response(JSON.stringify({ error: 'internal error' }), { status: 500 }),
			)
			.mockResolvedValueOnce(
				new Response(JSON.stringify({ programSource: 'cam1' }), { status: 200 }),
			);
		vi.stubGlobal('fetch', mockFetch);

		const result = await getState();

		expect(mockFetch).toHaveBeenCalledTimes(2);
		expect(mockFetch.mock.calls[0][0]).toContain('a.example.com');
		expect(mockFetch.mock.calls[1][0]).toContain('b.example.com');
		expect(result.programSource).toBe('cam1');
	});

	it('GET throws SwitchApiError when all engines return HTTP errors', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn()
			.mockResolvedValueOnce(
				new Response(JSON.stringify({ error: 'engine A down' }), { status: 500 }),
			)
			.mockResolvedValueOnce(
				new Response(JSON.stringify({ error: 'engine B overloaded' }), { status: 503 }),
			);
		vi.stubGlobal('fetch', mockFetch);

		const err = await getState().catch(e => e);
		expect(err).toBeInstanceOf(SwitchApiError);
		// Should return the last HTTP error (engine B)
		expect(err.status).toBe(503);
		expect(err.message).toBe('engine B overloaded');
	});

	it('GET throws generic error when all engines have network errors', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));
		vi.stubGlobal('fetch', mockFetch);

		await expect(getState()).rejects.toThrow('All engines failed for GET /api/switch/state');
	});

	it('DELETE requests still fan out to both engines', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(null, { status: 204 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await deleteSRTSource('srt:cam1');

		// DELETE is a mutation — should fan out to both
		expect(mockFetch).toHaveBeenCalledTimes(2);
	});

	it('PUT requests still fan out to both engines', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(null, { status: 204 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await updateSRTLatency('srt:cam1', 250);

		// PUT is a mutation — should fan out to both
		expect(mockFetch).toHaveBeenCalledTimes(2);
	});

	it('GET requests do not receive PTP timestamps', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		setDualTimestampProvider(() => ({ execute_at: 99999, command_seq: 1 }));
		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await getState();

		// GET request has no body — verify no body was sent
		const callOpts = mockFetch.mock.calls[0][1];
		expect(callOpts.body).toBeUndefined();
	});

	it('succeeds when one engine is unreachable and other responds', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn()
			.mockRejectedValueOnce(new TypeError('Failed to fetch'))
			.mockResolvedValueOnce(
				new Response(JSON.stringify({ programSource: 'cam1' }), { status: 200 }),
			);
		vi.stubGlobal('fetch', mockFetch);

		const result = await getState();
		expect(result.programSource).toBe('cam1');
	});
});

describe('dual-engine recordings', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		localStorage.clear();
		setEngineUrls(null);
		setDualTimestampProvider(null);
		resetCircuitBreakers();
	});

	it('listRecordingsDualEngine merges recordings from both engines', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);

		const recsA = [{ filename: 'rec-a.ts', path: '/data/rec-a.ts', byteSize: 1000, modTime: '2026-04-05T01:00:00Z' }];
		const recsB = [{ filename: 'rec-b.ts', path: '/data/rec-b.ts', byteSize: 2000, modTime: '2026-04-05T02:00:00Z' }];

		const mockFetch = vi.fn().mockImplementation((url: string) => {
			if (url.includes('engine-a')) {
				return Promise.resolve(new Response(JSON.stringify(recsA), { status: 200 }));
			}
			return Promise.resolve(new Response(JSON.stringify(recsB), { status: 200 }));
		});
		vi.stubGlobal('fetch', mockFetch);

		const groups = await listRecordingsDualEngine();
		expect(groups).toHaveLength(2);
		expect(groups[0].engineId).toBe('A');
		expect(groups[0].recordings).toEqual(recsA);
		expect(groups[1].engineId).toBe('B');
		expect(groups[1].recordings).toEqual(recsB);
	});

	it('listRecordingsDualEngine falls back to single-engine list', async () => {
		// No engine URLs set — single-engine mode
		const recs = [{ filename: 'rec.ts', path: '/data/rec.ts', byteSize: 500, modTime: '2026-04-05T00:00:00Z' }];
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve(recs),
			status: 200,
			headers: new Headers(),
		});
		vi.stubGlobal('fetch', mockFetch);

		const groups = await listRecordingsDualEngine();
		expect(groups).toHaveLength(1);
		expect(groups[0].engineId).toBe('A');
		expect(groups[0].recordings).toEqual(recs);
	});

	it('listRecordingsFromEngine returns empty on failure', async () => {
		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ error: 'fail' }), { status: 500 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		const recs = await listRecordingsFromEngine('https://engine-a.example.com');
		expect(recs).toEqual([]);
	});

	it('listRecordingsFromEngine returns empty on network error', async () => {
		const mockFetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));
		vi.stubGlobal('fetch', mockFetch);

		const recs = await listRecordingsFromEngine('https://engine-a.example.com');
		expect(recs).toEqual([]);
	});

	it('importRecordingFromEngine targets specific engine', async () => {
		const clip = { id: 'clip1', name: 'rec.ts', source: 'recording' };
		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify(clip), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		const result = await importRecordingFromEngine('https://engine-b.example.com', '/data/rec.ts');
		expect(mockFetch).toHaveBeenCalledTimes(1);
		expect(mockFetch.mock.calls[0][0]).toBe('https://engine-b.example.com/api/clips/from-recording');
		const body = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(body.path).toBe('/data/rec.ts');
		expect(result).toEqual(clip);
	});

	it('importRecordingFromEngine throws on error', async () => {
		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ error: 'not found' }), { status: 404 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await expect(importRecordingFromEngine('https://engine-b.example.com', '/data/missing.ts'))
			.rejects.toThrow(SwitchApiError);
	});

	it('listRecordingsDualEngine handles one engine failing', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);

		const recsA = [{ filename: 'rec-a.ts', path: '/data/rec-a.ts', byteSize: 1000, modTime: '2026-04-05T01:00:00Z' }];

		const mockFetch = vi.fn().mockImplementation((url: string) => {
			if (url.includes('engine-a')) {
				return Promise.resolve(new Response(JSON.stringify(recsA), { status: 200 }));
			}
			return Promise.reject(new TypeError('Failed to fetch'));
		});
		vi.stubGlobal('fetch', mockFetch);

		const groups = await listRecordingsDualEngine();
		// Both groups should be present — engine B returns empty due to catch
		expect(groups).toHaveLength(2);
		expect(groups[0].recordings).toEqual(recsA);
		expect(groups[1].recordings).toEqual([]);
	});
});

describe('dual-engine interceptor edge cases', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		localStorage.clear();
		setEngineUrls(null);
		setDualTimestampProvider(null);
		resetCircuitBreakers();
	});

	it('returns first successful response when both engines succeed', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ programSource: 'cam2', seq: 10 }), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		const result = await cut('cam2');
		// Both engines should have been called
		expect(mockFetch).toHaveBeenCalledTimes(2);
		// Result should have the expected data
		expect(result.programSource).toBe('cam2');
		expect(result.seq).toBe(10);
	});

	it('succeeds for POST mutation when engine A network fails but B succeeds', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn()
			.mockRejectedValueOnce(new TypeError('Failed to fetch'))
			.mockResolvedValueOnce(
				new Response(JSON.stringify({ programSource: 'cam1' }), { status: 200 }),
			);
		vi.stubGlobal('fetch', mockFetch);

		const result = await cut('cam1');
		expect(result.programSource).toBe('cam1');
	});

	it('adds PTP timestamps to startTransition (timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 1_000_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 7 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ inTransition: true }), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await startTransition('cam2', 'mix', 1000);

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
		expect(bodyA.command_seq).toBe(7);
		expect(bodyA.source).toBe('cam2');
		expect(bodyA.type).toBe('mix');
	});

	it('adds PTP timestamps to recallPreset (timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 1_500_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 2 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await recallPreset('my-preset');

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
	});

	it('adds PTP timestamps to dveSlotOn (regex timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 2_000_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 3 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await dveSlotOn(0);

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
	});

	it('adds PTP timestamps to clipPlayerPlay (regex timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 2_500_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 4 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await clipPlayerPlay(0);

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
	});

	it('adds PTP timestamps to clipPlayerStop (regex timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 3_000_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 5 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await clipPlayerStop(0);

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
	});

	it('adds PTP timestamps to setMasterLevel (audio timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 3_500_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 20 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await setMasterLevel(-6);

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
		expect(bodyA.level).toBe(-6);
	});

	it('adds PTP timestamps to runMacro (macro timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 4_000_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 21 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ status: 'running' }), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await runMacro('my-macro');

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
	});

	it('adds PTP timestamps to setSourceKey (keying timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 4_200_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 22 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ type: 'chroma', enabled: true }), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await setSourceKey('cam1', { type: 'chroma', enabled: true } as any);

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
	});

	it('adds PTP timestamps to setFormat (format timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 4_400_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 23 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await setFormat('1080p30');

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
		expect(bodyA.format).toBe('1080p30');
	});

	it('adds PTP timestamps to setEncoderBackend (encoder timed path)', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const realisticTs = Date.now() * 1000 + 4_600_000;
		setDualTimestampProvider(() => ({ execute_at: realisticTs, command_seq: 24 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await setEncoderBackend('nvenc');

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		expect(bodyA.execute_at).toBe(realisticTs);
		expect(bodyA.encoder).toBe('nvenc');
	});

	it('both engines receive identical bodies for mutations', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		// One instant for both engines: read per call, a millisecond tick
		// between the two calls gave the bodies different execute_at values.
		const executeAt = Date.now() * 1000 + 4_800_000;
		setDualTimestampProvider(() => ({ execute_at: executeAt, command_seq: 8 }));

		const mockFetch = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({}), { status: 200 }),
		);
		vi.stubGlobal('fetch', mockFetch);

		await cut('cam3');

		const bodyA = JSON.parse(mockFetch.mock.calls[0][1].body);
		const bodyB = JSON.parse(mockFetch.mock.calls[1][1].body);
		expect(bodyA).toEqual(bodyB);
	});

	it('GET falls back from A network error to B network error then throws', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);

		const mockFetch = vi.fn()
			.mockRejectedValueOnce(new TypeError('A is down'))
			.mockRejectedValueOnce(new TypeError('B is also down'));
		vi.stubGlobal('fetch', mockFetch);

		await expect(getState()).rejects.toThrow('All engines failed');
	});

	it('clones Blob body for each engine in dual mode', async () => {
		setEngineUrls([
			{ id: 'A', url: 'https://a.example.com' },
			{ id: 'B', url: 'https://b.example.com' },
		]);
		const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 202 }));
		vi.stubGlobal('fetch', mockFetch);

		// graphicsSequenceUpload calls dualFetch with a File body
		await graphicsSequenceUpload(1, new File(['test data'], 'seq.zip'), 30);

		expect(mockFetch).toHaveBeenCalledTimes(2);
		// Both engines should receive non-empty Blob bodies (File extends Blob)
		const bodyA = mockFetch.mock.calls[0][1].body;
		const bodyB = mockFetch.mock.calls[1][1].body;
		expect(bodyA).toBeInstanceOf(Blob);
		expect(bodyB).toBeInstanceOf(Blob);
		// The two bodies should be distinct objects (cloned, not the same reference)
		expect(bodyA).not.toBe(bodyB);
	});

	describe('getAuthToken', () => {
		it('returns token from localStorage', () => {
			localStorage.setItem('switchframe_operator_token', 'my-token');
			expect(getAuthToken()).toBe('my-token');
		});

		it('returns null when no token is stored', () => {
			localStorage.removeItem('switchframe_operator_token');
			expect(getAuthToken()).toBeNull();
		});
	});
});

describe('recording download and confidence thumbnail', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		localStorage.clear();
		setAuthToken('tok');
	});

	it('recordingDownloadUrl asks the engine holding the file for a ticketed URL', async () => {
		const { recordingDownloadUrl } = await import('./switch-api');
		const mockFetch = vi.fn().mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ url: '/api/clips/recordings/download?path=%2Fr%2Fa.ts&ticket=abc' }),
		});
		vi.stubGlobal('fetch', mockFetch);

		const url = await recordingDownloadUrl('https://engine-b', '/r/a.ts');
		expect(url).toBe('https://engine-b/api/clips/recordings/download?path=%2Fr%2Fa.ts&ticket=abc');
		const [reqUrl, init] = mockFetch.mock.calls[0];
		expect(reqUrl).toBe('https://engine-b/api/clips/recordings/download-url');
		expect(init.method).toBe('POST');
		expect(init.headers).toMatchObject({ Authorization: 'Bearer tok' });
		expect(JSON.parse(init.body)).toEqual({ path: '/r/a.ts' });
	});

	it('fetchConfidenceThumbnail sends the token and treats 204 as no thumbnail yet', async () => {
		const { fetchConfidenceThumbnail } = await import('./switch-api');
		const mockFetch = vi.fn().mockResolvedValue({ ok: true, status: 204 });
		vi.stubGlobal('fetch', mockFetch);

		expect(await fetchConfidenceThumbnail('https://engine-a')).toBeNull();
		const [reqUrl, init] = mockFetch.mock.calls[0];
		expect(reqUrl).toBe('https://engine-a/api/output/confidence');
		expect(init.headers).toMatchObject({ Authorization: 'Bearer tok' });
	});

	it('listRecordingsDualEngine marks an engine that does not answer as unreachable', async () => {
		const { listRecordingsDualEngine } = await import('./switch-api');
		setEngineUrls([{ id: 'A', url: 'https://engine-a' }, { id: 'B', url: 'https://engine-b' }] as never);
		vi.stubGlobal('fetch', vi.fn((url: string) => url.startsWith('https://engine-b')
			? Promise.reject(new TypeError('network'))
			: Promise.resolve({ ok: true, json: () => Promise.resolve([]) })));
		try {
			const groups = await listRecordingsDualEngine();
			expect(groups.map((g) => [g.engineId, g.unreachable])).toEqual([['A', false], ['B', true]]);
		} finally {
			setEngineUrls(null);
			vi.unstubAllGlobals();
		}
	});
});
