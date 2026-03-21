import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import BottomTabs from './BottomTabs.svelte';

afterEach(() => {
	cleanup();
	localStorage.removeItem('sf-active-tab');
});

describe('BottomTabs', () => {
	it('renders all tabs including Presets', () => {
		const { container } = render(BottomTabs);
		const tabs = container.querySelectorAll('[role="tab"]');
		const tabLabels = Array.from(tabs).map((t) => t.textContent?.replace(/\^[0-9]/, '').trim());
		expect(tabLabels).toContain('Audio');
		expect(tabLabels).toContain('Graphics');
		expect(tabLabels).toContain('Macros');
		expect(tabLabels).toContain('Keys');
		expect(tabLabels).toContain('Replay');
		expect(tabLabels).toContain('Presets');
		expect(tabLabels).toContain('SCTE');
		expect(tabLabels).toContain('Layout');
		expect(tabLabels).toContain('Captions');
		expect(tabLabels).toContain('Clips');
		expect(tabLabels).toContain('Team');
		expect(tabLabels).toContain('STMap');
		expect(tabs.length).toBe(12);
	});

	it('renders Presets tab', () => {
		const { container } = render(BottomTabs);
		const presetsTab = container.querySelector('#tab-presets');
		expect(presetsTab).toBeTruthy();
		expect(presetsTab!.textContent).toContain('Presets');
	});

	it('responds to Ctrl+Shift+9 for Presets tab', async () => {
		const { container } = render(BottomTabs);

		// Presets is the 9th tab, triggered by Ctrl+Shift+9
		const event = new KeyboardEvent('keydown', {
			code: 'Digit9',
			ctrlKey: true,
			shiftKey: true,
			altKey: false,
			metaKey: false,
			bubbles: true,
			cancelable: true,
		});
		document.dispatchEvent(event);

		// Wait for Svelte to update DOM
		await vi.dynamicImportSettled();

		const presetsTab = container.querySelector('#tab-presets');
		expect(presetsTab?.getAttribute('aria-selected')).toBe('true');
	});

	it('selects Audio tab by default', () => {
		const { container } = render(BottomTabs);
		const audioTab = container.querySelector('#tab-audio');
		expect(audioTab?.getAttribute('aria-selected')).toBe('true');
	});

	it('saves and restores active tab from localStorage', () => {
		localStorage.setItem('sf-active-tab', 'Presets');
		const { container } = render(BottomTabs);
		const presetsTab = container.querySelector('#tab-presets');
		expect(presetsTab?.getAttribute('aria-selected')).toBe('true');
		localStorage.removeItem('sf-active-tab');
	});

	it('ignores invalid localStorage tab values', () => {
		localStorage.setItem('sf-active-tab', 'InvalidTab');
		const { container } = render(BottomTabs);
		const audioTab = container.querySelector('#tab-audio');
		expect(audioTab?.getAttribute('aria-selected')).toBe('true');
		localStorage.removeItem('sf-active-tab');
	});
});
