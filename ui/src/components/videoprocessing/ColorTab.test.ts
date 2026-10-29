import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import ColorTab from './ColorTab.svelte';
import type { ControlRoomState } from '$lib/api/types';
import { reloadHolds } from '$lib/util/activity';

vi.mock('$lib/api/switch-api', () => ({
	setCorrector: vi.fn().mockResolvedValue(undefined),
	resetCorrector: vi.fn().mockResolvedValue(undefined),
	saveCustomLook: vi.fn().mockResolvedValue(undefined),
	setSourceCorrector: vi.fn().mockResolvedValue(undefined),
	resetSourceCorrector: vi.fn().mockResolvedValue(undefined),
}));
vi.mock('$lib/state/notifications.svelte', () => ({ notify: vi.fn() }));

describe('ColorTab', () => {
	// A stale-build reload of an untouched tab would drop the look name
	// being typed.
	it('holds a reload while a look name is being entered', async () => {
		const state = { sources: {}, programSource: '', previewSource: '' } as unknown as ControlRoomState;
		const { container } = render(ColorTab, { props: { state } });
		expect(reloadHolds()).not.toContain('unsaved look');
		await fireEvent.click(container.querySelector('.btn-save') as HTMLButtonElement);
		expect(reloadHolds()).toContain('unsaved look');
		await fireEvent.click(container.querySelector('.icon-btn-cancel') as HTMLButtonElement);
		expect(reloadHolds()).not.toContain('unsaved look');
	});
});
