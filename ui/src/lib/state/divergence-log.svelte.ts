export interface DivergenceLogEntry {
	timestamp: number;
	type: 'transient' | 'lagging' | 'diverged' | 'mismatch' | 'resolved' | 'snapshot' | 'info';
	message: string;
	warnings?: string[];
}

type DivergenceLogInput = Omit<DivergenceLogEntry, 'timestamp'>;

export function createDivergenceLog(maxEntries = 100) {
	let entries = $state<DivergenceLogEntry[]>([]);

	return {
		get entries() {
			return entries;
		},
		add(entry: DivergenceLogInput) {
			// Deduplicate consecutive identical entries
			if (entries.length > 0 && entries[0].type === entry.type && entries[0].message === entry.message) {
				return;
			}
			entries.unshift({
				...entry,
				timestamp: Date.now(),
			});
			if (entries.length > maxEntries) {
				entries.length = maxEntries;
			}
		},
		clear() {
			entries.length = 0;
		},
	};
}
