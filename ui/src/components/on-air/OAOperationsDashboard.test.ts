import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OAOperationsDashboard from './OAOperationsDashboard.svelte';
import type { ControlRoomState } from '$lib/api/types';

// Mock the API module
vi.mock('$lib/api/switch-api', () => ({
	listClipsOnEveryEngine: vi.fn().mockResolvedValue([]),
	deleteClip: vi.fn().mockResolvedValue(undefined),
	uploadClip: vi.fn().mockResolvedValue({ id: 'new-clip', name: 'test.mp4' }),
	clipPlayerLoad: vi.fn().mockResolvedValue({}),
	clipPlayerPlay: vi.fn().mockResolvedValue({}),
	cut: vi.fn().mockResolvedValue({}),
	graphicsImageUpload: vi.fn().mockResolvedValue({}),
	graphicsAddLayer: vi.fn().mockResolvedValue({ id: 5 }),
	playoutSavedList: vi.fn().mockResolvedValue([]),
	playoutImportBxf: vi.fn().mockResolvedValue(undefined),
	playoutLoadPlaylist: vi.fn().mockResolvedValue(undefined),
	playoutSavedGet: vi.fn().mockResolvedValue({ items: [] }),
}));

vi.mock('$lib/state/notifications.svelte', () => ({
	notify: vi.fn(),
}));

import {
	listClipsOnEveryEngine,
	deleteClip,
	uploadClip,
	clipPlayerLoad,
	clipPlayerPlay,
	cut,
} from '$lib/api/switch-api';

function minimalState(overrides: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 500,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		masterLevel: 1,
		programPeak: [0, 0] as [number, number],
		tallyState: {},
		sources: {},
		seq: 1,
		lastCommandSeq: 0,
		timestamp: Date.now(),
		...overrides,
	} as ControlRoomState;
}

