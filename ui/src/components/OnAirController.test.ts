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

	it('renders the status bar with program source', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		// Camera 1 appears in status bar, program monitor, and source grid
		expect(screen.getAllByText('Camera 1').length).toBeGreaterThanOrEqual(1);
	});

	it('renders SOURCES panel when no playlist loaded', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByText('SOURCES')).toBeTruthy();
	});

	it('renders source tiles in source grid', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		// OASourceGrid renders OASourceTile for each source
		expect(screen.getByText('Camera 2')).toBeTruthy();
		expect(screen.getByText('Camera 3')).toBeTruthy();
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

	it('shows CUT and AUTO buttons in live-monitor mode', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByText('CUT')).toBeTruthy();
		expect(screen.getByText('AUTO')).toBeTruthy();
	});

	it('shows FIRE POD button in playout mode', () => {
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
		expect(screen.getByText('FIRE POD')).toBeTruthy();
	});

	it('calls onCut when CUT button clicked', async () => {
		const cutFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onCut: cutFn,
			},
		});
		const cutBtn = screen.getByText('CUT');
		await fireEvent.click(cutBtn);
		expect(cutFn).toHaveBeenCalledWith('cam2');
	});

	it('calls onAuto when AUTO button clicked', async () => {
		const autoFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onAuto: autoFn,
			},
		});
		const autoBtn = screen.getByText('AUTO');
		await fireEvent.click(autoBtn);
		expect(autoFn).toHaveBeenCalledWith('cam2');
	});

	it('calls onBugToggle when BUG button clicked', async () => {
		const bugFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onBugToggle: bugFn,
			},
		});
		const bugBtn = screen.getByText('BUG: OFF');
		await fireEvent.click(bugBtn);
		expect(bugFn).toHaveBeenCalled();
	});

	it('shows BUG: ON when graphics layer is active', () => {
		render(OnAirController, {
			props: {
				crState: makeState({
					graphics: {
						layers: [{ id: 0, active: true, zOrder: 0, x: 0, y: 0, width: 1920, height: 1080 }],
					},
				}),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByText('BUG: ON')).toBeTruthy();
	});

	it('shows AUDIO and I/O utility buttons', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		expect(screen.getByTitle('Toggle audio panel')).toBeTruthy();
		expect(screen.getByTitle('Toggle I/O panel')).toBeTruthy();
	});

	it('opens audio slide-out when AUDIO button clicked', async () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		const audioBtn = screen.getByTitle('Toggle audio panel');
		await fireEvent.click(audioBtn);
		expect(screen.getByText('Audio mixer will be wired here')).toBeTruthy();
	});

	it('opens I/O slide-out when I/O button clicked', async () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		const ioBtn = screen.getByTitle('Toggle I/O panel');
		await fireEvent.click(ioBtn);
		expect(screen.getByText('I/O panel will be wired here')).toBeTruthy();
	});

	it('opens settings modal when SETTINGS button clicked', async () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		const settingsBtn = screen.getByText('SETTINGS');
		await fireEvent.click(settingsBtn);
		// OASettingsModal renders a "SETTINGS" title in the modal header
		const settingsElements = screen.getAllByText('SETTINGS');
		// At least 2: the button and the modal title
		expect(settingsElements.length).toBeGreaterThanOrEqual(2);
	});

	it('shows sync indicator in dual-engine mode', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				isDualEngine: true,
				syncStatus: 'synced',
			},
		});
		expect(screen.getByText('SYNC')).toBeTruthy();
	});

	it('shows OFFLINE when sync status is disconnected', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				isDualEngine: true,
				syncStatus: 'disconnected',
			},
		});
		expect(screen.getByText('OFFLINE')).toBeTruthy();
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
		expect(screen.queryByText('OFFLINE')).toBeNull();
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

	it('calls onCut via space keyboard shortcut in live mode', async () => {
		const cutFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onCut: cutFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'Space' });
		expect(cutFn).toHaveBeenCalledWith('cam2');
	});

	it('calls onAuto via enter keyboard shortcut in live mode', async () => {
		const autoFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onAuto: autoFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'Enter' });
		expect(autoFn).toHaveBeenCalledWith('cam2');
	});

	it('calls onBugToggle via B keyboard shortcut', async () => {
		const bugFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onBugToggle: bugFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'KeyB' });
		expect(bugFn).toHaveBeenCalled();
	});

	it('calls onHold via H keyboard shortcut in playout mode', async () => {
		const holdFn = vi.fn();
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
				onHold: holdFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'KeyH' });
		expect(holdFn).toHaveBeenCalled();
	});

	it('does not call onHold via H keyboard shortcut in live mode', async () => {
		const holdFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onHold: holdFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'KeyH' });
		expect(holdFn).not.toHaveBeenCalled();
	});

	it('calls onSkip via S keyboard shortcut in playout mode', async () => {
		const skipFn = vi.fn();
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
				onSkip: skipFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'KeyS' });
		expect(skipFn).toHaveBeenCalled();
	});

	it('does not call onSkip via S keyboard shortcut in live mode', async () => {
		const skipFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onSkip: skipFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'KeyS' });
		expect(skipFn).not.toHaveBeenCalled();
	});

	it('calls onTakeNext via N keyboard shortcut in playout mode', async () => {
		const takeNextFn = vi.fn();
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
				onTakeNext: takeNextFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'KeyN' });
		expect(takeNextFn).toHaveBeenCalled();
	});

	it('does not call onTakeNext via N keyboard shortcut in live mode', async () => {
		const takeNextFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onTakeNext: takeNextFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'KeyN' });
		expect(takeNextFn).not.toHaveBeenCalled();
	});

	it('calls onCut with source key via digit shortcut in live mode', async () => {
		const cutFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onCut: cutFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'Digit1' });
		expect(cutFn).toHaveBeenCalledWith('cam1');
	});

	it('calls onCut with third source via Digit3 in live mode', async () => {
		const cutFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onCut: cutFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'Digit3' });
		expect(cutFn).toHaveBeenCalledWith('cam3');
	});

	it('does not call onCut via digit shortcut in playout mode', async () => {
		const cutFn = vi.fn();
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
				onCut: cutFn,
			},
		});
		await fireEvent.keyDown(window, { code: 'Digit1' });
		expect(cutFn).not.toHaveBeenCalled();
	});

	it('ignores digit shortcut for out-of-range source index', async () => {
		const cutFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onCut: cutFn,
			},
		});
		// Only 3 sources, Digit9 is index 8 which is out of range
		await fireEvent.keyDown(window, { code: 'Digit9' });
		expect(cutFn).not.toHaveBeenCalled();
	});

	it('calls onSourceClick with source key when source tile clicked', async () => {
		const cutFn = vi.fn();
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
				onCut: cutFn,
			},
		});
		// Source tiles are rendered as buttons by OASourceTile
		const cam3Btn = screen.getByText('Camera 3');
		await fireEvent.click(cam3Btn);
		expect(cutFn).toHaveBeenCalledWith('cam3');
	});

	it('shows NEXT monitor', () => {
		render(OnAirController, {
			props: {
				crState: makeState(),
				onCanvasReady: vi.fn(),
				onSwitchLayout: vi.fn(),
			},
		});
		// OANextMonitor renders NEXT label
		expect(screen.getAllByText('NEXT').length).toBeGreaterThanOrEqual(1);
	});
});
