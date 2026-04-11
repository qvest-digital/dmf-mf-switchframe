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
		onDragStart?: (nodeName: string, event: PointerEvent) => void;
		onDragEnter?: (nodeName: string) => void;
		onDragLeave?: () => void;
		onDrop?: (nodeName: string) => void;
		isDropTarget?: boolean;
		willBeBypassed?: boolean;
		// Enhancement 2: Latency sparkline (60s rolling history)
		sparkline?: number[];
		// Enhancement 4: Node heat coloring (frame budget usage)
		frameBudgetNs?: number;
		// Enhancement 5: Audio VU bars (dBFS, -60..0)
		audioPeakL?: number;
		audioPeakR?: number;
		// Enhancement 6: Frame counter
		frameCount?: number;
		// Enhancement 7: SRT stats
		bitrateMbps?: number;
		rttMs?: number;
		// Enhancement 8: Source tally
		isProgramSource?: boolean;
		isPreviewSource?: boolean;
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
		onDragStart,
		onDragEnter,
		onDragLeave,
		onDrop,
		isDropTarget = false,
		willBeBypassed = false,
		sparkline,
		frameBudgetNs,
		audioPeakL,
		audioPeakR,
		frameCount,
		bitrateMbps,
		rttMs,
		isProgramSource = false,
		isPreviewSource = false,
	}: Props = $props();

	function handleOutputPointerDown(e: PointerEvent) {
		e.stopPropagation();
		onDragStart?.(name, e);
	}

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

	// Enhancement 4: Heat color based on frame budget usage
	const heatColor = $derived.by(() => {
		if (!frameBudgetNs || latencyNs <= 0) return null;
		const pct = latencyNs / frameBudgetNs;
		if (pct < 0.05) return null;
		if (pct < 0.15) return 'var(--accent-green, #22c55e)';
		if (pct < 0.40) return 'var(--accent-yellow, #eab308)';
		return 'var(--tally-program, #dc2626)';
	});

	const heatWidth = $derived.by(() => {
		if (!frameBudgetNs || latencyNs <= 0) return 0;
		return NODE_W * Math.min(1, latencyNs / frameBudgetNs);
	});

	// Enhancement 5: VU meter color
	function vuColor(dbfs: number): string {
		if (dbfs > -3) return '#ef4444';
		if (dbfs > -12) return '#eab308';
		return '#22c55e';
	}

	// Enhancement 8: Border color for tally
	const borderColor = $derived.by(() => {
		if (isProgramSource) return 'var(--tally-program, #dc2626)';
		if (isPreviewSource) return 'var(--tally-preview, #22c55e)';
		return null;
	});

	const label = $derived(displayName ?? name);
</script>

