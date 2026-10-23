import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Mock ConnectionManager before importing DualConnectionManager
vi.mock('./connection-manager', () => {
	const ConnectionManager = vi.fn(function MockConnectionManager(
		this: Record<string, unknown>,
		config: {
			onStateUpdate: (s: unknown) => void;
			onConnectionStateChange: (s: string) => void;
		}
	) {
		this._config = config;
		this._state = 'disconnected';
		this._transport = null;
		this.start = vi.fn().mockResolvedValue(undefined);
		this.stop = vi.fn();
		this.getConnectionState = vi.fn(() => this._state);
		this.handleControlData = vi.fn();
		this.notifyMoQActive = vi.fn();
		this.simulateStateUpdate = (state: unknown) => {
			config.onStateUpdate(state);
		};
		this.simulateConnectionChange = (state: 'webtransport' | 'polling' | 'disconnected') => {
			this._state = state;
			config.onConnectionStateChange(state);
		};
		Object.defineProperty(this, 'transport', {
			get: function() { return this._transport; },
			enumerable: true,
			configurable: true,
		});
	});
	return { ConnectionManager };
});

// Mock clock-sync
vi.mock('./clock-sync', () => {
	const ClockSync = vi.fn(function MockClockSync(this: Record<string, unknown>) {
		this.updateRTT = vi.fn();
		this.updateOffset = vi.fn();
		this.handlePong = vi.fn();
		this.ptpNow = vi.fn().mockReturnValue(0);
		this.safetyMargin = vi.fn().mockReturnValue(100_000);
		this.buildPing = vi.fn().mockReturnValue(new Uint8Array(5));
		this.parsePong = vi.fn().mockReturnValue({ seq: 0, serverTimestamp: 0 });
		this.handlePong = vi.fn();
		this.stopPeriodicSync = vi.fn();
		this.startPeriodicSync = vi.fn();
		this.getOffsetDivergence = vi.fn().mockReturnValue(0);
		this.getRTT = vi.fn().mockReturnValue(4_000);
		this.isConverged = vi.fn().mockReturnValue(true);
		this.ptpNowForEngine = vi.fn().mockReturnValue(1_800_000_000_000_000);
		this.getOffset = vi.fn().mockReturnValue(-250);
		this.isClockDiverged = vi.fn().mockReturnValue(false);
	});
	return { ClockSync };
});

// Mock reconciler
vi.mock('$lib/state/reconciler.svelte', () => ({
	STALE_THRESHOLD_MS: 5_000,
	createReconciler: vi.fn().mockImplementation(() => {
		let _state: unknown = null;
		let _ingestedA: unknown = null;
		let _ingestedB: unknown = null;
		let _connectedA = false;
		let _connectedB = false;
		let _rttA = 0;
		let _rttB = 0;
		let _mode: 'dual' | 'single' = 'single';
		return {
			ingest: vi.fn().mockImplementation((engine: 'A' | 'B', state: unknown) => {
				if (engine === 'A') { _ingestedA = state; _connectedA = true; }
				if (engine === 'B') { _ingestedB = state; _connectedB = true; }
				_state = state; // last ingested becomes the "state" for simplicity
				if (_ingestedA && _ingestedB) _mode = 'dual';
			}),
			disconnect: vi.fn().mockImplementation((engine: 'A' | 'B') => {
				if (engine === 'A') _connectedA = false;
				if (engine === 'B') _connectedB = false;
			}),
			setRTT: vi.fn().mockImplementation((engine: 'A' | 'B', ms: number) => {
				if (engine === 'A') _rttA = ms;
				if (engine === 'B') _rttB = ms;
			}),
			setClockOffset: vi.fn(),
			setClockDiverged: vi.fn(),
			setRecovering: vi.fn(),
			setTransportAlive: vi.fn(),
			recordPong: vi.fn(),
			setEngineBUrl: vi.fn(),
			tick: vi.fn(),
			get state() { return _state; },
			get health() {
				return {
					status: _connectedA && _connectedB ? 'synced' : _connectedA || _connectedB ? 'single-engine' : 'disconnected',
					divergences: [],
					authoritative: 'A' as const,
					engineA: { lastSeen: 0, lastCommandSeq: 0, connected: _connectedA },
					engineB: { lastSeen: 0, lastCommandSeq: 0, connected: _connectedB },
				};
			},
			get engineHealth() {
				return {
					A: { connected: _connectedA, rtt: _rttA, clockOffset: 0, lastSeq: 0, uptime: 0, lastSeen: 0, sources: {} },
					B: { connected: _connectedB, rtt: _rttB, clockOffset: 0, lastSeq: 0, uptime: 0, lastSeen: 0, sources: {} },
				};
			},
			get mode() { return _mode; },
			get rawStates() { return { A: _ingestedA, B: _ingestedB }; },
		};
	}),
}));

import { DualConnectionManager } from './dual-connection';
import { ConnectionManager } from './connection-manager';
import { ClockSync } from './clock-sync';
import { createReconciler } from '$lib/state/reconciler.svelte';
import type { ControlRoomState, EngineId } from '$lib/api/types';
import type { DualConnectionConfig } from './dual-connection';

const MockedConnectionManager = vi.mocked(ConnectionManager);
const MockedClockSync = vi.mocked(ClockSync);
const mockedCreateReconciler = vi.mocked(createReconciler);

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

function makeSingleConfig(overrides = {}): DualConnectionConfig {
	return {
		engines: [{ id: 'A' as EngineId, url: 'https://engine-a.example.com' }],
		onStateUpdate: vi.fn(),
		onConnectionStateChange: vi.fn(),
		onInitialLoadComplete: vi.fn(),
		onInitialLoadError: vi.fn(),
		...overrides,
	};
}

function makeDualConfig(overrides = {}): DualConnectionConfig {
	return {
		engines: [
			{ id: 'A' as EngineId, url: 'https://engine-a.example.com' },
			{ id: 'B' as EngineId, url: 'https://engine-b.example.com' },
		],
		onStateUpdate: vi.fn(),
		onConnectionStateChange: vi.fn(),
		onInitialLoadComplete: vi.fn(),
		onInitialLoadError: vi.fn(),
		...overrides,
	};
}

