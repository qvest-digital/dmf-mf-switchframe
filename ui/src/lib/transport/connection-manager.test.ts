import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Mock the imports before importing the module under test
vi.mock('$lib/transport/connection', () => ({
	createPrismConnection: vi.fn(),
}));

vi.mock('$lib/api/switch-api', () => ({
	getAuthHeaders: vi.fn().mockReturnValue({}),
}));

import { ConnectionManager } from './connection-manager';
import { createPrismConnection } from '$lib/transport/connection';
import { getAuthHeaders } from '$lib/api/switch-api';
import type { ControlRoomState } from '$lib/api/types';

const mockedCreatePrismConnection = vi.mocked(createPrismConnection);
const mockedGetAuthHeaders = vi.mocked(getAuthHeaders);

function makeState(overrides: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 0,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		masterLevel: 0,
		programPeak: [0, 0],
		tallyState: {},
		sources: {},
		seq: 1,
		lastCommandSeq: 0,
		timestamp: Date.now(),
		...overrides,
	};
}

function makeOkFetch(state: ControlRoomState) {
	return vi.fn().mockResolvedValue({
		ok: true,
		json: () => Promise.resolve(state),
		text: () => Promise.resolve(JSON.stringify(state)),
	} as unknown as Response);
}

function makeFailFetch(status = 500, body = 'Internal Server Error') {
	return vi.fn().mockResolvedValue({
		ok: false,
		status,
		text: () => Promise.resolve(body),
		json: () => Promise.resolve({ error: body }),
	} as unknown as Response);
}

// Capture the callbacks that ConnectionManager passes to createPrismConnection
interface CapturedCallbacks {
	onControlState: (data: Uint8Array) => void;
	onConnectionChange: (state: 'connected' | 'disconnected' | 'error' | 'connecting') => void;
}

function setupMocks(): {
	mockConnection: ReturnType<typeof createMockConnection>;
	callbacks: CapturedCallbacks;
} {
	const callbacks: CapturedCallbacks = {
		onControlState: vi.fn(),
		onConnectionChange: vi.fn(),
	};

	const mockConnection = createMockConnection();

	mockedCreatePrismConnection.mockImplementation((config) => {
		callbacks.onControlState = config.onControlState;
		callbacks.onConnectionChange = config.onConnectionChange;
		return mockConnection;
	});

	return { mockConnection, callbacks };
}

function createMockConnection() {
	return {
		state: 'disconnected' as const,
		transport: null as WebTransport | null,
		connect: vi.fn(),
		disconnect: vi.fn(),
		_handleControlData: vi.fn(),
		_setConnectionState: vi.fn(),
	};
}