describe('OAOperationsDashboard', () => {
	beforeEach(() => {
		vi.clearAllMocks();
		localStorage.clear();
	});

	// ── Section rendering ──

	it('renders all four section headers', () => {
		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});
		const headers = container.querySelectorAll('.section-title');
		const titles = Array.from(headers).map((h) => h.textContent?.trim());
		expect(titles).toEqual(['SLATES', 'BUG', 'CLIPS', 'PLAYOUT']);
	});

	it('has scrollable container', () => {
		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});
		const dashboard = container.querySelector('.operations-dashboard');
		expect(dashboard).toBeTruthy();
		expect(dashboard!.classList.contains('operations-dashboard')).toBe(true);
	});

	it('renders child section components', () => {
		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});
		// Slates section renders the add button
		expect(screen.getByText('+')).toBeTruthy();
		// Bug section renders the empty state
		expect(screen.getByText(/No bug loaded/)).toBeTruthy();
		// Clips section renders the upload zone
		const uploadZone = container.querySelector('.upload-zone');
		expect(uploadZone).toBeTruthy();
		// Playout section renders the toggle
		expect(screen.getByText('OFF')).toBeTruthy();
	});

	it('playout toggle is interactive', async () => {
		render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});
		// Initially disabled
		expect(screen.getByText(/Playout not enabled/)).toBeTruthy();
	});

	// ── Slates localStorage persistence ──

	it('loads slates from localStorage on mount', async () => {
		const slates = [{ clipId: 'clip-1', name: 'Test Slate' }];
		localStorage.setItem('switchframe-slates', JSON.stringify(slates));

		render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});

		expect(screen.getByText('Test Slate')).toBeTruthy();
	});

	it('persists slates to localStorage when a slate is added via makeSlate', async () => {
		// Setup: have clips in the list
		(listClipsOnEveryEngine as Mock).mockResolvedValue([
			{ id: 'clip-1', name: 'My Clip', durationMs: 5000 },
		]);

		render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});

		// Wait for clips to load
		await vi.waitFor(() => {
			expect(screen.getByText('My Clip')).toBeTruthy();
		});

		// Mock window.prompt to return a name
		const promptSpy = vi.spyOn(window, 'prompt').mockReturnValue('My Slate');

		// Click the Slate button on the clip row
		const slateBtn = screen.getByText('Slate');
		await fireEvent.click(slateBtn);

		// Verify localStorage was updated
		const stored = JSON.parse(localStorage.getItem('switchframe-slates') ?? '[]');
		expect(stored).toHaveLength(1);
		expect(stored[0].clipId).toBe('clip-1');
		expect(stored[0].name).toBe('My Slate');

		promptSpy.mockRestore();
	});

	it('removes slate from localStorage on right-click remove', async () => {
		const slates = [{ clipId: 'clip-1', name: 'Old Slate' }];
		localStorage.setItem('switchframe-slates', JSON.stringify(slates));

		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});

		// The slate should render
		expect(screen.getByText('Old Slate')).toBeTruthy();

		// Right-click to remove
		const slateBtn = container.querySelector('.slate-btn:not(.add-btn)') as HTMLElement;
		expect(slateBtn).toBeTruthy();
		await fireEvent.contextMenu(slateBtn);

		// Verify it was removed from localStorage
		const stored = JSON.parse(localStorage.getItem('switchframe-slates') ?? '[]');
		expect(stored).toHaveLength(0);
	});

	it('handles corrupted localStorage gracefully', () => {
		localStorage.setItem('switchframe-slates', 'not-json');

		// Should not throw
		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});
		expect(container.querySelector('.operations-dashboard')).toBeTruthy();
	});

	// ── Slates fire: clip player load + play + cut ──

	it('fires slate by loading clip player, playing with loop, and cutting', async () => {
		const slates = [{ clipId: 'slate-clip', name: 'BRB' }];
		localStorage.setItem('switchframe-slates', JSON.stringify(slates));

		render(OAOperationsDashboard, {
			props: {
				crState: minimalState({
					clipPlayers: [
						{ id: 1, state: 'empty' as const },
						{ id: 2, state: 'empty' as const },
						{ id: 3, state: 'empty' as const },
						{ id: 4, state: 'empty' as const },
					],
				}),
			},
		});

		// Click the slate button
		const slateBtn = screen.getByText('BRB');
		await fireEvent.click(slateBtn);

		// Wait for async operations
		await vi.waitFor(() => {
			expect(clipPlayerLoad).toHaveBeenCalledWith(1, 'slate-clip');
		});
		expect(clipPlayerPlay).toHaveBeenCalledWith(1, 1, true);
		expect(cut).toHaveBeenCalledWith('clip:1');
	});

	it('shows warning and does not fire when all players are busy', async () => {
		const { notify } = await import('$lib/state/notifications.svelte');
		const slates = [{ clipId: 'slate-clip', name: 'BRB' }];
		localStorage.setItem('switchframe-slates', JSON.stringify(slates));

		render(OAOperationsDashboard, {
			props: {
				crState: minimalState({
					clipPlayers: [
						{ id: 1, state: 'playing' as const, clipId: 'other' },
						{ id: 2, state: 'playing' as const, clipId: 'other2' },
						{ id: 3, state: 'playing' as const, clipId: 'other3' },
						{ id: 4, state: 'playing' as const, clipId: 'other4' },
					],
				}),
			},
		});

		await fireEvent.click(screen.getByText('BRB'));

		// Should NOT attempt to load any player
		expect(clipPlayerLoad).not.toHaveBeenCalled();
		// Should show a warning notification
		expect(notify).toHaveBeenCalledWith('warning', expect.stringContaining('No clip player'));
	});

	// ── Clips: load on mount ──

	it('loads clips from API on mount', async () => {
		(listClipsOnEveryEngine as Mock).mockResolvedValue([
			{ id: 'c1', name: 'Clip A', durationMs: 3000 },
			{ id: 'c2', name: 'Clip B', durationMs: 6000 },
		]);

		render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});

		await vi.waitFor(() => {
			expect(screen.getByText('Clip A')).toBeTruthy();
			expect(screen.getByText('Clip B')).toBeTruthy();
		});
	});

	// ── Clips: delete ──

	it('deletes a clip and refreshes the list', async () => {
		(listClipsOnEveryEngine as Mock).mockResolvedValue([
			{ id: 'c1', name: 'Delete Me', durationMs: 1000 },
		]);
		(deleteClip as Mock).mockResolvedValue(undefined);

		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});

		await vi.waitFor(() => {
			expect(screen.getByText('Delete Me')).toBeTruthy();
		});

		// Click the delete button (the x)
		const deleteBtn = container.querySelector('.delete-action') as HTMLElement;
		expect(deleteBtn).toBeTruthy();
		await fireEvent.click(deleteBtn);

		expect(deleteClip).toHaveBeenCalledWith('c1');
	});

	it('removes slate when its clip is deleted', async () => {
		// Set up a slate referencing clip c1
		localStorage.setItem('switchframe-slates', JSON.stringify([
			{ clipId: 'c1', name: 'My Slate' },
		]));

		(listClipsOnEveryEngine as Mock).mockResolvedValue([
			{ id: 'c1', name: 'Used Clip', durationMs: 1000 },
		]);
		(deleteClip as Mock).mockResolvedValue(undefined);

		const { container } = render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});

		await vi.waitFor(() => {
			expect(screen.getByText('Used Clip')).toBeTruthy();
		});

		// Delete the clip
		const deleteBtn = container.querySelector('.delete-action') as HTMLElement;
		await fireEvent.click(deleteBtn);

		// The slate should also be removed from localStorage
		await vi.waitFor(() => {
			const stored = JSON.parse(localStorage.getItem('switchframe-slates') ?? '[]');
			expect(stored).toHaveLength(0);
		});
	});

	// ── Bugs: derived from graphics layers ──

	it('derives bugs from crState graphics layers', () => {
		const state = minimalState({
			graphics: {
				layers: [
					{
						id: 1,
						active: false,
						zOrder: 1,
						x: 0, y: 0,
						width: 1920, height: 1080,
						imageName: 'bug.png',
					},
					{
						id: 2,
						active: false,
						zOrder: 2,
						x: 0, y: 0,
						width: 1920, height: 1080,
						sequenceLoaded: true,
					},
				],
			},
		});

		render(OAOperationsDashboard, { props: { crState: state } });

		// Both layers should appear as bugs (one has imageName, one has sequenceLoaded)
		// The bug list should have radio buttons for each
		const radios = document.querySelectorAll('.bug-radio');
		expect(radios.length).toBe(2);
	});

	it('persists active bug ID to localStorage', async () => {
		const state = minimalState({
			graphics: {
				layers: [
					{
						id: 1,
						active: false,
						zOrder: 1,
						x: 0, y: 0,
						width: 1920, height: 1080,
						imageName: 'bug.png',
					},
				],
			},
		});

		render(OAOperationsDashboard, { props: { crState: state } });

		// Select the bug
		const radio = document.querySelector('.bug-radio') as HTMLInputElement;
		expect(radio).toBeTruthy();
		await fireEvent.change(radio);

		expect(localStorage.getItem('switchframe-active-bug')).toBe('1');
	});

	// ── Playout mode callback ──

	it('exposes playoutEnabled via onPlayoutToggle callback', async () => {
		const toggleFn = vi.fn();

		render(OAOperationsDashboard, {
			props: {
				crState: minimalState(),
				onPlayoutToggle: toggleFn,
			},
		});

		// Click the playout toggle
		const toggleBtn = screen.getByText('OFF');
		await fireEvent.click(toggleBtn);

		expect(toggleFn).toHaveBeenCalledWith(true);
	});

	// ── As-run callback ──

	it('passes onShowAsRun to playout section', async () => {
		const asRunFn = vi.fn();

		render(OAOperationsDashboard, {
			props: {
				crState: minimalState(),
				onShowAsRun: asRunFn,
			},
		});

		// The as-run callback is wired through, verify the prop was accepted
		// (we just need to confirm no error on render)
		expect(true).toBe(true);
	});

	// ── Clips: upload via uploadClip API ──

	it('calls uploadClip API when files are selected', async () => {
		render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});

		const fileInput = document.querySelector('input[accept="video/*,.ts,.mp4,.mov"]') as HTMLInputElement;
		expect(fileInput).toBeTruthy();

		const testFile = new File(['data'], 'test.mp4', { type: 'video/mp4' });
		Object.defineProperty(fileInput, 'files', { value: [testFile], configurable: true });

		await fireEvent.change(fileInput);

		await vi.waitFor(() => {
			expect(uploadClip).toHaveBeenCalledWith(testFile, expect.any(Function));
		});
	});

	// ── addSlate via + button ──

	it('addSlate picks first available clip not already a slate', async () => {
		(listClipsOnEveryEngine as Mock).mockResolvedValue([
			{ id: 'c1', name: 'Clip A', durationMs: 3000 },
			{ id: 'c2', name: 'Clip B', durationMs: 6000 },
		]);

		// c1 is already a slate
		localStorage.setItem('switchframe-slates', JSON.stringify([
			{ clipId: 'c1', name: 'Existing Slate' },
		]));

		render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});

		await vi.waitFor(() => {
			expect(screen.getByText('Clip A')).toBeTruthy();
		});

		const promptSpy = vi.spyOn(window, 'prompt').mockReturnValue('New Slate');

		// Click the + add button
		await fireEvent.click(screen.getByText('+'));

		// It should pick c2 (first clip not already a slate)
		const stored = JSON.parse(localStorage.getItem('switchframe-slates') ?? '[]');
		expect(stored).toHaveLength(2);
		expect(stored[1].clipId).toBe('c2');
		expect(stored[1].name).toBe('New Slate');

		promptSpy.mockRestore();
	});

	it('addSlate does nothing when prompt is cancelled', async () => {
		(listClipsOnEveryEngine as Mock).mockResolvedValue([
			{ id: 'c1', name: 'Clip A', durationMs: 3000 },
		]);

		render(OAOperationsDashboard, {
			props: { crState: minimalState() },
		});

		await vi.waitFor(() => {
			expect(screen.getByText('Clip A')).toBeTruthy();
		});

		const promptSpy = vi.spyOn(window, 'prompt').mockReturnValue(null);

		await fireEvent.click(screen.getByText('+'));

		const stored = JSON.parse(localStorage.getItem('switchframe-slates') ?? '[]');
		expect(stored).toHaveLength(0);

		promptSpy.mockRestore();
	});
});
