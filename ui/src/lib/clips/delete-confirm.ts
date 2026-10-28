import type { ClipInfo } from '$lib/api/types';

/** The confirmation a delete needs: a clip in the shared clip library is
 * removed from the library itself, so no later booking of the production
 * finds it. A clip stored only in this switcher needs none. */
export function deleteConfirmation(clip: Pick<ClipInfo, 'name' | 'libraryPath'>): { title: string; message: string } | null {
	if (!clip.libraryPath) return null;
	return {
		title: 'Delete from shared clip library',
		message: `"${clip.name}" is deleted from the shared clip library, for every future booking that uses it, not only this one. This cannot be undone.`,
	};
}
