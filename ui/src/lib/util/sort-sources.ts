import type { SourceInfo } from '$lib/api/types';

export function sortedSourceKeys(sources: Record<string, SourceInfo>): string[] {
	return Object.keys(sources).sort((a, b) => {
		const posA = sources[a].position ?? 0;
		const posB = sources[b].position ?? 0;
		if (posA !== posB) return posA - posB;
		return a.localeCompare(b);
	});
}
