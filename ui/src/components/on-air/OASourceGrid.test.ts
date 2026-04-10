import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OASourceGrid from './OASourceGrid.svelte';
import type { SourceInfo, TallyStatus } from '$lib/api/types';

function makeSources(count: number): Record<string, SourceInfo> {
	const sources: Record<string, SourceInfo> = {};
	for (let i = 1; i <= count; i++) {
		sources[`cam${i}`] = {
			key: `cam${i}`,
			label: `Camera ${i}`,
			type: 'demo',
			status: 'healthy',
			position: i,
		};
	}
	return sources;
}

function makeTally(
	sources: Record<string, SourceInfo>,
	programKey?: string,
	previewKey?: string,
): Record<string, TallyStatus> {
	const tally: Record<string, TallyStatus> = {};
	for (const key of Object.keys(sources)) {
		if (key === programKey) tally[key] = 'program';
		else if (key === previewKey) tally[key] = 'preview';
		else tally[key] = 'idle';
	}
	return tally;
}

function defaultProps(overrides: Record<string, unknown> = {}) {
	const sources = makeSources(4);
	return {
		sources,
		tallyState: makeTally(sources, 'cam1', 'cam2'),
		onSourceClick: vi.fn(),
		...overrides,
	};
}

describe('OASourceGrid', () => {
	it('renders SOURCES header', () => {
		render(OASourceGrid, { props: defaultProps() });
		expect(screen.getByText('SOURCES')).toBeTruthy();
	});

	it('renders correct number of source tiles', () => {
		const { container } = render(OASourceGrid, { props: defaultProps() });
		const tiles = container.querySelectorAll('.oa-source-tile');
		expect(tiles.length).toBe(4);
	});

	it('renders source labels', () => {
		render(OASourceGrid, { props: defaultProps() });
		expect(screen.getByText('Camera 1')).toBeTruthy();
		expect(screen.getByText('Camera 2')).toBeTruthy();
		expect(screen.getByText('Camera 3')).toBeTruthy();
		expect(screen.getByText('Camera 4')).toBeTruthy();
	});

	it('shows tally border on program source', () => {
		const { container } = render(OASourceGrid, { props: defaultProps() });
		const programTile = container.querySelector('[data-source-key="cam1"]');
		expect(programTile).toBeTruthy();
		expect(programTile!.classList.contains('program')).toBe(true);
	});

	it('shows tally border on preview source', () => {
		const { container } = render(OASourceGrid, { props: defaultProps() });
		const previewTile = container.querySelector('[data-source-key="cam2"]');
		expect(previewTile).toBeTruthy();
		expect(previewTile!.classList.contains('preview')).toBe(true);
	});

	it('idle source has no tally class', () => {
		const { container } = render(OASourceGrid, { props: defaultProps() });
		const idleTile = container.querySelector('[data-source-key="cam3"]');
		expect(idleTile).toBeTruthy();
		expect(idleTile!.classList.contains('program')).toBe(false);
		expect(idleTile!.classList.contains('preview')).toBe(false);
	});

	it('shows PGM badge on program source', () => {
		render(OASourceGrid, { props: defaultProps() });
		const badges = screen.getAllByText('PGM');
		expect(badges.length).toBe(1);
	});

	it('shows PVW badge on preview source', () => {
		render(OASourceGrid, { props: defaultProps() });
		const badges = screen.getAllByText('PVW');
		expect(badges.length).toBe(1);
	});

	it('shows health indicators', () => {
		const sources = makeSources(2);
		sources['cam2'].status = 'offline';
		render(OASourceGrid, {
			props: defaultProps({ sources, tallyState: makeTally(sources) }),
		});
		const healthTexts = screen.getAllByText('healthy');
		expect(healthTexts.length).toBe(1);
		expect(screen.getByText('offline')).toBeTruthy();
	});

	it('shows stale health with warning style', () => {
		const sources = makeSources(1);
		sources['cam1'].status = 'stale';
		const { container } = render(OASourceGrid, {
			props: defaultProps({ sources, tallyState: makeTally(sources) }),
		});
		expect(screen.getByText('stale')).toBeTruthy();
		const healthEl = container.querySelector('.warning');
		expect(healthEl).toBeTruthy();
	});

	it('click callback fires with source key', async () => {
		const clickFn = vi.fn();
		const { container } = render(OASourceGrid, {
			props: defaultProps({ onSourceClick: clickFn }),
		});
		const tile = container.querySelector('[data-source-key="cam3"]');
		expect(tile).toBeTruthy();
		await fireEvent.click(tile!);
		expect(clickFn).toHaveBeenCalledOnce();
		expect(clickFn).toHaveBeenCalledWith('cam3');
	});

	it('renders Add SRT button when callback provided', () => {
		render(OASourceGrid, {
			props: defaultProps({ onAddSRT: vi.fn() }),
		});
		expect(screen.getByText('+ Add SRT')).toBeTruthy();
	});

	it('does not render Add SRT button when no callback', () => {
		render(OASourceGrid, { props: defaultProps() });
		expect(screen.queryByText('+ Add SRT')).toBeFalsy();
	});

	it('renders I/O button when callback provided', () => {
		render(OASourceGrid, {
			props: defaultProps({ onOpenIO: vi.fn() }),
		});
		expect(screen.getByText('I/O')).toBeTruthy();
	});

	it('Add SRT button fires callback', async () => {
		const addFn = vi.fn();
		render(OASourceGrid, {
			props: defaultProps({ onAddSRT: addFn }),
		});
		await fireEvent.click(screen.getByText('+ Add SRT'));
		expect(addFn).toHaveBeenCalledOnce();
	});

	it('I/O button fires callback', async () => {
		const ioFn = vi.fn();
		render(OASourceGrid, {
			props: defaultProps({ onOpenIO: ioFn }),
		});
		await fireEvent.click(screen.getByText('I/O'));
		expect(ioFn).toHaveBeenCalledOnce();
	});

	it('falls back to source key when label is missing', () => {
		const sources: Record<string, SourceInfo> = {
			srt_1: {
				key: 'srt_1',
				type: 'srt',
				status: 'healthy',
			},
		};
		render(OASourceGrid, {
			props: defaultProps({ sources, tallyState: makeTally(sources) }),
		});
		expect(screen.getByText('srt_1')).toBeTruthy();
	});

	it('sources are sorted by position', () => {
		const sources: Record<string, SourceInfo> = {
			cam_b: { key: 'cam_b', label: 'B Cam', type: 'demo', status: 'healthy', position: 2 },
			cam_a: { key: 'cam_a', label: 'A Cam', type: 'demo', status: 'healthy', position: 1 },
			cam_c: { key: 'cam_c', label: 'C Cam', type: 'demo', status: 'healthy', position: 3 },
		};
		const { container } = render(OASourceGrid, {
			props: defaultProps({ sources, tallyState: makeTally(sources) }),
		});
		const tiles = container.querySelectorAll('.oa-source-tile');
		expect(tiles[0].getAttribute('data-source-key')).toBe('cam_a');
		expect(tiles[1].getAttribute('data-source-key')).toBe('cam_b');
		expect(tiles[2].getAttribute('data-source-key')).toBe('cam_c');
	});

	it('renders NO SIGNAL placeholder in thumbnail', () => {
		render(OASourceGrid, { props: defaultProps() });
		const noSignals = screen.getAllByText('NO SIGNAL');
		expect(noSignals.length).toBe(4);
	});
});
