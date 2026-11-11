import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import GraphicsLiveKey from './GraphicsLiveKey.svelte';
import type { GraphicsLayerState, SourceInfo } from '$lib/api/types';

vi.mock('$lib/api/switch-api', () => ({
	graphicsSetLive: vi.fn().mockResolvedValue({}),
	graphicsClearLive: vi.fn().mockResolvedValue({}),
	apiCall: vi.fn((p: Promise<unknown>) => p),
}));

const sources = {
	'gfx-fill': { key: 'gfx-fill', label: 'GFX FILL', status: 'healthy' },
	'gfx-key': { key: 'gfx-key', label: 'GFX KEY', status: 'healthy' },
} as unknown as Record<string, SourceInfo>;

function layer(extra: Partial<GraphicsLayerState> = {}): GraphicsLayerState {
	return { id: 2, active: false, zOrder: 0, x: 0, y: 0, width: 0, height: 0, ...extra };
}

describe('GraphicsLiveKey', () => {
	beforeEach(() => vi.clearAllMocks());

	it('sets the chosen fill and key, shaped or not', async () => {
		const api = await import('$lib/api/switch-api');
		const { container } = render(GraphicsLiveKey, { props: { layer: layer(), sources } });
		const set = container.querySelector('.live-set') as HTMLButtonElement;
		expect(set.disabled).toBe(true);

		await fireEvent.change(container.querySelector('.live-fill')!, { target: { value: 'gfx-fill' } });
		await fireEvent.change(container.querySelector('.live-key-src')!, { target: { value: 'gfx-fill' } });
		expect(set.disabled, 'fill and key must differ').toBe(true);

		await fireEvent.change(container.querySelector('.live-key-src')!, { target: { value: 'gfx-key' } });
		await fireEvent.click(container.querySelector('.live-premultiplied')!);
		await fireEvent.click(set);
		expect(api.graphicsSetLive).toHaveBeenCalledWith(2, { fill: 'gfx-fill', key: 'gfx-key', premultiplied: true });
	});

	it('shows and clears the live setup of the layer', async () => {
		const api = await import('$lib/api/switch-api');
		const { container } = render(GraphicsLiveKey, {
			props: { layer: layer({ liveFill: 'gfx-fill', liveKey: 'gfx-key' }), sources },
		});
		expect((container.querySelector('.live-fill') as HTMLSelectElement).value).toBe('gfx-fill');
		expect((container.querySelector('.live-key-src') as HTMLSelectElement).value).toBe('gfx-key');
		await fireEvent.click(container.querySelector('.live-clear')!);
		expect(api.graphicsClearLive).toHaveBeenCalledWith(2);
	});

	it('keeps an edit across state broadcasts of the same setup', async () => {
		const { container, rerender } = render(GraphicsLiveKey, { props: { layer: layer(), sources } });
		await fireEvent.change(container.querySelector('.live-fill')!, { target: { value: 'gfx-fill' } });
		await rerender({ layer: layer({ fadePosition: 0.5 }), sources });
		expect((container.querySelector('.live-fill') as HTMLSelectElement).value).toBe('gfx-fill');
	});
});
