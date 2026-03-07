import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import { tick } from 'svelte';
import PreviewBus from './PreviewBus.svelte';
import type { ControlRoomState } from '$lib/api/types';

vi.mock('$lib/api/switch-api', () => ({
	setPreview: vi.fn().mockResolvedValue({}),
	apiCall: (p: Promise<unknown>) => p?.catch?.(() => {}),
}));

function makeState(overrides: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 0,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		audioChannels: undefined,
		masterLevel: 0,
		programPeak: [0, 0] as [number, number],
		tallyState: { cam1: 'program' as const, cam2: 'preview' as const, cam3: 'idle' as const },
		sources: {
			cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' },
			cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' },
			cam3: { key: 'cam3', label: 'Camera 3', status: 'healthy' },
		},
		seq: 1,
		timestamp: Date.now(),
		...overrides,
	};
}

describe('PreviewBus', () => {
	it('should render PVW bus label', () => {
		const { container } = render(PreviewBus, { props: { state: makeState() } });
		const label = container.querySelector('.bus-label');
		expect(label?.textContent).toBe('PVW');
	});

	it('should render one SourceTile per source', () => {
		const { container } = render(PreviewBus, { props: { state: makeState() } });
		const tiles = container.querySelectorAll('.source-tile');
		expect(tiles.length).toBe(3);
	});

	it('should highlight the preview source with preview tally', () => {
		const { container } = render(PreviewBus, { props: { state: makeState() } });
		const tiles = container.querySelectorAll('.source-tile');
		// Sources are sorted by key: cam1, cam2, cam3
		// cam2 is previewSource so it should have preview class
		const cam2Tile = tiles[1]; // sorted: cam1=0, cam2=1, cam3=2
		expect(cam2Tile?.classList.contains('preview')).toBe(true);
	});

	it('should give non-preview sources idle tally (no preview/program class)', () => {
		const { container } = render(PreviewBus, { props: { state: makeState() } });
		const tiles = container.querySelectorAll('.source-tile');
		// cam1 (index 0) and cam3 (index 2) should NOT have preview class
		expect(tiles[0]?.classList.contains('preview')).toBe(false);
		expect(tiles[0]?.classList.contains('program')).toBe(false);
		expect(tiles[2]?.classList.contains('preview')).toBe(false);
		expect(tiles[2]?.classList.contains('program')).toBe(false);
	});

	it('should call setPreview API on source click', async () => {
		const { setPreview } = await import('$lib/api/switch-api');
		const { container } = render(PreviewBus, { props: { state: makeState() } });
		const tiles = container.querySelectorAll('.source-tile');

		// Click cam3 (index 2 in sorted order)
		await fireEvent.click(tiles[2]);
		await tick();

		expect(setPreview).toHaveBeenCalledWith('cam3');
	});

	it('should render source labels in tiles', () => {
		const { container } = render(PreviewBus, { props: { state: makeState() } });
		expect(container.textContent).toContain('Camera 1');
		expect(container.textContent).toContain('Camera 2');
		expect(container.textContent).toContain('Camera 3');
	});

	it('should render tile numbers starting from 1', () => {
		const { container } = render(PreviewBus, { props: { state: makeState() } });
		const numbers = container.querySelectorAll('.tile-number');
		expect(numbers[0]?.textContent).toBe('1');
		expect(numbers[1]?.textContent).toBe('2');
		expect(numbers[2]?.textContent).toBe('3');
	});
});
