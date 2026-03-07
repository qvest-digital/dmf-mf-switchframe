import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SourceTile from './SourceTile.svelte';
import type { SourceInfo, TallyStatus } from '$lib/api/types';

function makeSource(overrides: Partial<SourceInfo> = {}): SourceInfo {
	return {
		key: 'cam1',
		label: 'Camera 1',
		status: 'healthy',
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

describe('SourceTile audio bar', () => {
	it('should not show audio bar when audioLevelDb is not provided (defaults to -96)', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0 },
		});
		const audioBar = container.querySelector('.audio-bar');
		expect(audioBar).toBeNull();
	});

	it('should not show audio bar when audioLevelDb is below -60', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0, audioLevelDb: -65 },
		});
		const audioBar = container.querySelector('.audio-bar');
		expect(audioBar).toBeNull();
	});

	it('should show audio bar when audioLevelDb is above -60', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0, audioLevelDb: -30 },
		});
		const audioBar = container.querySelector('.audio-bar');
		expect(audioBar).toBeTruthy();
	});

	it('should render audio bar fill with correct height for -30 dBFS', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0, audioLevelDb: -30 },
		});
		const fill = container.querySelector('.audio-bar-fill') as HTMLElement;
		expect(fill).toBeTruthy();
		// -30 dBFS: (-30 - (-60)) / (0 - (-60)) * 100 = 30/60*100 = 50%
		expect(fill.style.height).toBe('50%');
	});

	it('should render audio bar fill with 100% height at 0 dBFS', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0, audioLevelDb: 0 },
		});
		const fill = container.querySelector('.audio-bar-fill') as HTMLElement;
		expect(fill).toBeTruthy();
		expect(fill.style.height).toBe('100%');
	});

	it('should show green color for levels below -12 dBFS', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0, audioLevelDb: -20 },
		});
		const fill = container.querySelector('.audio-bar-fill') as HTMLElement;
		// Browser normalizes hex to rgb()
		expect(fill.style.background).toBe('rgb(34, 197, 94)');
	});

	it('should show yellow color for levels between -12 and -3 dBFS', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0, audioLevelDb: -6 },
		});
		const fill = container.querySelector('.audio-bar-fill') as HTMLElement;
		expect(fill.style.background).toBe('rgb(234, 179, 8)');
	});

	it('should show red color for levels above -3 dBFS', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0, audioLevelDb: -1 },
		});
		const fill = container.querySelector('.audio-bar-fill') as HTMLElement;
		expect(fill.style.background).toBe('rgb(239, 68, 68)');
	});

	it('should have aria-hidden on audio bar', () => {
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0, audioLevelDb: -20 },
		});
		const audioBar = container.querySelector('.audio-bar');
		expect(audioBar?.getAttribute('aria-hidden')).toBe('true');
	});
});

describe('SourceTile label editing', () => {
	it('enters edit mode on double-click', async () => {
		const onLabelChange = vi.fn();
		const { container } = render(SourceTile, {
			props: { source: makeSource(), tally: 'idle', index: 0, onLabelChange },
		});

		const label = container.querySelector('.tile-label') as HTMLElement;
		expect(label.tagName).toBe('SPAN');

		await fireEvent.dblClick(label);

		const input = container.querySelector('.tile-label-input') as HTMLInputElement;
		expect(input).toBeTruthy();
		expect(input.tagName).toBe('INPUT');
	});

	it('shows the current label value in the input', async () => {
		const onLabelChange = vi.fn();
		const { container } = render(SourceTile, {
			props: { source: makeSource({ label: 'My Source' }), tally: 'idle', index: 0, onLabelChange },
		});

		const label = container.querySelector('.tile-label') as HTMLElement;
		await fireEvent.dblClick(label);

		const input = container.querySelector('.tile-label-input') as HTMLInputElement;
		expect(input.value).toBe('My Source');
	});

	it('commits label on Enter', async () => {
		const onLabelChange = vi.fn();
		const { container } = render(SourceTile, {
			props: { source: makeSource({ key: 'cam1', label: 'Camera 1' }), tally: 'idle', index: 0, onLabelChange },
		});

		const label = container.querySelector('.tile-label') as HTMLElement;
		await fireEvent.dblClick(label);

		const input = container.querySelector('.tile-label-input') as HTMLInputElement;
		await fireEvent.input(input, { target: { value: 'Wide Shot' } });
		await fireEvent.keyDown(input, { key: 'Enter' });

		expect(onLabelChange).toHaveBeenCalledWith('cam1', 'Wide Shot');
		// Should exit edit mode
		expect(container.querySelector('.tile-label-input')).toBeNull();
	});

	it('commits label on blur', async () => {
		const onLabelChange = vi.fn();
		const { container } = render(SourceTile, {
			props: { source: makeSource({ key: 'cam1', label: 'Camera 1' }), tally: 'idle', index: 0, onLabelChange },
		});

		const label = container.querySelector('.tile-label') as HTMLElement;
		await fireEvent.dblClick(label);

		const input = container.querySelector('.tile-label-input') as HTMLInputElement;
		await fireEvent.input(input, { target: { value: 'Close Up' } });
		await fireEvent.blur(input);

		expect(onLabelChange).toHaveBeenCalledWith('cam1', 'Close Up');
		expect(container.querySelector('.tile-label-input')).toBeNull();
	});

	it('cancels editing on Escape without calling onLabelChange', async () => {
		const onLabelChange = vi.fn();
		const { container } = render(SourceTile, {
			props: { source: makeSource({ key: 'cam1', label: 'Camera 1' }), tally: 'idle', index: 0, onLabelChange },
		});

		const label = container.querySelector('.tile-label') as HTMLElement;
		await fireEvent.dblClick(label);

		const input = container.querySelector('.tile-label-input') as HTMLInputElement;
		await fireEvent.input(input, { target: { value: 'Changed' } });
		await fireEvent.keyDown(input, { key: 'Escape' });

		expect(onLabelChange).not.toHaveBeenCalled();
		// Should exit edit mode and show original label
		expect(container.querySelector('.tile-label-input')).toBeNull();
		expect(container.querySelector('.tile-label')?.textContent).toBe('Camera 1');
	});
});
