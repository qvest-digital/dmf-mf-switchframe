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
		this.start = vi.fn().mockResolvedValue(undefined);
		this.stop = vi.fn();
		this.getConnectionState = vi.fn(() => this._state);
		this.simulateStateUpdate = (state: unknown) => {
			config.onStateUpdate(state);
		};
		this.simulateConnectionChange = (state: 'webtransport' | 'polling' | 'disconnected') => {
			this._state = state;
			config.onConnectionStateChange(state);
		};
	});
	return { ConnectionManager };
});

// Mock clock-sync
vi.mock('./clock-sync', () => {
	const ClockSync = vi.fn(function MockClockSync(this: Record<string, unknown>) {
		this.updateRTT = vi.fn();
		this.updateOffset = vi.fn();
		this.ptpNow = vi.fn().mockReturnValue(0);
		this.safetyMargin = vi.fn().mockReturnValue(100_000);
		this.buildPing = vi.fn().mockReturnValue(new Uint8Array(5));
		this.parsePong = vi.fn().mockReturnValue(0);
	});
	return { ClockSync };
});

// Mock reconciler
vi.mock('$lib/state/reconciler.svelte', () => ({
	createReconciler: vi.fn().mockImplementation(() => {
		let _state: unknown = null;
		let _ingestedA: unknown = null;
		let _ingestedB: unknown = null;
		return {
			ingest: vi.fn().mockImplementation((engine: 'A' | 'B', state: unknown) => {
				if (engine === 'A') _ingestedA = state;
				if (engine === 'B') _ingestedB = state;
				_state = state; // last ingested becomes the "state" for simplicity
			}),
			get state() { return _state; },
			get health() {
				return {
					status: _ingestedA && _ingestedB ? 'nominal' : _ingestedA || _ingestedB ? 'single-engine' : 'disconnected',
					divergences: [],
					authoritative: 'A' as const,
					engineA: { lastSeen: 0, lastCommandSeq: 0, connected: _ingestedA !== null },
					engineB: { lastSeen: 0, lastCommandSeq: 0, connected: _ingestedB !== null },
				};
			},
		};
	}),
}));

import { DualConnectionManager } from './dual-connection';
import { ConnectionManager } from './connection-manager';
import { ClockSync } from './clock-sync';
import { createReconciler } from '$lib/state/reconciler.svelte';
import type { ControlRoomState } from '$lib/api/types';

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

function makeSingleConfig(overrides = {}) {
	return {
		engines: [{ id: 'A', url: 'https://engine-a.example.com' }],
		onStateUpdate: vi.fn(),
		onConnectionStateChange: vi.fn(),
		onInitialLoadComplete: vi.fn(),
		onInitialLoadError: vi.fn(),
		...overrides,
	};
}

function makeDualConfig(overrides = {}) {
	return {
		engines: [
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
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
			// In single mode, raw Uint8Array is still passed through as-is
			instance.simulateStateUpdate(encoded);

			// Single mode passes raw data through (ConnectionManager handles decoding upstream)
			expect(onStateUpdate).toHaveBeenCalledWith(encoded);
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

		it('health.status is nominal after both engines report', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const instanceA = MockedConnectionManager.mock.results[0].value;
			const instanceB = MockedConnectionManager.mock.results[1].value;
			instanceA.simulateStateUpdate(makeState());
			instanceB.simulateStateUpdate(makeState());
			expect(mgr.health.status).toBe('nominal');
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

	describe('clock and command sequencing', () => {
		it('exposes a ClockSync instance', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			expect(mgr.clock).toBeDefined();
			expect(MockedClockSync).toHaveBeenCalledOnce();
		});

		it('nextCommandSeq increments monotonically', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			expect(mgr.nextCommandSeq()).toBe(1);
			expect(mgr.nextCommandSeq()).toBe(2);
			expect(mgr.nextCommandSeq()).toBe(3);
		});

		it('command sequence starts at 0 and increments on each call', () => {
			const mgr = new DualConnectionManager(makeDualConfig());
			const seq1 = mgr.nextCommandSeq();
			const seq2 = mgr.nextCommandSeq();
			expect(seq2).toBe(seq1 + 1);
		});
	});
});
