<script lang="ts">
	interface PipelineNodeInfo {
		name: string;
		category: 'source' | 'decode' | 'gpu' | 'output' | 'transport' | 'audio';
		active: boolean;
		bypassed: boolean;
		lastNs: number;
		maxNs: number;
		latencyUs: number;
		health: 'healthy' | 'degraded' | 'error';
		error?: string;
	}

	interface PipelineEdge {
		from: string;
		to: string;
		active: boolean;
	}

	interface Props {
		visible: boolean;
		onclose: () => void;
	}

	let { visible, onclose }: Props = $props();

	// ── Column definitions ──

	type Column = 'SOURCES' | 'DECODE' | 'PROCESSING' | 'OUTPUT' | 'TRANSPORT';

	const COLUMNS: Column[] = ['SOURCES', 'DECODE', 'PROCESSING', 'OUTPUT', 'TRANSPORT'];

	const COLUMN_X: Record<Column, number> = {
		SOURCES: 40,
		DECODE: 260,
		PROCESSING: 480,
		OUTPUT: 700,
		TRANSPORT: 920,
	};

	// ── Mock data ──

	const mockNodes: PipelineNodeInfo[] = [
		{ name: 'cam1', category: 'source', active: true, bypassed: false, lastNs: 120000, maxNs: 300000, latencyUs: 120, health: 'healthy' },
		{ name: 'cam2', category: 'source', active: true, bypassed: false, lastNs: 115000, maxNs: 290000, latencyUs: 115, health: 'healthy' },
		{ name: 'cam3', category: 'source', active: true, bypassed: false, lastNs: 130000, maxNs: 310000, latencyUs: 130, health: 'degraded' },
		{ name: 'cam4', category: 'source', active: false, bypassed: false, lastNs: 0, maxNs: 0, latencyUs: 0, health: 'error', error: 'No signal' },
		{ name: 'h264_decode', category: 'decode', active: true, bypassed: false, lastNs: 450000, maxNs: 800000, latencyUs: 450, health: 'healthy' },
		{ name: 'gpu_key', category: 'gpu', active: true, bypassed: false, lastNs: 80000, maxNs: 150000, latencyUs: 80, health: 'healthy' },
		{ name: 'gpu_layout', category: 'gpu', active: true, bypassed: false, lastNs: 95000, maxNs: 180000, latencyUs: 95, health: 'healthy' },
		{ name: 'gpu_dsk', category: 'gpu', active: true, bypassed: false, lastNs: 60000, maxNs: 120000, latencyUs: 60, health: 'healthy' },
		{ name: 'gpu_colorgrade', category: 'gpu', active: false, bypassed: true, lastNs: 0, maxNs: 0, latencyUs: 0, health: 'healthy' },
		{ name: 'gpu_stmap', category: 'gpu', active: false, bypassed: true, lastNs: 0, maxNs: 0, latencyUs: 0, health: 'healthy' },
		{ name: 'gpu_encode', category: 'output', active: true, bypassed: false, lastNs: 200000, maxNs: 500000, latencyUs: 200, health: 'healthy' },
		{ name: 'srt_out', category: 'transport', active: true, bypassed: false, lastNs: 50000, maxNs: 100000, latencyUs: 50, health: 'healthy' },
		{ name: 'moq_relay', category: 'transport', active: true, bypassed: false, lastNs: 30000, maxNs: 80000, latencyUs: 30, health: 'healthy' },
	];

	const mockEdges: PipelineEdge[] = [
		{ from: 'cam1', to: 'h264_decode', active: true },
		{ from: 'cam2', to: 'h264_decode', active: true },
		{ from: 'cam3', to: 'h264_decode', active: true },
		{ from: 'cam4', to: 'h264_decode', active: false },
		{ from: 'h264_decode', to: 'gpu_key', active: true },
		{ from: 'gpu_key', to: 'gpu_layout', active: true },
		{ from: 'gpu_layout', to: 'gpu_dsk', active: true },
		{ from: 'gpu_dsk', to: 'gpu_colorgrade', active: false },
		{ from: 'gpu_dsk', to: 'gpu_encode', active: true },
		{ from: 'gpu_colorgrade', to: 'gpu_stmap', active: false },
		{ from: 'gpu_stmap', to: 'gpu_encode', active: false },
		{ from: 'gpu_encode', to: 'srt_out', active: true },
		{ from: 'gpu_encode', to: 'moq_relay', active: true },
	];

	// ── Local state (will become server-driven) ──

	let nodes = $state<PipelineNodeInfo[]>(mockNodes);
	let edges = $state<PipelineEdge[]>(mockEdges);

	// ── Column mapping ──

	const categoryToColumn: Record<PipelineNodeInfo['category'], Column> = {
		source: 'SOURCES',
		decode: 'DECODE',
		gpu: 'PROCESSING',
		output: 'OUTPUT',
		transport: 'TRANSPORT',
		audio: 'PROCESSING',
	};

	function nodesInColumn(col: Column): PipelineNodeInfo[] {
		return nodes.filter(n => categoryToColumn[n.category] === col);
	}

	// ── Node dimensions ──

	const NODE_W = 180;
	const NODE_H = 130;
	const THUMB_W = 160;
	const THUMB_H = 90;
	const ROW_GAP = 20;

	function nodeY(col: Column, index: number): number {
		return 60 + index * (NODE_H + ROW_GAP);
	}

	function nodeX(col: Column): number {
		return COLUMN_X[col];
	}

	// ── Edge geometry ──

	function edgeEndpoints(edge: PipelineEdge): { x1: number; y1: number; x2: number; y2: number } | null {
		const fromNode = nodes.find(n => n.name === edge.from);
		const toNode = nodes.find(n => n.name === edge.to);
		if (!fromNode || !toNode) return null;
		const fromCol = categoryToColumn[fromNode.category];
		const toCol = categoryToColumn[toNode.category];
		const fromIdx = nodesInColumn(fromCol).indexOf(fromNode);
		const toIdx = nodesInColumn(toCol).indexOf(toNode);
		if (fromIdx < 0 || toIdx < 0) return null;
		return {
			x1: nodeX(fromCol) + NODE_W,
			y1: nodeY(fromCol, fromIdx) + NODE_H / 2,
			x2: nodeX(toCol),
			y2: nodeY(toCol, toIdx) + NODE_H / 2,
		};
	}

	// ── Bypass toggle ──

	function toggleBypass(name: string) {
		nodes = nodes.map(n =>
			n.name === name ? { ...n, bypassed: !n.bypassed } : n
		);
	}

	// ── Derived ──

	const bypassCount = $derived(nodes.filter(n => n.bypassed).length);

	const totalLatencyUs = $derived(
		nodes
			.filter(n => n.active && !n.bypassed)
			.reduce((sum, n) => sum + n.latencyUs, 0)
	);

	// ── Health color ──

	function healthColor(health: PipelineNodeInfo['health']): string {
		switch (health) {
			case 'healthy': return 'var(--accent-green, #22c55e)';
			case 'degraded': return 'var(--accent-yellow, #eab308)';
			case 'error': return 'var(--tally-program, #dc2626)';
		}
	}

	// ── Escape handler ──

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			onclose();
		}
	}

	// ── SVG dimensions ──

	const svgWidth = 1140;

	const svgHeight = $derived.by(() => {
		let maxRows = 0;
		for (const col of COLUMNS) {
			const count = nodesInColumn(col).length;
			if (count > maxRows) maxRows = count;
		}
		return 60 + maxRows * (NODE_H + ROW_GAP) + 40;
	});

	// ── Format latency ──

	function formatLatency(us: number): string {
		if (us >= 1000) return `${(us / 1000).toFixed(1)}ms`;
		return `${us}us`;
	}

	// ── Polling stub ──

	$effect(() => {
		if (!visible) return;
		// TODO: Replace with real API polling
		// Will poll GET /api/pipeline/snapshot and /api/pipeline/thumbnails at 1Hz
	});
