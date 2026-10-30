import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import SecondaryEventEditor from './SecondaryEventEditor.svelte';
import type { SecondaryEvent } from '$lib/api/types';

function makeEvent(overrides: Partial<SecondaryEvent> = {}): SecondaryEvent {
	return {
		type: 'graphics_on',
		offset_ms: 0,
		anchor: 'start',
		...overrides,
	};
}

describe('SecondaryEventEditor', () => {
	it('renders event count', () => {
		const events = [makeEvent(), makeEvent({ type: 'scte35_cue' })];
		const { container } = render(SecondaryEventEditor, {
			props: { events, onUpdate: vi.fn() },
		});
		const label = container.querySelector('.sec-label');
		expect(label?.textContent).toBe('Events (2)');
	});

	it('is collapsed by default (no .sec-list)', () => {
		const { container } = render(SecondaryEventEditor, {
			props: { events: [makeEvent()], onUpdate: vi.fn() },
		});
		expect(container.querySelector('.sec-list')).toBeNull();
	});

	it('expands on header click', async () => {
		const { container } = render(SecondaryEventEditor, {
			props: { events: [makeEvent()], onUpdate: vi.fn() },
		});
		const header = container.querySelector('.sec-header') as HTMLElement;
		expect(header).toBeTruthy();
		await fireEvent.click(header);
		expect(container.querySelector('.sec-list')).toBeTruthy();
	});

	it('shows event type labels', async () => {
		const events = [
			makeEvent({ type: 'graphics_on' }),
			makeEvent({ type: 'scte35_cue' }),
		];
		const { container } = render(SecondaryEventEditor, {
			props: { events, onUpdate: vi.fn() },
		});
		// Expand first
		await fireEvent.click(container.querySelector('.sec-header')!);
		const labels = container.querySelectorAll('.sec-type-label');
		expect(labels).toHaveLength(2);
		expect(labels[0].textContent).toBe('GFX On');
		expect(labels[1].textContent).toBe('SCTE-35 Cue');
	});

	it('shows offset formatting from start', async () => {
		const events = [makeEvent({ offset_ms: 2500, anchor: 'start' })];
		const { container } = render(SecondaryEventEditor, {
			props: { events, onUpdate: vi.fn() },
		});
		await fireEvent.click(container.querySelector('.sec-header')!);
		const offset = container.querySelector('.sec-offset');
		expect(offset?.textContent).toBe('S+2.5s');
	});

	it('shows negative offset from end', async () => {
		const events = [makeEvent({ offset_ms: -1000, anchor: 'end' })];
		const { container } = render(SecondaryEventEditor, {
			props: { events, onUpdate: vi.fn() },
		});
		await fireEvent.click(container.querySelector('.sec-header')!);
		const offset = container.querySelector('.sec-offset');
		expect(offset?.textContent).toBe('E-1.0s');
	});

	it('shows "+ Add Event" button when expanded', async () => {
		const { container } = render(SecondaryEventEditor, {
			props: { events: [], onUpdate: vi.fn() },
		});
		await fireEvent.click(container.querySelector('.sec-header')!);
		const addBtn = container.querySelector('.sec-add-btn');
		expect(addBtn).toBeTruthy();
		expect(addBtn?.textContent).toBe('+ Add Event');
	});

	it('calls onUpdate when adding event', async () => {
		const onUpdate = vi.fn();
		const { container } = render(SecondaryEventEditor, {
			props: { events: [], onUpdate },
		});
		await fireEvent.click(container.querySelector('.sec-header')!);
		const addBtn = container.querySelector('.sec-add-btn') as HTMLElement;
		await fireEvent.click(addBtn);
		expect(onUpdate).toHaveBeenCalledOnce();
		const newEvents = onUpdate.mock.calls[0][0];
		expect(newEvents).toHaveLength(1);
		expect(newEvents[0]).toEqual({
			type: 'graphics_on',
			offset_ms: 0,
			anchor: 'start',
		});
	});

	it('calls onUpdate when removing event with correct remaining events', async () => {
		const onUpdate = vi.fn();
		const events = [
			makeEvent({ type: 'graphics_on', offset_ms: 0 }),
			makeEvent({ type: 'scte35_cue', offset_ms: 500 }),
			makeEvent({ type: 'audio_mute', offset_ms: 1000 }),
		];
		const { container } = render(SecondaryEventEditor, {
			props: { events, onUpdate },
		});
		await fireEvent.click(container.querySelector('.sec-header')!);
		// Remove the second event (index 1)
		const delBtns = container.querySelectorAll('.sec-del-btn');
		expect(delBtns).toHaveLength(3);
		await fireEvent.click(delBtns[1]);
		expect(onUpdate).toHaveBeenCalledOnce();
		const remaining = onUpdate.mock.calls[0][0];
		expect(remaining).toHaveLength(2);
		expect(remaining[0].type).toBe('graphics_on');
		expect(remaining[1].type).toBe('audio_mute');
	});

	it('shows timeline when itemDurationMs provided and events exist', async () => {
		const events = [makeEvent({ offset_ms: 500, anchor: 'start' })];
		const { container } = render(SecondaryEventEditor, {
			props: { events, onUpdate: vi.fn(), itemDurationMs: 5000 },
		});
		await fireEvent.click(container.querySelector('.sec-header')!);
		expect(container.querySelector('.sec-timeline')).toBeTruthy();
		expect(container.querySelector('.sec-timeline-marker')).toBeTruthy();
	});
});

