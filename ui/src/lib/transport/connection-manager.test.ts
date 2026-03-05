import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Mock the imports before importing the module under test
vi.mock('$lib/transport/connection', () => ({
	createPrismConnection: vi.fn(),
}));

vi.mock('$lib/api/switch-api', () => ({
	getState: vi.fn(),
}));

import { ConnectionManager } from './connection-manager';
import { createPrismConnection } from '$lib/transport/connection';
import { getState } from '$lib/api/switch-api';
import type { ControlRoomState } from '$lib/api/types';

const mockedCreatePrismConnection = vi.mocked(createPrismConnection);
const mockedGetState = vi.mocked(getState);

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
		timestamp: Date.now(),
		...overrides,
	};
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
		state: 'disconnected' as string,
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
	});

	afterEach(() => {
		vi.useRealTimers();
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

	describe('start()', () => {
		it('should fetch initial state on start', async () => {
			setupMocks();
			const state = makeState();
			mockedGetState.mockResolvedValue(state);

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

			expect(mockedGetState).toHaveBeenCalledOnce();
			expect(onStateUpdate).toHaveBeenCalledWith(state);
			expect(onInitialLoadComplete).toHaveBeenCalledOnce();
		});

		it('should fire onInitialLoadError and retry on fetch failure', async () => {
			setupMocks();
			const error = new Error('Network error');
			mockedGetState.mockRejectedValue(error);

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

			expect(onInitialLoadError).toHaveBeenCalledWith('Network error');
			expect(onInitialLoadComplete).not.toHaveBeenCalled();

			// Now make it succeed on retry
			const state = makeState();
			mockedGetState.mockResolvedValue(state);

			// Advance timer past 3-second retry
			await vi.advanceTimersByTimeAsync(3000);

			// getState is also called by polling (every 500ms), so we just verify the retry succeeded
			expect(onInitialLoadComplete).toHaveBeenCalledOnce();
		});

		it('should fire onInitialLoadError with string error message', async () => {
			setupMocks();
			mockedGetState.mockRejectedValue('string error');

			const onInitialLoadError = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError,
			});

			await manager.start();

			expect(onInitialLoadError).toHaveBeenCalledWith('string error');
		});

		it('should begin polling after start', async () => {
			setupMocks();
			const state = makeState();
			mockedGetState.mockResolvedValue(state);

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
			mockedGetState.mockClear();

			const pollingState = makeState({ seq: 2 });
			mockedGetState.mockResolvedValue(pollingState);

			// Advance past one polling interval (500ms)
			await vi.advanceTimersByTimeAsync(500);

			expect(mockedGetState).toHaveBeenCalledOnce();
			expect(onStateUpdate).toHaveBeenCalledWith(pollingState);
		});

		it('should start WebTransport connection', async () => {
			const { mockConnection } = setupMocks();
			mockedGetState.mockResolvedValue(makeState());

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
			mockedGetState.mockResolvedValue(makeState());

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
		it('should call getState repeatedly at 500ms intervals', async () => {
			setupMocks();
			mockedGetState.mockResolvedValue(makeState());

			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			mockedGetState.mockClear();

			mockedGetState.mockResolvedValue(makeState({ seq: 2 }));
			await vi.advanceTimersByTimeAsync(500);
			expect(mockedGetState).toHaveBeenCalledTimes(1);

			mockedGetState.mockResolvedValue(makeState({ seq: 3 }));
			await vi.advanceTimersByTimeAsync(500);
			expect(mockedGetState).toHaveBeenCalledTimes(2);
		});

		it('should silently ignore polling errors', async () => {
			setupMocks();
			mockedGetState.mockResolvedValue(makeState());

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
			mockedGetState.mockRejectedValue(new Error('Poll failed'));
			await vi.advanceTimersByTimeAsync(500);

			expect(onStateUpdate).not.toHaveBeenCalled();

			// Next poll should still work
			mockedGetState.mockResolvedValue(makeState({ seq: 5 }));
			await vi.advanceTimersByTimeAsync(500);
			expect(onStateUpdate).toHaveBeenCalledOnce();
		});

		it('should not start polling twice', async () => {
			setupMocks();
			mockedGetState.mockResolvedValue(makeState());

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
			mockedGetState.mockClear();
			onStateUpdate.mockClear();

			mockedGetState.mockResolvedValue(makeState({ seq: 10 }));
			await vi.advanceTimersByTimeAsync(500);

			// Should only fire once per interval (one poll, not two)
			expect(mockedGetState).toHaveBeenCalledTimes(1);
		});
	});

	describe('WebTransport connection state changes', () => {
		it('should update connectionState to webtransport when connected', async () => {
			const { callbacks } = setupMocks();
			mockedGetState.mockResolvedValue(makeState());

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
			mockedGetState.mockResolvedValue(makeState());

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
			mockedGetState.mockResolvedValue(makeState());

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
			mockedGetState.mockResolvedValue(makeState());

			const onStateUpdate = vi.fn();
			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate,
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			mockedGetState.mockClear();
			onStateUpdate.mockClear();

			// Simulate MoQ delivering state
			const moqState = makeState({ seq: 10, programSource: 'cam3' });
			const data = new TextEncoder().encode(JSON.stringify(moqState));
			callbacks.onControlState(data);

			// Polling should be stopped - advance time and verify no poll calls
			mockedGetState.mockResolvedValue(makeState({ seq: 20 }));
			await vi.advanceTimersByTimeAsync(1000);

			expect(mockedGetState).not.toHaveBeenCalled();
		});

		it('should update connection state to webtransport when MoQ delivers', async () => {
			const { callbacks } = setupMocks();
			mockedGetState.mockResolvedValue(makeState());

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
			mockedGetState.mockResolvedValue(makeState());

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

	describe('stop()', () => {
		it('should stop polling', async () => {
			setupMocks();
			mockedGetState.mockResolvedValue(makeState());

			const manager = new ConnectionManager({
				url: 'https://localhost:8080',
				onStateUpdate: vi.fn(),
				onConnectionStateChange: vi.fn(),
				onInitialLoadComplete: vi.fn(),
				onInitialLoadError: vi.fn(),
			});

			await manager.start();
			mockedGetState.mockClear();

			manager.stop();

			mockedGetState.mockResolvedValue(makeState({ seq: 99 }));
			await vi.advanceTimersByTimeAsync(1000);

			expect(mockedGetState).not.toHaveBeenCalled();
		});

		it('should disconnect WebTransport', async () => {
			const { mockConnection } = setupMocks();
			mockedGetState.mockResolvedValue(makeState());

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
			mockedGetState.mockRejectedValue(new Error('fail'));

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

			mockedGetState.mockClear();
			onInitialLoadError.mockClear();

			// Advance past the retry timer (3s)
			await vi.advanceTimersByTimeAsync(5000);

			// getState should NOT have been called again after stop
			expect(mockedGetState).not.toHaveBeenCalled();
		});

		it('should be safe to call stop multiple times', async () => {
			const { mockConnection } = setupMocks();
			mockedGetState.mockResolvedValue(makeState());

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