</script>

{#if visible}
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<div
		class="routing-overlay"
		role="dialog"
		aria-modal="true"
		aria-label="Signal Routing"
		tabindex="-1"
		onkeydown={handleKeydown}
	>
		<header class="routing-header">
			<div class="routing-header-left">
				<span class="routing-title">SIGNAL ROUTING</span>
				<span class="routing-live-badge">
					<span class="live-dot"></span>
					LIVE
				</span>
			</div>
			<button class="routing-close" onclick={onclose} aria-label="Close">
				&#x2715; CLOSE
			</button>
		</header>

		<div class="routing-graph-area">
			<svg
				class="routing-svg"
				viewBox="0 0 {svgWidth} {svgHeight}"
				width={svgWidth}
				height={svgHeight}
				role="img"
				aria-label="Pipeline routing graph"
			>
				<!-- Column headers -->
				{#each COLUMNS as col}
					<text
						x={nodeX(col) + NODE_W / 2}
						y={38}
						text-anchor="middle"
						class="col-header"
					>
						{col}
					</text>
				{/each}

				<!-- Edges -->
				{#each edges as edge}
					{@const pts = edgeEndpoints(edge)}
					{#if pts}
						<line
							x1={pts.x1}
							y1={pts.y1}
							x2={pts.x2}
							y2={pts.y2}
							class="routing-edge"
							class:edge-active={edge.active}
							class:edge-inactive={!edge.active}
							stroke-width={edge.active ? 2 : 1}
						/>
					{/if}
				{/each}

				<!-- Nodes -->
				{#each COLUMNS as col}
					{#each nodesInColumn(col) as node, idx}
						{@const x = nodeX(col)}
						{@const y = nodeY(col, idx)}
						<g class="routing-node" data-node={node.name}>
							<!-- Background rect -->
							<rect
								x={x}
								y={y}
								width={NODE_W}
								height={NODE_H}
								rx="6"
								ry="6"
								class="node-bg"
								class:node-bypassed={node.bypassed}
								class:node-error={node.health === 'error'}
								class:node-program={node.active && !node.bypassed}
							/>

							<!-- Thumbnail area -->
							<rect
								x={x + 10}
								y={y + 6}
								width={THUMB_W}
								height={THUMB_H}
								rx="3"
								ry="3"
								class="node-thumb"
							/>

							<!-- Node name -->
							<text
								x={x + 10}
								y={y + THUMB_H + 18}
								class="node-name"
							>
								{node.name}
							</text>

							<!-- Latency -->
							<text
								x={x + NODE_W - 10}
								y={y + THUMB_H + 18}
								text-anchor="end"
								class="node-latency"
							>
								{node.active ? formatLatency(node.latencyUs) : '--'}
							</text>

							<!-- Health dot -->
							<circle
								cx={x + NODE_W - 14}
								cy={y + 14}
								r="4"
								fill={healthColor(node.health)}
								class="node-health-dot"
							/>

							<!-- Bypass toggle -->
							<!-- svelte-ignore a11y_click_events_have_key_events -->
							<rect
								x={x + NODE_W - 36}
								y={y + 4}
								width="20"
								height="14"
								rx="3"
								ry="3"
								class="bypass-toggle"
								class:bypass-active={node.bypassed}
								role="button"
								tabindex="0"
								onclick={(e: MouseEvent) => { e.stopPropagation(); toggleBypass(node.name); }}
							/>
							<text
								x={x + NODE_W - 26}
								y={y + 14}
								text-anchor="middle"
								class="bypass-label"
							>
								{node.bypassed ? 'B' : ''}
							</text>
						</g>
					{/each}
				{/each}
			</svg>
		</div>

		<footer class="routing-footer">
			<span class="footer-bypass">
				{bypassCount} bypassed
			</span>
			<span class="footer-latency">
				Total latency: {formatLatency(totalLatencyUs)}
			</span>
		</footer>
	</div>
{/if}

<style>
	.routing-overlay {
		position: fixed;
		inset: 0;
		z-index: 9999;
		background: var(--bg-base, #0f0f13);
		display: flex;
		flex-direction: column;
	}

	/* ── Header ── */
	.routing-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 16px;
		background: var(--bg-surface, #16161e);
		border-bottom: 1px solid var(--border-default, #2a2a3a);
		flex-shrink: 0;
	}

	.routing-header-left {
		display: flex;
		align-items: center;
		gap: 12px;
	}

	.routing-title {
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 13px;
		font-weight: 700;
		letter-spacing: 0.08em;
		color: var(--text-primary, #e0e0e8);
		text-transform: uppercase;
	}

	.routing-live-badge {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		padding: 2px 8px;
		border-radius: var(--radius-sm, 4px);
		background: rgba(34, 197, 94, 0.12);
		border: 1px solid rgba(34, 197, 94, 0.3);
		font-family: var(--font-mono, monospace);
		font-size: 10px;
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--accent-green, #22c55e);
	}

	.live-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--accent-green, #22c55e);
		animation: live-pulse 1.5s ease-in-out infinite;
	}

	@keyframes live-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.3; }
	}

	.routing-close {
		background: none;
		border: 1px solid var(--border-default, #2a2a3a);
		border-radius: var(--radius-sm, 4px);
		color: var(--text-secondary, #888);
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 11px;
		font-weight: 600;
		letter-spacing: 0.04em;
		padding: 4px 12px;
		cursor: pointer;
		transition: color 120ms, border-color 120ms, background 120ms;
	}

	.routing-close:hover {
		color: var(--text-primary, #e0e0e8);
		border-color: var(--border-strong, #444);
		background: var(--bg-hover, #1e1e2a);
	}

	/* ── Graph area ── */
	.routing-graph-area {
		flex: 1;
		overflow: auto;
		background:
			radial-gradient(circle, rgba(26, 26, 36, 0.8) 1px, transparent 1px);
		background-size: 20px 20px;
	}

	.routing-svg {
		display: block;
		margin: 0 auto;
	}

	/* ── Column headers ── */
	.col-header {
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 10px;
		font-weight: 700;
		letter-spacing: 0.12em;
		fill: var(--text-secondary, #6b7280);
	}

	/* ── Edges ── */
	.routing-edge.edge-active {
		stroke: #06B6D4;
	}

	.routing-edge.edge-inactive {
		stroke: var(--border-subtle, #333);
		stroke-dasharray: 4 4;
	}

	/* ── Nodes ── */
	.node-bg {
		fill: var(--bg-elevated, #1c1c21);
		stroke: var(--border-default, #2a2a3a);
		stroke-width: 1;
		transition: stroke 150ms, stroke-width 150ms;
	}

	.node-bg.node-program {
		stroke: #06B6D4;
		stroke-width: 2;
	}

	.node-bg.node-bypassed {
		stroke: var(--accent-yellow, #eab308);
		stroke-width: 2;
		stroke-dasharray: 6 3;
	}

	.node-bg.node-error {
		stroke: var(--tally-program, #dc2626);
		stroke-width: 2;
	}

	.node-thumb {
		fill: var(--bg-canvas, #111118);
	}

	.node-name {
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 11px;
		font-weight: 600;
		fill: var(--text-primary, #e0e0e8);
	}

	.node-latency {
		font-family: var(--font-mono, monospace);
		font-size: 10px;
		fill: var(--text-secondary, #6b7280);
	}

	.node-health-dot {
		transition: fill 300ms;
	}

	/* ── Bypass toggle ── */
	.bypass-toggle {
		fill: var(--bg-control, #2a2a30);
		stroke: var(--border-subtle, #333);
		stroke-width: 1;
		cursor: pointer;
		transition: fill 120ms, stroke 120ms;
	}

	.bypass-toggle:hover {
		fill: var(--bg-hover, #333);
		stroke: var(--border-default, #444);
	}

	.bypass-toggle.bypass-active {
		fill: rgba(234, 179, 8, 0.2);
		stroke: var(--accent-yellow, #eab308);
	}

	.bypass-label {
		font-family: var(--font-mono, monospace);
		font-size: 8px;
		font-weight: 700;
		fill: var(--accent-yellow, #eab308);
		pointer-events: none;
	}

	/* ── Footer ── */
	.routing-footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 6px 16px;
		background: var(--bg-surface, #16161e);
		border-top: 1px solid var(--border-default, #2a2a3a);
		flex-shrink: 0;
	}

	.footer-bypass {
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 11px;
		font-weight: 600;
		color: var(--text-secondary, #6b7280);
		letter-spacing: 0.04em;
	}

	.footer-latency {
		font-family: var(--font-mono, monospace);
		font-size: 11px;
		color: var(--text-secondary, #6b7280);
	}
</style>
