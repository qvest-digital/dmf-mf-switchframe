import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import BottomTabs from './BottomTabs.svelte';

afterEach(() => {
	cleanup();
	localStorage.removeItem('sf-active-tab');
});

describe('BottomTabs', () => {
	it('renders all tabs in correct order', () => {
		const { container } = render(BottomTabs);
		const tabs = container.querySelectorAll('[role="tab"]');
		const tabLabels = Array.from(tabs).map((t) => t.textContent?.replace(/\^[0-9]/, '').trim());
		expect(tabLabels).toContain('Audio');
		expect(tabLabels).toContain('Video');
		expect(tabLabels).toContain('Compose');
		expect(tabLabels).toContain('Playout');
		expect(tabLabels).toContain('Replay');
		expect(tabLabels).toContain('Clips');
		expect(tabLabels).toContain('Captions');
		expect(tabLabels).toContain('SCTE');
		expect(tabLabels).toContain('Macros');
		expect(tabLabels).toContain('Team');
		expect(tabs.length).toBe(10);
	});

	it('does not render removed tabs', () => {
		const { container } = render(BottomTabs);
		const tabs = container.querySelectorAll('[role="tab"]');
		const tabLabels = Array.from(tabs).map((t) => t.textContent?.replace(/\^[0-9]/, '').trim());
		expect(tabLabels).not.toContain('Layout');
		expect(tabLabels).not.toContain('Graphics');
		expect(tabLabels).not.toContain('Keys');
		expect(tabLabels).not.toContain('Presets');
	});

	it('selects Audio tab by default', () => {
		const { container } = render(BottomTabs);
		const audioTab = container.querySelector('#tab-audio');
		expect(audioTab?.getAttribute('aria-selected')).toBe('true');
	});

	it('saves and restores active tab from localStorage', () => {
		localStorage.setItem('sf-active-tab', 'Clips');
		const { container } = render(BottomTabs);
		const clipsTab = container.querySelector('#tab-clips');
		expect(clipsTab?.getAttribute('aria-selected')).toBe('true');
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
