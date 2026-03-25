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
