import { describe, it, expect } from 'vitest';
import { virtualSourceBadge } from './source-badge';

describe('virtualSourceBadge', () => {
	it('marks a clip player holding a clip it does not play as cued', () => {
		expect(virtualSourceBadge('clip:2', [{ id: 1, state: 'playing' }, { id: 2, state: 'loaded', clipName: 'Opener' }])).toBe('CUE');
	});

	it('marks a clip player playing, paused or holding as a clip', () => {
		for (const state of ['playing', 'paused', 'holding'] as const) {
			expect(virtualSourceBadge('clip:1', [{ id: 1, state }])).toBe('CLIP');
		}
	});

	it('keeps the replay badge for other virtual sources', () => {
		expect(virtualSourceBadge('replay', [{ id: 1, state: 'loaded' }])).toBe('RPL');
	});
});
