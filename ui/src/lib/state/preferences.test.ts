import { describe, it, expect, vi, beforeEach } from 'vitest';

describe('preferences', () => {
	beforeEach(() => {
		localStorage.clear();
		// Reset module state by re-importing
		vi.resetModules();
	});

	it('getConfirmMode defaults to false', async () => {
		const { getConfirmMode } = await import('./preferences.svelte');
		expect(getConfirmMode()).toBe(false);
	});

	it('setConfirmMode(true) persists to localStorage', async () => {
		const { setConfirmMode } = await import('./preferences.svelte');
		setConfirmMode(true);
		expect(localStorage.getItem('switchframe_confirm_mode')).toBe('1');
	});

	it('setConfirmMode(false) persists to localStorage', async () => {
		const { setConfirmMode } = await import('./preferences.svelte');
		setConfirmMode(true);
		setConfirmMode(false);
		expect(localStorage.getItem('switchframe_confirm_mode')).toBe('0');
	});

	it('getConfirmMode returns true after setConfirmMode(true)', async () => {
		const { getConfirmMode, setConfirmMode } = await import('./preferences.svelte');
		setConfirmMode(true);
		expect(getConfirmMode()).toBe(true);
	});

	it('getConfirmMode returns false after setConfirmMode(false)', async () => {
		const { getConfirmMode, setConfirmMode } = await import('./preferences.svelte');
		setConfirmMode(true);
		setConfirmMode(false);
		expect(getConfirmMode()).toBe(false);
	});

	it('initializes from localStorage on module load', async () => {
		localStorage.setItem('switchframe_confirm_mode', '1');
		const { getConfirmMode } = await import('./preferences.svelte');
		expect(getConfirmMode()).toBe(true);
	});

	it('initializes as false when localStorage has "0"', async () => {
		localStorage.setItem('switchframe_confirm_mode', '0');
		const { getConfirmMode } = await import('./preferences.svelte');
		expect(getConfirmMode()).toBe(false);
	});

	it('initializes as false when localStorage has no value', async () => {
		const { getConfirmMode } = await import('./preferences.svelte');
		expect(getConfirmMode()).toBe(false);
	});
});