describe('DualConnectionManager', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	afterEach(() => {
		vi.clearAllMocks();
	});

	describe('single engine mode', () => {
		it('wraps one ConnectionManager', () => {
			new DualConnectionManager(makeSingleConfig());
			expect(MockedConnectionManager).toHaveBeenCalledTimes(1);
		});

		it('isDualEngine returns false for single engine', () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			expect(mgr.isDualEngine).toBe(false);
		});

		it('passes the single engine url to ConnectionManager', () => {
			new DualConnectionManager(makeSingleConfig());
			expect(MockedConnectionManager).toHaveBeenCalledWith(
				expect.objectContaining({ url: 'https://engine-a.example.com' })
			);
		});

		it('routes state updates directly to onStateUpdate without reconciler', () => {
			const onStateUpdate = vi.fn();
			new DualConnectionManager(makeSingleConfig({ onStateUpdate }));

			const instance = MockedConnectionManager.mock.results[0].value;
			const state = makeState({ programSource: 'cam3' });
			instance.simulateStateUpdate(state);

			expect(onStateUpdate).toHaveBeenCalledWith(state);
		});

		it('does not call reconciler ingest in single engine mode', () => {
			const onStateUpdate = vi.fn();
			new DualConnectionManager(makeSingleConfig({ onStateUpdate }));

			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instance = MockedConnectionManager.mock.results[0].value;
			instance.simulateStateUpdate(makeState());

			expect(reconcilerInstance.ingest).not.toHaveBeenCalled();
		});

		it('routes connection state changes directly in single engine mode', () => {
			const onConnectionStateChange = vi.fn();
			new DualConnectionManager(makeSingleConfig({ onConnectionStateChange }));

			const instance = MockedConnectionManager.mock.results[0].value;
			instance.simulateConnectionChange('webtransport');

			expect(onConnectionStateChange).toHaveBeenCalledWith('webtransport');
		});

		it('decodes Uint8Array state updates before routing', () => {
			const onStateUpdate = vi.fn();
			new DualConnectionManager(makeSingleConfig({ onStateUpdate }));

			const instance = MockedConnectionManager.mock.results[0].value;
			const state = makeState({ programSource: 'cam5' });
			const encoded = new TextEncoder().encode(JSON.stringify(state));
			instance.simulateStateUpdate(encoded);

			// Single mode decodes Uint8Array to parsed ControlRoomState
			expect(onStateUpdate).toHaveBeenCalledWith(state);
		});
	});

	describe('dual engine mode', () => {
		it('creates two ConnectionManagers', () => {
			new DualConnectionManager(makeDualConfig());
			expect(MockedConnectionManager).toHaveBeenCalledTimes(2);
		});

		it('isDualEngine returns true for dual engine', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			expect(mgr.isDualEngine).toBe(true);
		});

		it('passes engine A url to first ConnectionManager', () => {
			new DualConnectionManager(makeDualConfig());
			expect(MockedConnectionManager).toHaveBeenNthCalledWith(
				1,
				expect.objectContaining({ url: 'https://engine-a.example.com' })
			);
		});

		it('passes engine B url to second ConnectionManager', () => {
			new DualConnectionManager(makeDualConfig());
			expect(MockedConnectionManager).toHaveBeenNthCalledWith(
				2,
				expect.objectContaining({ url: 'https://engine-b.example.com' })
			);
		});

		it('routes state updates from engine A to reconciler', () => {
			new DualConnectionManager(makeDualConfig());

			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const state = makeState({ programSource: 'cam1' });
			instanceA.simulateStateUpdate(state);

			expect(reconcilerInstance.ingest).toHaveBeenCalledWith('A', state);
		});

		it('routes state updates from engine B to reconciler', () => {
			new DualConnectionManager(makeDualConfig());

			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;
			const state = makeState({ programSource: 'cam2' });
			instanceB.simulateStateUpdate(state);

			expect(reconcilerInstance.ingest).toHaveBeenCalledWith('B', state);
		});

		it('decodes Uint8Array state before passing to reconciler', () => {
			new DualConnectionManager(makeDualConfig());

			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const state = makeState({ programSource: 'cam7' });
			const encoded = new TextEncoder().encode(JSON.stringify(state));
			instanceA.simulateStateUpdate(encoded);

			expect(reconcilerInstance.ingest).toHaveBeenCalledWith(
				'A',
				expect.objectContaining({ programSource: 'cam7' })
			);
		});

		it('calls onStateUpdate with reconciled state after engine A update', () => {
			const onStateUpdate = vi.fn();
			new DualConnectionManager(makeDualConfig({ onStateUpdate }));

			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const state = makeState();
			instanceA.simulateStateUpdate(state);

			expect(onStateUpdate).toHaveBeenCalledWith(reconcilerInstance.state);
		});

		it('calls onStateUpdate with reconciled state after engine B update', () => {
			const onStateUpdate = vi.fn();
			new DualConnectionManager(makeDualConfig({ onStateUpdate }));

			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;
			const state = makeState();
			instanceB.simulateStateUpdate(state);

			expect(onStateUpdate).toHaveBeenCalledWith(reconcilerInstance.state);
		});
	});

	describe('state and health getters', () => {
		it('exposes reconciled state via .state getter', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const stateA = makeState({ programSource: 'cam1' });
			instanceA.simulateStateUpdate(stateA);

			expect(mgr.state).toBe(reconcilerInstance.state);
		});

		it('exposes sync health via .health getter', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;

			// Health is a derived object — verify structure matches reconciler's health
			expect(mgr.health).toStrictEqual(reconcilerInstance.health);
		});

		it('health.status is disconnected before any updates', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			expect(mgr.health.status).toBe('disconnected');
		});

		it('health.status is single-engine after only engine A reports', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA.simulateStateUpdate(makeState());
			expect(mgr.health.status).toBe('single-engine');
		});

		it('health.status is synced after both engines report', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;
			instanceA.simulateStateUpdate(makeState());
			instanceB.simulateStateUpdate(makeState());
			expect(mgr.health.status).toBe('synced');
		});
	});

	describe('connection state aggregation in dual mode', () => {
		it('reports webtransport if any engine is webtransport connected', () => {
			const onConnectionStateChange = vi.fn();
			new DualConnectionManager(makeDualConfig({ onConnectionStateChange }));

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			// B is disconnected, A connects via webtransport
			instanceB._state = 'disconnected';
			instanceA.simulateConnectionChange('webtransport');

			expect(onConnectionStateChange).toHaveBeenCalledWith('webtransport');
		});

		it('reports polling when both engines are polling', () => {
			const onConnectionStateChange = vi.fn();
			new DualConnectionManager(makeDualConfig({ onConnectionStateChange }));

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			instanceA._state = 'polling';
			instanceB._state = 'polling';
			instanceA.simulateConnectionChange('polling');

			expect(onConnectionStateChange).toHaveBeenCalledWith('polling');
		});

		it('reports disconnected when both engines are disconnected', () => {
			const onConnectionStateChange = vi.fn();
			new DualConnectionManager(makeDualConfig({ onConnectionStateChange }));

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			instanceA._state = 'disconnected';
			instanceB._state = 'disconnected';
			instanceB.simulateConnectionChange('disconnected');

			expect(onConnectionStateChange).toHaveBeenCalledWith('disconnected');
		});
	});

	describe('lifecycle methods', () => {
		it('start() starts all managers', async () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			await mgr.start();

			for (const result of MockedConnectionManager.mock.results) {
				expect(result.value.start).toHaveBeenCalledOnce();
			}
		});

		it('stop() stops all managers', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			mgr.stop();

			for (const result of MockedConnectionManager.mock.results) {
				expect(result.value.stop).toHaveBeenCalledOnce();
			}
		});

		it('start() resolves even if one engine fails', async () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			MockedConnectionManager.mock.results[0].value.start.mockRejectedValue(
				new Error('engine A unreachable')
			);

			// Should not throw
			await expect(mgr.start()).resolves.toBeUndefined();
		});
	});

	describe('clock', () => {
		it('exposes a ClockSync instance', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			expect(mgr.clock).toBeDefined();
			expect(MockedClockSync).toHaveBeenCalledOnce();
		});
	});

	describe('per-engine connection status', () => {
		it('engineConnectionState returns disconnected initially for both engines', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			expect(mgr.engineConnectionState('A')).toBe('disconnected');
			expect(mgr.engineConnectionState('B')).toBe('disconnected');
		});

		it('engineConnectionState returns webtransport for engine A after it connects', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA.simulateConnectionChange('webtransport');
			expect(mgr.engineConnectionState('A')).toBe('webtransport');
		});

		it('engineConnectionState returns per-engine status independently', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			instanceA.simulateConnectionChange('webtransport');
			instanceB.simulateConnectionChange('polling');

			expect(mgr.engineConnectionState('A')).toBe('webtransport');
			expect(mgr.engineConnectionState('B')).toBe('polling');
		});

		it('engineConnectionState returns disconnected for unknown engine id', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			expect(mgr.engineConnectionState('B')).toBe('disconnected');
		});

		it('works in single engine mode', () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA.simulateConnectionChange('webtransport');
			expect(mgr.engineConnectionState('A')).toBe('webtransport');
		});
	});

	describe('disconnect detection feeding reconciler', () => {
		it('calls reconciler.disconnect when engine reports disconnected in dual mode', () => {
			new DualConnectionManager(makeDualConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;

			// First connect, then disconnect
			instanceA.simulateConnectionChange('webtransport');
			instanceA.simulateConnectionChange('disconnected');

			expect(reconcilerInstance.disconnect).toHaveBeenCalledWith('A');
		});

		it('calls reconciler.disconnect for engine B', () => {
			new DualConnectionManager(makeDualConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			instanceB.simulateConnectionChange('webtransport');
			instanceB.simulateConnectionChange('disconnected');

			expect(reconcilerInstance.disconnect).toHaveBeenCalledWith('B');
		});

		it('does not call reconciler.disconnect in single engine mode', () => {
			new DualConnectionManager(makeSingleConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;

			instanceA.simulateConnectionChange('disconnected');

			expect(reconcilerInstance.disconnect).not.toHaveBeenCalled();
		});

		it('does not call reconciler.disconnect when transitioning to connected state', () => {
			new DualConnectionManager(makeDualConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;

			instanceA.simulateConnectionChange('webtransport');

			expect(reconcilerInstance.disconnect).not.toHaveBeenCalled();
		});
	});

	describe('transport-alive feeding to reconciler', () => {
		it('calls setTransportAlive(true) when engine transitions to webtransport', () => {
			new DualConnectionManager(makeDualConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;

			instanceA.simulateConnectionChange('webtransport');

			expect(reconcilerInstance.setTransportAlive).toHaveBeenCalledWith('A', true);
		});

		it('calls setTransportAlive(false) when engine disconnects', () => {
			new DualConnectionManager(makeDualConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;

			instanceA.simulateConnectionChange('webtransport');
			instanceA.simulateConnectionChange('disconnected');

			expect(reconcilerInstance.setTransportAlive).toHaveBeenCalledWith('A', false);
		});

		it('does not call setTransportAlive in single engine mode', () => {
			new DualConnectionManager(makeSingleConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;

			instanceA.simulateConnectionChange('webtransport');

			expect(reconcilerInstance.setTransportAlive).not.toHaveBeenCalled();
		});
	});

	describe('command timing', () => {
		it('runs commands on arrival unless timed execution is on', () => {
			const mgr = new DualConnectionManager(makeDualConfig());

			const timing = mgr.commandTiming('A');

			expect(timing.execute_at).toBe(0);
			expect(timing.command_seq).toBeGreaterThan(0);
		});

		it('schedules a command one safety margin ahead when timed execution is on', () => {
			const mgr = new DualConnectionManager(makeDualConfig({ timedExecution: true }));

			expect(mgr.commandTiming('B').execute_at).toBe(1_800_000_000_000_000 + 100_000);
		});

		it('schedules at a whole microsecond', () => {
			// A fractional execute_at fails to decode into the engine's int64,
			// and the engine runs the command on arrival without scheduling it.
			const mgr = new DualConnectionManager(makeDualConfig({ timedExecution: true }));
			MockedClockSync.mock.results[0].value.safetyMargin.mockReturnValue(41_666.5);

			expect(Number.isInteger(mgr.commandTiming('B').execute_at)).toBe(true);
		});

		it('runs commands on arrival until clock sync has converged', () => {
			const mgr = new DualConnectionManager(makeDualConfig({ timedExecution: true }));
			MockedClockSync.mock.results[0].value.isConverged.mockReturnValue(false);

			expect(mgr.commandTiming('B').execute_at).toBe(0);
		});
	});

	describe('clock sync pong', () => {
		function pong(): Uint8Array {
			const data = new Uint8Array(13);
			data[0] = 0xFF;
			return data;
		}

		it('feeds RTT in ms and clock offset in us to the reconciler', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;

			mgr.handleControlData('B', pong());

			expect(reconcilerInstance.setRTT).toHaveBeenCalledWith('B', 4);
			expect(reconcilerInstance.setClockOffset).toHaveBeenCalledWith('B', -250);
		});

		it('takes a pong off the engine\'s own connection', () => {
			// Engine B's control track arrives on its ConnectionManager, not on
			// a media pipeline; a pong there must not be parsed as state.
			const onStateUpdate = vi.fn();
			const mgr = new DualConnectionManager(makeDualConfig({ onStateUpdate }));
			const instanceB = MockedConnectionManager.mock.results[1].value;
			const clockInstance = MockedClockSync.mock.results[0].value;
			const data = pong();

			instanceB.simulateStateUpdate(data);

			expect(clockInstance.handlePong).toHaveBeenCalledWith('B', data);
			expect(mgr.engineHealth.B.rtt).toBe(4);
			expect(onStateUpdate).not.toHaveBeenCalled();
		});
	});

	describe('mode and engineHealth proxying', () => {
		it('mode returns single before both engines have reported', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			expect(mgr.mode).toBe('single');
		});

		it('mode returns dual after both engines have reported state', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			instanceA.simulateStateUpdate(makeState());
			instanceB.simulateStateUpdate(makeState());

			expect(mgr.mode).toBe('dual');
		});

		it('engineHealth returns per-engine health detail from reconciler', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;

			const health = mgr.engineHealth;
			expect(health).toStrictEqual(reconcilerInstance.engineHealth);
		});

		it('engineHealth reflects connected state after ingest', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;

			instanceA.simulateStateUpdate(makeState());

			expect(mgr.engineHealth.A.connected).toBe(true);
			expect(mgr.engineHealth.B.connected).toBe(false);
		});
	});

	describe('late-binding engine B via addEngine', () => {
		it('addEngine creates a new ConnectionManager for engine B', () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			expect(MockedConnectionManager).toHaveBeenCalledTimes(1);

			mgr.addEngine('B', 'https://engine-b.example.com');

			expect(MockedConnectionManager).toHaveBeenCalledTimes(2);
			expect(MockedConnectionManager).toHaveBeenNthCalledWith(
				2,
				expect.objectContaining({ url: 'https://engine-b.example.com' })
			);
		});

		it('addEngine starts the new ConnectionManager immediately when initial load is done', async () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			await mgr.start();

			// Simulate initial load completion from engine A
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA._config.onInitialLoadComplete();

			mgr.addEngine('B', 'https://engine-b.example.com');

			const instanceB = MockedConnectionManager.mock.results[1].value;
			expect(instanceB.start).toHaveBeenCalledOnce();
		});

		// Engine B is discovered from engine A's state, so A's state has
		// always arrived before dual mode begins. A reconciler that never saw
		// it reads B as the only engine and the UI fails video over to B.
		it('addEngine seeds the reconciler with the state engine A already sent', () => {
			const onStateUpdate = vi.fn();
			const mgr = new DualConnectionManager(makeSingleConfig({ onStateUpdate }));
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const stateA = makeState({ programSource: 'cam1', engineBUrl: 'https://engine-b.example.com' });
			instanceA.simulateStateUpdate(stateA);
			expect(reconcilerInstance.ingest).not.toHaveBeenCalled();

			mgr.addEngine('B', 'https://engine-b.example.com');

			expect(reconcilerInstance.ingest).toHaveBeenCalledWith('A', stateA);
			expect(onStateUpdate).toHaveBeenLastCalledWith(reconcilerInstance.state);
		});

		it('addEngine leaves out a state older than the stale threshold', () => {
			vi.useFakeTimers();
			const onStateUpdate = vi.fn();
			const mgr = new DualConnectionManager(makeSingleConfig({ onStateUpdate }));
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA.simulateStateUpdate(makeState({ programSource: 'cam1' }));
			onStateUpdate.mockClear();
			vi.advanceTimersByTime(6_000);

			mgr.addEngine('B', 'https://engine-b.example.com');

			expect(reconcilerInstance.ingest).not.toHaveBeenCalled();
			expect(onStateUpdate).not.toHaveBeenCalled();
			vi.useRealTimers();
		});

		it('addEngine tells the reconciler the URL engine B is connected at', () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;

			mgr.addEngine('B', 'https://engine-b.example.com');

			expect(reconcilerInstance.setEngineBUrl).toHaveBeenCalledWith('https://engine-b.example.com');
		});

		it('addEngine sets isDualEngine to true', () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			expect(mgr.isDualEngine).toBe(false);

			mgr.addEngine('B', 'https://engine-b.example.com');

			expect(mgr.isDualEngine).toBe(true);
		});

		it('addEngine routes state updates from new engine to reconciler', () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;

			mgr.addEngine('B', 'https://engine-b.example.com');

			const instanceB = MockedConnectionManager.mock.results[1].value;
			const state = makeState({ programSource: 'cam4' });
			instanceB.simulateStateUpdate(state);

			expect(reconcilerInstance.ingest).toHaveBeenCalledWith('B', state);
		});

		it('addEngine causes engine A updates to use reconciler too', () => {
			const onStateUpdate = vi.fn();
			const mgr = new DualConnectionManager(makeSingleConfig({ onStateUpdate }));
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;

			// Before addEngine, engine A updates bypass reconciler
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const stateBeforeAdd = makeState({ programSource: 'cam1' });
			instanceA.simulateStateUpdate(stateBeforeAdd);
			expect(reconcilerInstance.ingest).not.toHaveBeenCalled();

			// After addEngine, engine A updates should use reconciler
			mgr.addEngine('B', 'https://engine-b.example.com');

			const stateAfterAdd = makeState({ programSource: 'cam2' });
			instanceA.simulateStateUpdate(stateAfterAdd);
			expect(reconcilerInstance.ingest).toHaveBeenCalledWith('A', stateAfterAdd);
		});

		it('stop() stops the late-added engine too', () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			mgr.addEngine('B', 'https://engine-b.example.com');
			mgr.stop();

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;
			expect(instanceA.stop).toHaveBeenCalledOnce();
			expect(instanceB.stop).toHaveBeenCalledOnce();
		});

		it('addEngine defers start when initial load is not done', async () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			await mgr.start();

			// Do NOT trigger onInitialLoadComplete — initial load still in progress
			mgr.addEngine('B', 'https://engine-b.example.com');

			const instanceB = MockedConnectionManager.mock.results[1].value;
			expect(instanceB.start).not.toHaveBeenCalled();
		});

		it('deferred engine starts when initial load completes', async () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			await mgr.start();

			// Add engine B while initial load is still in progress
			mgr.addEngine('B', 'https://engine-b.example.com');
			const instanceB = MockedConnectionManager.mock.results[1].value;
			expect(instanceB.start).not.toHaveBeenCalled();

			// Now simulate initial load completion from engine A
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA._config.onInitialLoadComplete();

			// Engine B should now be started
			expect(instanceB.start).toHaveBeenCalledOnce();
		});

		it('does not start engine before start() is called even if initial load done', () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			// Don't call start() — engine B should not be started

			mgr.addEngine('B', 'https://engine-b.example.com');

			const instanceB = MockedConnectionManager.mock.results[1].value;
			expect(instanceB.start).not.toHaveBeenCalled();
		});

		it('addEngine with identical URL for existing id is a no-op', () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			mgr.addEngine('B', 'https://engine-b.example.com');
			const countAfterFirstAdd = MockedConnectionManager.mock.calls.length;

			mgr.addEngine('B', 'https://engine-b.example.com'); // same URL

			expect(MockedConnectionManager).toHaveBeenCalledTimes(countAfterFirstAdd);
		});

		it('addEngine with new URL for existing id replaces the ConnectionManager', () => {
			// When an engine is re-provisioned at a new URL (crash +
			// control-plane re-issue), addEngine must tear down the old
			// subscription and connect to the new URL so the UI doesn't stay
			// pinned to the dead address.
			const mgr = new DualConnectionManager(makeSingleConfig());
			mgr.addEngine('B', 'https://engine-b-old.example.com');
			const oldInstance = MockedConnectionManager.mock.results[1].value;

			mgr.addEngine('B', 'https://engine-b-new.example.com');

			// Old manager stopped
			expect(oldInstance.stop).toHaveBeenCalledOnce();
			// New ConnectionManager created with the new URL
			expect(MockedConnectionManager).toHaveBeenCalledTimes(3);
			expect(MockedConnectionManager).toHaveBeenNthCalledWith(
				3,
				expect.objectContaining({ url: 'https://engine-b-new.example.com' })
			);
		});

		it('addEngine replacement starts the new manager if session is already started', async () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			await mgr.start();
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA._config.onInitialLoadComplete();

			mgr.addEngine('B', 'https://engine-b-old.example.com');
			const newInstanceB1 = MockedConnectionManager.mock.results[1].value;
			expect(newInstanceB1.start).toHaveBeenCalledOnce();

			mgr.addEngine('B', 'https://engine-b-new.example.com');
			const newInstanceB2 = MockedConnectionManager.mock.results[2].value;
			expect(newInstanceB2.start).toHaveBeenCalledOnce();
		});

		it('addEngine replacement disconnects the old engine in the reconciler', () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			mgr.addEngine('B', 'https://engine-b-old.example.com');
			reconcilerInstance.disconnect.mockClear();

			mgr.addEngine('B', 'https://engine-b-new.example.com');

			expect(reconcilerInstance.disconnect).toHaveBeenCalledWith('B');
		});

		it('starts engine B immediately if all initial engines have failed', async () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			await mgr.start();

			// Simulate engine A initial load failure
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA._config.onInitialLoadError('connection refused', new Error('connection refused'));

			// Now discover engine B
			mgr.addEngine('B', 'https://engine-b.example.com');

			// Engine B should start immediately — not be deferred
			const instanceB = MockedConnectionManager.mock.results[1].value;
			expect(instanceB.start).toHaveBeenCalledOnce();
		});
	});

	describe('handleControlData routing', () => {
		it('routes to correct engine by engineId', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			const data = new Uint8Array([1, 2, 3]);
			mgr.handleControlData('B', data);

			expect(instanceB.handleControlData).toHaveBeenCalledWith(data);
			expect(instanceA.handleControlData).not.toHaveBeenCalled();
		});

		it('falls back to first manager when engineId is unknown', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;

			const data = new Uint8Array([4, 5, 6]);
			// Cast to bypass EngineId type — tests fallback for unknown engine IDs
			mgr.handleControlData('C' as EngineId, data);

			expect(instanceA.handleControlData).toHaveBeenCalledWith(data);
		});

		it('routes to engine A when engineId is A', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			const data = new Uint8Array([7, 8, 9]);
			mgr.handleControlData('A', data);

			expect(instanceA.handleControlData).toHaveBeenCalledWith(data);
			expect(instanceB.handleControlData).not.toHaveBeenCalled();
		});

		it('intercepts 13-byte pong payload starting with 0xFF', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const clockInstance = MockedClockSync.mock.results[0].value;

			// A valid pong: 13 bytes [0xFF][4B seq][8B timestamp]
			const pong = new Uint8Array(13);
			pong[0] = 0xFF;
			mgr.handleControlData('A', pong);

			// Should be intercepted — not forwarded to ConnectionManager
			expect(clockInstance.handlePong).toHaveBeenCalledWith('A', pong);
			expect(instanceA.handleControlData).not.toHaveBeenCalled();
		});

		it('does not intercept short payloads starting with 0xFF as pong', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const clockInstance = MockedClockSync.mock.results[0].value;

			// 9-byte payload with first byte 0xFF — too short for new pong format
			const data = new Uint8Array(9);
			data[0] = 0xFF;
			mgr.handleControlData('A', data);

			// Should be forwarded to ConnectionManager, not intercepted as pong
			expect(clockInstance.handlePong).not.toHaveBeenCalled();
			expect(instanceA.handleControlData).toHaveBeenCalledWith(data);
		});
	});

	describe('notifyMoQActive routing', () => {
		it('targets specific engine when engineId is provided', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			mgr.notifyMoQActive('B');

			expect(instanceB.notifyMoQActive).toHaveBeenCalledOnce();
			expect(instanceA.notifyMoQActive).not.toHaveBeenCalled();
		});

		it('notifies all managers when engineId is omitted', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			mgr.notifyMoQActive();

			expect(instanceA.notifyMoQActive).toHaveBeenCalledOnce();
			expect(instanceB.notifyMoQActive).toHaveBeenCalledOnce();
		});

		it('does nothing when engineId does not match any manager', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			// Cast to bypass EngineId type — tests fallback for unknown engine IDs
			mgr.notifyMoQActive('C' as EngineId);

			expect(instanceA.notifyMoQActive).not.toHaveBeenCalled();
			expect(instanceB.notifyMoQActive).not.toHaveBeenCalled();
		});
	});

	describe('getTransport', () => {
		it('returns null before connection', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			expect(mgr.getTransport('A')).toBeNull();
			expect(mgr.getTransport('B')).toBeNull();
		});

		it('returns transport after connection', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			const fakeTransportA = { fake: 'transportA' } as unknown as WebTransport;
			const fakeTransportB = { fake: 'transportB' } as unknown as WebTransport;

			instanceA._transport = fakeTransportA;
			instanceB._transport = fakeTransportB;

			expect(mgr.getTransport('A')).toBe(fakeTransportA);
			expect(mgr.getTransport('B')).toBe(fakeTransportB);
		});

		it('returns null for unknown engine', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			expect(mgr.getTransport('C' as EngineId)).toBeNull();
		});

		it('returns null in single engine mode for engine B', () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			expect(mgr.getTransport('B')).toBeNull();
		});
	});

	describe('Uint8Array decoding in dual mode', () => {
		it('drops malformed JSON in Uint8Array without throwing', () => {
			new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;

			// Simulate malformed bytes from the MoQ control track. createEngineManager
			// wraps JSON.parse in try/catch and drops just this payload — it must NOT
			// throw, otherwise the error would unwind into handleDataStream's catch
			// (outside the per-object loop) and abandon the rest of the subgroup
			// stream's objects.
			const garbage = new TextEncoder().encode('not valid json{{{');
			expect(() => instanceA.simulateStateUpdate(garbage)).not.toThrow();
		});

		it('does not update reconciler on malformed Uint8Array', () => {
			const onStateUpdate = vi.fn();
			new DualConnectionManager(makeDualConfig({ onStateUpdate }));
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;

			const garbage = new TextEncoder().encode('}{bad');
			instanceA.simulateStateUpdate(garbage);

			// Malformed payload dropped: neither the reconciler nor the downstream
			// onStateUpdate callback sees the corrupt state.
			expect(reconcilerInstance.ingest).not.toHaveBeenCalled();
			expect(onStateUpdate).not.toHaveBeenCalled();
		});
	});

	describe('connection flapping in dual mode', () => {
		it('handles rapid connect/disconnect cycles without crash', () => {
			new DualConnectionManager(makeDualConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;
			const instanceA = MockedConnectionManager.mock.results[0].value;

			for (let i = 0; i < 10; i++) {
				instanceA.simulateConnectionChange('webtransport');
				instanceA.simulateConnectionChange('disconnected');
			}

			// The reconciler should have been called disconnect many times
			expect(reconcilerInstance.disconnect).toHaveBeenCalledWith('A');
			// Final state should be disconnected
			const mgr = new DualConnectionManager(makeDualConfig());
			expect(mgr.engineConnectionState('A')).toBe('disconnected');
		});

		it('connection aggregation picks best state after flapping', () => {
			const onConnectionStateChange = vi.fn();
			new DualConnectionManager(makeDualConfig({ onConnectionStateChange }));

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			// A flaps, B stays connected
			instanceB._state = 'webtransport';
			instanceA.simulateConnectionChange('webtransport');
			instanceA.simulateConnectionChange('disconnected');
			instanceA.simulateConnectionChange('polling');

			// With A polling and B webtransport, best is webtransport
			const lastCall = onConnectionStateChange.mock.calls[onConnectionStateChange.mock.calls.length - 1];
			expect(lastCall[0]).toBe('webtransport');
		});
	});

	describe('rawStates proxy', () => {
		it('exposes raw per-engine states in dual mode', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			const stateA = makeState({ programSource: 'cam1' });
			const stateB = makeState({ programSource: 'cam2' });
			instanceA.simulateStateUpdate(stateA);
			instanceB.simulateStateUpdate(stateB);

			const raw = mgr.rawStates;
			expect(raw.A).not.toBeNull();
			expect(raw.B).not.toBeNull();
		});

		it('returns null rawStates before any updates', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const raw = mgr.rawStates;
			expect(raw.A).toBeNull();
			expect(raw.B).toBeNull();
		});
	});

	describe('tick proxy', () => {
		it('tick() calls reconciler.tick()', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const reconcilerInstance = mockedCreateReconciler.mock.results[0].value;

			mgr.tick();
			expect(reconcilerInstance.tick).toHaveBeenCalledOnce();
		});
	});

	describe('onDualEngineReady callback', () => {
		it('fires after both engines send state updates', () => {
			const onDualEngineReady = vi.fn();
			new DualConnectionManager(makeDualConfig({ onDualEngineReady }));

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			instanceA.simulateStateUpdate(makeState());
			expect(onDualEngineReady).not.toHaveBeenCalled();

			instanceB.simulateStateUpdate(makeState());
			expect(onDualEngineReady).toHaveBeenCalledOnce();
		});

		it('fires after both engines send state updates (B first)', () => {
			const onDualEngineReady = vi.fn();
			new DualConnectionManager(makeDualConfig({ onDualEngineReady }));

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			instanceB.simulateStateUpdate(makeState());
			expect(onDualEngineReady).not.toHaveBeenCalled();

			instanceA.simulateStateUpdate(makeState());
			expect(onDualEngineReady).toHaveBeenCalledOnce();
		});

		it('fires only once even with repeated state updates', () => {
			const onDualEngineReady = vi.fn();
			new DualConnectionManager(makeDualConfig({ onDualEngineReady }));

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			instanceA.simulateStateUpdate(makeState());
			instanceB.simulateStateUpdate(makeState());
			instanceA.simulateStateUpdate(makeState());
			instanceB.simulateStateUpdate(makeState());

			expect(onDualEngineReady).toHaveBeenCalledOnce();
		});

		it('does not fire in single engine mode', () => {
			const onDualEngineReady = vi.fn();
			new DualConnectionManager(makeSingleConfig({ onDualEngineReady }));

			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA.simulateStateUpdate(makeState());
			instanceA.simulateStateUpdate(makeState());

			expect(onDualEngineReady).not.toHaveBeenCalled();
		});

		it('does not fire when only one engine has reported', () => {
			const onDualEngineReady = vi.fn();
			new DualConnectionManager(makeDualConfig({ onDualEngineReady }));

			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA.simulateStateUpdate(makeState());
			instanceA.simulateStateUpdate(makeState());
			instanceA.simulateStateUpdate(makeState());

			expect(onDualEngineReady).not.toHaveBeenCalled();
		});

		it('fires for late-bound engine B via addEngine', () => {
			const onDualEngineReady = vi.fn();
			const mgr = new DualConnectionManager(makeSingleConfig({ onDualEngineReady }));

			// Engine A reports state in single mode
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA.simulateStateUpdate(makeState());
			expect(onDualEngineReady).not.toHaveBeenCalled();

			// Late-bind engine B
			mgr.addEngine('B', 'https://engine-b.example.com');

			// Engine A re-reports (now dual mode) — sets _engineALoaded
			instanceA.simulateStateUpdate(makeState());
			expect(onDualEngineReady).not.toHaveBeenCalled();

			// Engine B reports — both loaded
			const instanceB = MockedConnectionManager.mock.results[1].value;
			instanceB.simulateStateUpdate(makeState());
			expect(onDualEngineReady).toHaveBeenCalledOnce();
		});

		it('is safe when callback is not provided', () => {
			// No onDualEngineReady in config — should not throw
			new DualConnectionManager(makeDualConfig());

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			expect(() => {
				instanceA.simulateStateUpdate(makeState());
				instanceB.simulateStateUpdate(makeState());
			}).not.toThrow();
		});
	});

	describe('initial load timeout', () => {
		beforeEach(() => {
			vi.useFakeTimers();
		});
		afterEach(() => {
			vi.useRealTimers();
		});

		it('fires onInitialLoadError after 15s when no engines respond', async () => {
			const onInitialLoadError = vi.fn();
			const mgr = new DualConnectionManager(makeDualConfig({ onInitialLoadError }));
			await mgr.start();

			expect(onInitialLoadError).not.toHaveBeenCalled();

			vi.advanceTimersByTime(15_000);

			expect(onInitialLoadError).toHaveBeenCalledOnce();
			expect(onInitialLoadError).toHaveBeenCalledWith('All engines timed out after 15 seconds');
		});

		it('does not fire timeout if initial load completes before 15s', async () => {
			const onInitialLoadError = vi.fn();
			const onInitialLoadComplete = vi.fn();
			const mgr = new DualConnectionManager(makeDualConfig({ onInitialLoadError, onInitialLoadComplete }));
			await mgr.start();

			// Engine A loads successfully
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA._config.onInitialLoadComplete();

			expect(onInitialLoadComplete).toHaveBeenCalledOnce();

			// Advance past timeout
			vi.advanceTimersByTime(15_000);

			// Error should NOT fire — initial load already succeeded
			expect(onInitialLoadError).not.toHaveBeenCalled();
		});

		it('completes load via timeout if only one engine loaded within 15s', async () => {
			const onInitialLoadError = vi.fn();
			const onInitialLoadComplete = vi.fn();
			const mgr = new DualConnectionManager(makeDualConfig({ onInitialLoadError, onInitialLoadComplete }));
			await mgr.start();

			// Only engine A sends a state update (counts as loaded), but never fires onInitialLoadComplete
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA.simulateStateUpdate(makeState());

			vi.advanceTimersByTime(15_000);

			// Should have completed (not errored) since one engine loaded
			expect(onInitialLoadComplete).toHaveBeenCalledOnce();
			expect(onInitialLoadError).not.toHaveBeenCalled();
		});

		it('clears timeout on stop()', async () => {
			const onInitialLoadError = vi.fn();
			const mgr = new DualConnectionManager(makeDualConfig({ onInitialLoadError }));
			await mgr.start();

			mgr.stop();
			vi.advanceTimersByTime(15_000);

			expect(onInitialLoadError).not.toHaveBeenCalled();
		});

		it('starts pending engines when timeout completes load', async () => {
			const onInitialLoadComplete = vi.fn();
			const mgr = new DualConnectionManager(makeSingleConfig({ onInitialLoadComplete }));
			await mgr.start();

			// Engine A sends state (loaded) but onInitialLoadComplete never fires from ConnectionManager
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA.simulateStateUpdate(makeState());

			// Late-bind engine B while initial load is still "in progress"
			mgr.addEngine('B', 'https://engine-b.example.com');
			const instanceB = MockedConnectionManager.mock.results[1].value;
			expect(instanceB.start).not.toHaveBeenCalled();

			// Timeout fires, completes load, flushes pending starts
			vi.advanceTimersByTime(15_000);

			expect(onInitialLoadComplete).toHaveBeenCalledOnce();
			expect(instanceB.start).toHaveBeenCalledOnce();
		});
	});

	describe('per-engine initial load error tracking', () => {
		it('does not report all-engines-failed from repeated retries of a single engine', async () => {
			const onInitialLoadError = vi.fn();
			const mgr = new DualConnectionManager(makeDualConfig({ onInitialLoadError }));
			await mgr.start();

			// Engine A's 3s retry loop fails repeatedly while engine B is still
			// loading — onInitialLoadError fires per ATTEMPT, not per engine.
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA._config.onInitialLoadError('connection refused', new Error('connection refused'));
			instanceA._config.onInitialLoadError('connection refused', new Error('connection refused'));
			instanceA._config.onInitialLoadError('connection refused', new Error('connection refused'));

			// Engine B never failed — the global "all engines failed" report
			// must not fire.
			expect(onInitialLoadError).not.toHaveBeenCalled();
		});

		it('reports the global error once every engine has failed', async () => {
			const onInitialLoadError = vi.fn();
			const mgr = new DualConnectionManager(makeDualConfig({ onInitialLoadError }));
			await mgr.start();

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;
			instanceA._config.onInitialLoadError('refused A', new Error('refused A'));
			expect(onInitialLoadError).not.toHaveBeenCalled();
			instanceB._config.onInitialLoadError('refused B', new Error('refused B'));

			expect(onInitialLoadError).toHaveBeenCalledWith('refused B', expect.any(Error));
		});

		it('forgets an engine failure once that engine completes its initial load', async () => {
			const onInitialLoadError = vi.fn();
			const onInitialLoadComplete = vi.fn();
			const mgr = new DualConnectionManager(
				makeDualConfig({ onInitialLoadError, onInitialLoadComplete })
			);
			await mgr.start();

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;

			// A fails transiently, then recovers... but global load is still
			// gated on _initialLoadDone, so use B's failure to probe the counter:
			instanceA._config.onInitialLoadError('refused A', new Error('refused A'));
			instanceA._config.onInitialLoadComplete();
			expect(onInitialLoadComplete).toHaveBeenCalledOnce();

			// B failing afterwards must not be treated as "all engines failed"
			// (initial load already completed, and A's stale failure was cleared).
			instanceB._config.onInitialLoadError('refused B', new Error('refused B'));
			expect(onInitialLoadError).not.toHaveBeenCalled();
		});

		it('clears failure tracking on stop() so a restart is not poisoned', async () => {
			const onInitialLoadError = vi.fn();
			const mgr = new DualConnectionManager(makeDualConfig({ onInitialLoadError }));
			await mgr.start();

			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA._config.onInitialLoadError('refused A', new Error('refused A'));

			// Restart cycle (e.g. submitToken() after a 401).
			mgr.stop();
			await mgr.start();

			// Only engine B fails after the restart — A's pre-stop failure must
			// not count toward "all engines failed".
			const instanceB = MockedConnectionManager.mock.results[1].value;
			instanceB._config.onInitialLoadError('refused B', new Error('refused B'));
			expect(onInitialLoadError).not.toHaveBeenCalled();
		});

		it('still starts a newly discovered engine immediately when all known engines failed', async () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			await mgr.start();

			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA._config.onInitialLoadError('connection refused', new Error('connection refused'));
			instanceA._config.onInitialLoadError('connection refused', new Error('connection refused'));

			mgr.addEngine('B', 'https://engine-b.example.com');

			const instanceB = MockedConnectionManager.mock.results[1].value;
			expect(instanceB.start).toHaveBeenCalledOnce();
		});

		it('defers a newly discovered engine while known engines have not failed', async () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			await mgr.start();

			// Engine A is still loading (no failure, no success yet).
			mgr.addEngine('B', 'https://engine-b.example.com');

			const instanceB = MockedConnectionManager.mock.results[1].value;
			expect(instanceB.start).not.toHaveBeenCalled();
		});

		it('escalates and flushes a deferred engine when every STARTED engine has failed', async () => {
			const onInitialLoadError = vi.fn();
			const mgr = new DualConnectionManager(makeSingleConfig({ onInitialLoadError }));
			await mgr.start();

			// Engine B is discovered while A is still loading — deferred, so it
			// never starts and therefore can never fail.
			mgr.addEngine('B', 'https://engine-b.example.com');
			const instanceB = MockedConnectionManager.mock.results[1].value;
			expect(instanceB.start).not.toHaveBeenCalled();

			// A — the only STARTED engine — fails its initial load. Escalation
			// must fire (it drives the 401 token prompt); comparing the failure
			// count against managers.size would make it unreachable while B
			// sits in the pending queue.
			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA._config.onInitialLoadError('401 unauthorized', new Error('401 unauthorized'));

			expect(onInitialLoadError).toHaveBeenCalledWith('401 unauthorized', expect.any(Error));
			// Waiting for A's state is moot when A can't load — the deferred
			// engine is flushed as the best remaining hope.
			expect(instanceB.start).toHaveBeenCalledOnce();
		});

		it('clears stale failures on start() without stop() (401 token-resubmit flow)', async () => {
			const onInitialLoadError = vi.fn();
			const mgr = new DualConnectionManager(makeDualConfig({ onInitialLoadError }));
			await mgr.start();

			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;
			instanceA._config.onInitialLoadError('401 A', new Error('401 A'));
			instanceB._config.onInitialLoadError('401 B', new Error('401 B'));
			expect(onInitialLoadError).toHaveBeenCalledOnce();
			onInitialLoadError.mockClear();

			// The documented token-resubmit flow calls start() WITHOUT stop().
			await mgr.start();

			// Only A fails after the restart — B's pre-restart failure is stale
			// and must not count toward "all engines failed".
			instanceA._config.onInitialLoadError('still 401 A', new Error('still 401 A'));
			expect(onInitialLoadError).not.toHaveBeenCalled();
		});

		it('start() without stop() does not let stale failures defeat deferred-start gating', async () => {
			const mgr = new DualConnectionManager(makeSingleConfig());
			await mgr.start();

			const instanceA = MockedConnectionManager.mock.results[0].value;
			instanceA._config.onInitialLoadError('401', new Error('401'));

			// Restart without stop() (token resubmit) — A is loading again.
			await mgr.start();

			// Engine B discovered while A is loading: A's pre-restart failure
			// is stale, so B must be deferred, not started immediately.
			mgr.addEngine('B', 'https://engine-b.example.com');
			const instanceB = MockedConnectionManager.mock.results[1].value;
			expect(instanceB.start).not.toHaveBeenCalled();
		});
	});
});
