import type { PipelineGraph, GraphNode, GraphEdge, GraphColumn, BufferIndicator } from './types';

// Layout constants
export const NODE_WIDTH = 160;
export const NODE_HEIGHT = 72;
export const COL_GAP = 80;
export const ROW_GAP = 24;
export const PADDING = 40;

/** Canonical left-to-right column order. */
const COLUMN_ORDER: GraphColumn[] = ['ingest', 'decode', 'processing', 'output', 'browser'];

export interface NodeLayout {
	x: number;
	y: number;
	width: number;
	height: number;
}

export interface EdgeLayout {
	path: string; // SVG path d attribute
	buffer?: BufferIndicator & { x: number; y: number }; // midpoint position for buffer pill
	edge: GraphEdge; // original edge data
}

export interface GraphLayout {
	nodes: Record<string, NodeLayout>; // keyed by node.id
	edges: EdgeLayout[];
	width: number; // total SVG width
	height: number; // total SVG height
}

/**
 * Group nodes by their column, returning only columns that have nodes.
 * Preserves canonical column order.
 */
function groupByColumn(nodes: GraphNode[]): { column: GraphColumn; nodes: GraphNode[] }[] {
	const map = new Map<GraphColumn, GraphNode[]>();
	for (const node of nodes) {
		let list = map.get(node.column);
		if (!list) {
			list = [];
			map.set(node.column, list);
		}
		list.push(node);
	}

	const result: { column: GraphColumn; nodes: GraphNode[] }[] = [];
	for (const col of COLUMN_ORDER) {
		const list = map.get(col);
		if (list && list.length > 0) {
			// Sort nodes within a column by row index
			list.sort((a, b) => a.row - b.row);
			result.push({ column: col, nodes: list });
		}
	}
	return result;
}

/**
 * Compute the SVG layout for a pipeline graph.
 *
 * Columns are positioned left-to-right with COL_GAP between them.
 * Empty columns are skipped (compact layout).
 * Within each column, nodes are sorted by row index and vertically centered.
 */
export function computeLayout(
	graph: PipelineGraph,
	viewWidth: number,
	viewHeight: number
): GraphLayout {
	const nodeLayouts: Record<string, NodeLayout> = {};
	const edgeLayouts: EdgeLayout[] = [];

	if (graph.nodes.length === 0) {
		return {
			nodes: nodeLayouts,
			edges: edgeLayouts,
			width: Math.max(0, viewWidth),
			height: Math.max(0, viewHeight)
		};
	}

	const columns = groupByColumn(graph.nodes);

	// Compute total content dimensions
	const contentWidth = PADDING * 2 + columns.length * NODE_WIDTH + (columns.length - 1) * COL_GAP;
	const maxRows = Math.max(...columns.map((c) => c.nodes.length));
	const contentHeight = PADDING * 2 + maxRows * NODE_HEIGHT + (maxRows - 1) * ROW_GAP;

	const totalWidth = Math.max(contentWidth, viewWidth);
	const totalHeight = Math.max(contentHeight, viewHeight);

	// Position nodes
	for (let colIdx = 0; colIdx < columns.length; colIdx++) {
		const { nodes } = columns[colIdx];
		const x = PADDING + colIdx * (NODE_WIDTH + COL_GAP);
		const columnHeight = nodes.length * NODE_HEIGHT + (nodes.length - 1) * ROW_GAP;
		const startY = (totalHeight - columnHeight) / 2;

		for (let rowIdx = 0; rowIdx < nodes.length; rowIdx++) {
			const node = nodes[rowIdx];
			nodeLayouts[node.id] = {
				x,
				y: startY + rowIdx * (NODE_HEIGHT + ROW_GAP),
				width: NODE_WIDTH,
				height: NODE_HEIGHT
			};
		}
	}

	// Compute edge paths
	for (const edge of graph.edges) {
		const fromLayout = nodeLayouts[edge.from];
		const toLayout = nodeLayouts[edge.to];
		if (!fromLayout || !toLayout) continue;

		const fromX = fromLayout.x + fromLayout.width;
		const fromY = fromLayout.y + fromLayout.height / 2;
		const toX = toLayout.x;
		const toY = toLayout.y + toLayout.height / 2;

		const dx = toX - fromX;
		const cp1x = fromX + dx * 0.4;
		const cp2x = fromX + dx * 0.6;

		const path = `M ${fromX} ${fromY} C ${cp1x} ${fromY}, ${cp2x} ${toY}, ${toX} ${toY}`;

		const edgeLayout: EdgeLayout = { path, edge };

		if (edge.buffer) {
			const midX = (fromX + toX) / 2;
			const midY = (fromY + toY) / 2;
			edgeLayout.buffer = { ...edge.buffer, x: midX, y: midY };
		}

		edgeLayouts.push(edgeLayout);
	}

	return {
		nodes: nodeLayouts,
		edges: edgeLayouts,
		width: totalWidth,
		height: totalHeight
	};
}
