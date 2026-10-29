import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, createEvent } from '@testing-library/svelte';
import { tick } from 'svelte';
import GraphicsPanel from './GraphicsPanel.svelte';
import * as api from '$lib/api/switch-api';
import { notify } from '$lib/state/notifications.svelte';
import { reloadHolds } from '$lib/util/activity';

vi.mock('$lib/state/notifications.svelte', () => ({ notify: vi.fn() }));

vi.mock('$lib/api/switch-api', async (importOriginal) => ({
	...(await importOriginal<typeof import('$lib/api/switch-api')>()),
	graphicsSetLayerSettings: vi.fn().mockResolvedValue({}),
	graphicsSetLayerOrder: vi.fn().mockResolvedValue({}),
	graphicsImportLayers: vi.fn().mockResolvedValue({}),
}));

const ctx2d = new Proxy({}, {
	get: (_t, prop) => (prop === 'measureText' ? () => ({ width: 50 }) : prop === 'getImageData' ? () => ({ data: new Uint8ClampedArray(4) }) : () => {}),
	set: () => true,
});
// @ts-expect-error - jsdom has no OffscreenCanvas
globalThis.OffscreenCanvas = class { constructor(public width: number, public height: number) {} getContext() { return ctx2d; } };
HTMLCanvasElement.prototype.getContext = (() => ctx2d) as never;

const baseState = {
	programSource: '', previewSource: '', transitionType: 'cut', transitionDurationMs: 0,
	transitionPosition: 0, inTransition: false, ftbActive: false, masterLevel: 0,
	programPeak: [0, 0] as [number, number], tallyState: {}, sources: {}, seq: 1, lastCommandSeq: 0, timestamp: 1,
};

function layer(id: number, zOrder: number, extra: Record<string, unknown> = {}) {
	return { id, zOrder, active: false, template: 'lower-third', x: 0, y: 0, width: 1920, height: 1080, ...extra };
}

function stateWith(...layers: ReturnType<typeof layer>[]) {
	return { ...baseState, graphics: { layers } };
}

