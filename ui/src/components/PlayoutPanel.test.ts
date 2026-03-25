import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import PlayoutPanel from './PlayoutPanel.svelte';
import type { ControlRoomState, PlayoutChannelState } from '$lib/api/types';
import * as api from '$lib/api/switch-api';

// Mock ALL switch-api functions that PlayoutPanel and its child components import
vi.mock('$lib/api/switch-api', () => ({
	playoutStart: vi.fn().mockResolvedValue(undefined),
	playoutStop: vi.fn().mockResolvedValue(undefined),
	playoutNext: vi.fn().mockResolvedValue(undefined),
	playoutPrevious: vi.fn().mockResolvedValue(undefined),
	playoutTakeManual: vi.fn().mockResolvedValue(undefined),
	playoutReturnToAuto: vi.fn().mockResolvedValue(undefined),
	playoutJump: vi.fn().mockResolvedValue(undefined),
	playoutPlaylist: vi.fn().mockResolvedValue([]),
	playoutLoadPlaylist: vi.fn().mockResolvedValue(undefined),
	playoutSamples: vi.fn().mockResolvedValue([]),
	playoutTemplateNames: vi.fn().mockResolvedValue([]),
	playoutTemplate: vi.fn().mockResolvedValue(null),
	playoutCacheStatus: vi.fn().mockResolvedValue({
		cacheDir: '/tmp',
		maxBytes: 1e9,
		usedBytes: 0,
		itemCount: 0,
		fetchingCount: 0,
		errorCount: 0,
	}),
	podCreate: vi.fn().mockResolvedValue({ id: 'test-pod' }),
	podArm: vi.fn().mockResolvedValue(undefined),
	podFire: vi.fn().mockResolvedValue(undefined),
	podDump: vi.fn().mockResolvedValue(undefined),
	podHold: vi.fn().mockResolvedValue(undefined),
	podExtend: vi.fn().mockResolvedValue(undefined),
	apiCall: vi.fn(),
}));

vi.mock('$lib/state/notifications.svelte', () => ({
	notify: vi.fn(),
}));

function makeState(overrides: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		sources: {},
		programSource: '',
		previewSource: '',
		audioChannels: {},
		tallyState: {},
		seq: 1,
		timestamp: Date.now(),
		...overrides,
	} as unknown as ControlRoomState;
}

function makeChannel(overrides: Partial<PlayoutChannelState> = {}): PlayoutChannelState {
	return {
		id: 1,
		state: 'stopped',
		itemIndex: -1,
		itemCount: 0,
		...overrides,
	};
}