describe('ConnectionManager', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.clearAllMocks();
		mockedGetAuthHeaders.mockReturnValue({});
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	it('should create a ConnectionManager instance', () => {
		const { mockConnection } = setupMocks();
		const manager = new ConnectionManager({
			url: 'https://localhost:8080',
			onStateUpdate: vi.fn(),
			onConnectionStateChange: vi.fn(),
			onInitialLoadComplete: vi.fn(),
			onInitialLoadError: vi.fn(),
		});

		expect(manager).toBeDefined();
		expect(mockedCreatePrismConnection).toHaveBeenCalledOnce();
	});

	it('should expose transport from underlying PrismConnection', () => {
		const { mockConnection } = setupMocks();
		const manager = new ConnectionManager({
			url: 'https://localhost:8080',
			onStateUpdate: vi.fn(),
			onConnectionStateChange: vi.fn(),
			onInitialLoadComplete: vi.fn(),
			onInitialLoadError: vi.fn(),
		});

		expect(manager.transport).toBeNull();

		// Simulate transport becoming available
		const fakeTransport = {} as WebTransport;
		mockConnection.transport = fakeTransport;
		expect(manager.transport).toBe(fakeTransport);
	});

	it('should return disconnected as initial connection state', () => {
		setupMocks();
		const manager = new ConnectionManager({
			url: 'https://localhost:8080',
			onStateUpdate: vi.fn(),
			onConnectionStateChange: vi.fn(),
			onInitialLoadComplete: vi.fn(),
			onInitialLoadError: vi.fn(),
		});

		expect(manager.getConnectionState()).toBe('disconnected');
	});

	// ─── Engine-specific URL test (TDD: write first) ───

	describe('engine-specific URL polling', () => {
		it('fetches from the engine-specific URL, not global getState', async () => {
			setupMocks();
			const state = makeState();
			const mockFetch = makeOkFetch(state);
			vi.stubGlobal('fetch', mockFetch);

			const onStateUpdate = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://engine-a.example.com',
				onStateUpdate,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();

			// All fetch calls should target the engine-specific URL
			for (const call of mockFetch.mock.calls) {
				expect(call[0]).toBe('https://engine-a.example.com/api/switch/state');
			}
			expect(mockFetch).toHaveBeenCalled();
		});

		it('polling also uses the engine-specific URL', async () => {
			setupMocks();
			const state = makeState();
			const mockFetch = makeOkFetch(state);
			vi.stubGlobal('fetch', mockFetch);

			const manager = new ConnectionManager({
				url: 'https://engine-b.example.com',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			mockFetch.mockClear();

			// Advance past one poll interval
			await vi.advanceTimersByTimeAsync(500);

			expect(mockFetch).toHaveBeenCalledOnce();
			expect(mockFetch.mock.calls[0][0]).toBe('https://engine-b.example.com/api/switch/state');
		});

		it('two managers with different URLs each fetch their own engine', async () => {
			setupMocks();

			const stateA = makeState({ programSource: 'cam-a' });
			const stateB = makeState({ programSource: 'cam-b' });

			const mockFetch = vi.fn().mockImplementation((url: string) => {
				if (url.includes('engine-a')) {
					return Promise.resolve({ ok: true, json: () => Promise.resolve(stateA), text: () => Promise.resolve('') } as unknown as Response);
				}
				return Promise.resolve({ ok: true, json: () => Promise.resolve(stateB), text: () => Promise.resolve('') } as unknown as Response);
			});
			vi.stubGlobal('fetch', mockFetch);

			const onStateUpdateA = vi.fn();
			const managerA = new ConnectionManager({
				url: 'https://engine-a.example.com',
				onStateUpdate: onStateUpdateA,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			const onStateUpdateB = vi.fn();
			const managerB = new ConnectionManager({
				url: 'https://engine-b.example.com',
				onStateUpdate: onStateUpdateB,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await managerA.start();
			await managerB.start();

			// A received cam-a state, B received cam-b state
			expect(onStateUpdateA).toHaveBeenCalledWith(stateA);
			expect(onStateUpdateB).toHaveBeenCalledWith(stateB);
		});
	});

	describe('start()', () => {
		it('should fetch initial state on start', async () => {
			setupMocks();
			const state = makeState();
			const mockFetch = makeOkFetch(state);
			vi.stubGlobal('fetch', mockFetch);

			const onStateUpdate = vi.fn();
			const onInitialLoadComplete = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete,
				onInitialLoadError: vi.fn(),
			});

			await manager.start();

			expect(mockFetch).toHaveBeenCalledWith(
				'https://localhost:8080/api/switch/state',
				expect.objectContaining({ headers: {} })
			);
			expect(onStateUpdate).toHaveBeenCalledWith(state);
			expect(onInitialLoadComplete).toHaveBeenCalledOnce();
		});

		it('should fire onInitialLoadError and retry on fetch failure', async () => {
			setupMocks();
			const mockFetch = makeFailFetch(503, 'Service Unavailable');
			vi.stubGlobal('fetch', mockFetch);

			const onInitialLoadError = vi.fn();
			const onInitialLoadComplete = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete,
				onInitialLoadError,
			});

			await manager.start();

			expect(onInitialLoadError).toHaveBeenCalledOnce();
			expect(onInitialLoadComplete).not.toHaveBeenCalled();

			// Now make it succeed on retry
			const state = makeState();
			mockFetch.mockResolvedValue({ ok: true, json: () => Promise.resolve(state), text: () => Promise.resolve('') } as unknown as Response);

			// Advance timer past 3-second retry
			await vi.advanceTimersByTimeAsync(3000);

			expect(onInitialLoadComplete).toHaveBeenCalledOnce();
		});

		it('should fire onInitialLoadError when fetch rejects (network error)', async () => {
			setupMocks();
			const mockFetch = vi.fn().mockRejectedValue(new Error('Network error'));
			vi.stubGlobal('fetch', mockFetch);

			const onInitialLoadError = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError,
			});

			await manager.start();

			expect(onInitialLoadError).toHaveBeenCalledWith('Network error', expect.any(Error));
		});

		it('should begin polling after start', async () => {
			setupMocks();
			const state = makeState();
			const mockFetch = makeOkFetch(state);
			vi.stubGlobal('fetch', mockFetch);

			const onStateUpdate = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();

			// Reset to track only polling calls
			onStateUpdate.mockClear();
			mockFetch.mockClear();

			const pollingState = makeState({ seq: 2 });
			mockFetch.mockResolvedValue({ ok: true, json: () => Promise.resolve(pollingState), text: () => Promise.resolve('') } as unknown as Response);

			// Advance past one polling interval (500ms)
			await vi.advanceTimersByTimeAsync(500);

			expect(mockFetch).toHaveBeenCalledOnce();
			expect(onStateUpdate).toHaveBeenCalledWith(pollingState);
		});

		it('should start WebTransport connection', async () => {
			const { mockConnection } = setupMocks();
			const mockFetch = makeOkFetch(makeState());
			vi.stubGlobal('fetch', mockFetch);

			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();

			expect(mockConnection.connect).toHaveBeenCalledOnce();
		});

		it('should set connection state to polling on start', async () => {
			setupMocks();
			const mockFetch = makeOkFetch(makeState());
			vi.stubGlobal('fetch', mockFetch);

			const onConnectionStateChange = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange,
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();

			expect(onConnectionStateChange).toHaveBeenCalledWith('polling');
			expect(manager.getConnectionState()).toBe('polling');
		});
	});

	describe('polling', () => {
		it('should fetch repeatedly at 500ms intervals', async () => {
			setupMocks();
			const mockFetch = makeOkFetch(makeState());
			vi.stubGlobal('fetch', mockFetch);

			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			mockFetch.mockClear();

			mockFetch.mockResolvedValue({ ok: true, json: () => Promise.resolve(makeState({ seq: 2 })), text: () => Promise.resolve('') } as unknown as Response);
			await vi.advanceTimersByTimeAsync(500);
			expect(mockFetch).toHaveBeenCalledTimes(1);

			mockFetch.mockResolvedValue({ ok: true, json: () => Promise.resolve(makeState({ seq: 3 })), text: () => Promise.resolve('') } as unknown as Response);
			await vi.advanceTimersByTimeAsync(500);
			expect(mockFetch).toHaveBeenCalledTimes(2);
		});

		it('should silently ignore polling errors', async () => {
			setupMocks();
			const mockFetch = makeOkFetch(makeState());
			vi.stubGlobal('fetch', mockFetch);

			const onStateUpdate = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			onStateUpdate.mockClear();

			// Polling error should not crash
			mockFetch.mockRejectedValue(new Error('Poll failed'));
			await vi.advanceTimersByTimeAsync(500);

			expect(onStateUpdate).not.toHaveBeenCalled();

			// Next poll uses exponential backoff (500ms * 2 = 1000ms after failure)
			const nextState = makeState({ seq: 5 });
			mockFetch.mockResolvedValue({ ok: true, json: () => Promise.resolve(nextState), text: () => Promise.resolve('') } as unknown as Response);
			await vi.advanceTimersByTimeAsync(1000);
			expect(onStateUpdate).toHaveBeenCalledOnce();
		});

		it('should not start polling twice', async () => {
			setupMocks();
			const mockFetch = makeOkFetch(makeState());
			vi.stubGlobal('fetch', mockFetch);

			const onStateUpdate = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();

			// Calling start again should not create a second poll interval
			await manager.start();

			// Clear call counts after both starts have completed
			mockFetch.mockClear();
			onStateUpdate.mockClear();

			mockFetch.mockResolvedValue({ ok: true, json: () => Promise.resolve(makeState({ seq: 10 })), text: () => Promise.resolve('') } as unknown as Response);
			await vi.advanceTimersByTimeAsync(500);

			// Should only fire once per interval (one poll, not two)
			expect(mockFetch).toHaveBeenCalledTimes(1);
		});
	});

	describe('WebTransport connection state changes', () => {
		it('should update connectionState to webtransport when connected', async () => {
			const { callbacks } = setupMocks();
			vi.stubGlobal('fetch', makeOkFetch(makeState()));

			const onConnectionStateChange = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange,
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			onConnectionStateChange.mockClear();

			callbacks.onConnectionChange('connected');

			expect(manager.getConnectionState()).toBe('webtransport');
			expect(onConnectionStateChange).toHaveBeenCalledWith('webtransport');
		});

		it('should fall back to polling when WebTransport disconnects', async () => {
			const { callbacks } = setupMocks();
			vi.stubGlobal('fetch', makeOkFetch(makeState()));

			const onConnectionStateChange = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange,
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();

			// Simulate WebTransport connecting then disconnecting
			callbacks.onConnectionChange('connected');
			onConnectionStateChange.mockClear();

			callbacks.onConnectionChange('disconnected');

			// Should be polling (because startPolling restarts it)
			expect(manager.getConnectionState()).toBe('polling');
			expect(onConnectionStateChange).toHaveBeenCalledWith('polling');
		});

		it('should fall back to polling when WebTransport errors', async () => {
			const { callbacks } = setupMocks();
			vi.stubGlobal('fetch', makeOkFetch(makeState()));

			const onConnectionStateChange = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange,
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			callbacks.onConnectionChange('connected');
			onConnectionStateChange.mockClear();

			callbacks.onConnectionChange('error');

			expect(manager.getConnectionState()).toBe('polling');
			expect(onConnectionStateChange).toHaveBeenCalledWith('polling');
		});
	});

	describe('MoQ state callback', () => {
		it('should stop polling when MoQ delivers state', async () => {
			const { callbacks } = setupMocks();
			const mockFetch = makeOkFetch(makeState());
			vi.stubGlobal('fetch', mockFetch);

			const onStateUpdate = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			mockFetch.mockClear();
			onStateUpdate.mockClear();

			// Simulate MoQ delivering state
			const moqState = makeState({ seq: 10, programSource: 'cam3' });
			const data = new TextEncoder().encode(JSON.stringify(moqState));
			callbacks.onControlState(data);

			// Polling should be stopped - advance time and verify no poll calls
			mockFetch.mockResolvedValue({ ok: true, json: () => Promise.resolve(makeState({ seq: 20 })), text: () => Promise.resolve('') } as unknown as Response);
			await vi.advanceTimersByTimeAsync(1000);

			expect(mockFetch).not.toHaveBeenCalled();
		});

		it('should update connection state to webtransport when MoQ delivers', async () => {
			const { callbacks } = setupMocks();
			vi.stubGlobal('fetch', makeOkFetch(makeState()));

			const onConnectionStateChange = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange,
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			onConnectionStateChange.mockClear();

			const data = new TextEncoder().encode(JSON.stringify(makeState({ seq: 10 })));
			callbacks.onControlState(data);

			expect(manager.getConnectionState()).toBe('webtransport');
			expect(onConnectionStateChange).toHaveBeenCalledWith('webtransport');
		});

		it('should fire onStateUpdate with MoQ data', async () => {
			const { callbacks } = setupMocks();
			vi.stubGlobal('fetch', makeOkFetch(makeState()));

			const onStateUpdate = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			onStateUpdate.mockClear();

			const data = new TextEncoder().encode(JSON.stringify(makeState({ seq: 5 })));
			callbacks.onControlState(data);

			expect(onStateUpdate).toHaveBeenCalledWith(data);
		});
	});

	describe('handleControlData()', () => {
		it('should dispatch state update and stop polling', async () => {
			setupMocks();
			const mockFetch = makeOkFetch(makeState());
			vi.stubGlobal('fetch', mockFetch);

			const onStateUpdate = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			mockFetch.mockClear();
			onStateUpdate.mockClear();

			const data = new TextEncoder().encode(JSON.stringify(makeState({ seq: 10 })));
			manager.handleControlData(data);

			expect(onStateUpdate).toHaveBeenCalledWith(data);
			expect(manager.getConnectionState()).toBe('webtransport');

			// Polling should be stopped
			await vi.advanceTimersByTimeAsync(1000);
			expect(mockFetch).not.toHaveBeenCalled();
		});

		it('should work before start() is called', () => {
			setupMocks();

			const onStateUpdate = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			const data = new TextEncoder().encode(JSON.stringify(makeState({ seq: 1 })));
			manager.handleControlData(data);

			expect(onStateUpdate).toHaveBeenCalledWith(data);
			expect(manager.getConnectionState()).toBe('webtransport');
		});
	});

	describe('stop()', () => {
		it('should stop polling', async () => {
			setupMocks();
			const mockFetch = makeOkFetch(makeState());
			vi.stubGlobal('fetch', mockFetch);

			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			mockFetch.mockClear();

			manager.stop();

			mockFetch.mockResolvedValue({ ok: true, json: () => Promise.resolve(makeState({ seq: 99 })), text: () => Promise.resolve('') } as unknown as Response);
			await vi.advanceTimersByTimeAsync(1000);

			expect(mockFetch).not.toHaveBeenCalled();
		});

		it('should disconnect WebTransport', async () => {
			const { mockConnection } = setupMocks();
			vi.stubGlobal('fetch', makeOkFetch(makeState()));

			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			manager.stop();

			expect(mockConnection.disconnect).toHaveBeenCalledOnce();
		});

		it('should clear retry timers', async () => {
			setupMocks();
			const mockFetch = vi.fn().mockRejectedValue(new Error('fail'));
			vi.stubGlobal('fetch', mockFetch);

			const onInitialLoadError = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError,
			});

			await manager.start();

			// There's a retry timer scheduled. Stop should clear it.
			manager.stop();

			mockFetch.mockClear();
			onInitialLoadError.mockClear();

			// Advance past the retry timer (3s)
			await vi.advanceTimersByTimeAsync(5000);

			// fetch should NOT have been called again after stop
			expect(mockFetch).not.toHaveBeenCalled();
		});

		it('should be safe to call stop multiple times', async () => {
			const { mockConnection } = setupMocks();
			vi.stubGlobal('fetch', makeOkFetch(makeState()));

			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();

			// Should not throw
			manager.stop();
			manager.stop();

			expect(mockConnection.disconnect).toHaveBeenCalledTimes(2);
		});

		it('should be safe to call stop without start', () => {
			setupMocks();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			// Should not throw
			manager.stop();
		});
	});
});
