<script lang="ts">
	interface Props {
		x: number;
		y: number;
		name: string;
		displayName?: string;
		category: 'source' | 'decode' | 'gpu' | 'output' | 'transport' | 'audio';
		thumbnail?: string;
		latencyNs: number;
		health: 'healthy' | 'degraded' | 'error';
		bypassed: boolean;
		isProgram: boolean;
		nodeWidth?: number;
		nodeHeight?: number;
		onBypassToggle: () => void;
		onclick: () => void;
	}

	let {
		x,
		y,
		name,
		displayName,
		category,
		thumbnail,
		latencyNs,
		health,
		bypassed,
		isProgram,
		nodeWidth = 180,
		nodeHeight = 130,
		onBypassToggle,
		onclick,
	}: Props = $props();

	const NODE_W = $derived(nodeWidth);
	const NODE_H = $derived(nodeHeight);
	const THUMB_W = $derived(NODE_W - 20);
	const THUMB_H = $derived(NODE_H - 40);

	function healthColor(h: Props['health']): string {
		switch (h) {
			case 'healthy': return '#22c55e';
			case 'degraded': return '#eab308';
			case 'error': return '#ef4444';
		}
	}

	function formatLatency(ns: number): string {
		return `${(ns / 1e6).toFixed(3)}ms`;
	}

	const label = $derived(displayName ?? name);
</script>

<g class="routing-node" data-node={name}>
	<!-- Background rect / clickable body -->
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<rect
		x={x}
		y={y}
		width={NODE_W}
		height={NODE_H}
		rx="4"
		ry="4"
		class="node-bg"
		class:node-bypassed={bypassed}
		class:node-program={isProgram}
		class:node-error={health === 'error'}
		role="button"
		tabindex="0"
		onclick={onclick}
	/>

	<!-- Thumbnail area -->
	{#if thumbnail}
		<image
			x={x + 10}
			y={y + 5}
			width={THUMB_W}
			height={THUMB_H}
			href={thumbnail}
			class="node-thumb-image"
			preserveAspectRatio="xMidYMid slice"
		/>
	{:else}
		<rect
			x={x + 10}
			y={y + 5}
			width={THUMB_W}
			height={THUMB_H}
			rx="3"
			ry="3"
			class="node-thumb"
		/>
		<text
			x={x + 10 + THUMB_W / 2}
			y={y + 5 + THUMB_H / 2 + 3}
			text-anchor="middle"
			class="node-no-preview"
		>
			No preview
		</text>
	{/if}

	<!-- Bypass overlay when bypassed -->
	{#if bypassed}
		<rect
			x={x + 10}
			y={y + 5}
			width={THUMB_W}
			height={THUMB_H}
			rx="3"
			ry="3"
			class="bypass-overlay-bg"
		/>
		<text
			x={x + 10 + THUMB_W / 2}
			y={y + 5 + THUMB_H / 2 + 4}
			text-anchor="middle"
			class="bypass-overlay-text"
		>
			BYPASSED
		</text>
	{/if}

	<!-- Node name -->
	<text
		x={x + 10}
		y={y + NODE_H - 15}
		class="node-name"
	>
		{label}
	</text>

	<!-- Latency -->
	<text
		x={x + NODE_W - 30}
		y={y + NODE_H - 15}
		text-anchor="end"
		class="node-latency"
	>
		{formatLatency(latencyNs)}
	</text>

	<!-- Health dot -->
	<circle
		cx={x + NODE_W - 10}
		cy={y + NODE_H - 13}
		r="3"
		fill={healthColor(health)}
		class="node-health-dot"
	/>

	<!-- Bypass toggle -->
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<rect
		x={x + NODE_W - 20}
		y={y + 5}
		width="15"
		height="15"
		rx="2"
		ry="2"
		class="bypass-toggle"
		class:bypass-active={bypassed}
		role="button"
		tabindex="0"
		onclick={(e: MouseEvent) => { e.stopPropagation(); onBypassToggle(); }}
	/>
	<text
		x={x + NODE_W - 12.5}
		y={y + 15}
		text-anchor="middle"
		class="bypass-toggle-icon"
	>&#x23FB;</text>
</g>

<style>
	.node-bg {
		fill: var(--bg-elevated, #1c1c21);
		stroke: var(--border-default, #2a2a3a);
		stroke-width: 1;
		cursor: pointer;
		transition: stroke 150ms, stroke-width 150ms;
	}

	.node-bg.node-program {
		stroke: var(--tally-program, #dc2626);
		stroke-width: 2;
	}

	.node-bg.node-bypassed {
		stroke: var(--accent-yellow, #eab308);
		stroke-width: 1;
		stroke-dasharray: 4 4;
	}

	.node-bg.node-error {
		stroke: var(--tally-program, #dc2626);
		stroke-width: 2;
	}

	.node-thumb {
		fill: var(--bg-canvas, #111118);
	}

	.node-no-preview {
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 9px;
		font-weight: 500;
		fill: var(--text-secondary, #6b7280);
		pointer-events: none;
	}

	.node-thumb-image {
		border-radius: 3px;
	}

	.bypass-overlay-bg {
		fill: rgba(0, 0, 0, 0.6);
	}

	.bypass-overlay-text {
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 11px;
		font-weight: 700;
		letter-spacing: 0.08em;
		fill: var(--accent-yellow, #eab308);
		pointer-events: none;
	}

	.node-name {
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 10px;
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

	.bypass-toggle-icon {
		font-size: 8px;
		fill: var(--text-secondary, #6b7280);
		pointer-events: none;
	}
</style>
