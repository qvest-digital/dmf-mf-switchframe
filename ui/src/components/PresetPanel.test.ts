import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import PresetPanel from './PresetPanel.svelte';
import { reloadHolds } from '$lib/util/activity';

vi.mock('$lib/api/switch-api', () => ({
	listPresets: vi.fn().mockResolvedValue([]),
	createPreset: vi.fn().mockResolvedValue({ id: '1', name: 'Test', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T00:00:00Z' }),
	recallPresetByName: vi.fn().mockResolvedValue({ preset: { id: '1', name: 'Test', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T00:00:00Z' } }),
	deletePresetByName: vi.fn().mockResolvedValue(undefined),
	apiCall: vi.fn(),
}));

vi.mock('$lib/state/notifications.svelte', () => ({
	notify: vi.fn(),
}));

describe('PresetPanel', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('renders add-preset control', () => {
		const { container } = render(PresetPanel);
		const btn = container.querySelector('.add-preset') as HTMLButtonElement;
		expect(btn).toBeTruthy();
		expect(btn.getAttribute('aria-label')).toBe('Save new preset');
	});

	it('shows empty state when no presets', () => {
		const { container } = render(PresetPanel);
		expect(container.textContent).toContain('No presets saved');
	});

	// A stale-build reload of an untouched tab would drop the name being typed.
	it('holds the reload while a preset name is being entered', async () => {
		const { container } = render(PresetPanel);
		expect(reloadHolds()).not.toContain('unsaved switcher preset');
		await fireEvent.click(container.querySelector('.add-preset') as HTMLButtonElement);
		expect(reloadHolds()).toContain('unsaved switcher preset');
		await fireEvent.click(container.querySelector('.save-cancel') as HTMLButtonElement);
		expect(reloadHolds()).not.toContain('unsaved switcher preset');
	});

	it('shows name input when add preset clicked', async () => {
		const { container } = render(PresetPanel);
		const btn = container.querySelector('.add-preset') as HTMLButtonElement;
		await fireEvent.click(btn);
		const input = container.querySelector('.save-input') as HTMLInputElement;
		expect(input).toBeTruthy();
		expect(input.placeholder.toLowerCase()).toContain('name');
	});

	it('shows save and cancel icon buttons in save mode', async () => {
		const { container } = render(PresetPanel);
		const btn = container.querySelector('.add-preset') as HTMLButtonElement;
		await fireEvent.click(btn);
		const confirmBtn = container.querySelector('.save-submit') as HTMLButtonElement;
		const cancelBtn = container.querySelector('.save-cancel') as HTMLButtonElement;
		expect(confirmBtn).toBeTruthy();
		expect(cancelBtn).toBeTruthy();
	});

	it('hides input when cancel clicked', async () => {
		const { container } = render(PresetPanel);
		const saveBtn = container.querySelector('.add-preset') as HTMLButtonElement;
		await fireEvent.click(saveBtn);
		expect(container.querySelector('.save-input')).toBeTruthy();
		const cancelBtn = container.querySelector('.save-cancel') as HTMLButtonElement;
		await fireEvent.click(cancelBtn);
		expect(container.querySelector('.save-input')).toBeFalsy();
	});

	it('calls createPreset when confirm clicked with name', async () => {
		const { createPreset } = await import('$lib/api/switch-api');
		const { container } = render(PresetPanel);
		const saveBtn = container.querySelector('.add-preset') as HTMLButtonElement;
		await fireEvent.click(saveBtn);
		const input = container.querySelector('.save-input') as HTMLInputElement;
		await fireEvent.input(input, { target: { value: 'My Preset' } });
		const confirmBtn = container.querySelector('.save-submit') as HTMLButtonElement;
		await fireEvent.click(confirmBtn);
		expect(createPreset).toHaveBeenCalledWith('My Preset');
	});

	it('does not call createPreset when name is empty', async () => {
		const { createPreset } = await import('$lib/api/switch-api');
		const { container } = render(PresetPanel);
		const saveBtn = container.querySelector('.add-preset') as HTMLButtonElement;
		await fireEvent.click(saveBtn);
		const confirmBtn = container.querySelector('.save-submit') as HTMLButtonElement;
		await fireEvent.click(confirmBtn);
		expect(createPreset).not.toHaveBeenCalled();
	});

	it('renders preset list when presets exist', async () => {
		const { listPresets } = await import('$lib/api/switch-api');
		vi.mocked(listPresets).mockResolvedValueOnce([
			{ id: '1', name: 'Preset A', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T12:00:00Z' },
			{ id: '2', name: 'Preset B', programSource: 'cam2', previewSource: 'cam1', transitionType: 'mix', transitionDurMs: 500, audioChannels: {}, masterLevel: -3, createdAt: '2026-03-06T13:00:00Z' },
		]);
		const { container } = render(PresetPanel);
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Preset A');
		});
		expect(container.textContent).toContain('Preset B');
	});

	it('calls recallPresetByName when preset card is clicked', async () => {
		const { listPresets, recallPresetByName } = await import('$lib/api/switch-api');
		vi.mocked(listPresets).mockResolvedValueOnce([
			{ id: '1', name: 'Preset A', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T12:00:00Z' },
		]);
		const { container } = render(PresetPanel);
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Preset A');
		});
		const recallBtn = container.querySelector('.preset-recall') as HTMLButtonElement;
		await fireEvent.click(recallBtn);
		expect(recallPresetByName).toHaveBeenCalledWith('Preset A');
	});

	it('shows delete confirmation when delete button clicked', async () => {
		const { listPresets } = await import('$lib/api/switch-api');
		vi.mocked(listPresets).mockResolvedValueOnce([
			{ id: '1', name: 'Preset A', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T12:00:00Z' },
		]);
		const { container } = render(PresetPanel);
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Preset A');
		});
		const deleteBtn = container.querySelector('.preset-trash') as HTMLButtonElement;
		await fireEvent.click(deleteBtn);
		const confirmYes = container.querySelector('.delete-confirm') as HTMLButtonElement;
		const confirmNo = container.querySelector('.delete-dismiss') as HTMLButtonElement;
		expect(confirmYes).toBeTruthy();
		expect(confirmNo).toBeTruthy();
		expect(confirmYes.getAttribute('aria-label')).toBe('Confirm delete');
		expect(confirmNo.getAttribute('aria-label')).toBe('Cancel delete');
	});

	it('calls deletePresetByName when Yes clicked in confirmation', async () => {
		const { listPresets, deletePresetByName } = await import('$lib/api/switch-api');
		vi.mocked(listPresets).mockResolvedValueOnce([
			{ id: '1', name: 'Preset A', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T12:00:00Z' },
		]);
		const { container } = render(PresetPanel);
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Preset A');
		});
		const deleteBtn = container.querySelector('.preset-trash') as HTMLButtonElement;
		await fireEvent.click(deleteBtn);
		const confirmYes = container.querySelector('.delete-confirm') as HTMLButtonElement;
		await fireEvent.click(confirmYes);
		expect(deletePresetByName).toHaveBeenCalledWith('Preset A');
	});

	it('cancels delete when No clicked in confirmation', async () => {
		const { listPresets, deletePresetByName } = await import('$lib/api/switch-api');
		vi.mocked(listPresets).mockResolvedValueOnce([
			{ id: '1', name: 'Preset A', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T12:00:00Z' },
		]);
		const { container } = render(PresetPanel);
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Preset A');
		});
		const deleteBtn = container.querySelector('.preset-trash') as HTMLButtonElement;
		await fireEvent.click(deleteBtn);
		const confirmNo = container.querySelector('.delete-dismiss') as HTMLButtonElement;
		await fireEvent.click(confirmNo);
		expect(deletePresetByName).not.toHaveBeenCalled();
	});

	it('renders Presets section label', () => {
		const { container } = render(PresetPanel);
		expect(container.textContent?.toLowerCase()).toContain('presets');
	});
});
