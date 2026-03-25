import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import PlayoutSettings from './PlayoutSettings.svelte';
import type { Playlist, SampleClip } from '$lib/api/types';

function makePlaylist(overrides: Partial<Playlist> = {}): Playlist {
	return {
		id: 'pl-001',
		name: 'Test Show',
		default_transition: { type: 'cut' },
		items: [],
		...overrides,
	};
}

function makeSampleClips(): SampleClip[] {
	return [
		{ name: 'slate', title: 'Holding Slate', path: '/clips/slate.ts', duration: 10000 },
		{ name: 'bars', title: 'Color Bars', path: '/clips/bars.ts', duration: 5000 },
	];
}

describe('PlayoutSettings', () => {
	it('renders collapsed by default', () => {
		const { container } = render(PlayoutSettings, {
			props: { playlist: makePlaylist(), onChange: vi.fn() },
		});
		expect(container.textContent).toContain('Playlist Settings');
		expect(container.querySelector('.section-body')).toBeFalsy();
	});

	it('expands on click', async () => {
		const { container } = render(PlayoutSettings, {
			props: { playlist: makePlaylist(), onChange: vi.fn() },
		});
		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;
		await fireEvent.click(toggle);
		expect(container.querySelector('.section-body')).toBeTruthy();
	});

	it('shows playlist name', async () => {
		const { container } = render(PlayoutSettings, {
			props: { playlist: makePlaylist({ name: 'NFL Sunday' }), onChange: vi.fn() },
		});
		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;
		await fireEvent.click(toggle);
		const nameInput = container.querySelector('.field-input[type="text"]') as HTMLInputElement;
		expect(nameInput).toBeTruthy();
		expect(nameInput.value).toBe('NFL Sunday');
	});

	it('calls onChange when name changed', async () => {
		const onChange = vi.fn();
		const { container } = render(PlayoutSettings, {
			props: { playlist: makePlaylist({ name: 'Old Name' }), onChange },
		});
		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;
		await fireEvent.click(toggle);
		const nameInput = container.querySelector('.field-input[type="text"]') as HTMLInputElement;
		await fireEvent.change(nameInput, { target: { value: 'New Name' } });
		expect(onChange).toHaveBeenCalledWith(
			expect.objectContaining({ name: 'New Name' }),
		);
	});

	it('shows idle asset selector', async () => {
		const { container } = render(PlayoutSettings, {
			props: {
				playlist: makePlaylist(),
				onChange: vi.fn(),
				sampleClips: makeSampleClips(),
			},
		});
		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;
		await fireEvent.click(toggle);
		const select = container.querySelector('.field-select') as HTMLSelectElement;
		expect(select).toBeTruthy();
		// Should have None, 2 clips, and Custom URI options
		const options = select.querySelectorAll('option');
		expect(options.length).toBe(4); // None + 2 clips + Custom URI
	});

	it('shows default transition selector', async () => {
		const { container } = render(PlayoutSettings, {
			props: {
				playlist: makePlaylist({ default_transition: { type: 'dissolve', duration_ms: 500 } }),
				onChange: vi.fn(),
			},
		});
		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;
		await fireEvent.click(toggle);
		// Find the transition type select (second select in the DOM since idle is first)
		const selects = container.querySelectorAll('.field-select');
		// First is idle asset, second is transition type
		const transitionSelect = selects[1] as HTMLSelectElement;
		expect(transitionSelect).toBeTruthy();
		expect(transitionSelect.value).toBe('dissolve');
	});

	it('shows pod template list', async () => {
		const playlist = makePlaylist({
			pods: [
				{ label: 'Halftime', duration_ms: 180000, auto_return: true },
				{ label: 'Commercial', duration_ms: 120000, auto_return: true },
			],
		});
		const { container } = render(PlayoutSettings, {
			props: { playlist, onChange: vi.fn() },
		});
		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;
		await fireEvent.click(toggle);
		const podRows = container.querySelectorAll('.pod-row');
		expect(podRows.length).toBe(2);
		// Badge should show pod count
		const badge = container.querySelector('.section-badge');
		expect(badge).toBeTruthy();
		expect(badge!.textContent).toBe('2 pods');
	});

	it('adds pod template on button click', async () => {
		const onChange = vi.fn();
		const { container } = render(PlayoutSettings, {
			props: { playlist: makePlaylist({ pods: [] }), onChange },
		});
		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;
		await fireEvent.click(toggle);
		const addBtn = container.querySelector('.pod-add-btn') as HTMLButtonElement;
		expect(addBtn).toBeTruthy();
		await fireEvent.click(addBtn);
		expect(onChange).toHaveBeenCalledWith(
			expect.objectContaining({
				pods: [expect.objectContaining({ label: 'New Break', duration_ms: 60000, auto_return: true })],
			}),
		);
	});

	it('removes pod template on delete', async () => {
		const onChange = vi.fn();
		const playlist = makePlaylist({
			pods: [
				{ label: 'Keep', duration_ms: 60000, auto_return: true },
				{ label: 'Remove', duration_ms: 30000, auto_return: false },
			],
		});
		const { container } = render(PlayoutSettings, {
			props: { playlist, onChange },
		});
		const toggle = container.querySelector('.section-toggle') as HTMLButtonElement;
		await fireEvent.click(toggle);
		const removeBtns = container.querySelectorAll('.pod-remove-btn');
		expect(removeBtns.length).toBe(2);
		// Remove the second pod
		await fireEvent.click(removeBtns[1]);
		expect(onChange).toHaveBeenCalledWith(
			expect.objectContaining({
				pods: [expect.objectContaining({ label: 'Keep' })],
			}),
		);
	});
});
