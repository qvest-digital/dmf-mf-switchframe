import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';
import { tick } from 'svelte';
import WipePatternBrowser from './WipePatternBrowser.svelte';

const previews: (() => void)[] = [];
vi.mock('$lib/api/switch-api', () => ({
	wipeListPatterns: vi.fn().mockResolvedValue({
		categories: [{
			category: 'basic',
			patterns: Array.from({ length: 8 }, (_, i) => ({ id: `p${i}`, name: `P${i}`, category: 'basic' })),
		}],
		total: 8,
	}),
	wipeGetPreview: vi.fn(() => new Promise<Blob>((resolve) => { previews.push(() => resolve(new Blob())); })),
	wipeUploadCustom: vi.fn(),
	wipeDeleteCustom: vi.fn(),
	apiCall: vi.fn(),
}));

describe('WipePatternBrowser', () => {
	// Every preview that arrives re-runs the effect that requests them; the
	// ones still on their way were requested again each time, 36 requests
	// for 8 patterns.
	it('requests each pattern preview once', async () => {
		URL.createObjectURL ??= () => 'blob:x';
		const api = await import('$lib/api/switch-api');
		const config = { pattern: 'p0', soft_edge: 0, border_width: 0, border_color_y: 0, border_color_cb: 128, border_color_cr: 128, reverse: false, offset_x: 0, offset_y: 0 } as never;
		render(WipePatternBrowser, { props: { config, onConfigChange: () => {} } });
		await vi.waitFor(() => expect(previews.length).toBeGreaterThan(0));
		while (previews.length) {
			previews.shift()!();
			await tick();
			await new Promise((r) => setTimeout(r, 0));
		}
		expect(vi.mocked(api.wipeGetPreview)).toHaveBeenCalledTimes(8);
	});
});
