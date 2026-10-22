import { describe, it, expect } from 'vitest';
import { sortedSourceKeys } from './sort-sources';
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
