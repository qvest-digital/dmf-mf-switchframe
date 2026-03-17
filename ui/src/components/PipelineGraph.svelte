<script lang="ts">
	import { onMount } from 'svelte';
	import { resolveApiUrl } from '$lib/api/base-url';
	import { authHeaders } from '$lib/api/switch-api';
	import type { MediaPipeline } from '$lib/transport/media-pipeline';
	import type { PipelineGraph as PipelineGraphType } from './pipeline-graph/types';
	import type { GraphLayout, NodeLayout, EdgeLayout } from './pipeline-graph/layout';
	import type { GraphNode, GraphColumn, HealthStatus } from './pipeline-graph/types';
	import { buildGraph } from './pipeline-graph/builder';
	import { computeLayout, NODE_WIDTH, NODE_HEIGHT, PADDING } from './pipeline-graph/layout';
	import { healthColor, nsToMs } from './pipeline-graph/health';

	// Column display labels
	const COLUMN_LABELS: Record<GraphColumn, string> = {
		ingest: 'INGEST',
		decode: 'DECODE',
		processing: 'PROCESSING',
		output: 'OUTPUT',
		browser: 'BROWSER'
	};

	// --- Props ---
	interface Props {
		visible: boolean;
		onclose: () => void;
		pipeline?: MediaPipeline | null;
	}

	let { visible, onclose, pipeline = null }: Props = $props();

	// --- State ---
	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	let perfData = $state<any>(null);
	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	let browserDiag = $state<Record<string, any> | null>(null);
	let selectedNode = $state<GraphNode | null>(null);
	let graph = $state<PipelineGraphType>({ nodes: [], edges: [] });
	let graphLayout = $state<GraphLayout>({ nodes: {}, edges: [], width: 0, height: 0 });
	let pulseOn = $state(false);
	let containerWidth = $state(0);
	let containerHeight = $state(0);

	let intervalId: ReturnType<typeof setInterval> | undefined;
	let abortController: AbortController | undefined;

	// --- Polling ---
	async function poll() {
		try {
			abortController?.abort();
			abortController = new AbortController();
			const resp = await fetch(resolveApiUrl('/api/perf'), {
				signal: abortController.signal,
				headers: authHeaders()
			});
			if (resp.ok) {
				perfData = await resp.json();
			}
		} catch {
			/* ignore network + abort errors */
		}

		// Browser diagnostics
		if (pipeline) {
			try {
				browserDiag = await pipeline.getAllDiagnostics();
			} catch {
				browserDiag = null;
			}
		} else {
			browserDiag = null;
		}

		// Rebuild graph
		pulseOn = !pulseOn;
		rebuildGraph();
	}

	function rebuildGraph() {
		graph = buildGraph(perfData, browserDiag);
		if (containerWidth > 0 && containerHeight > 0) {
			graphLayout = computeLayout(graph, containerWidth, containerHeight);
		}
	}

	// Recompute layout when container size changes
	$effect(() => {
		if (containerWidth > 0 && containerHeight > 0 && graph.nodes.length > 0) {
			graphLayout = computeLayout(graph, containerWidth, containerHeight);
		}
	});

	// Start/stop polling on visibility change
	$effect(() => {
		if (visible) {
			perfData = null;
			browserDiag = null;
			selectedNode = null;
			poll();
			intervalId = setInterval(poll, 1000);
		} else {
			if (intervalId) {
				clearInterval(intervalId);
				intervalId = undefined;
			}
		}
		return () => {
			abortController?.abort();
			if (intervalId) {
				clearInterval(intervalId);
				intervalId = undefined;
			}
		};
	});

	// --- Escape key handler ---
	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			onclose();
		}
	}

	// --- Helpers ---
	function nodePosition(nodeId: string): NodeLayout | null {
		return graphLayout.nodes[nodeId] ?? null;
	}

	function badgeColor(badge: string): string {
		switch (badge) {
			case 'SRT':
				return '#3b82f6';
			case 'MXL':
				return '#eab308';
			case 'Clip':
				return '#a78bfa';
			case 'Demo':
				return '#6b7280';
			default:
				return '#6b7280';
		}
	}

	function bufferFillColor(health: HealthStatus): string {
		switch (health) {
			case 'healthy':
				return '#22c55e';
			case 'degraded':
				return '#eab308';
			case 'error':
				return '#ef4444';
		}
	}

	/** Compute column header X positions from the graph layout. */
	function columnHeaderPositions(): { column: GraphColumn; x: number }[] {
		const seen = new Map<GraphColumn, number>();
		for (const node of graph.nodes) {
			const layout = graphLayout.nodes[node.id];
			if (layout && !seen.has(node.column)) {
				seen.set(node.column, layout.x);
			}
		}
		const result: { column: GraphColumn; x: number }[] = [];
		const order: GraphColumn[] = ['ingest', 'decode', 'processing', 'output', 'browser'];
		for (const col of order) {
			const x = seen.get(col);
			if (x !== undefined) {
				result.push({ column: col, x });
			}
		}
		return result;
	}

	/** Format a detail value for display. */
	function formatDetailValue(value: unknown): string {
		if (value === null || value === undefined) return '-';
		if (typeof value === 'number') {
			if (Number.isInteger(value) && Math.abs(value) > 1_000_000) {
				return nsToMs(value);
			}
			if (!Number.isInteger(value)) {
				return value.toFixed(2);
			}
			return String(value);
		}
		if (typeof value === 'boolean') return value ? 'yes' : 'no';
		if (typeof value === 'string') return value;
		return JSON.stringify(value);
	}

	/** Get detail entries for the selected node tooltip. */
	function detailEntries(node: GraphNode): { key: string; value: string }[] {
		const entries: { key: string; value: string }[] = [];

		// KPIs first
		for (const [k, v] of Object.entries(node.kpis)) {
			entries.push({ key: k, value: v });
		}

		// Then detail fields
		if (node.detail) {
			for (const [k, v] of Object.entries(node.detail)) {
				if (typeof v === 'object' && v !== null && !Array.isArray(v)) {
					// Nested object (e.g., windows) — flatten
					const obj = v as Record<string, unknown>;
					for (const [subK, subV] of Object.entries(obj)) {
						entries.push({ key: `${k}.${subK}`, value: formatDetailValue(subV) });
					}
				} else {
					entries.push({ key: k, value: formatDetailValue(v) });
				}
			}
		}

		return entries;
	}

	/** Compute tooltip position near the selected node. */
	function tooltipStyle(node: GraphNode): string {
		const layout = nodePosition(node.id);
		if (!layout) return 'display: none';
		// Position to the right of the node, offset by a bit
		const left = layout.x + layout.width + 12;
		const top = layout.y;
		return `left: ${left}px; top: ${top}px;`;
	}

	function handleBackdropClick(e: MouseEvent) {
		if (e.target === e.currentTarget) {
			onclose();
		}
	}

	function handleSvgClick() {
		selectedNode = null;
	}

	function handleNodeClick(e: MouseEvent, node: GraphNode) {
		e.stopPropagation();
		selectedNode = selectedNode?.id === node.id ? null : node;
	}
