import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import SourceTile from './SourceTile.svelte';
import type { SourceInfo, TallyStatus } from '$lib/api/types';

function makeSource(overrides: Partial<SourceInfo> = {}): SourceInfo {
	return {
		key: 'cam1',
		label: 'Camera 1',
		status: 'healthy',
		lastFrameTime: Date.now(),
		...overrides,
	};
}

describe('SourceTile', () => {
	it('should render source label', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource({ label: 'Main Camera' }), tally: 'idle', index: 0 },
		});
		expect(container.textContent).toContain('Main Camera');
	});

	it('should render source key when label is empty', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource({ key: 'cam3', label: '' }), tally: 'idle', index: 0 },
		});
		expect(container.textContent).toContain('cam3');
	});

	it('should render source key when label is undefined', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource({ key: 'cam4', label: undefined }), tally: 'idle', index: 0 },
		});
		expect(container.textContent).toContain('cam4');
	});

	it('should show index+1 as tile number', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0 },
		});
		const tileNumber = container.querySelector('.tile-number');
		expect(tileNumber?.textContent).toBe('1');
	});

	it('should show index+1 for higher indices', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 4 },
		});
		const tileNumber = container.querySelector('.tile-number');
		expect(tileNumber?.textContent).toBe('5');
	});

	it('should apply program class when tally is program', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'program', index: 0 },
		});
		const button = container.querySelector('.source-tile');
		expect(button?.classList.contains('program')).toBe(true);
		expect(button?.classList.contains('preview')).toBe(false);
	});

	it('should apply preview class when tally is preview', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'preview', index: 0 },
		});
		const button = container.querySelector('.source-tile');
		expect(button?.classList.contains('preview')).toBe(true);
		expect(button?.classList.contains('program')).toBe(false);
	});

	it('should not apply tally class when tally is idle', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0 },
		});
		const button = container.querySelector('.source-tile');
		expect(button?.classList.contains('program')).toBe(false);
		expect(button?.classList.contains('preview')).toBe(false);
	});

	it('should fire onclick callback when clicked', async () => {
		const handleClick = vi.fn();
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0, onclick: handleClick },
		});
		const button = container.querySelector('.source-tile') as HTMLButtonElement;
		await fireEvent.click(button);
		expect(handleClick).toHaveBeenCalledTimes(1);
	});

	it('should show status text for offline source', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource({ status: 'offline' }), tally: 'idle', index: 0 },
		});
		const status = container.querySelector('.tile-status');
		expect(status?.textContent).toContain('offline');
	});

	it('should show status text for stale source', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource({ status: 'stale' }), tally: 'idle', index: 0 },
		});
		const status = container.querySelector('.tile-status');
		expect(status?.textContent).toContain('stale');
	});

	it('should show status text for no_signal source', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource({ status: 'no_signal' }), tally: 'idle', index: 0 },
		});
		const status = container.querySelector('.tile-status');
		expect(status?.textContent).toContain('no_signal');
	});

	it('should not show status text when source is healthy', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource({ status: 'healthy' }), tally: 'idle', index: 0 },
		});
		const status = container.querySelector('.tile-status');
		expect(status?.textContent?.trim()).toBe('');
	});

	it('should apply offline class on tile-status when status is offline', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource({ status: 'offline' }), tally: 'idle', index: 0 },
		});
		const status = container.querySelector('.tile-status');
		expect(status?.classList.contains('offline')).toBe(true);
	});

	it('should apply stale class on tile-status when status is stale', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource({ status: 'stale' }), tally: 'idle', index: 0 },
		});
		const status = container.querySelector('.tile-status');
		expect(status?.classList.contains('stale')).toBe(true);
	});
});
