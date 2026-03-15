import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import { tick } from 'svelte';
import ProgramBus from './ProgramBus.svelte';
import type { ControlRoomState } from '$lib/api/types';

vi.mock('$lib/api/switch-api', () => ({
	cut: vi.fn().mockResolvedValue({}),
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
			cam1: { key: 'cam1', label: 'Camera 1', type: 'demo' as const, status: 'healthy' },
			cam2: { key: 'cam2', label: 'Camera 2', type: 'demo' as const, status: 'healthy' },
			cam3: { key: 'cam3', label: 'Camera 3', type: 'demo' as const, status: 'healthy' },
		},
		seq: 1,
		timestamp: Date.now(),
		...overrides,
	};
}

describe('ProgramBus', () => {
	it('should render PGM bus label', () => {
		const { container } = render(ProgramBus, { props: { state: makeState() } });
		const label = container.querySelector('.bus-label');
		expect(label?.textContent).toBe('PGM');
	});

	it('should render one SourceTile per source', () => {
		const { container } = render(ProgramBus, { props: { state: makeState() } });
		const tiles = container.querySelectorAll('.source-tile');
		expect(tiles.length).toBe(3);
	});

	it('should highlight the program source with program tally', () => {
		const { container } = render(ProgramBus, { props: { state: makeState() } });
		const tiles = container.querySelectorAll('.source-tile');
		// Sources are sorted by key: cam1, cam2, cam3
		// cam1 is programSource so it should have program class
		const cam1Tile = tiles[0];
		expect(cam1Tile?.classList.contains('program')).toBe(true);
	});

	it('should give non-program sources idle tally (no program/preview class)', () => {
		const { container } = render(ProgramBus, { props: { state: makeState() } });
		const tiles = container.querySelectorAll('.source-tile');
		// cam2 (index 1) and cam3 (index 2) should NOT have program class
		expect(tiles[1]?.classList.contains('program')).toBe(false);
		expect(tiles[1]?.classList.contains('preview')).toBe(false);
		expect(tiles[2]?.classList.contains('program')).toBe(false);
		expect(tiles[2]?.classList.contains('preview')).toBe(false);
	});

	it('should call cut API on source click', async () => {
		const { cut } = await import('$lib/api/switch-api');
		const { container } = render(ProgramBus, { props: { state: makeState() } });
		const tiles = container.querySelectorAll('.source-tile');

		// Click cam3 (index 2 in sorted order)
		await fireEvent.click(tiles[2]);
		await tick();

		expect(cut).toHaveBeenCalledWith('cam3');
	});

	it('should render source labels in tiles', () => {
		const { container } = render(ProgramBus, { props: { state: makeState() } });
		expect(container.textContent).toContain('Camera 1');
		expect(container.textContent).toContain('Camera 2');
		expect(container.textContent).toContain('Camera 3');
	});

	it('should render tile numbers starting from 1', () => {
		const { container } = render(ProgramBus, { props: { state: makeState() } });
		const numbers = container.querySelectorAll('.tile-number');
		expect(numbers[0]?.textContent).toBe('1');
		expect(numbers[1]?.textContent).toBe('2');
		expect(numbers[2]?.textContent).toBe('3');
	});
});
