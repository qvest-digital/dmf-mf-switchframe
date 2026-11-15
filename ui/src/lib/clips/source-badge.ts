import type { ClipPlayerState, PlayoutChannelState } from '$lib/api/types';

/**
 * The badge of a virtual source. A clip player's source is CUE while its
 * slot holds a clip it does not play, and a playout channel's while the
 * channel is cued: a cut or a transition to it starts the clip or the
 * channel. A clip player's is CLIP otherwise, and the replay player's
 * source is RPL.
 */
export function virtualSourceBadge(key: string, clipPlayers?: ClipPlayerState[], playoutChannels?: PlayoutChannelState[]): string {
	const p = /^playout:(\d+)$/.exec(key);
	if (p && playoutChannels?.find((c) => c.id === Number(p[1]))?.state === 'cued') return 'CUE';
	const m = /^clip:(\d+)$/.exec(key);
	if (!m) return 'RPL';
	const player = clipPlayers?.find((p) => p.id === Number(m[1]));
	return player?.state === 'loaded' ? 'CUE' : 'CLIP';
}
