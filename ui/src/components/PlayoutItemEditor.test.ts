import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import PlayoutItemEditor from './PlayoutItemEditor.svelte';
import type { PlaylistItem, SourceInfo } from '$lib/api/types';

function liveSource(key: string, position: number): SourceInfo {
	return { key, type: 'mxl', status: 'healthy', position };
}

function makeItem(overrides: Partial<PlaylistItem> = {}): PlaylistItem {
	return {
		id: 'item-1',
		type: 'clip',
		title: 'Test Clip',
		duration_ms: 30000,
		asset_uri: '/media/test.ts',
		...overrides,
	};
}

describe('PlayoutItemEditor', () => {
	it('renders type selector with current type', () => {
		const { container } = render(PlayoutItemEditor, {
			props: { item: makeItem({ type: 'live' }), onChange: vi.fn() },
		});
		const typeSelect = container.querySelector('.field-type-select') as HTMLSelectElement;
		expect(typeSelect).toBeTruthy();
		expect(typeSelect.value).toBe('live');
	});

	it('shows clip fields for clip type', () => {
		const { container } = render(PlayoutItemEditor, {
			props: { item: makeItem({ type: 'clip', asset_uri: '/media/test.ts' }), onChange: vi.fn() },
		});
		expect(container.querySelector('.field-asset-uri')).toBeTruthy();
		expect(container.querySelector('.field-duration')).toBeTruthy();
		expect(container.querySelector('.field-in-point')).toBeTruthy();
		expect(container.querySelector('.field-out-point')).toBeTruthy();
		expect(container.querySelector('.field-loop')).toBeTruthy();
		expect(container.querySelector('.field-hold')).toBeTruthy();
	});

	it('shows source dropdown for live type', () => {
		const { container } = render(PlayoutItemEditor, {
			props: {
				item: makeItem({ type: 'live', source: 'cam1' }),
				onChange: vi.fn(),
				sources: {
					cam1: liveSource('cam1', 1),
					cam2: liveSource('cam2', 2),
					cam3: liveSource('cam3', 3),
				},
			},
		});
		const sourceSelect = container.querySelector('.field-source-select') as HTMLSelectElement;
		expect(sourceSelect).toBeTruthy();
		expect(sourceSelect.value).toBe('cam1');
		const options = [...sourceSelect.querySelectorAll('option')].filter((o) => !o.disabled);
		expect(options).toHaveLength(3);
	});

	// An NMOS input is shown under the connected sender's name in the
	// switcher, and only under its key here, so the sender an operator
	// meant could not be found.
	it('lists live sources under their switcher label and stores the input key', async () => {
		const onChange = vi.fn();
		const { container } = render(PlayoutItemEditor, {
			props: {
				item: makeItem({ type: 'live' }),
				onChange,
				sources: {
					'mxl:mxl-1': { ...liveSource('mxl:mxl-1', 1), label: 'srt-ingest-2 p-demo' },
					'mxl:mxl-2': { ...liveSource('mxl:mxl-2', 2), label: 'srt-ingest-1 p-demo' },
				},
			},
		});
		const sourceSelect = container.querySelector('.field-source-select') as HTMLSelectElement;
		const options = [...sourceSelect.querySelectorAll('option')].filter((o) => !o.disabled);
		expect(options.map((o) => [o.value, o.textContent])).toEqual([
			['mxl:mxl-1', 'srt-ingest-2 p-demo'],
			['mxl:mxl-2', 'srt-ingest-1 p-demo'],
		]);
		// No source chosen yet: the first source must not look chosen.
		expect(sourceSelect.value).toBe('');

		sourceSelect.value = 'mxl:mxl-2';
		await fireEvent.change(sourceSelect);
		expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ source: 'mxl:mxl-2' }));
	});

	it('follows a label change on the selected input', async () => {
		const sources = (label: string) => ({ 'mxl:mxl-1': { ...liveSource('mxl:mxl-1', 1), label } });
		const { container, rerender } = render(PlayoutItemEditor, {
			props: { item: makeItem({ type: 'live', source: 'mxl:mxl-1' }), onChange: vi.fn(), sources: sources('sender-a') },
		});
		const sourceSelect = () => container.querySelector('.field-source-select') as HTMLSelectElement;
		expect(sourceSelect().selectedOptions[0].textContent).toBe('sender-a');

		await rerender({ sources: sources('sender-b') });
		expect(sourceSelect().value).toBe('mxl:mxl-1');
		expect(sourceSelect().selectedOptions[0].textContent).toBe('sender-b');
	});

	it('shows duration for slate type', () => {
		const { container } = render(PlayoutItemEditor, {
			props: { item: makeItem({ type: 'slate', duration_ms: 10000 }), onChange: vi.fn() },
		});
		expect(container.querySelector('.field-duration')).toBeTruthy();
		// No clip-specific fields
		expect(container.querySelector('.field-asset-uri')).toBeFalsy();
		expect(container.querySelector('.field-in-point')).toBeFalsy();
		expect(container.querySelector('.field-loop')).toBeFalsy();
	});

	it('calls onChange when title changed', async () => {
		const onChange = vi.fn();
		const { container } = render(PlayoutItemEditor, {
			props: { item: makeItem({ title: 'Original' }), onChange },
		});
		const titleInput = container.querySelector('.field-title') as HTMLInputElement;
		expect(titleInput).toBeTruthy();
		titleInput.value = 'Updated Title';
		await fireEvent.input(titleInput);
		expect(onChange).toHaveBeenCalledOnce();
		const updated = onChange.mock.calls[0][0] as PlaylistItem;
		expect(updated.title).toBe('Updated Title');
		// Should preserve other fields
		expect(updated.id).toBe('item-1');
		expect(updated.type).toBe('clip');
	});

	it('calls onChange when type changed', async () => {
		const onChange = vi.fn();
		const { container } = render(PlayoutItemEditor, {
			props: { item: makeItem({ type: 'clip' }), onChange },
		});
		const typeSelect = container.querySelector('.field-type-select') as HTMLSelectElement;
		await fireEvent.change(typeSelect, { target: { value: 'live' } });
		expect(onChange).toHaveBeenCalledOnce();
		const updated = onChange.mock.calls[0][0] as PlaylistItem;
		expect(updated.type).toBe('live');
		// Type change should clear type-specific fields
		expect(updated.asset_uri).toBeUndefined();
		expect(updated.in_point_ms).toBeUndefined();
		expect(updated.out_point_ms).toBeUndefined();
		expect(updated.loop).toBeUndefined();
	});

	it('shows transition config', () => {
		const { container } = render(PlayoutItemEditor, {
			props: {
				item: makeItem({ transition: { type: 'dissolve', duration_ms: 500 } }),
				onChange: vi.fn(),
			},
		});
		const transSelect = container.querySelector('.field-transition-type') as HTMLSelectElement;
		expect(transSelect).toBeTruthy();
		expect(transSelect.value).toBe('dissolve');
		const transDur = container.querySelector('.field-transition-duration') as HTMLInputElement;
		expect(transDur).toBeTruthy();
		expect(transDur.value).toBe('500');
	});

	it('shows secondary events section', () => {
		const { container } = render(PlayoutItemEditor, {
			props: {
				item: makeItem({
					secondary_events: [
						{ type: 'graphics_on', offset_ms: 0, anchor: 'start' },
						{ type: 'scte35_cue', offset_ms: 5000, anchor: 'start' },
					],
				}),
				onChange: vi.fn(),
			},
		});
		expect(container.querySelector('.sec-events')).toBeTruthy();
		expect(container.textContent).toContain('Events (2)');
	});

	it('shows loop checkbox for clip items', () => {
		const { container } = render(PlayoutItemEditor, {
			props: { item: makeItem({ type: 'clip', loop: true }), onChange: vi.fn() },
		});
		const loopCheckbox = container.querySelector('.field-loop') as HTMLInputElement;
		expect(loopCheckbox).toBeTruthy();
		expect(loopCheckbox.checked).toBe(true);
	});

	it('hides loop checkbox for non-clip items', () => {
		const { container } = render(PlayoutItemEditor, {
			props: { item: makeItem({ type: 'live' }), onChange: vi.fn() },
		});
		expect(container.querySelector('.field-loop')).toBeFalsy();
	});

	it('sets transition to null when type is cut', async () => {
		const onChange = vi.fn();
		const { container } = render(PlayoutItemEditor, {
			props: {
				item: makeItem({ transition: { type: 'dissolve', duration_ms: 500 } }),
				onChange,
			},
		});
		const transSelect = container.querySelector('.field-transition-type') as HTMLSelectElement;
		await fireEvent.change(transSelect, { target: { value: 'cut' } });
		expect(onChange).toHaveBeenCalledOnce();
		const updated = onChange.mock.calls[0][0] as PlaylistItem;
		expect(updated.transition).toBeUndefined();
	});

	it('hides transition duration when type is cut', () => {
		const { container } = render(PlayoutItemEditor, {
			props: {
				item: makeItem({ transition: undefined }),
				onChange: vi.fn(),
			},
		});
		// Transition type should show "cut" for undefined/null transition
		const transSelect = container.querySelector('.field-transition-type') as HTMLSelectElement;
		expect(transSelect.value).toBe('cut');
		// Duration input should not be visible
		expect(container.querySelector('.field-transition-duration')).toBeFalsy();
	});
});
