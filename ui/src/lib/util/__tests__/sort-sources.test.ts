import { describe, it, expect } from 'vitest';
import { sortedSourceKeys } from '../sort-sources';
import type { SourceInfo } from '$lib/api/types';

describe('sortedSourceKeys', () => {
	it('sorts by position ascending', () => {
		const sources: Record<string, SourceInfo> = {
			'cam-wide': { key: 'cam-wide', status: 'healthy', position: 3 },
			'cam-1': { key: 'cam-1', status: 'healthy', position: 1 },
			'cam-2': { key: 'cam-2', status: 'healthy', position: 2 },
		};
		expect(sortedSourceKeys(sources)).toEqual(['cam-1', 'cam-2', 'cam-wide']);
	});

	it('falls back to alphabetical for equal positions', () => {
		const sources: Record<string, SourceInfo> = {
			'bravo': { key: 'bravo', status: 'healthy', position: 1 },
			'alpha': { key: 'alpha', status: 'healthy', position: 1 },
		};
		expect(sortedSourceKeys(sources)).toEqual(['alpha', 'bravo']);
	});

	it('handles missing position (defaults to 0)', () => {
		const sources: Record<string, SourceInfo> = {
			'cam-1': { key: 'cam-1', status: 'healthy' } as SourceInfo,
			'cam-2': { key: 'cam-2', status: 'healthy', position: 1 },
		};
		expect(sortedSourceKeys(sources)).toEqual(['cam-1', 'cam-2']);
	});

	it('returns empty array for empty sources', () => {
		expect(sortedSourceKeys({})).toEqual([]);
	});
});
