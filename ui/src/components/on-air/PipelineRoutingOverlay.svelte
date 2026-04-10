<script lang="ts">
	import RoutingNode from './RoutingNode.svelte';
	import RoutingEdge from './RoutingEdge.svelte';
	import RoutingDetailPopover from './RoutingDetailPopover.svelte';
	import { resolveApiUrl } from '$lib/api/base-url';

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
		onBypassCountChange?: (count: number) => void;
	}

	let { visible, onclose, onBypassCountChange }: Props = $props();

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

	// ── Mock data (fallback when API unavailable) ──

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

	// ── Local state ──

	let nodes = $state<PipelineNodeInfo[]>(mockNodes);
	let edges = $state<PipelineEdge[]>(mockEdges);
	let thumbnails = $state<Record<string, string>>({});

	// ── Selected node for detail popover ──

	let selectedNode = $state<PipelineNodeInfo | null>(null);
	let popoverX = $state(0);
	let popoverY = $state(0);

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

	// ── Bypass toggle (API-first, fallback to local) ──

	async function toggleBypass(name: string) {
		const node = nodes.find(n => n.name === name);
		if (!node) return;

		try {
			const method = node.bypassed ? 'DELETE' : 'PUT';
			const res = await fetch(resolveApiUrl(`/api/pipeline/nodes/${name}/bypass`), { method });
			if (res.ok) {
				const data = await res.json();
				if (data.nodes) {
					nodes = data.nodes;
					return;
				}
			}
		} catch {
			// API unavailable, fall through to local toggle
		}

		// Local fallback
		nodes = nodes.map(n =>
			n.name === name ? { ...n, bypassed: !n.bypassed } : n
		);
	}

	// ── Node click → open detail popover ──

	function handleNodeClick(node: PipelineNodeInfo, event?: MouseEvent) {
		const clickX = event?.clientX ?? 400;
		const clickY = event?.clientY ?? 300;
		popoverX = clickX + 10;
		popoverY = clickY - 20;
		selectedNode = node;
	}

	function closePopover() {
		selectedNode = null;
	}

	// ── Derived ──

	const bypassCount = $derived(nodes.filter(n => n.bypassed).length);

	// Notify parent when bypass count changes (only while overlay is open)
	$effect(() => {
		if (!visible) return;
		onBypassCountChange?.(bypassCount);
	});

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
			if (selectedNode) {
				closePopover();
			} else {
				onclose();
			}
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

	// ── API polling ──

	async function fetchSnapshot() {
		try {
			const res = await fetch(resolveApiUrl('/api/pipeline/snapshot'));
			if (res.ok) {
				const data = await res.json();
				if (data.nodes) nodes = data.nodes;
				if (data.edges) edges = data.edges;
			}
		} catch {
			// API unavailable, keep mock/current data
		}
	}

	async function fetchThumbnails() {
		try {
			const res = await fetch(resolveApiUrl('/api/pipeline/thumbnails'));
			if (res.ok) {
				const data = await res.json();
				thumbnails = data.nodes ?? {};
			}
		} catch {
			// API unavailable, keep empty thumbnails
		}
	}

	$effect(() => {
		if (!visible) return;
		fetchSnapshot();
		fetchThumbnails();
		const id = setInterval(() => {
			fetchSnapshot();
			fetchThumbnails();
		}, 1000);
		return () => clearInterval(id);
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
						<RoutingEdge
							x1={pts.x1}
							y1={pts.y1}
							x2={pts.x2}
							y2={pts.y2}
							active={edge.active}
							isProgram={edge.active}
						/>
					{/if}
				{/each}

				<!-- Nodes -->
				{#each COLUMNS as col}
					{#each nodesInColumn(col) as node, idx}
						<RoutingNode
							x={nodeX(col)}
							y={nodeY(col, idx)}
							name={node.name}
							category={node.category}
							thumbnail={thumbnails[node.name]}
							latencyNs={node.lastNs}
							health={node.health}
							bypassed={node.bypassed}
							isProgram={node.active && !node.bypassed}
							onBypassToggle={() => toggleBypass(node.name)}
							onclick={(e?: MouseEvent) => handleNodeClick(node, e)}
						/>
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

	{#if selectedNode}
		<RoutingDetailPopover
			visible={true}
			x={popoverX}
			y={popoverY}
			node={{
				name: selectedNode.name,
				category: selectedNode.category,
				health: selectedNode.health,
				bypassed: selectedNode.bypassed,
				latencyNs: selectedNode.lastNs,
				maxNs: selectedNode.maxNs,
				error: selectedNode.error,
				thumbnail: thumbnails[selectedNode.name],
			}}
			onclose={closePopover}
			onBypassToggle={() => { toggleBypass(selectedNode!.name); }}
		/>
	{/if}
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
