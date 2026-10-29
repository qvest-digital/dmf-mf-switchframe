import { describe, it, expect } from 'vitest';
import { layerLabel, layerRef, moveLayer, parseLayerExport, storedConfig } from './layer-config';

describe('moveLayer', () => {
	const order = [10, 11, 12, 13];

	it('moves a layer before or after any other, ends included', () => {
		expect(moveLayer(order, 13, 10, 'before')).toEqual([13, 10, 11, 12]);
		expect(moveLayer(order, 10, 13, 'after')).toEqual([11, 12, 13, 10]);
		expect(moveLayer(order, 10, 12, 'before')).toEqual([11, 10, 12, 13]);
		expect(moveLayer(order, 10, 12, 'after')).toEqual([11, 12, 10, 13]);
		expect(moveLayer(order, 13, 11, 'after')).toEqual([10, 11, 13, 12]);
		expect(moveLayer(order, 13, 11, 'before')).toEqual([10, 13, 11, 12]);
	});

	it('leaves the order alone for a drop on itself or an unknown id', () => {
		expect(moveLayer(order, 11, 11, 'after')).toEqual(order);
		expect(moveLayer(order, 99, 11, 'after')).toEqual(order);
	});

	it('does not modify its input', () => {
		const copy = order.slice();
		moveLayer(copy, 13, 10, 'before');
		expect(copy).toEqual(order);
	});
});

describe('layer identity', () => {
	it('labels a layer by name, else by its id reference', () => {
		expect(layerRef(4)).toBe('#4');
		expect(layerLabel({ id: 4, name: 'Bug' })).toBe('Bug');
		expect(layerLabel({ id: 4, name: '  ' })).toBe('Layer #4');
		expect(layerLabel({ id: 4 })).toBe('Layer #4');
	});

	it('reads a missing or malformed config as empty', () => {
		expect(storedConfig(undefined)).toEqual({});
		expect(storedConfig({ id: 1, active: false, zOrder: 0, x: 0, y: 0, width: 0, height: 0 })).toEqual({});
		expect(storedConfig({ id: 1, active: false, zOrder: 0, x: 0, y: 0, width: 0, height: 0, config: { template: 'ticker' } })).toEqual({ template: 'ticker' });
	});
});

describe('parseLayerExport', () => {
	it('accepts an export document', () => {
		const doc = parseLayerExport('{"format":"switchframe-graphics","version":1,"layers":[{"id":2}]}');
		expect(doc.layers).toHaveLength(1);
	});

	it('rejects anything else before it reaches the engine', () => {
		expect(() => parseLayerExport('nope')).toThrow('not a JSON file');
		expect(() => parseLayerExport('{"layers":[]}')).toThrow('not a graphics layer export');
		expect(() => parseLayerExport('{"format":"switchframe-graphics","layers":{}}')).toThrow('not a graphics layer export');
		expect(() => parseLayerExport('[]')).toThrow('not a graphics layer export');
	});
});
