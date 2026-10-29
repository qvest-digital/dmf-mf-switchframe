import type { PlayoutChannelState } from '$lib/api/types';

/** Two engines' playout channel as the Redundancy panel compares it. */
export interface PlayoutComparison {
	itemA: string;
	itemB: string;
	inSync: boolean;
	ptsFrameDelta?: number;
	cacheA?: string;
	cacheB?: string;
	/** Why an engine could not open the item it has on air. */
	errorA?: string;
	errorB?: string;
}

/** Compares one playout channel across the engines, or returns null while
 * neither has it or both have it stopped. One engine unable to open the item
 * the other plays is out of sync even on the same item. */
export function comparePlayout(
	chA: PlayoutChannelState | undefined,
	chB: PlayoutChannelState | undefined,
): PlayoutComparison | null {
	if (!chA || !chB) return null;
	if (chA.state === 'stopped' && chB.state === 'stopped') return null;
	return {
		itemA: chA.currentTitle ?? `Item ${chA.itemIndex}`,
		itemB: chB.currentTitle ?? `Item ${chB.itemIndex}`,
		inSync: chA.itemIndex === chB.itemIndex && chA.currentTitle === chB.currentTitle
			&& !!chA.itemError === !!chB.itemError,
		errorA: chA.itemError,
		errorB: chB.itemError,
	};
}