describe('GraphicsPanel settings on the engine', () => {
	beforeEach(() => {
		vi.clearAllMocks();
		vi.useFakeTimers();
	});
	afterEach(() => vi.useRealTimers());

	it('saves an entered value to the engine', async () => {
		const { container } = render(GraphicsPanel, { props: { state: stateWith(layer(0, 0)) } });
		const input = container.querySelector('.field-inp') as HTMLInputElement;
		await fireEvent.input(input, { target: { value: 'Ada Lovelace' } });
		expect(api.graphicsSetLayerSettings).not.toHaveBeenCalled();
		vi.advanceTimersByTime(350);
		expect(api.graphicsSetLayerSettings).toHaveBeenCalledTimes(1);
		const [id, settings] = vi.mocked(api.graphicsSetLayerSettings).mock.calls[0];
		expect(id).toBe(0);
		expect((settings.config as { fields: Record<string, string> }).fields.name).toBe('Ada Lovelace');
	});

	it('saves a value still waiting when the panel closes, as on a tab switch', async () => {
		const { container, unmount } = render(GraphicsPanel, { props: { state: stateWith(layer(0, 0)) } });
		await fireEvent.input(container.querySelector('.field-inp') as HTMLInputElement, { target: { value: 'Grace' } });
		unmount();
		expect(api.graphicsSetLayerSettings).toHaveBeenCalledTimes(1);
	});

	it('flags a save the engine refused, keeps the value and retries', async () => {
		vi.mocked(api.graphicsSetLayerSettings)
			.mockRejectedValueOnce(new Error('engine unreachable'))
			.mockResolvedValueOnce({});
		const { container } = render(GraphicsPanel, { props: { state: stateWith(layer(0, 0)) } });
		const input = container.querySelector('.field-inp') as HTMLInputElement;
		await fireEvent.input(input, { target: { value: 'Ada' } });
		await vi.advanceTimersByTimeAsync(350);
		expect(notify).toHaveBeenCalledWith('error', expect.stringContaining('engine unreachable'));
		expect(container.querySelector('.gfx-unsaved')?.textContent).toBe('NOT SAVED');
		expect((container.querySelector('.field-inp') as HTMLInputElement).value).toBe('Ada');

		await vi.advanceTimersByTimeAsync(3100);
		expect(api.graphicsSetLayerSettings).toHaveBeenCalledTimes(2);
		expect(vi.mocked(api.graphicsSetLayerSettings).mock.calls[1][1].config).toMatchObject({ fields: { name: 'Ada' } });
		expect(container.querySelector('.gfx-unsaved')).toBeNull();
	});

	// A stale-build reload of an unattended tab would drop the edit the
	// panel is still retrying.
	it('holds a reload while a refused save is being retried', async () => {
		vi.mocked(api.graphicsSetLayerSettings)
			.mockRejectedValueOnce(new Error('engine unreachable'))
			.mockResolvedValueOnce({});
		const { container, unmount } = render(GraphicsPanel, { props: { state: stateWith(layer(0, 0)) } });
		await fireEvent.input(container.querySelector('.field-inp') as HTMLInputElement, { target: { value: 'Ada' } });
		expect(reloadHolds()).not.toContain('unsaved graphics settings');
		await vi.advanceTimersByTimeAsync(350);
		expect(reloadHolds()).toContain('unsaved graphics settings');
		await vi.advanceTimersByTimeAsync(3100);
		expect(reloadHolds()).not.toContain('unsaved graphics settings');
		unmount();
	});

	it('shows a refusal by the engine instead of retrying it', async () => {
		vi.mocked(api.graphicsSetLayerSettings).mockRejectedValueOnce(new api.SwitchApiError(400, 'layer config too large'));
		const { container } = render(GraphicsPanel, { props: { state: stateWith(layer(0, 0)) } });
		const input = container.querySelector('.field-inp') as HTMLInputElement;
		const engineValue = input.value;
		await fireEvent.input(input, { target: { value: 'Ada' } });
		await vi.advanceTimersByTimeAsync(350);
		expect(notify).toHaveBeenCalledWith('error', expect.stringContaining('layer config too large'));
		expect(container.querySelector('.gfx-unsaved')).toBeNull();
		expect((container.querySelector('.field-inp') as HTMLInputElement).value).toBe(engineValue);
		await vi.advanceTimersByTimeAsync(10000);
		expect(api.graphicsSetLayerSettings).toHaveBeenCalledTimes(1);
	});

	it('clears NOT SAVED and stops retrying once another session removes the layer', async () => {
		vi.mocked(api.graphicsSetLayerSettings).mockRejectedValue(new Error('engine unreachable'));
		const { container, rerender } = render(GraphicsPanel, { props: { state: stateWith(layer(0, 0), layer(1, 1)) } });
		await fireEvent.click(container.querySelector('[data-layer-id="0"]') as HTMLElement);
		await fireEvent.input(container.querySelector('.field-inp') as HTMLInputElement, { target: { value: 'Ada' } });
		await vi.advanceTimersByTimeAsync(350);
		expect(container.querySelector('.gfx-unsaved')).toBeTruthy();

		await rerender({ state: { ...stateWith(layer(1, 1)), timestamp: 2 } });
		await tick();
		expect(container.querySelector('.gfx-unsaved')).toBeNull();
		const calls = vi.mocked(api.graphicsSetLayerSettings).mock.calls.length;
		await vi.advanceTimersByTimeAsync(10000);
		expect(api.graphicsSetLayerSettings).toHaveBeenCalledTimes(calls);
		vi.mocked(api.graphicsSetLayerSettings).mockReset();
		vi.mocked(api.graphicsSetLayerSettings).mockResolvedValue({});
	});

	it('clears NOT SAVED when an import replaces the layers', async () => {
		vi.mocked(api.graphicsSetLayerSettings).mockRejectedValue(new Error('engine unreachable'));
		const { container } = render(GraphicsPanel, { props: { state: stateWith(layer(0, 0)) } });
		await fireEvent.input(container.querySelector('.field-inp') as HTMLInputElement, { target: { value: 'Ada' } });
		await vi.advanceTimersByTimeAsync(350);
		expect(container.querySelector('.gfx-unsaved')).toBeTruthy();

		// The import opens a file picker; hand it an export file.
		const create = document.createElement.bind(document);
		let picker: HTMLInputElement | undefined;
		const spy = vi.spyOn(document, 'createElement').mockImplementation((tag: string, opts?: ElementCreationOptions) => {
			const el = create(tag, opts);
			if (tag === 'input') { picker = el as HTMLInputElement; picker.click = () => {}; }
			return el;
		});
		const importBtn = Array.from(container.querySelectorAll('.gfx-io')).find((b) => b.textContent === 'IMPORT') as HTMLButtonElement;
		await fireEvent.click(importBtn);
		spy.mockRestore();
		const file = { text: async () => '{"format":"switchframe-graphics","version":1,"layers":[{"id":0}]}' };
		Object.defineProperty(picker!, 'files', { value: [file] });
		await picker!.onchange!(new Event('change'));
		await tick();
		expect(api.graphicsImportLayers).toHaveBeenCalled();
		expect(container.querySelector('.gfx-unsaved')).toBeNull();
		const calls = vi.mocked(api.graphicsSetLayerSettings).mock.calls.length;
		await vi.advanceTimersByTimeAsync(10000);
		expect(api.graphicsSetLayerSettings).toHaveBeenCalledTimes(calls);
		vi.mocked(api.graphicsSetLayerSettings).mockReset();
		vi.mocked(api.graphicsSetLayerSettings).mockResolvedValue({});
	});

	it('shows the values the engine holds when it opens', () => {
		const state = stateWith(layer(0, 0, {
			config: { template: 'lower-third', fields: { name: 'Ada Lovelace', title: 'Analyst' } },
		}));
		const { container } = render(GraphicsPanel, { props: { state } });
		const values = Array.from(container.querySelectorAll('.field-inp')).map((el) => (el as HTMLInputElement).value);
		expect(values).toContain('Ada Lovelace');
		expect(values).toContain('Analyst');
	});
});

