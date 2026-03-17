import { describe, it, expect } from 'vitest';
import {
	computeLayout,
	NODE_WIDTH,
	NODE_HEIGHT,
	COL_GAP,
	PADDING
} from './layout';
import type { GraphNode, GraphEdge, PipelineGraph, GraphColumn } from './types';

function makeNode(id: string, column: GraphColumn, row: number): GraphNode {
	return {
		id,
		label: id,
		category: 'source',
		column,
		row,
		kpis: {},
		health: 'healthy'
	};
}

function makeGraph(nodes: GraphNode[], edges: GraphEdge[] = []): PipelineGraph {
	return { nodes, edges };
}

describe('computeLayout', () => {
	it('positions nodes in different columns left-to-right', () => {
		const graph = makeGraph([
			makeNode('a', 'ingest', 0),
			makeNode('b', 'decode', 0),
			makeNode('c', 'output', 0)
		]);
		const layout = computeLayout(graph, 800, 600);

		expect(layout.nodes['a'].x).toBeLessThan(layout.nodes['b'].x);
		expect(layout.nodes['b'].x).toBeLessThan(layout.nodes['c'].x);
	});

	it('positions nodes in the same column top-to-bottom by row', () => {
		const graph = makeGraph([
			makeNode('a', 'ingest', 0),
			makeNode('b', 'ingest', 1),
			makeNode('c', 'ingest', 2)
		]);
		const layout = computeLayout(graph, 800, 600);

		expect(layout.nodes['a'].y).toBeLessThan(layout.nodes['b'].y);
		expect(layout.nodes['b'].y).toBeLessThan(layout.nodes['c'].y);
		// Same x for same column
		expect(layout.nodes['a'].x).toBe(layout.nodes['b'].x);
		expect(layout.nodes['b'].x).toBe(layout.nodes['c'].x);
	});

	it('computes edge paths between connected nodes', () => {
		const graph = makeGraph(
			[makeNode('src', 'ingest', 0), makeNode('dec', 'decode', 0)],
			[{ from: 'src', to: 'dec' }]
		);
		const layout = computeLayout(graph, 800, 600);

		expect(layout.edges).toHaveLength(1);
		expect(layout.edges[0].path).toBeTruthy();
		expect(layout.edges[0].path).toContain('M ');
		expect(layout.edges[0].path).toContain('C ');
	});

	it('produces positive total SVG dimensions', () => {
		const graph = makeGraph([makeNode('a', 'ingest', 0)]);
		const layout = computeLayout(graph, 800, 600);

		expect(layout.width).toBeGreaterThan(0);
		expect(layout.height).toBeGreaterThan(0);
	});

	it('respects minimum viewport dimensions', () => {
		const graph = makeGraph([makeNode('a', 'ingest', 0)]);
		const layout = computeLayout(graph, 1200, 900);

		expect(layout.width).toBeGreaterThanOrEqual(1200);
		expect(layout.height).toBeGreaterThanOrEqual(900);
	});

	it('skips empty columns in layout', () => {
		// Only ingest and output, no decode/processing/browser
		const graph = makeGraph([
			makeNode('a', 'ingest', 0),
			makeNode('b', 'output', 0)
		]);
		const layout = computeLayout(graph, 800, 600);

		// With 2 columns, the gap between them should be exactly COL_GAP + NODE_WIDTH
		// (one column width plus one gap), not 3 gaps for 3 skipped columns
		const dx = layout.nodes['b'].x - layout.nodes['a'].x;
		expect(dx).toBe(NODE_WIDTH + COL_GAP);
	});

	it('positions buffer indicator at edge midpoint', () => {
		const graph = makeGraph(
			[makeNode('src', 'ingest', 0), makeNode('dec', 'decode', 0)],
			[
				{
					from: 'src',
					to: 'dec',
					buffer: { name: 'videoProcCh', fill: 3, capacity: 8, health: 'healthy' }
				}
			]
		);
		const layout = computeLayout(graph, 800, 600);

		expect(layout.edges[0].buffer).toBeDefined();
		const buf = layout.edges[0].buffer!;

		// Buffer should be between the two node edges
		const fromRight = layout.nodes['src'].x + NODE_WIDTH;
		const toLeft = layout.nodes['dec'].x;
		const expectedMidX = (fromRight + toLeft) / 2;

		expect(buf.x).toBe(expectedMidX);
		expect(buf.name).toBe('videoProcCh');
		expect(buf.fill).toBe(3);
		expect(buf.capacity).toBe(8);
	});

	it('handles single-node graph', () => {
		const graph = makeGraph([makeNode('solo', 'processing', 0)]);
		const layout = computeLayout(graph, 800, 600);

		expect(Object.keys(layout.nodes)).toHaveLength(1);
		expect(layout.nodes['solo']).toBeDefined();
		expect(layout.nodes['solo'].width).toBe(NODE_WIDTH);
		expect(layout.nodes['solo'].height).toBe(NODE_HEIGHT);
		expect(layout.edges).toHaveLength(0);
		expect(layout.width).toBeGreaterThanOrEqual(800);
		expect(layout.height).toBeGreaterThanOrEqual(600);
	});

	it('handles empty graph', () => {
		const graph = makeGraph([]);
		const layout = computeLayout(graph, 800, 600);

		expect(Object.keys(layout.nodes)).toHaveLength(0);
		expect(layout.edges).toHaveLength(0);
		expect(layout.width).toBeGreaterThanOrEqual(800);
		expect(layout.height).toBeGreaterThanOrEqual(600);
	});

	it('assigns correct node dimensions', () => {
		const graph = makeGraph([makeNode('a', 'ingest', 0)]);
		const layout = computeLayout(graph, 800, 600);

		expect(layout.nodes['a'].width).toBe(NODE_WIDTH);
		expect(layout.nodes['a'].height).toBe(NODE_HEIGHT);
	});

	it('vertically centers nodes within the viewport', () => {
		const graph = makeGraph([makeNode('a', 'ingest', 0)]);
		const layout = computeLayout(graph, 800, 600);

		// Single node should be centered vertically
		const expectedY = (layout.height - NODE_HEIGHT) / 2;
		expect(layout.nodes['a'].y).toBe(expectedY);
	});

	it('starts first column at PADDING offset', () => {
		const graph = makeGraph([makeNode('a', 'ingest', 0)]);
		const layout = computeLayout(graph, 800, 600);

		expect(layout.nodes['a'].x).toBe(PADDING);
	});

	it('preserves column order even when nodes are added out of order', () => {
		const graph = makeGraph([
			makeNode('out', 'output', 0),
			makeNode('in', 'ingest', 0),
			makeNode('proc', 'processing', 0)
		]);
		const layout = computeLayout(graph, 800, 600);

		expect(layout.nodes['in'].x).toBeLessThan(layout.nodes['proc'].x);
		expect(layout.nodes['proc'].x).toBeLessThan(layout.nodes['out'].x);
	});

	it('skips edges with missing source or target nodes', () => {
		const graph = makeGraph(
			[makeNode('a', 'ingest', 0)],
			[{ from: 'a', to: 'nonexistent' }]
		);
		const layout = computeLayout(graph, 800, 600);

		expect(layout.edges).toHaveLength(0);
	});

	it('does not include buffer on edge without buffer data', () => {
		const graph = makeGraph(
			[makeNode('a', 'ingest', 0), makeNode('b', 'decode', 0)],
			[{ from: 'a', to: 'b' }]
		);
		const layout = computeLayout(graph, 800, 600);

		expect(layout.edges[0].buffer).toBeUndefined();
	});

	it('handles multiple edges', () => {
		const graph = makeGraph(
			[
				makeNode('a', 'ingest', 0),
				makeNode('b', 'decode', 0),
				makeNode('c', 'processing', 0)
			],
			[
				{ from: 'a', to: 'b' },
				{ from: 'b', to: 'c' }
			]
		);
		const layout = computeLayout(graph, 800, 600);

		expect(layout.edges).toHaveLength(2);
		expect(layout.edges[0].edge.from).toBe('a');
		expect(layout.edges[1].edge.from).toBe('b');
	});

	it('sorts nodes within a column by row regardless of input order', () => {
		const graph = makeGraph([
			makeNode('b', 'ingest', 2),
			makeNode('a', 'ingest', 0),
			makeNode('c', 'ingest', 1)
		]);
		const layout = computeLayout(graph, 800, 600);

		// a (row 0) should be above c (row 1) which should be above b (row 2)
		expect(layout.nodes['a'].y).toBeLessThan(layout.nodes['c'].y);
		expect(layout.nodes['c'].y).toBeLessThan(layout.nodes['b'].y);
	});
});
