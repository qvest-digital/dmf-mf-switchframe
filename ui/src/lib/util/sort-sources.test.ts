import { describe, it, expect } from 'vitest';
import { liveSourceOptions, sortedSourceKeys } from './sort-sources';
import type { SourceInfo } from '$lib/api/types';

const src = (key: string, position: number, unconnected?: boolean): SourceInfo => ({
	key,
	type: 'mxl',
	status: 'healthy',
	position,
	unconnected,
});

describe('sortedSourceKeys', () => {
	it('orders by position, then key', () => {
		expect(sortedSourceKeys({ b: src('b', 2), a: src('a', 2), c: src('c', 1) })).toEqual(['c', 'a', 'b']);
	});

	// An input no flow is connected to has nothing to show, so it is not
	// offered to the operator until a controller connects it.
	it('leaves out sources connected to nothing', () => {
		const sources = {
			'mxl:cam1': src('mxl:cam1', 1),
			'mxl:cam2': src('mxl:cam2', 2, true),
			'mxl:cam3': src('mxl:cam3', 3),
		};
		expect(sortedSourceKeys(sources)).toEqual(['mxl:cam1', 'mxl:cam3']);
	});
});

describe('liveSourceOptions', () => {
	// An NMOS input shows the connected sender's name in the switcher; the
	// playlist editor listed only the input key, so an operator could not
	// tell which input carried which sender.
	it('offers each source under the label the switcher shows, keyed by input', () => {
		const sources = {
			'mxl:mxl-1': { ...src('mxl:mxl-1', 2), label: 'srt-ingest-2' },
			'mxl:mxl-2': { ...src('mxl:mxl-2', 1), label: 'srt-ingest-1' },
			'source:bars': src('source:bars', 3),
		};
		expect(liveSourceOptions(sources)).toEqual([
			{ key: 'mxl:mxl-2', label: 'srt-ingest-1' },
			{ key: 'mxl:mxl-1', label: 'srt-ingest-2' },
			{ key: 'source:bars', label: 'source:bars' },
		]);
	});

	// Otherwise the select would show the first source as the item's.
	it('keeps offering the key an item names when it is not listed', () => {
		const sources = {
			'mxl:mxl-1': { ...src('mxl:mxl-1', 1), label: 'srt-ingest-2' },
			'mxl:mxl-3': src('mxl:mxl-3', 2, true),
		};
		expect(liveSourceOptions(sources, 'mxl:mxl-3')).toEqual([
			{ key: 'mxl:mxl-1', label: 'srt-ingest-2' },
			{ key: 'mxl:mxl-3', label: 'mxl:mxl-3' },
		]);
		expect(liveSourceOptions(sources, 'mxl:gone')).toEqual([
			{ key: 'mxl:mxl-1', label: 'srt-ingest-2' },
			{ key: 'mxl:gone', label: 'mxl:gone' },
		]);
	});
});
