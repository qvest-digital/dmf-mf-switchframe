/**
 * The reconciler compares clip players as the engines broadcast them. A field
 * the UI type declares but the server never sends is always undefined, and
 * logic built on it is dead without any test noticing: the loading state was
 * missing from the broadcast this way. The fixture is the server's broadcast
 * of a clip player with every field set, checked against the server by
 * TestClipPlayerBroadcastMatchesUIFixture.
 */
import { describe, it, expect } from 'vitest';
import type { ClipPlayerState } from './types';
import broadcast from './testdata/clip-player-broadcast.json';

// Required<> makes the type checker reject this literal when ClipPlayerState
// gains or loses a field, so the key list below follows the type.
const everyField: Required<ClipPlayerState> = {
	id: 0,
	clipId: '',
	clipName: '',
	state: 'empty',
	speed: 0,
	position: 0,
	loop: false,
	loadingClipId: '',
	loadingClipName: '',
};

describe('clip player broadcast contract', () => {
	it('carries exactly the fields of the UI ClipPlayerState type', () => {
		expect(Object.keys(broadcast).sort()).toEqual(Object.keys(everyField).sort());
	});

	it('carries each field with the type the UI expects', () => {
		for (const [key, value] of Object.entries(everyField)) {
			expect(typeof (broadcast as Record<string, unknown>)[key], key).toBe(typeof value);
		}
	});
});
