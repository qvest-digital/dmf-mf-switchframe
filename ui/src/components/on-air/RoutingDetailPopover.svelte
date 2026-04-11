<script lang="ts">
	interface NodeInfo {
		name: string;
		category: string;
		health: string;
		bypassed: boolean;
		latencyNs: number;
		maxNs: number;
		error?: string;
		thumbnail?: string;
	}

	interface Props {
		visible: boolean;
		x: number;
		y: number;
		node: NodeInfo;
		onclose: () => void;
		onBypassToggle: () => void;
	}

	let { visible, x, y, node, onclose, onBypassToggle }: Props = $props();

	function formatLatency(ns: number): string {
		return `${(ns / 1e6).toFixed(3)}ms`;
	}

	function healthColor(health: string): string {
		switch (health) {
			case 'healthy': return '#22c55e';
			case 'degraded': return '#eab308';
			case 'error': return '#ef4444';
			default: return '#6b7280';
		}
	}

	// Clamp position to stay on screen
	const clampedX = $derived(Math.min(Math.max(x, 8), typeof window !== 'undefined' ? window.innerWidth - 360 : 800));
	const clampedY = $derived(Math.min(Math.max(y, 8), typeof window !== 'undefined' ? window.innerHeight - 400 : 600));
</script>

{#if visible}
	<div
		class="routing-detail-popover"
		style:left="{clampedX}px"
		style:top="{clampedY}px"
		role="dialog"
		aria-label="Node detail: {node.name}"
	>
		<header class="detail-header">
			<span class="detail-title">{node.name}</span>
			<button class="detail-close-btn" onclick={onclose} aria-label="Close">&times;</button>
		</header>

		{#if node.thumbnail}
			<div class="detail-thumb-wrap">
				<img
					class="detail-thumb"
					src={node.thumbnail}
					alt="{node.name} preview"
					width="320"
					height="180"
				/>
			</div>
		{/if}

		<div class="detail-body">
			<span class="detail-category">{node.category}</span>

			<div class="detail-metrics">
				<div class="detail-metric">
					<span class="detail-metric-label">Latency</span>
					<span class="detail-metric-value">{formatLatency(node.latencyNs)}</span>
				</div>
				<div class="detail-metric">
					<span class="detail-metric-label">Max</span>
					<span class="detail-metric-value">{formatLatency(node.maxNs)}</span>
				</div>
				<div class="detail-metric">
					<span class="detail-metric-label">Health</span>
					<span class="detail-metric-value detail-health-row">
						<span class="detail-health-dot" style:background={healthColor(node.health)}></span>
						<span class="detail-health-label">{node.health}</span>
					</span>
				</div>
			</div>

			{#if node.error}
				<div class="detail-error">{node.error}</div>
			{/if}

			<button class="detail-bypass-btn" onclick={onBypassToggle}>
				{node.bypassed ? `Re-enable ${node.name}` : `Bypass ${node.name}`}
			</button>
		</div>
	</div>
{/if}

<style>
	.routing-detail-popover {
		position: fixed;
		z-index: var(--z-system);
		min-width: 280px;
		max-width: 360px;
		background: var(--bg-panel, #1a1a22);
		border: 1px solid var(--border-strong, #444);
		border-radius: var(--radius-md, 6px);
		box-shadow: 0 8px 32px rgba(0, 0, 0, 0.6);
		overflow: hidden;
	}

	.detail-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 12px;
		background: var(--bg-surface, #16161e);
		border-bottom: 1px solid var(--border-default, #2a2a3a);
	}

	.detail-title {
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 13px;
		font-weight: 700;
		color: var(--text-primary, #e0e0e8);
	}

	.detail-close-btn {
		background: none;
		border: none;
		color: var(--text-secondary, #888);
		font-size: 18px;
		line-height: 1;
		cursor: pointer;
		padding: 2px 6px;
		border-radius: var(--radius-sm, 4px);
		transition: color 120ms;
	}

	.detail-close-btn:hover {
		color: var(--text-primary, #e0e0e8);
	}

	.detail-thumb-wrap {
		padding: 8px 12px 0;
	}

	.detail-thumb {
		width: 100%;
		height: auto;
		border-radius: 3px;
		background: var(--bg-canvas, #111118);
	}

	.detail-body {
		padding: 10px 12px 12px;
	}

	.detail-category {
		display: inline-block;
		padding: 2px 8px;
		border-radius: var(--radius-sm, 4px);
		background: rgba(6, 182, 212, 0.12);
		border: 1px solid rgba(6, 182, 212, 0.3);
		font-family: var(--font-mono, monospace);
		font-size: 10px;
		font-weight: 600;
		letter-spacing: 0.06em;
		color: #06B6D4;
		text-transform: uppercase;
		margin-bottom: 10px;
	}

	.detail-metrics {
		display: flex;
		flex-direction: column;
		gap: 6px;
		margin-bottom: 10px;
	}

	.detail-metric {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.detail-metric-label {
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 11px;
		color: var(--text-secondary, #6b7280);
	}

	.detail-metric-value {
		font-family: var(--font-mono, monospace);
		font-size: 11px;
		color: var(--text-primary, #e0e0e8);
	}

	.detail-health-row {
		display: inline-flex;
		align-items: center;
		gap: 6px;
	}

	.detail-health-dot {
		display: inline-block;
		width: 8px;
		height: 8px;
		border-radius: 50%;
	}

	.detail-health-label {
		font-family: var(--font-mono, monospace);
		font-size: 11px;
		color: var(--text-primary, #e0e0e8);
	}

	.detail-error {
		padding: 6px 8px;
		margin-bottom: 10px;
		border-radius: var(--radius-sm, 4px);
		background: rgba(239, 68, 68, 0.1);
		border: 1px solid rgba(239, 68, 68, 0.3);
		font-family: var(--font-mono, monospace);
		font-size: 11px;
		color: #ef4444;
	}

	.detail-bypass-btn {
		width: 100%;
		padding: 6px 12px;
		border-radius: var(--radius-sm, 4px);
		background: var(--bg-control, #2a2a30);
		border: 1px solid var(--border-default, #2a2a3a);
		color: var(--text-primary, #e0e0e8);
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 11px;
		font-weight: 600;
		cursor: pointer;
		transition: background 120ms, border-color 120ms;
	}

	.detail-bypass-btn:hover {
		background: var(--bg-hover, #333);
		border-color: var(--border-strong, #444);
	}
</style>
