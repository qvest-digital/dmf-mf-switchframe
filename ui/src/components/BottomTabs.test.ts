import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import BottomTabs from './BottomTabs.svelte';

afterEach(() => {
	cleanup();
	localStorage.removeItem('sf-active-tab');
});

describe('BottomTabs', () => {
	it('renders all tabs including Compose', () => {
		const { container } = render(BottomTabs);
		const tabs = container.querySelectorAll('[role="tab"]');
		const tabLabels = Array.from(tabs).map((t) => t.textContent?.replace(/\^[0-9]/, '').trim());
		expect(tabLabels).toContain('Audio');
		expect(tabLabels).toContain('Compose');
		expect(tabLabels).toContain('Video');
		expect(tabLabels).toContain('Replay');
		expect(tabLabels).toContain('Captions');
		expect(tabLabels).toContain('SCTE');
		expect(tabLabels).toContain('Macros');
		expect(tabLabels).toContain('Presets');
		expect(tabLabels).toContain('Clips');
		expect(tabLabels).toContain('Playout');
		expect(tabLabels).toContain('AR');
		expect(tabLabels).toContain('Team');
		expect(tabs.length).toBe(12);
	});

	it('does not render old Layout, Graphics, or Keys tabs', () => {
		const { container } = render(BottomTabs);
		const tabs = container.querySelectorAll('[role="tab"]');
		const tabLabels = Array.from(tabs).map((t) => t.textContent?.replace(/\^[0-9]/, '').trim());
		expect(tabLabels).not.toContain('Layout');
		expect(tabLabels).not.toContain('Graphics');
		expect(tabLabels).not.toContain('Keys');
	});

	it('renders Presets tab', () => {
		const { container } = render(BottomTabs);
		const presetsTab = container.querySelector('#tab-presets');
		expect(presetsTab).toBeTruthy();
		expect(presetsTab!.textContent).toContain('Presets');
	});

	it('responds to Ctrl+Shift+9 for Presets tab', async () => {
		const { container } = render(BottomTabs);

		// Presets is the 9th tab (after AR was added), triggered by Ctrl+Shift+9
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

	it('migrates old Color tab to Video', () => {
		localStorage.setItem('sf-active-tab', 'Color');
		const { container } = render(BottomTabs);
		const videoTab = container.querySelector('#tab-video');
		expect(videoTab?.getAttribute('aria-selected')).toBe('true');
		expect(localStorage.getItem('sf-active-tab')).toBe('Video');
	});

	it('migrates old STMap tab to Video', () => {
		localStorage.setItem('sf-active-tab', 'STMap');
		const { container } = render(BottomTabs);
		const videoTab = container.querySelector('#tab-video');
		expect(videoTab?.getAttribute('aria-selected')).toBe('true');
		expect(localStorage.getItem('sf-active-tab')).toBe('Video');
	});

	it('migrates old AI tab to Video', () => {
		localStorage.setItem('sf-active-tab', 'AI');
		const { container } = render(BottomTabs);
		const videoTab = container.querySelector('#tab-video');
		expect(videoTab?.getAttribute('aria-selected')).toBe('true');
		expect(localStorage.getItem('sf-active-tab')).toBe('Video');
	});

	it('migrates old Layout tab to Compose', () => {
		localStorage.setItem('sf-active-tab', 'Layout');
		const { container } = render(BottomTabs);
		const composeTab = container.querySelector('#tab-compose');
		expect(composeTab?.getAttribute('aria-selected')).toBe('true');
		expect(localStorage.getItem('sf-active-tab')).toBe('Compose');
	});

	it('migrates old Graphics tab to Compose', () => {
		localStorage.setItem('sf-active-tab', 'Graphics');
		const { container } = render(BottomTabs);
		const composeTab = container.querySelector('#tab-compose');
		expect(composeTab?.getAttribute('aria-selected')).toBe('true');
		expect(localStorage.getItem('sf-active-tab')).toBe('Compose');
	});

	it('migrates old Keys tab to Compose', () => {
		localStorage.setItem('sf-active-tab', 'Keys');
		const { container } = render(BottomTabs);
		const composeTab = container.querySelector('#tab-compose');
		expect(composeTab?.getAttribute('aria-selected')).toBe('true');
		expect(localStorage.getItem('sf-active-tab')).toBe('Compose');
	});

	it('ignores invalid localStorage tab values', () => {
		localStorage.setItem('sf-active-tab', 'InvalidTab');
		const { container } = render(BottomTabs);
		const audioTab = container.querySelector('#tab-audio');
		expect(audioTab?.getAttribute('aria-selected')).toBe('true');
		localStorage.removeItem('sf-active-tab');
	});
});
