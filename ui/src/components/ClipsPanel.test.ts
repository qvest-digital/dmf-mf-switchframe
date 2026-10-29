import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, waitFor, fireEvent, screen } from '@testing-library/svelte';
import ClipsPanel from './ClipsPanel.svelte';
import type { ControlRoomState, ClipInfo } from '$lib/api/types';
import { listClipsOnEveryEngine, deleteClip, listRecordingsDualEngine, recordingDownloadUrl } from '$lib/api/switch-api';

vi.mock('$lib/api/switch-api', () => ({
	listClipsOnEveryEngine: vi.fn().mockResolvedValue([]),
	uploadClip: vi.fn(),
	deleteClip: vi.fn(),
	pinClip: vi.fn(),
	listRecordingsDualEngine: vi.fn().mockResolvedValue([{ engineId: 'A', engineUrl: '', recordings: [] }]),
	importRecordingFromEngine: vi.fn(),
	recordingDownloadUrl: vi.fn().mockResolvedValue('https://engine-a/api/clips/recordings/download?path=x&ticket=t'),
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

	describe('recordings', () => {
		const recordings = [
			{ filename: 'rec_live_001.ts', path: '/r/rec_live_001.ts', byteSize: 2_000_000, modTime: '', state: 'recording' },
			{ filename: 'rec_copy_001.ts', path: '/r/rec_copy_001.ts', byteSize: 3_000_000, modTime: '', state: 'finalizing' },
			{ filename: 'rec_done_001.ts', path: '/r/rec_done_001.ts', byteSize: 4_000_000, modTime: '', state: 'ready' },
			{ filename: 'rec_empty_001.ts', path: '/r/rec_empty_001.ts', byteSize: 0, modTime: '', state: 'failed',
				error: 'recording is empty: no program video reached the recorder' },
		];

		function item(container: HTMLElement, name: string): HTMLElement {
			const el = Array.from(container.querySelectorAll<HTMLElement>('.recording-item'))
				.find((e) => e.textContent?.includes(name));
			if (!el) throw new Error(`no recording item ${name}`);
			return el;
		}

		it('shows each file state, and offers download and import only when ready', async () => {
			vi.mocked(listRecordingsDualEngine).mockResolvedValue([
				{ engineId: 'A', engineUrl: 'https://engine-a', recordings },
			] as never);
			const { container } = render(ClipsPanel, { props: { state: makeState() } });
			await waitFor(() => expect(container.querySelectorAll('.recording-item')).toHaveLength(4));

			expect(item(container, 'rec_live').textContent).toContain('Recording');
			expect(item(container, 'rec_copy').textContent).toContain('Finalizing');
			expect(item(container, 'rec_done').textContent).toContain('Ready');
			expect(item(container, 'rec_empty').textContent).toContain('Failed');
			expect(item(container, 'rec_empty').textContent).toContain('no program video');

			for (const name of ['rec_live', 'rec_copy', 'rec_empty']) {
				expect(item(container, name).querySelector('.download-btn')).toBeNull();
				expect(item(container, name).querySelector('.import-btn')).toBeNull();
			}
			expect(item(container, 'rec_done').querySelector('.import-btn')).not.toBeNull();
			// No bare link to the download route: a navigation cannot carry the token.
			expect(container.querySelector('a[href*="/api/clips/recordings/download"]')).toBeNull();
		});

		it('downloads through a ticketed URL from the engine that holds the file', async () => {
			vi.mocked(listRecordingsDualEngine).mockResolvedValue([
				{ engineId: 'A', engineUrl: 'https://engine-a', recordings },
			] as never);
			const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
			const { container } = render(ClipsPanel, { props: { state: makeState() } });
			await waitFor(() => expect(container.querySelectorAll('.recording-item')).toHaveLength(4));

			await fireEvent.click(item(container, 'rec_done').querySelector('.download-btn')!);
			await waitFor(() => expect(click).toHaveBeenCalled());
			expect(recordingDownloadUrl).toHaveBeenCalledWith('https://engine-a', '/r/rec_done_001.ts');
			click.mockRestore();
		});

		it('reads the list again when a recording starts or stops', async () => {
			vi.mocked(listRecordingsDualEngine).mockResolvedValue([{ engineId: 'A', engineUrl: '', recordings: [] }] as never);
			const { rerender } = render(ClipsPanel, { props: { state: makeState() } });
			await waitFor(() => expect(listRecordingsDualEngine).toHaveBeenCalledTimes(1));

			await rerender({ state: makeState({ recording: { active: true, filename: 'rec_a_001.ts' } } as never) });
			await waitFor(() => expect(listRecordingsDualEngine).toHaveBeenCalledTimes(2));

			await rerender({ state: makeState({ recording: { active: false } } as never) });
			await waitFor(() => expect(listRecordingsDualEngine).toHaveBeenCalledTimes(3));
		});

		it('polls while a file is recording or finalizing, and stops when none is', async () => {
			vi.useFakeTimers();
			try {
				vi.mocked(listRecordingsDualEngine).mockResolvedValue([
					{ engineId: 'A', engineUrl: '', recordings: [recordings[0]] },
				] as never);
				render(ClipsPanel, { props: { state: makeState() } });
				await vi.advanceTimersByTimeAsync(0);
				const afterLoad = vi.mocked(listRecordingsDualEngine).mock.calls.length;
				await vi.advanceTimersByTimeAsync(4100);
				expect(vi.mocked(listRecordingsDualEngine).mock.calls.length).toBe(afterLoad + 2);

				vi.mocked(listRecordingsDualEngine).mockResolvedValue([
					{ engineId: 'A', engineUrl: '', recordings: [recordings[2]] },
				] as never);
				await vi.advanceTimersByTimeAsync(2000);
				const settled = vi.mocked(listRecordingsDualEngine).mock.calls.length;
				await vi.advanceTimersByTimeAsync(10000);
				expect(vi.mocked(listRecordingsDualEngine).mock.calls.length).toBe(settled);
			} finally {
				vi.useRealTimers();
			}
		});
			it('stops polling after a failed read instead of retrying every 2 s', async () => {
			vi.useFakeTimers();
			try {
				vi.mocked(listRecordingsDualEngine).mockResolvedValue([
					{ engineId: 'A', engineUrl: '', recordings: [recordings[0]] },
				] as never);
				render(ClipsPanel, { props: { state: makeState() } });
				await vi.advanceTimersByTimeAsync(0);
				vi.mocked(listRecordingsDualEngine).mockRejectedValue(new Error('engine unreachable'));
				await vi.advanceTimersByTimeAsync(2100);
				const afterFailure = vi.mocked(listRecordingsDualEngine).mock.calls.length;
				await vi.advanceTimersByTimeAsync(10000);
				expect(vi.mocked(listRecordingsDualEngine).mock.calls.length).toBe(afterFailure);
			} finally {
				vi.useRealTimers();
			}
		});

		it('says an engine is not reachable and does not poll for it', async () => {
			vi.useFakeTimers();
			try {
				vi.mocked(listRecordingsDualEngine).mockResolvedValue([
					{ engineId: 'A', engineUrl: 'https://engine-a', recordings: [recordings[2]] },
					{ engineId: 'B', engineUrl: 'https://engine-b', recordings: [], unreachable: true },
				] as never);
				const { container } = render(ClipsPanel, { props: { state: makeState() } });
				await vi.advanceTimersByTimeAsync(0);
				expect(container.textContent).toContain('Engine B not reachable');
				const calls = vi.mocked(listRecordingsDualEngine).mock.calls.length;
				await vi.advanceTimersByTimeAsync(10000);
				expect(vi.mocked(listRecordingsDualEngine).mock.calls.length).toBe(calls);
			} finally {
				vi.useRealTimers();
			}
		});

		it('reads again after an in-flight read when a recording stops meanwhile', async () => {
			let release!: (v: unknown) => void;
			vi.mocked(listRecordingsDualEngine).mockImplementationOnce(() => new Promise((r) => { release = r; }) as never);
			const { rerender } = render(ClipsPanel, { props: { state: makeState({ recording: { active: true, filename: 'r.ts' } } as never) } });
			await waitFor(() => expect(listRecordingsDualEngine).toHaveBeenCalledTimes(1));
			vi.mocked(listRecordingsDualEngine).mockResolvedValue([{ engineId: 'A', engineUrl: '', recordings: [] }] as never);
			await rerender({ state: makeState({ recording: { active: false } } as never) });
			expect(listRecordingsDualEngine).toHaveBeenCalledTimes(1); // not concurrently
			release([{ engineId: 'A', engineUrl: '', recordings: [recordings[0]] }]);
			await waitFor(() => expect(listRecordingsDualEngine).toHaveBeenCalledTimes(2));
		});
	});
});
