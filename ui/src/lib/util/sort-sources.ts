import type { SourceInfo } from '$lib/api/types';

/**
 * The keys of the sources an operator works with, in position order. A
 * source connected to nothing is left out: it has nothing to show until a
 * controller connects it.
 */
export function sortedSourceKeys(sources: Record<string, SourceInfo>): string[] {
	return Object.keys(sources)
		.filter((k) => !sources[k].unconnected)
		.sort((a, b) => {
			const posA = sources[a].position ?? 0;
			const posB = sources[b].position ?? 0;
			if (posA !== posB) return posA - posB;
			return a.localeCompare(b);
		});
}
