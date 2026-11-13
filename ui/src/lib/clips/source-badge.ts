import type { ClipPlayerState } from '$lib/api/types';

/**
 * The badge of a virtual source. A clip player's source is CUE while its
 * slot holds a clip it does not play: a cut or a transition to it starts
 * the clip. It is CLIP otherwise, and the replay player's source is RPL.
 */
export function virtualSourceBadge(key: string, clipPlayers?: ClipPlayerState[]): string {
	const m = /^clip:(\d+)$/.exec(key);
	if (!m) return 'RPL';
	const player = clipPlayers?.find((p) => p.id === Number(m[1]));
	return player?.state === 'loaded' ? 'CUE' : 'CLIP';
}
