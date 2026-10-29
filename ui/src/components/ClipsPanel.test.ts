import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, waitFor, fireEvent, screen } from '@testing-library/svelte';
import ClipsPanel from './ClipsPanel.svelte';
import type { ControlRoomState, ClipInfo } from '$lib/api/types';
import { listClipsOnEveryEngine, deleteClip } from '$lib/api/switch-api';

vi.mock('$lib/api/switch-api', () => ({
	listClipsOnEveryEngine: vi.fn().mockResolvedValue([]),
	uploadClip: vi.fn(),
	deleteClip: vi.fn(),
	pinClip: vi.fn(),
	listRecordingsDualEngine: vi.fn().mockResolvedValue([{ engineId: 'A', engineUrl: '', recordings: [] }]),
	importRecordingFromEngine: vi.fn(),
	clipPlayerLoadByName: vi.fn(),
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

	it('renders upload zone', () => {
		const { container } = render(ClipsPanel, { props: { state: makeState() } });
		expect(container.textContent).toContain('Add clips');
	});

	it('shows 4 player slots', () => {
		const { container } = render(ClipsPanel, { props: { state: makeState() } });
		const slotIds = Array.from(container.querySelectorAll('.player-card .player-slot-id'));
		expect(slotIds).toHaveLength(4);
		expect(slotIds.map((el) => el.textContent)).toEqual(['P1', 'P2', 'P3', 'P4']);
	});

	it('shows clip library sections', () => {
		const { container } = render(ClipsPanel, { props: { state: makeState() } });
		expect(container.textContent).toContain('Uploaded');
		expect(container.textContent).toContain('Replay');
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

	// Another operator's upload: this browser sends no bytes, so its own
	// upload counter stays at zero.
	it('shows the engine progress of an upload another browser sends', () => {
		const state = makeState({
			clipUpload: { stage: 'uploading', percent: 50, filename: 'other.mp4' },
		});
		const { container } = render(ClipsPanel, { props: { state } });
		expect(container.querySelector('.upload-pct')?.textContent).toBe('13%');
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

	// A clip on one engine only would answer a load with "clip not found" on
	// the other, so the panel lists what every engine holds.
	it('lists the clips every engine holds and refreshes when the count moves', async () => {
		const bunny = { id: 'b', name: 'bunny', filename: 'bunny.ts', source: 'upload' } as ClipInfo;
		vi.mocked(listClipsOnEveryEngine).mockResolvedValue([]);

		const { container, rerender } = render(ClipsPanel, { props: { state: makeState({ clipCount: 0 }) } });
		await waitFor(() => expect(listClipsOnEveryEngine).toHaveBeenCalledTimes(1));

		vi.mocked(listClipsOnEveryEngine).mockResolvedValue([bunny]);
		await rerender({ state: makeState({ clipCount: 1 }) });
		await waitFor(() => expect(container.querySelector('.clip-name')?.textContent).toBe('bunny'));
		expect(listClipsOnEveryEngine).toHaveBeenCalledTimes(2);
	});

	function libraryClip(id: string, libraryPath?: string): ClipInfo {
		return {
			id, name: `clip ${id}`, filename: '', source: 'upload', codec: 'h264',
			width: 1920, height: 1080, fpsNum: 50, fpsDen: 1, durationMs: 10000,
			byteSize: 1, loop: false, createdAt: '2026-01-01T00:00:00Z', ephemeral: false,
			libraryPath,
		};
	}

	it('deletes a library clip only after a confirmation naming the shared library', async () => {
		vi.mocked(listClipsOnEveryEngine).mockResolvedValue([libraryClip('a', 'a.ts')]);
		vi.mocked(deleteClip).mockResolvedValue(undefined);
		const { container } = render(ClipsPanel, { props: { state: makeState({ clipCount: 1 }) } });
		await waitFor(() => expect(container.querySelector('.clip-name')?.textContent).toBe('clip a'));

		await fireEvent.click(screen.getByTitle('Delete'));
		expect(deleteClip).not.toHaveBeenCalled();
		expect(screen.getByRole('alertdialog').textContent).toContain('every future booking');

		await fireEvent.click(screen.getByRole('alertdialog').querySelector('.confirm-btn')!);
		expect(deleteClip).toHaveBeenCalledWith('a');
	});

	it('deletes a clip stored only here without asking', async () => {
		vi.mocked(listClipsOnEveryEngine).mockResolvedValue([libraryClip('b')]);
		vi.mocked(deleteClip).mockResolvedValue(undefined);
		const { container } = render(ClipsPanel, { props: { state: makeState({ clipCount: 1 }) } });
		await waitFor(() => expect(container.querySelector('.clip-name')?.textContent).toBe('clip b'));

		await fireEvent.click(screen.getByTitle('Delete'));
		expect(deleteClip).toHaveBeenCalledWith('b');
		expect(screen.queryByRole('alertdialog')).toBeNull();
	});
});
