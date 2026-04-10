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
		onSourceCut?: (sourceKey: string) => void;
	}

	let { visible, onclose, onBypassCountChange, onSourceCut }: Props = $props();

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

	// ── Local state ──

	let nodes = $state<PipelineNodeInfo[]>([]);
	let edges = $state<PipelineEdge[]>([]);
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

	// ── Dynamic node dimensions based on viewport ──

	let viewportHeight = $state(typeof window !== 'undefined' ? window.innerHeight : 800);

	$effect(() => {
		if (!visible) return;
		function onResize() {
			viewportHeight = window.innerHeight;
		}
		window.addEventListener('resize', onResize);
		return () => window.removeEventListener('resize', onResize);
	});

	const maxNodesInColumn = $derived.by(() => {
		let max = 1;
		for (const col of COLUMNS) {
			const count = nodesInColumn(col).length;
			if (count > max) max = count;
		}
		return max;
	});

	// Scale node dimensions to fit viewport. Reserve 100px for header+footer.
	const NODE_H = $derived(Math.max(60, Math.min(130, (viewportHeight - 100) / maxNodesInColumn - 8)));
	const NODE_W = $derived(Math.min(180, NODE_H * 1.4));
	const ROW_GAP = $derived(Math.min(8, Math.max(4, (viewportHeight - 100 - maxNodesInColumn * NODE_H) / (maxNodesInColumn + 1))));

	function nodeY(col: Column, index: number): number {
		return 50 + index * (NODE_H + ROW_GAP);
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
					// Re-find selected node in updated array so popover stays current
					if (selectedNode) {
						selectedNode = nodes.find(n => n.name === selectedNode!.name) ?? null;
					}
					return;
				}
			}
			// Server rejected (400 for protected node) — don't toggle locally
			return;
		} catch {
			// Network error only — local fallback
			nodes = nodes.map(n =>
				n.name === name ? { ...n, bypassed: !n.bypassed } : n
			);
		}
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

	// ── Drag-and-drop state ──

	let isDragging = $state(false);
	let dragFromNode = $state<string | null>(null);
	let dragFromCategory = $state<string | null>(null);
	let dragLineEnd = $state<{x: number, y: number} | null>(null);
	let dropTargetNode = $state<string | null>(null);
	let nodesToBypass = $state<string[]>([]);

	function svgPoint(svg: SVGSVGElement, clientX: number, clientY: number): { x: number; y: number } {
		const pt = svg.createSVGPoint();
		pt.x = clientX;
		pt.y = clientY;
		const ctm = svg.getScreenCTM();
		if (!ctm) return { x: clientX, y: clientY };
		const svgPt = pt.matrixTransform(ctm.inverse());
		return { x: svgPt.x, y: svgPt.y };
	}

	function handleDragStart(nodeName: string, event: PointerEvent) {
		isDragging = true;
		dragFromNode = nodeName;
		const node = nodes.find(n => n.name === nodeName);
		dragFromCategory = node?.category ?? null;

		const svg = (event.currentTarget as Element)?.closest('svg') as SVGSVGElement | null;
		if (svg) {
			dragLineEnd = svgPoint(svg, event.clientX, event.clientY);
		}
	}

	function handlePointerMove(event: PointerEvent) {
		if (!isDragging) return;
		const svg = event.currentTarget as SVGSVGElement;
		dragLineEnd = svgPoint(svg, event.clientX, event.clientY);
	}

	function handleDragEnter(nodeName: string) {
		if (!isDragging || !dragFromNode) return;

		const fromNode = nodes.find(n => n.name === dragFromNode);
		const toNode = nodes.find(n => n.name === nodeName);
		if (!fromNode || !toNode) return;

		// Source -> GPU head: valid if dragging from a source to the first GPU node
		if (fromNode.category === 'source' && toNode.category === 'gpu') {
			dropTargetNode = nodeName;
			nodesToBypass = [];
			return;
		}

		// GPU -> GPU: valid if toNode is downstream (higher index in chain)
		if (fromNode.category === 'gpu' && toNode.category === 'gpu') {
			const gpuNodes = nodes.filter(n => n.category === 'gpu');
			const fromIdx = gpuNodes.findIndex(n => n.name === dragFromNode);
			const toIdx = gpuNodes.findIndex(n => n.name === nodeName);
			if (toIdx > fromIdx + 1) {
				dropTargetNode = nodeName;
				nodesToBypass = gpuNodes.slice(fromIdx + 1, toIdx).map(n => n.name);
				return;
			}
		}

		// Invalid drop target
		dropTargetNode = null;
		nodesToBypass = [];
	}

	function handleDragLeave() {
		dropTargetNode = null;
		nodesToBypass = [];
	}

	async function handleDrop(nodeName: string) {
		if (!isDragging || !dragFromNode) { cancelDrag(); return; }

		const fromNode = nodes.find(n => n.name === dragFromNode);

		// Source -> GPU: this is a cut to program
		if (fromNode?.category === 'source') {
			onSourceCut?.(dragFromNode);
			cancelDrag();
			return;
		}

		// GPU -> GPU: bypass nodes in between
		if (nodesToBypass.length > 0) {
			for (const bypassName of nodesToBypass) {
				await toggleBypass(bypassName);
			}
		}

		cancelDrag();
	}

	function cancelDrag() {
		isDragging = false;
		dragFromNode = null;
		dragFromCategory = null;
		dragLineEnd = null;
		dropTargetNode = null;
		nodesToBypass = [];
	}

	// ── Drag line geometry ──

	function dragLineStart(): { x: number; y: number } | null {
		if (!dragFromNode) return null;
		const fromNode = nodes.find(n => n.name === dragFromNode);
		if (!fromNode) return null;
		const col = categoryToColumn[fromNode.category];
		const colNodes = nodesInColumn(col);
		const idx = colNodes.indexOf(fromNode);
		if (idx < 0) return null;
		return {
			x: nodeX(col) + NODE_W,
			y: nodeY(col, idx) + NODE_H / 2,
		};
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
			if (isDragging) {
				cancelDrag();
			} else if (selectedNode) {
				closePopover();
			} else {
				onclose();
			}
		}
		switch (e.code) {
			case 'KeyR':
				e.preventDefault();
				fetchSnapshot();
				fetchThumbnails();
				break;
		}
	}

	// ── SVG dimensions ──

	const svgWidth = 1140;

	const svgHeight = $derived(50 + maxNodesInColumn * (NODE_H + ROW_GAP) + 20);

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
				if (data.connections) edges = data.connections;
			}
		} catch {
			// API unavailable, keep current data
		}
	}

	async function fetchThumbnails() {
		try {
			const res = await fetch(resolveApiUrl('/api/pipeline/thumbnails'));
			if (res.ok) {
				const data = await res.json();
				const nodes = data.nodes ?? {};
				const mapped: Record<string, string> = {};
				for (const [id, info] of Object.entries(nodes)) {
					if ((info as any).jpeg) {
						mapped[id] = `data:image/jpeg;base64,${(info as any).jpeg}`;
					}
				}
				thumbnails = mapped;
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
				preserveAspectRatio="xMidYMid meet"
				role="img"
				aria-label="Pipeline routing graph"
				onpointermove={handlePointerMove}
				onpointerup={cancelDrag}
				onpointerleave={cancelDrag}
				style:cursor={isDragging ? 'grabbing' : 'default'}
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
							nodeWidth={NODE_W}
							nodeHeight={NODE_H}
							onBypassToggle={() => toggleBypass(node.name)}
							onclick={(e?: MouseEvent) => handleNodeClick(node, e)}
							onDragStart={handleDragStart}
							onDragEnter={handleDragEnter}
							onDragLeave={handleDragLeave}
							onDrop={handleDrop}
							isDropTarget={dropTargetNode === node.name}
							willBeBypassed={nodesToBypass.includes(node.name)}
						/>
					{/each}
				{/each}

				<!-- Drag preview line -->
				{#if isDragging && dragLineEnd}
					{@const start = dragLineStart()}
					{#if start}
						<line
							x1={start.x}
							y1={start.y}
							x2={dragLineEnd.x}
							y2={dragLineEnd.y}
							stroke="#06B6D4"
							stroke-width={2}
							stroke-dasharray="6 4"
							opacity={0.8}
							class="drag-preview-line"
						/>
					{/if}
				{/if}
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
		overflow: hidden;
		display: flex;
		align-items: center;
		justify-content: center;
		background:
			radial-gradient(circle, rgba(26, 26, 36, 0.8) 1px, transparent 1px);
		background-size: 20px 20px;
	}

	.routing-svg {
		display: block;
		width: 100%;
		height: 100%;
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

	/* ── Drag preview ── */
	.drag-preview-line {
		pointer-events: none;
	}
</style>
