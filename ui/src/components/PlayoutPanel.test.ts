import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from '@testing-library/svelte';
import PlayoutPanel from './PlayoutPanel.svelte';
import type { ControlRoomState, PlayoutChannelState } from '$lib/api/types';

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
	});

	it('renders "No playlist loaded" when no playlist', () => {
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		expect(container.textContent).toContain('No playlist loaded');
	});

	it('shows channel selector', () => {
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		const select = container.querySelector('.channel-select');
		expect(select).toBeTruthy();
	});

	it('shows STOPPED state pill when stopped', () => {
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

	it('shows ON AIR state pill when on_air', () => {
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
		const pill = container.querySelector('.state-pill.on-air');
		expect(pill).toBeTruthy();
		expect(pill!.textContent).toBe('ON AIR');
	});

	it('shows current and next titles when on air', () => {
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

	it('shows warning class on countdown when under 10s', () => {
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
		const countdown = container.querySelector('.countdown.warning');
		expect(countdown).toBeTruthy();
	});

	it('shows progress bar with correct width when on air', () => {
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
		const fill = container.querySelector('.progress-fill') as HTMLElement;
		expect(fill).toBeTruthy();
		expect(fill.style.width).toBe('42%');
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

	it('shows item counter', () => {
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
		// itemIndex=2 displays as 3 (1-based), itemCount=10
		expect(container.textContent).toContain('3/10');
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

	it('renders cache status section', () => {
		const state = makeState();
		const { container } = render(PlayoutPanel, { props: { state } });
		expect(container.textContent).toContain('Cache Status');
	});
});
