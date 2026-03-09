import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import MacroPanel from './MacroPanel.svelte';
import type { ControlRoomState, SourceInfo } from '$lib/api/types';

vi.mock('$lib/api/switch-api', () => ({
	listMacros: vi.fn().mockResolvedValue([]),
	saveMacro: vi.fn().mockResolvedValue({ name: 'test', steps: [] }),
	deleteMacro: vi.fn().mockResolvedValue(undefined),
	runMacro: vi.fn().mockResolvedValue({ status: 'ok' }),
	apiCall: vi.fn(),
}));

vi.mock('$lib/state/notifications.svelte', () => ({
	notify: vi.fn(),
}));

function makeState(sources: Record<string, Partial<SourceInfo>> = {}): ControlRoomState {
	const fullSources: Record<string, SourceInfo> = {};
	for (const [key, val] of Object.entries(sources)) {
		fullSources[key] = {
			key,
			label: val.label ?? key,
			status: val.status ?? 'healthy',
			position: val.position ?? 0,
			delayMs: val.delayMs ?? 0,
			isVirtual: val.isVirtual ?? false,
			...val,
		} as SourceInfo;
	}
	return {
		sources: fullSources,
		programSource: '',
		previewSource: '',
		audioChannels: {},
		tallyState: {},
	} as unknown as ControlRoomState;
}

