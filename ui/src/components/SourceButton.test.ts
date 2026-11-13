import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import SourceButton from './SourceButton.svelte';
import { reloadHolds } from '$lib/util/activity';
import type { SourceInfo } from '$lib/api/types';

const source: SourceInfo = { key: 'cam1', label: 'Camera 1', type: 'demo', status: 'healthy' } as SourceInfo;

describe('SourceButton', () => {
	// A stale-build reload of an untouched tab would drop the label being typed.
	it('holds the reload while the label is being edited', async () => {
		const { container } = render(SourceButton, {
			props: { source, tally: 'idle', index: 0, onLabelChange: vi.fn() },
		});
		expect(reloadHolds()).not.toContain('source rename');
		await fireEvent.dblClick(container.querySelector('.label-text') as HTMLElement);
		expect(reloadHolds()).toContain('source rename');
		await fireEvent.keyDown(container.querySelector('.label-input') as HTMLInputElement, { key: 'Escape' });
		expect(reloadHolds()).not.toContain('source rename');
	});

	// A loaded clip player is a source to cut to; the badge says a cut
	// starts it.
	it('marks a cued clip player source', () => {
		const clipSource = { key: 'clip:1', label: 'CLIP:1', type: 'clip', status: 'healthy', isVirtual: true } as SourceInfo;
		const { container } = render(SourceButton, {
			props: { source: clipSource, clipPlayers: [{ id: 1, state: 'loaded', clipName: 'Opener' }], tally: 'idle', index: 0, onCut: vi.fn() },
		});
		expect(container.querySelector('.virtual-badge')?.textContent).toBe('CUE');
	});
});