</script>

{#if visible}
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions a11y_interactive_supports_focus -->
	<div
		class="pipeline-graph-backdrop"
		role="dialog"
		aria-modal="true"
		aria-label="Pipeline Graph"
		onkeydown={handleKeydown}
		onclick={handleBackdropClick}
	>
		<div class="pipeline-graph-modal">
			<!-- Title bar -->
			<div class="title-bar">
				<div class="title-group">
					<span class="title-text">PIPELINE GRAPH</span>
					<span class="pulse-dot" class:active={pulseOn}></span>
				</div>
				<button class="close-btn" onclick={onclose} aria-label="Close pipeline graph">&times;</button>
			</div>

			<!-- Graph area -->
			<div
				class="graph-area"
				bind:clientWidth={containerWidth}
				bind:clientHeight={containerHeight}
			>
				{#if !perfData}
					<div class="connecting-msg">Connecting...</div>
				{:else if graph.nodes.length === 0}
					<div class="connecting-msg">No pipeline data</div>
				{:else}
					<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
					<svg
						class="graph-svg"
						viewBox="0 0 {graphLayout.width} {graphLayout.height}"
						width={graphLayout.width}
						height={graphLayout.height}
						onclick={handleSvgClick}
					>
						<!-- Defs: arrowhead marker -->
						<defs>
							<marker
								id="arrowhead"
								markerWidth="8"
								markerHeight="6"
								refX="7"
								refY="3"
								orient="auto"
								markerUnits="strokeWidth"
							>
								<polygon points="0 0, 8 3, 0 6" fill="#4a4a5a" />
							</marker>
						</defs>

						<!-- Column headers -->
						{#each columnHeaderPositions() as { column, x }}
							<text
								x={x + NODE_WIDTH / 2}
								y={PADDING / 2 + 4}
								text-anchor="middle"
								class="column-header"
							>
								{COLUMN_LABELS[column]}
							</text>
						{/each}

						<!-- Edges -->
						{#each graphLayout.edges as edgeLayout}
							<path
								d={edgeLayout.path}
								class="graph-edge"
								fill="none"
								marker-end="url(#arrowhead)"
							/>
						{/each}

						<!-- Buffer pills -->
						{#each graphLayout.edges as edgeLayout}
							{#if edgeLayout.buffer}
								<g class="buffer-pill">
									<rect
										x={edgeLayout.buffer.x - 28}
										y={edgeLayout.buffer.y - 10}
										width="56"
										height="20"
										rx="10"
										fill="#1a1a24"
										stroke={bufferFillColor(edgeLayout.buffer.health)}
										stroke-width="1"
									/>
									<text
										x={edgeLayout.buffer.x}
										y={edgeLayout.buffer.y + 4}
										text-anchor="middle"
										class="buffer-text"
									>
										{edgeLayout.buffer.fill}/{edgeLayout.buffer.capacity}
									</text>
								</g>
							{/if}
						{/each}

						<!-- Nodes -->
						{#each graph.nodes as node}
							{@const layout = nodePosition(node.id)}
							{#if layout}
								<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
								<g
									class="graph-node"
									class:selected={selectedNode?.id === node.id}
									onclick={(e) => handleNodeClick(e, node)}
								>
									<!-- Node background -->
									<rect
										x={layout.x}
										y={layout.y}
										width={layout.width}
										height={layout.height}
										rx="6"
										class="node-rect"
										style="stroke: {healthColor(node.health)}"
									/>

									<!-- Node label -->
									<text
										x={layout.x + 8}
										y={layout.y + 18}
										class="node-label"
									>
										{node.label}
									</text>

									<!-- Badge -->
									{#if node.badge}
										{@const labelWidth = node.label.length * 7}
										<rect
											x={layout.x + 10 + labelWidth}
											y={layout.y + 7}
											width={node.badge.length * 7 + 6}
											height="16"
											rx="3"
											fill={badgeColor(node.badge)}
											opacity="0.8"
										/>
										<text
											x={layout.x + 13 + labelWidth}
											y={layout.y + 19}
											class="badge-text"
										>
											{node.badge}
										</text>
									{/if}

									<!-- KPI values (up to 3) -->
									{#each Object.entries(node.kpis).slice(0, 3) as [key, value], i}
										<text
											x={layout.x + 8}
											y={layout.y + 34 + i * 14}
											class="kpi-text"
										>
											<tspan class="kpi-key">{key}:</tspan> {value}
										</text>
									{/each}
								</g>
							{/if}
						{/each}
					</svg>

					<!-- Detail tooltip -->
					{#if selectedNode}
						{@const entries = detailEntries(selectedNode)}
						<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
						<div
							class="detail-tooltip"
							style={tooltipStyle(selectedNode)}
							onclick={(e) => e.stopPropagation()}
						>
							<div class="tooltip-header">
								<span class="tooltip-title">{selectedNode.label}</span>
								<span class="tooltip-health" style="color: {healthColor(selectedNode.health)}">
									{selectedNode.health}
								</span>
							</div>
							<div class="tooltip-body">
								{#each entries as { key, value }}
									<div class="tooltip-row">
										<span class="tooltip-key">{key}</span>
										<span class="tooltip-value">{value}</span>
									</div>
								{/each}
								{#if entries.length === 0}
									<div class="tooltip-row">
										<span class="tooltip-key">status</span>
										<span class="tooltip-value">{selectedNode.health}</span>
									</div>
								{/if}
							</div>
						</div>
					{/if}
				{/if}
			</div>
		</div>
	</div>
{/if}

<style>
	/* --- Backdrop & Modal --- */
	.pipeline-graph-backdrop {
		position: fixed;
		inset: 0;
		z-index: 9999;
		background: rgba(0, 0, 0, 0.7);
		display: flex;
		align-items: center;
		justify-content: center;
	}

	.pipeline-graph-modal {
		width: 95vw;
		height: 90vh;
		background: #0f0f13;
		border-radius: 8px;
		border: 1px solid #2a2a3a;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	/* --- Title bar --- */
	.title-bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 10px 16px;
		background: #16161e;
		border-bottom: 1px solid #2a2a3a;
		flex-shrink: 0;
	}

	.title-group {
		display: flex;
		align-items: center;
		gap: 10px;
	}

	.title-text {
		font-size: 12px;
		font-weight: 700;
		letter-spacing: 0.1em;
		color: #e0e0e8;
	}

	.pulse-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: #22c55e;
		opacity: 0.3;
		transition: opacity 0.3s ease;
	}

	.pulse-dot.active {
		opacity: 1;
	}

	.close-btn {
		background: none;
		border: none;
		color: #888;
		font-size: 20px;
		cursor: pointer;
		padding: 0 4px;
		line-height: 1;
	}

	.close-btn:hover {
		color: #fff;
	}

	/* --- Graph area --- */
	.graph-area {
		flex: 1;
		overflow: auto;
		position: relative;
		background:
			radial-gradient(circle, #1a1a24 1px, transparent 1px);
		background-size: 20px 20px;
	}

	.connecting-msg {
		position: absolute;
		top: 50%;
		left: 50%;
		transform: translate(-50%, -50%);
		color: #6b7280;
		font-size: 14px;
		font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
		letter-spacing: 0.05em;
	}

	.graph-svg {
		display: block;
	}

	/* --- Column headers --- */
	.column-header {
		font-size: 10px;
		font-weight: 700;
		letter-spacing: 0.12em;
		fill: #6b7280;
		font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
	}

	/* --- Edges --- */
	.graph-edge {
		stroke: #4a4a5a;
		stroke-width: 1.5;
	}

	/* --- Buffer pills --- */
	.buffer-text {
		font-size: 9px;
		fill: #a0a0b0;
		font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
	}

	/* --- Nodes --- */
	.node-rect {
		fill: #1a1a24;
		stroke-width: 2;
		transition: stroke 0.3s ease;
		cursor: pointer;
	}

	.graph-node:hover .node-rect {
		fill: #1e1e2a;
	}

	.graph-node.selected .node-rect {
		fill: #1e1e2a;
		stroke-width: 2.5;
	}

	.node-label {
		font-size: 12px;
		font-weight: 600;
		fill: #e0e0e8;
		font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
	}

	.badge-text {
		font-size: 9px;
		font-weight: 600;
		fill: #fff;
		font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
	}

	.kpi-text {
		font-size: 10px;
		fill: #a0a0b0;
		font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
	}

	.kpi-key {
		fill: #6b7280;
	}

	/* --- Detail tooltip --- */
	.detail-tooltip {
		position: absolute;
		min-width: 180px;
		max-width: 280px;
		background: #1a1a24;
		border: 1px solid #3a3a4a;
		border-radius: 6px;
		box-shadow: 0 4px 16px rgba(0, 0, 0, 0.5);
		z-index: 10;
		overflow: hidden;
	}

	.tooltip-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 12px;
		border-bottom: 1px solid #2a2a3a;
	}

	.tooltip-title {
		font-size: 12px;
		font-weight: 600;
		color: #e0e0e8;
	}

	.tooltip-health {
		font-size: 10px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.tooltip-body {
		padding: 8px 12px;
	}

	.tooltip-row {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		padding: 2px 0;
		gap: 12px;
	}

	.tooltip-key {
		font-size: 10px;
		color: #6b7280;
		font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
		white-space: nowrap;
	}

	.tooltip-value {
		font-size: 10px;
		color: #e0e0e8;
		font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
		text-align: right;
	}
</style>
