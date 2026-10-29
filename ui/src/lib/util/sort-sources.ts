import type { SourceInfo } from '$lib/api/types';

/**
 * The keys of the sources an operator works with, in position order. A
 * source connected to nothing is left out: it has nothing to show until a
 * controller connects it.
 */
export function sortedSourceKeys(sources: Record<string, SourceInfo>): string[] {
	return byPosition(sources, Object.keys(sources).filter((k) => !sources[k].unconnected));
}

function byPosition(sources: Record<string, SourceInfo>, keys: string[]): string[] {
	return keys.sort((a, b) => {
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
 * to that input whatever is connected to it at air time. That is also why
 * inputs connected to nothing are offered, after the others and marked: a
 * controller may connect one before the item airs. A key the item already
 * names stays offered when it is not listed, so the editor never shows
 * another source in its place. Labels two sources share carry the key.
 */
export function liveSourceOptions(sources: Record<string, SourceInfo>, current?: string): SourceOption[] {
	const options = sortedSourceKeys(sources).map((key) => ({ key, label: sources[key].label || key }));
	const unconnected = byPosition(sources, Object.keys(sources).filter((k) => sources[k].unconnected));
	for (const key of unconnected) options.push({ key, label: `${key} (unconnected)` });
	if (current && !sources[current]) options.push({ key: current, label: current });

	const count = new Map<string, number>();
	for (const o of options) count.set(o.label, (count.get(o.label) ?? 0) + 1);
	return options.map((o) => (count.get(o.label)! > 1 ? { ...o, label: `${o.label} (${o.key})` } : o));
}