<g class="routing-node" class:node-will-bypass={willBeBypassed} data-node={name}>
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
		class:node-will-bypass={willBeBypassed}
		class:node-tally-program={isProgramSource}
		class:node-tally-preview={isPreviewSource}
		role="button"
		tabindex="0"
		onclick={onclick}
		style={borderColor ? `stroke: ${borderColor}; stroke-width: 2;` : ''}
	/>

	<!-- Input connector (left edge, vertically centered) -->
	<circle
		class="connector connector-input"
		class:connector-drop-target={isDropTarget}
		cx={x}
		cy={y + NODE_H / 2}
		r={5}
		data-connector="input"
		data-node={name}
		onpointerenter={() => onDragEnter?.(name)}
		onpointerleave={() => onDragLeave?.()}
		onpointerup={() => onDrop?.(name)}
	/>

	<!-- Output connector (right edge, vertically centered) -->
	<circle
		class="connector connector-output"
		cx={x + NODE_W}
		cy={y + NODE_H / 2}
		r={5}
		data-connector="output"
		data-node={name}
		onpointerdown={handleOutputPointerDown}
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

	<!-- Enhancement 5: Audio VU bars for audio nodes -->
	{#if audioPeakL != null && audioPeakR != null}
		{@const barH = THUMB_H * 0.8}
		{@const normL = Math.max(0, Math.min(1, (audioPeakL + 60) / 60))}
		{@const normR = Math.max(0, Math.min(1, (audioPeakR + 60) / 60))}
		<rect x={x + 10 + THUMB_W - 10} y={y + 5 + THUMB_H - normL * barH}
			width={3} height={normL * barH} fill={vuColor(audioPeakL)} rx={1} opacity={0.9} />
		<rect x={x + 10 + THUMB_W - 5} y={y + 5 + THUMB_H - normR * barH}
			width={3} height={normR * barH} fill={vuColor(audioPeakR)} rx={1} opacity={0.9} />
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

	<!-- Enhancement 8: Tally badge for source nodes -->
	{#if isProgramSource}
		<rect x={x + 10} y={y + 5} width={26} height={12} rx={2} fill="var(--tally-program, #dc2626)" />
		<text x={x + 23} y={y + 14} text-anchor="middle" class="tally-badge-text">PGM</text>
	{:else if isPreviewSource}
		<rect x={x + 10} y={y + 5} width={26} height={12} rx={2} fill="var(--tally-preview, #22c55e)" />
		<text x={x + 23} y={y + 14} text-anchor="middle" class="tally-badge-text">PVW</text>
	{/if}

	<!-- Node name -->
	<text
		x={x + 10}
		y={y + NODE_H - 15}
		class="node-name"
	>
		{label}
	</text>

	<!-- Enhancement 6: Frame counter -->
	{#if frameCount != null && frameCount > 0}
		<text x={x + NODE_W - 5} y={y + NODE_H - 26} text-anchor="end"
			class="frame-count" font-size="8" fill="var(--text-secondary, #6b7280)" opacity="0.5">
			{frameCount.toLocaleString()} frames
		</text>
	{/if}

	<!-- Latency -->
	<text
		x={x + NODE_W - 30}
		y={y + NODE_H - 15}
		text-anchor="end"
		class="node-latency"
	>
		{formatLatency(latencyNs)}
	</text>

	<!-- Enhancement 2: Latency sparkline -->
	{#if sparkline && sparkline.length > 1}
		{@const sparkW = 40}
		{@const sparkH = 12}
		{@const sparkX = x + NODE_W - sparkW - 5}
		{@const sparkY = y + NODE_H - sparkH - 2}
		{@const maxVal = Math.max(...sparkline, 1)}
		{@const points = sparkline.map((v, i) =>
			`${sparkX + (i / (sparkline.length - 1)) * sparkW},${sparkY + sparkH - (v / maxVal) * sparkH}`
		).join(' ')}
		<polyline
			{points}
			fill="none"
			stroke={healthColor(health)}
			stroke-width={1}
			opacity={0.7}
		/>
	{/if}

	<!-- Health dot -->
	<circle
		cx={x + NODE_W - 10}
		cy={y + NODE_H - 13}
		r="3"
		fill={healthColor(health)}
		class="node-health-dot"
	/>

	<!-- Enhancement 7: SRT stats -->
	{#if bitrateMbps != null}
		<text x={x + 5} y={y + NODE_H - 2} font-size="8" fill="var(--text-secondary, #6b7280)"
			font-family="var(--font-mono, monospace)" class="srt-stats-text">
			{bitrateMbps.toFixed(1)} Mbps{rttMs != null ? ` RTT ${rttMs.toFixed(0)}ms` : ''}
		</text>
	{/if}

	<!-- Enhancement 4: Heat bar at bottom of node -->
	{#if heatColor}
		<rect x={x} y={y + NODE_H - 2} width={heatWidth} height={2} fill={heatColor} rx={1} />
	{/if}

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

	.node-bg.node-tally-program {
		stroke: var(--tally-program, #dc2626);
		stroke-width: 2;
	}

	.node-bg.node-tally-preview {
		stroke: var(--tally-preview, #22c55e);
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

	/* ── Connectors ── */

	.connector {
		fill: var(--bg-control, #2a2a30);
		stroke: var(--border-strong, #444);
		stroke-width: 1;
		transition: r 120ms, fill 120ms, stroke 120ms;
	}

	.connector-input {
		pointer-events: all;
	}

	.connector-output {
		pointer-events: all;
		cursor: grab;
	}

	.connector:hover {
		r: 7;
		stroke: #06B6D4;
	}

	.connector-drop-target {
		fill: var(--accent-green, #22c55e);
		stroke: var(--accent-green, #22c55e);
		stroke-width: 2;
		r: 7;
		animation: connector-pulse 800ms ease-in-out infinite;
	}

	@keyframes connector-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.5; }
	}

	/* ── Will-be-bypassed state ── */

	.node-bg.node-will-bypass {
		opacity: 0.4;
		stroke: var(--accent-yellow, #eab308);
		stroke-dasharray: 6 4;
		stroke-width: 2;
	}

	.node-will-bypass {
		opacity: 0.4;
	}

	/* ── Enhancement 8: Tally badge ── */

	.tally-badge-text {
		font-family: var(--font-ui, -apple-system, sans-serif);
		font-size: 8px;
		font-weight: 700;
		letter-spacing: 0.04em;
		fill: #fff;
		pointer-events: none;
	}

	/* ── Enhancement 6: Frame count ── */

	.frame-count {
		font-family: var(--font-mono, monospace);
		pointer-events: none;
	}

	/* ── Enhancement 7: SRT stats ── */

	.srt-stats-text {
		pointer-events: none;
	}
</style>