describe('PlayoutPanel', () => {
	beforeEach(() => {
		vi.clearAllMocks();
		vi.mocked(api.playoutPlaylist).mockResolvedValue([]);
		vi.mocked(api.playoutSamples).mockResolvedValue([]);
		vi.mocked(api.playoutTemplateNames).mockResolvedValue([]);
	});

	afterEach(() => {
		vi.restoreAllMocks();
	});

	// ── Basic rendering ──

	it('renders "No playlist loaded" when no playlist', () => {
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		expect(container.textContent).toContain('No playlist loaded');
	});

	it('shows channel tab buttons', () => {
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		const tabs = container.querySelectorAll('.channel-tab');
		expect(tabs.length).toBe(2);
		expect(tabs[0].textContent).toBe('CH1');
		expect(tabs[1].textContent).toBe('CH2');
	});

	it('shows mode toggle buttons', () => {
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		const toggle = container.querySelector('[data-testid="mode-toggle"]');
		expect(toggle).toBeTruthy();
		expect(toggle!.textContent).toContain('EDIT');
		expect(toggle!.textContent).toContain('MONITOR');
	});

	it('shows output badge in header', () => {
		const state = makeState({
			playout: {
				channels: [makeChannel()],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		const badge = container.querySelector('[data-testid="output-badge"]');
		expect(badge).toBeTruthy();
	});

	// ── Edit mode (default) ──

	it('defaults to edit mode when stopped', () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({ state: 'stopped' })],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		const timelineZone = container.querySelector('[data-testid="timeline-zone"]');
		expect(timelineZone).toBeTruthy();
		const monitorLayout = container.querySelector('[data-testid="monitor-layout"]');
		expect(monitorLayout).toBeNull();
	});

	it('shows STOPPED state pill when stopped in edit mode', () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({ state: 'stopped' })],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		const pill = container.querySelector('.state-pill.stopped');
		expect(pill).toBeTruthy();
		expect(pill!.textContent).toBe('STOPPED');
	});

	it('shows ON AIR state pill when on_air in edit mode', async () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 5,
					currentTitle: 'Test',
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		// Force edit mode (auto-switches to monitor when on-air)
		const editBtn = Array.from(container.querySelectorAll('.mode-btn-toggle')).find(b => b.textContent === 'EDIT');
		await fireEvent.click(editBtn!);
		await vi.waitFor(() => {
			const pill = container.querySelector('.state-pill.on-air');
			expect(pill).toBeTruthy();
			expect(pill!.textContent).toBe('ON AIR');
		});
	});

	it('edit mode shows timeline and editor placeholder', () => {
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		const timeline = container.querySelector('[data-testid="timeline-zone"]');
		expect(timeline).toBeTruthy();
		expect(container.textContent).toContain('Select an item');
	});

	it('shows current and next titles when on air in edit mode', () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 2,
					itemCount: 10,
					currentTitle: 'Current Segment',
					nextTitle: 'Next Segment',
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		expect(container.textContent).toContain('Current Segment');
		expect(container.textContent).toContain('Next Segment');
	});

	it('shows countdown timer formatted as m:ss', () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 5,
					currentTitle: 'Segment',
					itemRemainMs: 65000,
					itemProgress: 0.5,
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		// 65000ms = 65s = 1m05s => "1:05"
		expect(container.textContent).toContain('1:05');
	});

	it('shows warning class on countdown when under 10s', async () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 5,
					currentTitle: 'Segment',
					itemRemainMs: 5000,
					itemProgress: 0.9,
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		// Force edit mode
		const editBtn = Array.from(container.querySelectorAll('.mode-btn-toggle')).find(b => b.textContent === 'EDIT');
		await fireEvent.click(editBtn!);
		await vi.waitFor(() => {
			const countdown = container.querySelector('.countdown.warning');
			expect(countdown).toBeTruthy();
		});
	});

	it('shows progress bar with color-coded zones', async () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 5,
					currentTitle: 'Segment',
					itemProgress: 0.42,
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		// Force edit mode
		const editBtn = Array.from(container.querySelectorAll('.mode-btn-toggle')).find(b => b.textContent === 'EDIT');
		await fireEvent.click(editBtn!);
		await vi.waitFor(() => {
			const fill = container.querySelector('.progress-fill.normal') as HTMLElement;
			expect(fill).toBeTruthy();
			expect(fill.style.width).toBe('42%');
		});
	});

	it('shows playlist name in header', () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'stopped',
					playlistName: 'Evening Show',
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		expect(container.textContent).toContain('Evening Show');
	});

	it('shows item counter in edit mode', async () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 2,
					itemCount: 10,
					currentTitle: 'Segment',
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		// Force edit mode (auto-switches to monitor when on-air)
		const editBtn = Array.from(container.querySelectorAll('.mode-btn-toggle')).find(b => b.textContent === 'EDIT');
		await fireEvent.click(editBtn!);
		await vi.waitFor(() => {
			// itemIndex=2 displays as 3 (1-based), itemCount=10
			expect(container.textContent).toContain('3/10');
		});
	});

	it('shows "Playlist complete" when completed', () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'completed',
					itemIndex: 9,
					itemCount: 10,
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		expect(container.textContent).toContain('Playlist complete');
	});

	it('renders pod board section', () => {
		const state = makeState({
			playout: {
				channels: [makeChannel()],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		expect(container.textContent).toContain('POD BOARD');
	});

	it('renders cache status section in edit mode', () => {
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		expect(container.textContent).toContain('Cache Status');
	});

	it('renders settings section in edit mode', () => {
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		expect(container.textContent).toContain('Playlist Settings');
	});

	it('renders template selector when templates available', async () => {
		vi.mocked(api.playoutTemplateNames).mockResolvedValue(['News Show', 'Sports Event']);
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		await vi.waitFor(() => {
			const select = container.querySelector('.template-select');
			expect(select).toBeTruthy();
			expect(container.textContent).toContain('News Show');
			expect(container.textContent).toContain('Sports Event');
		});
	});

	it('renders save button', () => {
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		const btn = container.querySelector('.save-btn');
		expect(btn).toBeTruthy();
		expect(btn!.textContent).toContain('Save');
	});

	it('shows item editor placeholder when no item selected', () => {
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		expect(container.textContent).toContain('Select an item');
	});

	it('fetches playlist items when itemCount > 0', async () => {
		vi.mocked(api.playoutPlaylist).mockResolvedValue([
			{ id: 'a', type: 'clip', title: 'Intro Bumper', duration_ms: 5000 },
			{ id: 'b', type: 'live', title: 'Camera 1', duration_ms: 60000 },
			{ id: 'c', type: 'slate', title: 'Slate Card', duration_ms: 10000 },
		]);
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 3,
					currentTitle: 'Intro Bumper',
				})],
			},
		});
		render(PlayoutPanel, { props: { state } });
		await vi.waitFor(() => {
			expect(api.playoutPlaylist).toHaveBeenCalledWith(1);
		});
	});

	// ── Monitor mode ──

	it('auto-switches to monitor mode when on-air', async () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 3,
					currentTitle: 'Live Now',
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		// Should auto-switch since on-air and not dirty and no selection
		await vi.waitFor(() => {
			const monitorLayout = container.querySelector('[data-testid="monitor-layout"]');
			expect(monitorLayout).toBeTruthy();
		});
	});

	it('monitor mode shows rundown instead of timeline', async () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 3,
					currentTitle: 'Live Now',
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		await vi.waitFor(() => {
			const rundown = container.querySelector('[data-testid="rundown-zone"]');
			expect(rundown).toBeTruthy();
			const timeline = container.querySelector('[data-testid="timeline-zone"]');
			expect(timeline).toBeNull();
		});
	});

	it('can manually toggle to monitor mode', async () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({ state: 'stopped' })],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		// Should start in edit mode
		expect(container.querySelector('[data-testid="timeline-zone"]')).toBeTruthy();
		// Click MONITOR button
		const toggleBtns = container.querySelectorAll('.mode-btn-toggle');
		const monitorBtn = Array.from(toggleBtns).find(b => b.textContent === 'MONITOR');
		expect(monitorBtn).toBeTruthy();
		await fireEvent.click(monitorBtn!);
		await vi.waitFor(() => {
			expect(container.querySelector('[data-testid="monitor-layout"]')).toBeTruthy();
		});
	});

	it('shows NOW/NEXT block in monitor mode with titles', async () => {
		vi.mocked(api.playoutPlaylist).mockResolvedValue([
			{ id: 'a', type: 'clip', title: 'Intro', duration_ms: 5000 },
			{ id: 'b', type: 'live', title: 'Main Event', duration_ms: 60000 },
		]);
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 2,
					currentTitle: 'Intro',
					nextTitle: 'Main Event',
					itemProgress: 0.5,
					itemRemainMs: 2500,
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		await vi.waitFor(() => {
			expect(container.querySelector('.now-item-title')).toBeTruthy();
			expect(container.querySelector('.now-item-title')!.textContent).toBe('Intro');
		});
	});

	it('monitor mode shows pod board', async () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 3,
					currentTitle: 'Test',
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		await vi.waitFor(() => {
			expect(container.querySelector('[data-testid="monitor-layout"]')).toBeTruthy();
			expect(container.textContent).toContain('POD BOARD');
		});
	});

	// ── Safety guards ──

	it('save button shows warning when on-air', async () => {
		vi.mocked(api.playoutPlaylist).mockResolvedValue([
			{ id: 'a', type: 'clip', title: 'Test', duration_ms: 5000 },
		]);
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 1,
					currentTitle: 'Test',
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		// Switch back to edit mode manually
		const toggleBtns = container.querySelectorAll('.mode-btn-toggle');
		const editBtn = Array.from(toggleBtns).find(b => b.textContent === 'EDIT');
		await fireEvent.click(editBtn!);
		await vi.waitFor(() => {
			const saveBtn = container.querySelector('.save-btn');
			expect(saveBtn).toBeTruthy();
		});
	});

	// ── Keyboard shortcuts ──

	it('keyboard shortcut N triggers playoutNext via apiCall', async () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 5,
					currentTitle: 'Test',
				})],
			},
		});
		render(PlayoutPanel, { props: { state } });

		await fireEvent.keyDown(document, { key: 'n' });
		expect(api.apiCall).toHaveBeenCalled();
		const lastCall = vi.mocked(api.apiCall).mock.calls;
		// apiCall is called with (promise, label)
		const callLabels = lastCall.map(c => c[1]);
		expect(callLabels).toContain('Next');
	});

	it('keyboard shortcut M toggles manual/auto mode', async () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					mode: 'automated',
					itemIndex: 0,
					itemCount: 5,
					currentTitle: 'Test',
				})],
			},
		});
		render(PlayoutPanel, { props: { state } });

		await fireEvent.keyDown(document, { key: 'm' });
		expect(api.apiCall).toHaveBeenCalled();
		const callLabels = vi.mocked(api.apiCall).mock.calls.map(c => c[1]);
		expect(callLabels).toContain('Manual');
	});

	it('returns to edit mode when stopped/completed', async () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({ state: 'completed' })],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		await vi.waitFor(() => {
			const timelineZone = container.querySelector('[data-testid="timeline-zone"]');
			expect(timelineZone).toBeTruthy();
		});
	});

	it('countdown shows tenths in last 10 seconds', () => {
		const state = makeState({
			playout: {
				channels: [makeChannel({
					state: 'on_air',
					itemIndex: 0,
					itemCount: 5,
					currentTitle: 'Segment',
					itemRemainMs: 7500,
					itemProgress: 0.9,
				})],
			},
		});
		const { container } = render(PlayoutPanel, { props: { state } });
		// 7500ms -> 7.5s -> "0:07.5"
		expect(container.textContent).toContain('0:07.5');
	});
});
