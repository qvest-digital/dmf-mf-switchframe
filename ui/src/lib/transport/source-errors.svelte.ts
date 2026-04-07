/**
 * Reactive per-source pipeline error tracking.
 *
 * The media pipeline sets errors here when decoders fail, and UI components
 * read them to show visual indicators on source tiles.
 */

let errors = $state<Map<string, string>>(new Map());

/** Record an error message for a source. Overwrites any previous error. */
export function setSourceError(key: string, message: string): void {
	const newMap = new Map(errors);
	newMap.set(key, message);
	errors = newMap;
}

/** Clear the error for a source. No-op if no error exists. */
export function clearSourceError(key: string): void {
	if (!errors.has(key)) return;
	const newMap = new Map(errors);
	newMap.delete(key);
	errors = newMap;
}

/** Get the error message for a source, or null if none. */
export function getSourceError(key: string): string | null {
	return errors.get(key) ?? null;
}

/** Get the full error map (all sources with active errors). */
export function getSourceErrors(): Map<string, string> {
	return errors;
}

/** Clear all source errors. */
export function clearAllErrors(): void {
	errors = new Map();
}

// --- Frame stall detection ---

/** Per-source stall state: true means the source has stalled (no frames for > threshold). */
let stalls = $state<Map<string, boolean>>(new Map());

/** Mark a source as stalled. */
export function setSourceStalled(key: string): void {
	if (stalls.get(key) === true) return;
	const newMap = new Map(stalls);
	newMap.set(key, true);
	stalls = newMap;
}

/** Clear the stall state for a source (frames resumed). */
export function clearSourceStall(key: string): void {
	if (!stalls.has(key) || stalls.get(key) === false) return;
	const newMap = new Map(stalls);
	newMap.delete(key);
	stalls = newMap;
}

/** Check if a source is currently stalled. */
export function isSourceStalled(key: string): boolean {
	return stalls.get(key) === true;
}

/** Get all stalled source keys. */
export function getStalledSources(): Set<string> {
	const result = new Set<string>();
	for (const [key, stalled] of stalls) {
		if (stalled) result.add(key);
	}
	return result;
}

/** Clear all stall states. */
export function clearAllStalls(): void {
	stalls = new Map();
}