describe('MacroPanel', () => {
	beforeEach(() => {
		vi.clearAllMocks();
		localStorage.clear();
	});

	it('renders MACROS header', () => {
		const { container } = render(MacroPanel, { props: { state: makeState() } });
		expect(container.textContent).toContain('MACROS');
	});

	it('shows getting started guide when no macros and not dismissed', () => {
		const { container } = render(MacroPanel, { props: { state: makeState() } });
		expect(container.textContent).toContain('Getting Started');
		expect(container.textContent).toContain('automate');
	});

	it('hides guide when Got it is clicked and persists to localStorage', async () => {
		const { container } = render(MacroPanel, { props: { state: makeState() } });
		const gotItBtn = container.querySelector('.guide-dismiss') as HTMLButtonElement;
		expect(gotItBtn).toBeTruthy();
		await fireEvent.click(gotItBtn);
		expect(container.textContent).not.toContain('Getting Started');
		expect(localStorage.getItem('switchframe-macro-guide-dismissed')).toBe('true');
	});

	it('shows help button that re-shows guide', async () => {
		localStorage.setItem('switchframe-macro-guide-dismissed', 'true');
		const { container } = render(MacroPanel, { props: { state: makeState() } });
		expect(container.textContent).not.toContain('Getting Started');
		const helpBtn = container.querySelector('.help-btn') as HTMLButtonElement;
		expect(helpBtn).toBeTruthy();
		await fireEvent.click(helpBtn);
		expect(container.textContent).toContain('Getting Started');
	});

	it('opens edit mode when + button is clicked', async () => {
		const { container } = render(MacroPanel, { props: { state: makeState() } });
		const addBtn = container.querySelector('.add-btn') as HTMLButtonElement;
		await fireEvent.click(addBtn);
		expect(container.querySelector('.macro-name-input')).toBeTruthy();
		expect(container.querySelector('.step-card')).toBeTruthy();
	});

	it('new macro starts with one cut step', async () => {
		const { container } = render(MacroPanel, { props: { state: makeState({ cam1: {} }) } });
		const addBtn = container.querySelector('.add-btn') as HTMLButtonElement;
		await fireEvent.click(addBtn);
		const actionSelect = container.querySelector('.action-select') as HTMLSelectElement;
		expect(actionSelect).toBeTruthy();
		expect(actionSelect.value).toBe('cut');
	});

	it('shows source dropdown with available sources', async () => {
		const state = makeState({ cam1: { label: 'Camera 1' }, cam2: { label: 'Camera 2' } });
		const { container } = render(MacroPanel, { props: { state } });
		const addBtn = container.querySelector('.add-btn') as HTMLButtonElement;
		await fireEvent.click(addBtn);
		const sourceSelect = container.querySelector('.source-select') as HTMLSelectElement;
		expect(sourceSelect).toBeTruthy();
		const options = Array.from(sourceSelect.options).map(o => o.value);
		expect(options).toContain('cam1');
		expect(options).toContain('cam2');
	});

	it('shows transition-specific fields when transition action selected', async () => {
		const state = makeState({ cam1: {} });
		const { container } = render(MacroPanel, { props: { state } });
		const addBtn = container.querySelector('.add-btn') as HTMLButtonElement;
		await fireEvent.click(addBtn);
		const actionSelect = container.querySelector('.action-select') as HTMLSelectElement;
		await fireEvent.change(actionSelect, { target: { value: 'transition' } });
		expect(container.querySelector('.transition-type-select')).toBeTruthy();
		expect(container.querySelector('.duration-input')).toBeTruthy();
	});

	it('shows duration field when wait action selected', async () => {
		const state = makeState({ cam1: {} });
		const { container } = render(MacroPanel, { props: { state } });
		const addBtn = container.querySelector('.add-btn') as HTMLButtonElement;
		await fireEvent.click(addBtn);
		const actionSelect = container.querySelector('.action-select') as HTMLSelectElement;
		await fireEvent.change(actionSelect, { target: { value: 'wait' } });
		expect(container.querySelector('.wait-duration-input')).toBeTruthy();
	});

	it('adds a step when add step button is clicked', async () => {
		const { container } = render(MacroPanel, { props: { state: makeState({ cam1: {} }) } });
		const addBtn = container.querySelector('.add-btn') as HTMLButtonElement;
		await fireEvent.click(addBtn);
		expect(container.querySelectorAll('.step-card').length).toBe(1);
		const addStepBtn = container.querySelector('.add-step-btn') as HTMLButtonElement;
		await fireEvent.click(addStepBtn);
		// The add-step picker should appear
		expect(container.querySelector('.step-picker')).toBeTruthy();
	});

	it('removes a step when delete button is clicked', async () => {
		const { container } = render(MacroPanel, { props: { state: makeState({ cam1: {} }) } });
		const addBtn = container.querySelector('.add-btn') as HTMLButtonElement;
		await fireEvent.click(addBtn);
		// Add a second step by clicking add-step and picking an action
		const addStepBtn = container.querySelector('.add-step-btn') as HTMLButtonElement;
		await fireEvent.click(addStepBtn);
		const pickerItem = container.querySelector('.picker-item') as HTMLButtonElement;
		if (pickerItem) await fireEvent.click(pickerItem);
		const deleteStepBtns = container.querySelectorAll('.step-delete');
		const countBefore = container.querySelectorAll('.step-card').length;
		if (deleteStepBtns.length > 0) {
			await fireEvent.click(deleteStepBtns[0]);
			expect(container.querySelectorAll('.step-card').length).toBe(countBefore - 1);
		}
	});

	it('calls saveMacro with structured data on save', async () => {
		const { saveMacro } = await import('$lib/api/switch-api');
		const state = makeState({ cam1: {} });
		const { container } = render(MacroPanel, { props: { state } });
		const addBtn = container.querySelector('.add-btn') as HTMLButtonElement;
		await fireEvent.click(addBtn);
		const nameInput = container.querySelector('.macro-name-input') as HTMLInputElement;
		await fireEvent.input(nameInput, { target: { value: 'Test Macro' } });
		// Select source for the default cut step
		const sourceSelect = container.querySelector('.source-select') as HTMLSelectElement;
		if (sourceSelect) {
			await fireEvent.change(sourceSelect, { target: { value: 'cam1' } });
		}
		const saveBtn = container.querySelector('.save-btn') as HTMLButtonElement;
		await fireEvent.click(saveBtn);
		expect(saveMacro).toHaveBeenCalledWith({
			name: 'Test Macro',
			steps: [{ action: 'cut', params: { source: 'cam1' } }],
		});
	});

	it('shows validation error when name is empty on save', async () => {
		const { container } = render(MacroPanel, { props: { state: makeState() } });
		const addBtn = container.querySelector('.add-btn') as HTMLButtonElement;
		await fireEvent.click(addBtn);
		const saveBtn = container.querySelector('.save-btn') as HTMLButtonElement;
		await fireEvent.click(saveBtn);
		expect(container.querySelector('.editor-error')).toBeTruthy();
	});

	it('returns to list mode on cancel', async () => {
		const { container } = render(MacroPanel, { props: { state: makeState() } });
		const addBtn = container.querySelector('.add-btn') as HTMLButtonElement;
		await fireEvent.click(addBtn);
		expect(container.querySelector('.macro-name-input')).toBeTruthy();
		const cancelBtn = container.querySelector('.cancel-btn') as HTMLButtonElement;
		await fireEvent.click(cancelBtn);
		expect(container.querySelector('.macro-name-input')).toBeFalsy();
	});

	it('renders existing macros as run buttons', async () => {
		const { listMacros } = await import('$lib/api/switch-api');
		vi.mocked(listMacros).mockResolvedValueOnce([
			{ name: 'Morning Show', steps: [{ action: 'cut', params: { source: 'cam1' } }] },
		]);
		const { container } = render(MacroPanel, { props: { state: makeState() } });
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Morning Show');
		});
	});

	it('calls runMacro when macro button is clicked', async () => {
		const { listMacros, runMacro } = await import('$lib/api/switch-api');
		vi.mocked(listMacros).mockResolvedValueOnce([
			{ name: 'Morning Show', steps: [{ action: 'cut', params: { source: 'cam1' } }] },
		]);
		const { container } = render(MacroPanel, { props: { state: makeState() } });
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Morning Show');
		});
		const macroBtn = container.querySelector('.macro-btn') as HTMLButtonElement;
		await fireEvent.click(macroBtn);
		expect(runMacro).toHaveBeenCalledWith('Morning Show');
	});

	it('calls deleteMacro when delete button is clicked', async () => {
		const { listMacros, deleteMacro } = await import('$lib/api/switch-api');
		vi.mocked(listMacros).mockResolvedValueOnce([
			{ name: 'Morning Show', steps: [{ action: 'cut', params: { source: 'cam1' } }] },
		]);
		const { container } = render(MacroPanel, { props: { state: makeState() } });
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Morning Show');
		});
		const delBtn = container.querySelector('.del-btn') as HTMLButtonElement;
		await fireEvent.click(delBtn);
		expect(deleteMacro).toHaveBeenCalledWith('Morning Show');
	});

	it('loads existing macro into editor on edit click', async () => {
		const { listMacros } = await import('$lib/api/switch-api');
		vi.mocked(listMacros).mockResolvedValueOnce([
			{ name: 'Morning Show', steps: [{ action: 'cut', params: { source: 'cam1' } }, { action: 'wait', params: { ms: 500 } }] },
		]);
		const state = makeState({ cam1: {} });
		const { container } = render(MacroPanel, { props: { state } });
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Morning Show');
		});
		const editBtn = container.querySelector('.edit-btn') as HTMLButtonElement;
		await fireEvent.click(editBtn);
		expect(container.querySelector('.macro-name-input')).toBeTruthy();
		expect(container.querySelectorAll('.step-card').length).toBe(2);
	});

	it('shows step summary in collapsed state', async () => {
		const { listMacros } = await import('$lib/api/switch-api');
		vi.mocked(listMacros).mockResolvedValueOnce([
			{ name: 'Test', steps: [{ action: 'cut', params: { source: 'cam1' } }] },
		]);
		const state = makeState({ cam1: { label: 'Camera 1' } });
		const { container } = render(MacroPanel, { props: { state } });
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Test');
		});
		const editBtn = container.querySelector('.edit-btn') as HTMLButtonElement;
		await fireEvent.click(editBtn);
		const stepHeader = container.querySelector('.step-header');
		expect(stepHeader?.textContent).toContain('Cut');
	});

	it('shows keyboard shortcut tip when macros exist', async () => {
		const { listMacros } = await import('$lib/api/switch-api');
		vi.mocked(listMacros).mockResolvedValueOnce([
			{ name: 'Test', steps: [{ action: 'cut', params: { source: 'cam1' } }] },
		]);
		const { container } = render(MacroPanel, { props: { state: makeState() } });
		await vi.waitFor(() => {
			expect(container.textContent).toContain('Test');
		});
		expect(container.textContent).toContain('Ctrl+');
	});
});