describe('GraphicsPanel reorder', () => {
	beforeEach(() => vi.clearAllMocks());

	// Rows are 20px high; the upper half drops before a row, the lower half after it.
	function dropOn(container: HTMLElement, sourceId: number, targetId: number, half: 'upper' | 'lower') {
		const source = container.querySelector(`[data-layer-id="${sourceId}"]`) as HTMLElement;
		const target = container.querySelector(`[data-layer-id="${targetId}"]`) as HTMLElement;
		target.getBoundingClientRect = () => ({ top: 100, height: 20, bottom: 120, left: 0, right: 100, width: 100, x: 0, y: 100, toJSON: () => ({}) });
		const clientY = half === 'upper' ? 105 : 115;
		// jsdom has no DragEvent, so the pointer position is set by hand.
		const at = (ev: Event) => { Object.defineProperty(ev, 'clientY', { value: clientY }); return ev; };
		fireEvent.dragStart(source);
		fireEvent(target, at(createEvent.dragOver(target)));
		fireEvent(target, at(createEvent.drop(target)));
	}

	// Stack bottom to top: #0, #1, #2. The rail lists #2, #1, #0.
	const state = stateWith(layer(0, 0), layer(1, 1), layer(2, 2));

	it('moves the bottom layer to the top, which tied z-orders made impossible', async () => {
		const { container } = render(GraphicsPanel, { props: { state } });
		dropOn(container, 0, 2, 'upper');
		await tick();
		expect(api.graphicsSetLayerOrder).toHaveBeenCalledWith([1, 2, 0]);
	});

	it('moves the top layer to the bottom', async () => {
		const { container } = render(GraphicsPanel, { props: { state } });
		dropOn(container, 2, 0, 'lower');
		await tick();
		expect(api.graphicsSetLayerOrder).toHaveBeenCalledWith([2, 0, 1]);
	});

	it('moves a layer just below or just above another', async () => {
		const { container } = render(GraphicsPanel, { props: { state } });
		dropOn(container, 2, 1, 'lower');
		await tick();
		expect(api.graphicsSetLayerOrder).toHaveBeenLastCalledWith([0, 2, 1]);
		dropOn(container, 0, 1, 'upper');
		await tick();
		expect(api.graphicsSetLayerOrder).toHaveBeenLastCalledWith([1, 0, 2]);
	});

	it('sends nothing for a drop that changes nothing', async () => {
		const { container } = render(GraphicsPanel, { props: { state } });
		dropOn(container, 1, 2, 'lower');
		await tick();
		expect(api.graphicsSetLayerOrder).not.toHaveBeenCalled();
	});

	it('holds a reload while a layer name is being typed', async () => {
		const { container, unmount } = render(GraphicsPanel, { props: { state } });
		await fireEvent.dblClick(container.querySelector('[data-layer-id="1"]') as HTMLElement);
		expect(reloadHolds()).toContain('layer rename');
		await fireEvent.keyDown(container.querySelector('.rename-input') as HTMLInputElement, { key: 'Enter' });
		expect(reloadHolds()).not.toContain('layer rename');
		unmount();
	});

	it('renames a layer without touching its id', async () => {
		const { container } = render(GraphicsPanel, { props: { state } });
		await fireEvent.dblClick(container.querySelector('[data-layer-id="1"]') as HTMLElement);
		const input = container.querySelector('.rename-input') as HTMLInputElement;
		await fireEvent.input(input, { target: { value: 'Score bug' } });
		await fireEvent.keyDown(input, { key: 'Enter' });
		expect(api.graphicsSetLayerSettings).toHaveBeenCalledWith(1, { name: 'Score bug' });
	});
});