describe('SecondaryEventEditor graphics layer', () => {
	const graphicsLayers = [
		{ id: 0, name: 'Sponsor', active: false, zOrder: 1, x: 0, y: 0, width: 0, height: 0 },
		{ id: 2, active: false, zOrder: 0, x: 0, y: 0, width: 0, height: 0 },
	];

	async function openEditor(evt: SecondaryEvent) {
		const onUpdate = vi.fn();
		const r = render(SecondaryEventEditor, { props: { events: [evt], onUpdate, graphicsLayers } });
		await fireEvent.click(r.container.querySelector('.sec-header') as HTMLElement);
		return { ...r, onUpdate };
	}

	it('names the layer an event addresses by id and label', async () => {
		const { container } = await openEditor(makeEvent({ layer: 2 }));
		expect(container.querySelector('.sec-layer')?.textContent).toBe('#2 Layer #2');
		await fireEvent.click(container.querySelector('.sec-edit-btn') as HTMLElement);
		const opts = Array.from(container.querySelectorAll('.sec-editor select option')).map((o) => o.textContent?.trim());
		expect(opts).toContain('#0 Sponsor');
		expect(opts).toContain('#2 Layer #2');
	});

	it('keeps a missing layer visible instead of retargeting the event', async () => {
		const { container } = await openEditor(makeEvent({ layer: 5 }));
		expect(container.querySelector('.sec-layer')?.textContent).toBe('#5 (no such layer)');
		await fireEvent.click(container.querySelector('.sec-edit-btn') as HTMLElement);
		const select = Array.from(container.querySelectorAll('.sec-editor select')).find((s) =>
			Array.from((s as HTMLSelectElement).options).some((o) => o.textContent?.includes('no such layer'))) as HTMLSelectElement;
		expect(select.value).toBe('5');
	});

	it('retargets an event to the chosen layer', async () => {
		const { container, onUpdate } = await openEditor(makeEvent({ layer: 2 }));
		await fireEvent.click(container.querySelector('.sec-edit-btn') as HTMLElement);
		const select = Array.from(container.querySelectorAll('.sec-editor select')).find((s) =>
			Array.from((s as HTMLSelectElement).options).some((o) => o.textContent === '#0 Sponsor')) as HTMLSelectElement;
		await fireEvent.change(select, { target: { value: '0' } });
		expect(onUpdate).toHaveBeenCalledWith([expect.objectContaining({ layer: 0 })]);
	});
});

describe('SecondaryEventEditor GFX Update on an HTML5 layer', () => {
	const graphicsLayers = [
		{ id: 2, name: 'Lower third', active: false, zOrder: 0, x: 0, y: 0, width: 0, height: 0 },
		{ id: 6, active: true, zOrder: 1, x: 0, y: 0, width: 1920, height: 1080, html5Url: 'file:///ograf-host/index.html', html5Format: 'ograf' as const },
	];

	async function openEditor(evt: SecondaryEvent) {
		const onUpdate = vi.fn();
		const r = render(SecondaryEventEditor, { props: { events: [evt], onUpdate, graphicsLayers } });
		await fireEvent.click(r.container.querySelector('.sec-header') as HTMLElement);
		await fireEvent.click(r.container.querySelector('.sec-edit-btn') as HTMLElement);
		const field = Array.from(r.container.querySelectorAll('.sec-field')).find((f) =>
			f.querySelector('input[type="text"]')) as HTMLElement;
		return { ...r, onUpdate, field, input: field.querySelector('input') as HTMLInputElement };
	}

	// The engine sends the field to the graphic as its data, so the editor
	// has to say so; "Template" leaves an operator guessing what to type.
	it('labels the field as data JSON for an HTML5 layer', async () => {
		const { field } = await openEditor(makeEvent({ type: 'graphics_update', layer: 6 }));
		expect(field.querySelector('.sec-field-label')?.textContent).toBe('Data (JSON)');
	});

	it('keeps the template label for a template layer', async () => {
		const { field } = await openEditor(makeEvent({ type: 'graphics_update', layer: 2 }));
		expect(field.querySelector('.sec-field-label')?.textContent).toBe('Template');
	});

	it('passes the data JSON through unchanged', async () => {
		const { input, onUpdate } = await openEditor(makeEvent({ type: 'graphics_update', layer: 6 }));
		const data = '{"subtitle": "KAMERAKIND: NINA"}';
		await fireEvent.change(input, { target: { value: data } });
		expect(onUpdate).toHaveBeenCalledWith([expect.objectContaining({ layer: 6, template: data })]);
	});

	// The engine rejects data that is not JSON; the operator sees that here
	// rather than only in the engine log.
	it('marks data that is not JSON', async () => {
		const { input } = await openEditor(makeEvent({ type: 'graphics_update', layer: 6, template: 'subtitle: NINA' }));
		expect(input.getAttribute('aria-invalid')).toBe('true');
	});

	it('does not mark valid data JSON', async () => {
		const { input } = await openEditor(makeEvent({ type: 'graphics_update', layer: 6, template: '{"subtitle": "NINA"}' }));
		expect(input.getAttribute('aria-invalid')).not.toBe('true');
	});
});
