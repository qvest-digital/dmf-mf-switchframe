import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import PresetPanel from './PresetPanel.svelte';

vi.mock('$lib/api/switch-api', () => ({
	listPresets: vi.fn().mockResolvedValue([]),
	createPreset: vi.fn().mockResolvedValue({ id: '1', name: 'Test', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T00:00:00Z' }),
	recallPreset: vi.fn().mockResolvedValue({ preset: { id: '1', name: 'Test', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T00:00:00Z' } }),
	deletePreset: vi.fn().mockResolvedValue(undefined),
	apiCall: vi.fn(),
}));

vi.mock('$lib/state/notifications.svelte', () => ({
	notify: vi.fn(),
}));

describe('PresetPanel', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('renders save button', () => {
		const { container } = render(PresetPanel);
		const btn = container.querySelector('.save-btn') as HTMLButtonElement;
		expect(btn).toBeTruthy();
		expect(btn.textContent).toContain('Save Preset');
	});

	it('shows empty state when no presets', () => {
		const { container } = render(PresetPanel);
		expect(container.textContent).toContain('No presets saved');
	});

	it('shows name input when save button clicked', async () => {
		const { container } = render(PresetPanel);
		const btn = container.querySelector('.save-btn') as HTMLButtonElement;
		await fireEvent.click(btn);
		const input = container.querySelector('.save-input') as HTMLInputElement;
		expect(input).toBeTruthy();
		expect(input.placeholder.toLowerCase()).toContain('preset name');
	});

	it('shows confirm and cancel buttons in save mode', async () => {
		const { container } = render(PresetPanel);
		const btn = container.querySelector('.save-btn') as HTMLButtonElement;
		await fireEvent.click(btn);
		const confirmBtn = container.querySelector('.confirm-btn') as HTMLButtonElement;
		const cancelBtn = container.querySelector('.cancel-btn') as HTMLButtonElement;
		expect(confirmBtn).toBeTruthy();
		expect(cancelBtn).toBeTruthy();
	});

	it('hides input when cancel clicked', async () => {
		const { container } = render(PresetPanel);
		const saveBtn = container.querySelector('.save-btn') as HTMLButtonElement;
		await fireEvent.click(saveBtn);
		expect(container.querySelector('.save-input')).toBeTruthy();
		const cancelBtn = container.querySelector('.cancel-btn') as HTMLButtonElement;
		await fireEvent.click(cancelBtn);
		expect(container.querySelector('.save-input')).toBeFalsy();
	});

	it('calls createPreset when confirm clicked with name', async () => {
		const { createPreset } = await import('$lib/api/switch-api');
		const { container } = render(PresetPanel);
		const saveBtn = container.querySelector('.save-btn') as HTMLButtonElement;
		await fireEvent.click(saveBtn);
		const input = container.querySelector('.save-input') as HTMLInputElement;
		await fireEvent.input(input, { target: { value: 'My Preset' } });
		const confirmBtn = container.querySelector('.confirm-btn') as HTMLButtonElement;
		await fireEvent.click(confirmBtn);
		expect(createPreset).toHaveBeenCalledWith('My Preset');
	});

	it('does not call createPreset when name is empty', async () => {
		const { createPreset } = await import('$lib/api/switch-api');
		const { container } = render(PresetPanel);
		const saveBtn = container.querySelector('.save-btn') as HTMLButtonElement;
		await fireEvent.click(saveBtn);
		const confirmBtn = container.querySelector('.confirm-btn') as HTMLButtonElement;
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

	it('calls recallPreset when preset card is clicked', async () => {
		const { listPresets, recallPreset } = await import('$lib/api/switch-api');
		vi.mocked(listPresets).mockResolvedValueOnce([
			{ id: '1', name: 'Preset A', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T12:00:00Z' },
		]);
		const { container } = render(PresetPanel);
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Preset A');
		});
		const recallBtn = container.querySelector('.preset-recall') as HTMLButtonElement;
		await fireEvent.click(recallBtn);
		expect(recallPreset).toHaveBeenCalledWith('1');
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
		const deleteBtn = container.querySelector('.delete-btn') as HTMLButtonElement;
		await fireEvent.click(deleteBtn);
		const confirmYes = container.querySelector('.confirm-yes') as HTMLButtonElement;
		const confirmNo = container.querySelector('.confirm-no') as HTMLButtonElement;
		expect(confirmYes).toBeTruthy();
		expect(confirmNo).toBeTruthy();
		expect(confirmYes.textContent).toBe('Yes');
		expect(confirmNo.textContent).toBe('No');
	});

	it('calls deletePreset when Yes clicked in confirmation', async () => {
		const { listPresets, deletePreset } = await import('$lib/api/switch-api');
		vi.mocked(listPresets).mockResolvedValueOnce([
			{ id: '1', name: 'Preset A', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T12:00:00Z' },
		]);
		const { container } = render(PresetPanel);
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Preset A');
		});
		const deleteBtn = container.querySelector('.delete-btn') as HTMLButtonElement;
		await fireEvent.click(deleteBtn);
		const confirmYes = container.querySelector('.confirm-yes') as HTMLButtonElement;
		await fireEvent.click(confirmYes);
		expect(deletePreset).toHaveBeenCalledWith('1');
	});

	it('cancels delete when No clicked in confirmation', async () => {
		const { listPresets, deletePreset } = await import('$lib/api/switch-api');
		vi.mocked(listPresets).mockResolvedValueOnce([
			{ id: '1', name: 'Preset A', programSource: 'cam1', previewSource: 'cam2', transitionType: 'cut', transitionDurMs: 0, audioChannels: {}, masterLevel: 0, createdAt: '2026-03-06T12:00:00Z' },
		]);
		const { container } = render(PresetPanel);
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Preset A');
		});
		const deleteBtn = container.querySelector('.delete-btn') as HTMLButtonElement;
		await fireEvent.click(deleteBtn);
		const confirmNo = container.querySelector('.confirm-no') as HTMLButtonElement;
		await fireEvent.click(confirmNo);
		expect(deletePreset).not.toHaveBeenCalled();
	});

	it('renders PRESETS header', () => {
		const { container } = render(PresetPanel);
		expect(container.textContent).toContain('PRESETS');
	});
});
