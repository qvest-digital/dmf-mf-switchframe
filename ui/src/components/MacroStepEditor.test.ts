import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';
import MacroStepEditor from './MacroStepEditor.svelte';
import type { MacroStep } from '$lib/api/types';

// Mock notifications
vi.mock('$lib/state/notifications.svelte', () => ({
	notify: vi.fn(),
}));

function renderEditor(step: MacroStep, overrides = {}) {
	return render(MacroStepEditor, {
		step,
		index: 0,
		sourceKeys: ['cam1', 'cam2', 'cam3'],
		sourceLabel: (key: string) => key.toUpperCase(),
		stingerNames: ['intro', 'outro'],
		presetNames: [{ id: 'p1', name: 'Preset 1' }],
		onupdate: vi.fn(),
		onchangeaction: vi.fn(),
		...overrides,
	});
}

describe('MacroStepEditor', () => {
	it('renders action select', () => {
		const { container } = renderEditor({ action: 'cut', params: { source: 'cam1' } });
		const select = container.querySelector('.action-select') as HTMLSelectElement;
		expect(select).toBeTruthy();
		expect(select.value).toBe('cut');
	});

	it('shows source picker for cut action', () => {
		const { container } = renderEditor({ action: 'cut', params: { source: 'cam1' } });
		const sourceSelect = container.querySelector('.source-select') as HTMLSelectElement;
		expect(sourceSelect).toBeTruthy();
	});

	it('shows source picker for audio_mute action', () => {
		const { container } = renderEditor({ action: 'audio_mute', params: { source: 'cam1', muted: true } });
		const sourceSelect = container.querySelector('.source-select') as HTMLSelectElement;
		expect(sourceSelect).toBeTruthy();
	});

	it('hides source picker for ftb action', () => {
		const { container } = renderEditor({ action: 'ftb', params: {} });
		const sourceSelect = container.querySelector('.source-select');
		expect(sourceSelect).toBeFalsy();
	});

	it('hides source picker for graphics_on action', () => {
		const { container } = renderEditor({ action: 'graphics_on', params: {} });
		const sourceSelect = container.querySelector('.source-select');
		expect(sourceSelect).toBeFalsy();
	});

	it('shows transition type select for transition action', () => {
		const { container } = renderEditor({ action: 'transition', params: { source: 'cam1', type: 'mix', durationMs: 1000 } });
		const typeSelect = container.querySelector('.transition-type-select') as HTMLSelectElement;
		expect(typeSelect).toBeTruthy();
		expect(typeSelect.value).toBe('mix');
	});

	it('shows stinger option in transition type select', () => {
		const { container } = renderEditor({ action: 'transition', params: { source: 'cam1', type: 'stinger', durationMs: 1000 } });
		const typeSelect = container.querySelector('.transition-type-select') as HTMLSelectElement;
		expect(typeSelect).toBeTruthy();
		const options = Array.from(typeSelect.querySelectorAll('option'));
		expect(options.some(o => o.value === 'stinger')).toBe(true);
	});

	it('shows wipe direction dropdown when type=wipe', () => {
		const { container } = renderEditor({ action: 'transition', params: { source: 'cam1', type: 'wipe', durationMs: 1000 } });
		const dirSelect = container.querySelector('.wipe-direction-select') as HTMLSelectElement;
		expect(dirSelect).toBeTruthy();
	});

	it('hides wipe direction when type=mix', () => {
		const { container } = renderEditor({ action: 'transition', params: { source: 'cam1', type: 'mix', durationMs: 1000 } });
		const dirSelect = container.querySelector('.wipe-direction-select');
		expect(dirSelect).toBeFalsy();
	});

	it('shows stinger picker when type=stinger', () => {
		const { container } = renderEditor({ action: 'transition', params: { source: 'cam1', type: 'stinger', durationMs: 1000 } });
		const stingerSelect = container.querySelector('.stinger-select') as HTMLSelectElement;
		expect(stingerSelect).toBeTruthy();
	});

	it('hides stinger picker when type=mix', () => {
		const { container } = renderEditor({ action: 'transition', params: { source: 'cam1', type: 'mix', durationMs: 1000 } });
		const stingerSelect = container.querySelector('.stinger-select');
		expect(stingerSelect).toBeFalsy();
	});

	it('shows duration input for transition', () => {
		const { container } = renderEditor({ action: 'transition', params: { source: 'cam1', type: 'mix', durationMs: 1000 } });
		const durationInput = container.querySelector('.duration-input') as HTMLInputElement;
		expect(durationInput).toBeTruthy();
		expect(durationInput.value).toBe('1000');
	});

	it('shows wait duration input', () => {
		const { container } = renderEditor({ action: 'wait', params: { ms: 500 } });
		const waitInput = container.querySelector('.wait-duration-input') as HTMLInputElement;
		expect(waitInput).toBeTruthy();
		expect(waitInput.value).toBe('500');
	});

	it('shows level input for set_audio', () => {
		const { container } = renderEditor({ action: 'set_audio', params: { source: 'cam1', level: -6 } });
		const levelInput = container.querySelector('.level-input') as HTMLInputElement;
		expect(levelInput).toBeTruthy();
		expect(levelInput.value).toBe('-6');
	});

	it('shows muted checkbox for audio_mute', () => {
		const { container } = renderEditor({ action: 'audio_mute', params: { source: 'cam1', muted: true } });
		const checkbox = container.querySelector('.field-checkbox') as HTMLInputElement;
		expect(checkbox).toBeTruthy();
		expect(checkbox.checked).toBe(true);
	});

	it('shows preset select for preset_recall', () => {
		const { container } = renderEditor({ action: 'preset_recall', params: {} });
		const presetSelect = container.querySelector('.preset-select') as HTMLSelectElement;
		expect(presetSelect).toBeTruthy();
	});

	it('shows speed select for replay_play', () => {
		const { container } = renderEditor({ action: 'replay_play', params: { source: 'cam1', speed: 0.5 } });
		const selects = container.querySelectorAll('.field-select');
		// Should have action select, source select, and speed select
		expect(selects.length).toBeGreaterThanOrEqual(3);
	});

	it('shows validation warning for wipe without direction', () => {
		const { container } = renderEditor({ action: 'transition', params: { source: 'cam1', type: 'wipe', durationMs: 1000 } });
		const warning = container.querySelector('.step-warning');
		expect(warning).toBeTruthy();
		expect(warning?.textContent).toContain('direction');
	});

	it('shows validation warning for stinger without name', () => {
		const { container } = renderEditor({ action: 'transition', params: { source: 'cam1', type: 'stinger', durationMs: 1000 } });
		const warning = container.querySelector('.step-warning');
		expect(warning).toBeTruthy();
		expect(warning?.textContent).toContain('Stinger name');
	});

	it('no warning when wipe has direction', () => {
		const { container } = renderEditor({ action: 'transition', params: { source: 'cam1', type: 'wipe', durationMs: 1000, wipeDirection: 'h-left' } });
		const warning = container.querySelector('.step-warning');
		expect(warning).toBeFalsy();
	});

	it('no warning when stinger has name', () => {
		const { container } = renderEditor({ action: 'transition', params: { source: 'cam1', type: 'stinger', durationMs: 1000, stingerName: 'intro' } });
		const warning = container.querySelector('.step-warning');
		expect(warning).toBeFalsy();
	});

	it('shows no params for recording_start', () => {
		const { container } = renderEditor({ action: 'recording_start', params: {} });
		const sourceSelect = container.querySelector('.source-select');
		expect(sourceSelect).toBeFalsy();
	});

	it('shows event ID for scte35 actions', () => {
		const { container } = renderEditor({ action: 'scte35_return', params: { eventId: 0 } });
		const eventIdInput = container.querySelector('.event-id-input') as HTMLInputElement;
		expect(eventIdInput).toBeTruthy();
	});

	// --- Graphics action parameter editors ---

	it('shows layer ID input for graphics_on', () => {
		const { container } = renderEditor({ action: 'graphics_on', params: { layerId: 2 } });
		const inputs = container.querySelectorAll('.field-input');
		const layerInput = Array.from(inputs).find(el => (el as HTMLInputElement).value === '2') as HTMLInputElement;
		expect(layerInput).toBeTruthy();
		expect(layerInput.type).toBe('number');
	});

	it('shows direction select and duration for graphics_fly_in', () => {
		const { container } = renderEditor({ action: 'graphics_fly_in', params: { layerId: 0, direction: 'left', durationMs: 500 } });
		const selects = container.querySelectorAll('.field-select');
		// action select + direction select
		const dirOption = container.querySelector('option[value="right"]');
		expect(dirOption).toBeTruthy();
		// duration input
		const inputs = container.querySelectorAll('.field-input');
		expect(inputs.length).toBeGreaterThanOrEqual(2); // layerId + durationMs
	});

	it('shows direction select and duration for graphics_fly_out', () => {
		const { container } = renderEditor({ action: 'graphics_fly_out', params: { layerId: 1, direction: 'bottom', durationMs: 300 } });
		const dirOption = container.querySelector('option[value="bottom"]');
		expect(dirOption).toBeTruthy();
	});

	it('shows x/y/width/height for graphics_set_rect', () => {
		const { container } = renderEditor({ action: 'graphics_set_rect', params: { layerId: 0, x: 100, y: 50, width: 960, height: 540 } });
		const inputs = container.querySelectorAll('.field-input');
		// layerId + x + y + width + height = 5
		expect(inputs.length).toBe(5);
	});

	it('shows x/y/width/height + duration for graphics_slide', () => {
		const { container } = renderEditor({ action: 'graphics_slide', params: { layerId: 0, x: 0, y: 0, width: 1920, height: 1080, durationMs: 500 } });
		const inputs = container.querySelectorAll('.field-input');
		// layerId + x + y + width + height + durationMs = 6
		expect(inputs.length).toBe(6);
	});

	it('shows z-order input for graphics_set_zorder', () => {
		const { container } = renderEditor({ action: 'graphics_set_zorder', params: { layerId: 0, zOrder: 3 } });
		const inputs = container.querySelectorAll('.field-input');
		// layerId + zOrder = 2
		expect(inputs.length).toBe(2);
	});

	it('shows mode select and pulse params for graphics_animate', () => {
		const { container } = renderEditor({ action: 'graphics_animate', params: { layerId: 0, mode: 'pulse', minAlpha: 0.3, maxAlpha: 1.0, speedHz: 1.0 } });
		const selects = container.querySelectorAll('.field-select');
		// action select + mode select
		expect(selects.length).toBe(2);
		const modeSelect = selects[1] as HTMLSelectElement;
		expect(modeSelect.value).toBe('pulse');
		// pulse params: layerId + minAlpha + maxAlpha + speedHz = 4
		const inputs = container.querySelectorAll('.field-input');
		expect(inputs.length).toBe(4);
	});

	it('shows transition params when graphics_animate mode is transition', () => {
		const { container } = renderEditor({ action: 'graphics_animate', params: { layerId: 0, mode: 'transition', toAlpha: 0.5, durationMs: 500 } });
		const inputs = container.querySelectorAll('.field-input');
		// layerId + toAlpha + durationMs = 3
		expect(inputs.length).toBe(3);
	});

	it('shows template select for graphics_upload_frame', () => {
		const { container } = renderEditor({ action: 'graphics_upload_frame', params: { layerId: 0, template: 'lower-third' } });
		const selects = container.querySelectorAll('.field-select');
		// action select + template select
		expect(selects.length).toBe(2);
		const templateSelect = selects[1] as HTMLSelectElement;
		expect(templateSelect.options.length).toBe(6);
	});

	it('shows no layer ID for graphics_add_layer', () => {
		const { container } = renderEditor({ action: 'graphics_add_layer', params: {} });
		const inputs = container.querySelectorAll('.field-input');
		expect(inputs.length).toBe(0);
	});
});
