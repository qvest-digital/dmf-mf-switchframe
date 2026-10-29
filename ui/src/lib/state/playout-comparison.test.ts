import { describe, it, expect } from 'vitest';
import { comparePlayout } from './playout-comparison';
import type { PlayoutChannelState } from '$lib/api/types';

const ch = (over: Partial<PlayoutChannelState> = {}): PlayoutChannelState => ({
	id: 0, state: 'on_air', currentTitle: 'Replay', itemIndex: 0, itemCount: 1, ...over,
});

describe('comparePlayout', () => {
	// Same item and title on both engines, but one cannot open it: its
	// output plays nothing while the other plays the item.
	it('is out of sync when one engine cannot open the item on air', () => {
		const cmp = comparePlayout(ch({ itemRemainMs: 3300 }), ch({ itemError: 'asset open failed: playout: asset not yet cached' }));
		expect(cmp).toMatchObject({ inSync: false, errorA: undefined, errorB: 'asset open failed: playout: asset not yet cached' });
	});

	it('is in sync on the same item with no open error', () => {
		expect(comparePlayout(ch(), ch())?.inSync).toBe(true);
	});

	it('is out of sync on different items', () => {
		expect(comparePlayout(ch(), ch({ itemIndex: 1, currentTitle: 'Bumper' }))?.inSync).toBe(false);
	});

	it('is null while both channels are stopped or one is missing', () => {
		expect(comparePlayout(ch({ state: 'stopped' }), ch({ state: 'stopped' }))).toBeNull();
		expect(comparePlayout(ch(), undefined)).toBeNull();
	});
});
