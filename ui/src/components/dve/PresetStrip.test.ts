import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import PresetStrip from './PresetStrip.svelte';
import { reloadHolds } from '$lib/util/activity';

describe('PresetStrip', () => {
	// A stale-build reload of an untouched tab would drop the name being typed.
	it('holds a reload while a preset name is being entered', async () => {
		const { container } = render(PresetStrip, {
			props: { activePreset: '', userPresets: [], onRecall: vi.fn(), onSave: vi.fn(), onDelete: vi.fn(), onClear: vi.fn() },
		});
		expect(reloadHolds()).not.toContain('unsaved preset');
		await fireEvent.click(container.querySelector('[title="Save current layout"]') as HTMLButtonElement);
		expect(reloadHolds()).toContain('unsaved preset');
		await fireEvent.click(container.querySelector('[title="Cancel"]') as HTMLButtonElement);
		expect(reloadHolds()).not.toContain('unsaved preset');
	});
});
