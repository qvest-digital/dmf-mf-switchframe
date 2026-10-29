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

export interface SourceOption {
	key: string;
	label: string;
}

/**
 * The sources a live playlist item can name, in switcher order and under
 * the label the switcher shows: the connected sender's name for an NMOS
 * input, else the source's own name. The item stores the key, so it cuts
 * to that input whatever is connected to it at air time. A key the item
 * already names stays offered when it is not listed, so the editor never
 * shows another source in its place.
 */
export function liveSourceOptions(sources: Record<string, SourceInfo>, current?: string): SourceOption[] {
	const keys = sortedSourceKeys(sources);
	if (current && !keys.includes(current)) keys.push(current);
	return keys.map((key) => ({ key, label: sources[key]?.label || key }));
}
