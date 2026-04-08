/**
 * Behavioral tests for DualConnectionManager.
 *
 * These tests verify the real behavior of the dual-connection layer:
 * - State from both engines flows through the reconciler
 * - Connection status aggregation picks the best available
 * - Uint8Array state is decoded before reaching callbacks
 * - Engine discovery/addition works mid-session
 * - Disconnect/reconnect flows update health correctly
 *
 * We can't mock the ConnectionManager constructor easily since it's imported
 * directly, so these tests focus on the public API contract and observable
 * behavior through the callback system.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { ControlRoomState, EngineId } from '$lib/api/types';

// We need to mock the ConnectionManager before importing DualConnectionManager
vi.mock('./connection-manager', () => {
	class MockConnectionManager {
		_config: Record<string, unknown>;
		_connectionState = 'disconnected';
		_transport: WebTransport | null = null;
		start = vi.fn().mockResolvedValue(undefined);
		stop = vi.fn();
		getConnectionState = vi.fn().mockReturnValue('disconnected');
		handleControlData = vi.fn();
		notifyMoQActive = vi.fn();

		constructor(config: Record<string, unknown>) {
			this._config = config;
			if (!(globalThis as Record<string, unknown>).__connectionManagers) {
				(globalThis as Record<string, unknown>).__connectionManagers = [];
			}
			((globalThis as Record<string, unknown>).__connectionManagers as unknown[]).push(this);
		}

		get transport() { return this._transport; }
	}
	return { ConnectionManager: MockConnectionManager };
});

import { DualConnectionManager, type DualConnectionConfig } from './dual-connection';

function makeState(overrides: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 500,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		audioChannels: undefined,
		masterLevel: 0,
		programPeak: [0, 0],
		tallyState: {},
		sources: {},
		seq: 1,
		lastCommandSeq: 1,
		timestamp: 1000,
		...overrides,
	};
}

function getCreatedManagers(): Array<{
	_config: {
		onStateUpdate: (data: ControlRoomState | Uint8Array) => void;
		onConnectionStateChange: (state: string) => void;
	};
	getConnectionState: ReturnType<typeof vi.fn>;
	start: ReturnType<typeof vi.fn>;
	stop: ReturnType<typeof vi.fn>;
}> {
	return ((globalThis as Record<string, unknown>).__connectionManagers as Array<unknown>) as ReturnType<typeof getCreatedManagers>;
}

describe('DualConnectionManager behavior', () => {
	let onStateUpdate: ReturnType<typeof vi.fn>;
	let onConnectionStateChange: ReturnType<typeof vi.fn>;
	let onInitialLoadComplete: ReturnType<typeof vi.fn>;
	let onInitialLoadError: ReturnType<typeof vi.fn>;

	beforeEach(() => {
		(globalThis as Record<string, unknown>).__connectionManagers = [];
		onStateUpdate = vi.fn();
		onConnectionStateChange = vi.fn();
		onInitialLoadComplete = vi.fn();
		onInitialLoadError = vi.fn();
	});

	afterEach(() => {
		vi.clearAllMocks();
	});

	function createDualManager(engines: Array<{ id: EngineId; url: string }>): DualConnectionManager {
		return new DualConnectionManager({
			engines,
			onStateUpdate: onStateUpdate as unknown as DualConnectionConfig['onStateUpdate'],
			onConnectionStateChange: onConnectionStateChange as unknown as DualConnectionConfig['onConnectionStateChange'],
			onInitialLoadComplete: onInitialLoadComplete as unknown as DualConnectionConfig['onInitialLoadComplete'],
			onInitialLoadError: onInitialLoadError as unknown as DualConnectionConfig['onInitialLoadError'],
		});
	}

	// ─── Single-engine mode ───

	describe('single-engine mode', () => {
		it('creates one ConnectionManager for single engine', () => {
			createDualManager([{ id: 'A', url: 'https://a.example.com' }]);
			expect(getCreatedManagers()).toHaveLength(1);
		});

		it('reports isDualEngine=false for single engine', () => {
			const dcm = createDualManager([{ id: 'A', url: 'https://a.example.com' }]);
			expect(dcm.isDualEngine).toBe(false);
		});

		it('passes state directly to callback in single-engine mode (no reconciler)', () => {
			const dcm = createDualManager([{ id: 'A', url: 'https://a.example.com' }]);
			const managers = getCreatedManagers();

			const state = makeState({ programSource: 'cam3' });
			managers[0]._config.onStateUpdate(state);

			expect(onStateUpdate).toHaveBeenCalledOnce();
			// In single-engine mode, the state should be passed directly
			const received = onStateUpdate.mock.calls[0][0] as ControlRoomState;
			expect(received.programSource).toBe('cam3');
		});

		it('decodes Uint8Array state in single-engine mode', () => {
			const dcm = createDualManager([{ id: 'A', url: 'https://a.example.com' }]);
			const managers = getCreatedManagers();

			const state = makeState({ programSource: 'cam-from-binary' });
			const encoded = new TextEncoder().encode(JSON.stringify(state));

			managers[0]._config.onStateUpdate(encoded);

			expect(onStateUpdate).toHaveBeenCalledOnce();
			const received = onStateUpdate.mock.calls[0][0] as ControlRoomState;
			expect(received.programSource).toBe('cam-from-binary');
		});
	});

	// ─── Dual-engine mode ───

	describe('dual-engine mode', () => {
		it('creates two ConnectionManagers for dual engines', () => {
			createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);
			expect(getCreatedManagers()).toHaveLength(2);
		});

		it('reports isDualEngine=true', () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);
			expect(dcm.isDualEngine).toBe(true);
		});

		it('routes state through reconciler in dual-engine mode', () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);
			const managers = getCreatedManagers();

			// Engine A sends state
			const stateA = makeState({ programSource: 'cam1', lastCommandSeq: 10 });
			managers[0]._config.onStateUpdate(stateA);

			// Engine B sends different state with lower seq
			const stateB = makeState({ programSource: 'cam2', lastCommandSeq: 5 });
			managers[1]._config.onStateUpdate(stateB);

			// The callback should receive reconciled state (A is authoritative)
			expect(onStateUpdate).toHaveBeenCalled();
			const lastCall = onStateUpdate.mock.calls[onStateUpdate.mock.calls.length - 1][0] as ControlRoomState;
			expect(lastCall.programSource).toBe('cam1');
		});

		it('decodes Uint8Array state before reconciling in dual-engine mode', () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);
			const managers = getCreatedManagers();

			const stateA = makeState({ programSource: 'binary-a', lastCommandSeq: 10 });
			const encodedA = new TextEncoder().encode(JSON.stringify(stateA));

			managers[0]._config.onStateUpdate(encodedA);

			expect(onStateUpdate).toHaveBeenCalledOnce();
			const received = onStateUpdate.mock.calls[0][0] as ControlRoomState;
			expect(received.programSource).toBe('binary-a');
		});
	});

	// ─── Connection state aggregation ───

	describe('connection state aggregation', () => {
		it('reports webtransport when at least one engine has webtransport', () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);
			const managers = getCreatedManagers();

			managers[0].getConnectionState.mockReturnValue('webtransport');
			managers[1].getConnectionState.mockReturnValue('disconnected');

			// Trigger connection state change on A
			managers[0]._config.onConnectionStateChange('webtransport');

			expect(onConnectionStateChange).toHaveBeenCalledWith('webtransport');
		});

		it('reports polling when best available is polling', () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);
			const managers = getCreatedManagers();

			managers[0].getConnectionState.mockReturnValue('polling');
			managers[1].getConnectionState.mockReturnValue('disconnected');

			managers[0]._config.onConnectionStateChange('polling');

			expect(onConnectionStateChange).toHaveBeenCalledWith('polling');
		});

		it('reports disconnected when both engines are disconnected', () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);
			const managers = getCreatedManagers();

			managers[0].getConnectionState.mockReturnValue('disconnected');
			managers[1].getConnectionState.mockReturnValue('disconnected');

			managers[0]._config.onConnectionStateChange('disconnected');

			expect(onConnectionStateChange).toHaveBeenCalledWith('disconnected');
		});

		it('tracks per-engine connection state', () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);

			expect(dcm.engineConnectionState('A')).toBe('disconnected');
			expect(dcm.engineConnectionState('B')).toBe('disconnected');
		});
	});

	// ─── Late engine addition ───

	describe('addEngine', () => {
		it('adds engine B mid-session and switches to dual mode', () => {
			const dcm = createDualManager([{ id: 'A', url: 'https://a.example.com' }]);
			expect(dcm.isDualEngine).toBe(false);

			dcm.addEngine('B', 'https://b.example.com');
			expect(dcm.isDualEngine).toBe(true);
			expect(getCreatedManagers()).toHaveLength(2);
		});

		it('does not duplicate if engine already exists', () => {
			const dcm = createDualManager([{ id: 'A', url: 'https://a.example.com' }]);

			dcm.addEngine('A', 'https://a.example.com');
			expect(getCreatedManagers()).toHaveLength(1);
		});

		it('starts new engine immediately if already started and initial load done', async () => {
			const dcm = createDualManager([{ id: 'A', url: 'https://a.example.com' }]);
			await dcm.start();

			// Simulate initial load completion via ConnectionManager callback
			const managers = getCreatedManagers();
			const onInitialLoadComplete = (managers[0]._config as unknown as { onInitialLoadComplete: () => void }).onInitialLoadComplete;
			onInitialLoadComplete();

			dcm.addEngine('B', 'https://b.example.com');

			const allManagers = getCreatedManagers();
			expect(allManagers[1].start).toHaveBeenCalled();
		});

		it('does not auto-start new engine if not yet started', () => {
			const dcm = createDualManager([{ id: 'A', url: 'https://a.example.com' }]);
			// Not started yet

			dcm.addEngine('B', 'https://b.example.com');

			const managers = getCreatedManagers();
			expect(managers[1].start).not.toHaveBeenCalled();
		});
	});

	// ─── Lifecycle ───

	describe('lifecycle', () => {
		it('start() starts all managers', async () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);

			await dcm.start();

			const managers = getCreatedManagers();
			expect(managers[0].start).toHaveBeenCalledOnce();
			expect(managers[1].start).toHaveBeenCalledOnce();
		});

		it('stop() stops all managers', () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);

			dcm.stop();

			const managers = getCreatedManagers();
			expect(managers[0].stop).toHaveBeenCalledOnce();
			expect(managers[1].stop).toHaveBeenCalledOnce();
		});

		it('start() succeeds even if one manager fails', async () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);

			const managers = getCreatedManagers();
			managers[1].start.mockRejectedValue(new Error('B failed to connect'));

			// Should not throw
			await dcm.start();
		});
	});

	// ─── RTT and clock ───

	describe('RTT reporting', () => {
		it('reportRTT feeds both reconciler and clock sync', () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);

			dcm.reportRTT('A', 15);

			// Check clock sync got it (in microseconds)
			expect(dcm.clock.getRTT('A')).toBe(15_000);

			// Check reconciler got it (through engineHealth)
			expect(dcm.engineHealth.A.rtt).toBe(15);
		});
	});

	// ─── Command sequence ───

	describe('command sequence', () => {
		it('increments monotonically', () => {
			const dcm = createDualManager([{ id: 'A', url: 'https://a.example.com' }]);

			const seq1 = dcm.nextCommandSeq();
			const seq2 = dcm.nextCommandSeq();
			const seq3 = dcm.nextCommandSeq();

			expect(seq2).toBe(seq1 + 1);
			expect(seq3).toBe(seq2 + 1);
		});

		it('starts from 1', () => {
			const dcm = createDualManager([{ id: 'A', url: 'https://a.example.com' }]);
			expect(dcm.nextCommandSeq()).toBe(1);
		});
	});

	// ─── Health pass-through ───

	describe('health', () => {
		it('exposes reconciler health', () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);
			const managers = getCreatedManagers();

			// Feed state to both engines
			const state = makeState({ lastCommandSeq: 5 });
			managers[0]._config.onStateUpdate(state);
			managers[1]._config.onStateUpdate({ ...state });

			expect(dcm.health.status).toBe('synced');
		});

		it('exposes raw per-engine states', () => {
			const dcm = createDualManager([
				{ id: 'A', url: 'https://a.example.com' },
				{ id: 'B', url: 'https://b.example.com' },
			]);
			const managers = getCreatedManagers();

			managers[0]._config.onStateUpdate(makeState({ programSource: 'cam1' }));

			expect(dcm.rawStates.A).toBeDefined();
			expect(dcm.rawStates.A!.programSource).toBe('cam1');
			expect(dcm.rawStates.B).toBeNull();
		});
	});
});
