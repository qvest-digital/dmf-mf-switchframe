import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from '@testing-library/svelte';
import ClipsPanel from './ClipsPanel.svelte';
import type { ControlRoomState } from '$lib/api/types';

vi.mock('$lib/api/switch-api', () => ({
	listClips: vi.fn().mockResolvedValue([]),
	uploadClip: vi.fn(),
	deleteClip: vi.fn(),
	pinClip: vi.fn(),
	listRecordings: vi.fn().mockResolvedValue([]),
	importRecording: vi.fn(),
	clipPlayerLoad: vi.fn(),
	clipPlayerEject: vi.fn(),
	clipPlayerPlay: vi.fn(),
	clipPlayerPause: vi.fn(),
	clipPlayerStop: vi.fn(),
	clipPlayerSeek: vi.fn(),
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
		...overrides,
	} as unknown as ControlRoomState;
}

describe('ClipsPanel', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('renders upload button', () => {
		const { container } = render(ClipsPanel, { props: { state: makeState() } });
		expect(container.textContent).toContain('Upload');
	});

	it('shows 4 player slots', () => {
		const { container } = render(ClipsPanel, { props: { state: makeState() } });
		const playerHeaders = Array.from(container.querySelectorAll('.player-slot .player-header'));
		const playerTexts = playerHeaders.map(el => el.textContent);
		expect(playerTexts.filter(t => t && /Player \d/.test(t))).toHaveLength(4);
	});

	it('shows clip library sections', () => {
		const { container } = render(ClipsPanel, { props: { state: makeState() } });
		expect(container.textContent).toContain('Uploaded');
		expect(container.textContent).toContain('Replay Clips');
		expect(container.textContent).toContain('Recordings');
	});

	it('shows player state from ControlRoomState', () => {
		const state = makeState({
			clipPlayers: [
				{ id: 1, clipId: 'c1', clipName: 'Intro Bumper', state: 'playing', speed: 1.0, position: 0.5 },
				{ id: 2, state: 'empty' },
				{ id: 3, state: 'empty' },
				{ id: 4, state: 'empty' },
			],
		});
		const { container } = render(ClipsPanel, { props: { state } });
		expect(container.textContent).toContain('Intro Bumper');
	});

	it('shows progress bar when clipUpload is present', () => {
		const state = makeState({
			clipUpload: {
				stage: 'transcoding',
				percent: 42,
				filename: 'test.mkv',
			},
		});
		const { container } = render(ClipsPanel, { props: { state } });
		const progressBar = container.querySelector('.upload-progress');
		expect(progressBar).toBeTruthy();
		expect(container.textContent).toContain('Transcoding');
		expect(container.textContent).toContain('test.mkv');
	});

	it('does not show progress bar when no upload', () => {
		const state = makeState();
		const { container } = render(ClipsPanel, { props: { state } });
		const progressBar = container.querySelector('.upload-progress');
		expect(progressBar).toBeFalsy();
	});

	it('shows stage indicators during upload progress', () => {
		const state = makeState({
			clipUpload: {
				stage: 'analyzing',
				percent: 0,
				filename: 'clip.webm',
			},
		});
		const { container } = render(ClipsPanel, { props: { state } });
		const stages = container.querySelectorAll('.stage-dot');
		expect(stages).toHaveLength(4);
		// "Analyze" should be the active stage
		const activeStages = container.querySelectorAll('.stage-dot.active');
		expect(activeStages.length).toBeGreaterThan(0);
	});
});
