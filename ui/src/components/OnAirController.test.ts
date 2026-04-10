import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OnAirController from './OnAirController.svelte';
import type { ControlRoomState } from '$lib/api/types';

function makeState(overrides: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 1000,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		audioChannels: undefined,
		masterLevel: 0,
		programPeak: [0, 0],
		tallyState: { cam1: 'program', cam2: 'preview' },
		sources: {
			cam1: { key: 'cam1', label: 'Camera 1', type: 'demo' as const, status: 'healthy' },
			cam2: { key: 'cam2', label: 'Camera 2', type: 'demo' as const, status: 'healthy' },
			cam3: { key: 'cam3', label: 'Camera 3', type: 'demo' as const, status: 'healthy' },
		},
		seq: 1,
		lastCommandSeq: 0,
		timestamp: Date.now(),
		...overrides,
	};
}

describe('OnAirController', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('renders the ON AIR badge', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByText('ON AIR')).toBeTruthy();
	});

	it('renders SwitchFrame brand', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByText('SwitchFrame')).toBeTruthy();
	});

	it('renders PROGRAM monitor label', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByText('PROGRAM')).toBeTruthy();
	});

	it('shows program source label', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByText('Camera 1')).toBeTruthy();
	});

	it('shows SOURCES panel when no playlist loaded', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByText('SOURCES')).toBeTruthy();
		expect(screen.getByText('3 sources available')).toBeTruthy();
	});

	it('shows PLAYOUT SCHEDULE panel when playlist loaded', () => {
		render(OnAirController, {
			props: {
				crState: makeState({
					playout: {
						channels: [{
							id: 0,
							state: 'on_air',
							itemIndex: 0,
							itemCount: 5,
						}],
					},
				}),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByText('PLAYOUT SCHEDULE')).toBeTruthy();
	});

	it('shows channel state when playlist loaded', () => {
		render(OnAirController, {
			props: {
				crState: makeState({
					playout: {
						channels: [{
							id: 0,
							state: 'on_air',
							itemIndex: 0,
							itemCount: 3,
						}],
					},
				}),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByText('Channel state: on_air')).toBeTruthy();
	});

	it('gear button fires layout switch callback', async () => {
		const switchFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: switchFn,
			},
		});
		const gearBtn = screen.getByTitle('Switch layout mode');
		await fireEvent.click(gearBtn);
		expect(switchFn).toHaveBeenCalled();
	});

	it('shows sync indicator in dual-engine mode', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				isDualEngine: true,
				syncStatus: 'ok',
			},
		});
		expect(screen.getByText('SYNC')).toBeTruthy();
	});

	it('shows RESYNC when sync status is resyncing', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				isDualEngine: true,
				syncStatus: 'resyncing',
			},
		});
		expect(screen.getByText('RESYNC')).toBeTruthy();
	});

	it('shows NO SYNC when sync status is disconnected', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				isDualEngine: true,
				syncStatus: 'disconnected',
			},
		});
		expect(screen.getByText('NO SYNC')).toBeTruthy();
	});

	it('hides sync indicator in single-engine mode', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				isDualEngine: false,
			},
		});
		expect(screen.queryByText('SYNC')).toBeNull();
		expect(screen.queryByText('RESYNC')).toBeNull();
		expect(screen.queryByText('NO SYNC')).toBeNull();
	});

	it('calls onCanvasReady with canvas elements', () => {
		const canvasReadyFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: canvasReadyFn,
				onSwitchLayout: vi.fn(),
			},
		});
		expect(canvasReadyFn).toHaveBeenCalledWith(
			expect.any(HTMLCanvasElement),
			expect.any(HTMLCanvasElement),
		);
	});

	it('renders Actions placeholder in action bar', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByText('Actions')).toBeTruthy();
	});
});
