<script lang="ts">
	import { healthDotColor } from '$lib/util/health-dots';
	import { setupHiDPICanvas } from '$lib/video/canvas-utils';
	import type { SourceHealthStatus } from '$lib/api/types';

	interface Props {
		sourceKey: string;
		label: string;
		tallyState: 'program' | 'preview' | 'idle';
		health?: SourceHealthStatus;
		isDualEngine?: boolean;
		engineAHealth?: SourceHealthStatus;
		engineBHealth?: SourceHealthStatus;
		compact?: boolean;
		onClick: () => void;
	}

	let {
		sourceKey,
		label,
		tallyState,
		health = 'healthy',
		isDualEngine = false,
		engineAHealth,
		engineBHealth,
		compact = false,
		onClick,
	}: Props = $props();

	let canvasEl: HTMLCanvasElement;
	let wrapEl: HTMLDivElement;
	let hasVideo = $state(false);

	// Track whether canvas has received video frames
	$effect(() => {
		if (!canvasEl) return;
		const obs = new MutationObserver(() => {
			// Canvas dimensions change when pipeline attaches a renderer
			if (canvasEl.width > 0 && canvasEl.height > 0) {
				hasVideo = true;
			}
		});
		obs.observe(canvasEl, { attributes: true, attributeFilter: ['width', 'height'] });

		// Also poll briefly to detect renderer attachment
		const id = setInterval(() => {
			if (canvasEl.width > 0 && canvasEl.height > 0) {
				hasVideo = true;
			}
		}, 500);

		return () => { obs.disconnect(); clearInterval(id); };
	});

	// Hi-DPI canvas sizing via ResizeObserver
	$effect(() => {
		if (!canvasEl || !wrapEl) return;
		const obs = new ResizeObserver(([entry]) => {
			const { width, height } = entry.contentRect;
			if (width > 0 && height > 0) setupHiDPICanvas(canvasEl, width, height);
		});
		obs.observe(wrapEl);
		return () => obs.disconnect();
	});

	let healthLabel = $derived(
		health === 'no_signal' ? 'no signal' : (health ?? 'healthy')
	);

	let healthColor = $derived(healthDotColor(health ?? 'healthy'));
	let engineAColor = $derived(healthDotColor(engineAHealth ?? 'healthy'));
	let engineBColor = $derived(healthDotColor(engineBHealth ?? 'healthy'));

	let isWarning = $derived(
		health === 'stale' || health === 'no_signal'
	);
</script>

<button
	class="oa-source-tile"
	class:program={tallyState === 'program'}
	class:preview={tallyState === 'preview'}
	class:compact
	onclick={onClick}
	data-source-key={sourceKey}
>
	<!-- Thumbnail with live canvas -->
	<div class="thumbnail" bind:this={wrapEl}>
		<canvas
			class="thumb-canvas"
			id="tile-{sourceKey}"
			bind:this={canvasEl}
		></canvas>
		{#if !hasVideo}
			<span class="no-signal-text">NO SIGNAL</span>
		{/if}
	</div>

	<!-- Label + tally badge -->
	<div class="label-row">
		<span class="source-label" title={label}>{label}</span>
		{#if tallyState === 'program'}
			<span class="tally-badge tally-pgm" aria-label="On program">PGM</span>
		{:else if tallyState === 'preview'}
			<span class="tally-badge tally-pvw" aria-label="On preview">PVW</span>
		{/if}
	</div>

	<!-- Health indicators (hidden in compact mode) -->
	{#if !compact}
		<div class="health-row">
			{#if isDualEngine && engineAHealth != null && engineBHealth != null}
				<div class="engine-health" data-testid="engine-health-dual">
					<span class="engine-line">
						<span class="engine-id">A:</span>
						<span class="health-dot" style="background: {engineAColor}" aria-label="Engine A {engineAHealth}"></span>
						<span class="health-text">{engineAHealth}</span>
					</span>
					<span class="engine-line">
						<span class="engine-id">B:</span>
						<span class="health-dot" style="background: {engineBColor}" aria-label="Engine B {engineBHealth}"></span>
						<span class="health-text">{engineBHealth}</span>
					</span>
				</div>
			{:else}
				<span class="single-health" class:warning={isWarning} class:offline={health === 'offline'} data-testid="source-health">
					<span class="health-dot" style="background: {healthColor}" aria-label="{healthLabel}"></span>
					<span class="health-text">{healthLabel}</span>
				</span>
			{/if}
		</div>
	{:else}
		<div class="health-row compact-health">
			<span class="health-dot" style="background: {healthColor}" aria-label="{healthLabel}"></span>
		</div>
	{/if}
</button>

<style>
	.oa-source-tile {
		display: flex;
		flex-direction: column;
		border: 2px solid transparent;
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		cursor: pointer;
		overflow: hidden;
		font-family: var(--font-ui);
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast),
			box-shadow var(--transition-normal),
			transform var(--transition-fast);
	}

	.oa-source-tile:hover {
		border-color: var(--border-strong);
		background: var(--bg-hover);
	}

	.oa-source-tile:active {
		transform: scale(0.97);
	}

	/* Tally states */
	.oa-source-tile.program {
		border-color: var(--tally-program);
		box-shadow: var(--tally-program-glow);
	}

	.oa-source-tile.program:hover {
		border-color: var(--tally-program);
	}

	.oa-source-tile.preview {
		border-color: var(--tally-preview);
		box-shadow: var(--tally-preview-glow);
	}

	.oa-source-tile.preview:hover {
		border-color: var(--tally-preview);
	}

	/* Thumbnail area */
	.thumbnail {
		position: relative;
		aspect-ratio: 16 / 9;
		background: var(--bg-canvas);
		overflow: hidden;
		border-radius: var(--radius-xs);
	}

	.thumb-canvas {
		width: 100%;
		height: 100%;
		display: block;
	}

	.no-signal-text {
		position: absolute;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		font-family: var(--font-mono);
		font-size: 10px;
		font-weight: 500;
		color: var(--text-secondary);
		letter-spacing: 0.06em;
		opacity: 0.5;
		pointer-events: none;
	}

	/* Label row */
	.label-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 4px;
		padding: 6px 8px 2px;
		min-width: 0;
	}

	.source-label {
		font-size: 12px;
		font-weight: 500;
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		min-width: 0;
	}

	.tally-badge {
		flex-shrink: 0;
		font-size: 9px;
		font-weight: 700;
		letter-spacing: 0.04em;
		padding: 1px 4px;
		border-radius: var(--radius-xs);
		line-height: 1.2;
	}

	.tally-pgm {
		background: var(--tally-program);
		color: var(--text-on-color);
	}

	.tally-pvw {
		background: var(--tally-preview);
		color: var(--text-on-color);
	}

	/* Health row */
	.health-row {
		padding: 2px 8px 6px;
	}

	.single-health {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.engine-health {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.engine-line {
		display: flex;
		align-items: center;
		gap: 3px;
	}

	.engine-id {
		font-family: var(--font-mono);
		font-size: 10px;
		font-weight: 600;
		color: var(--text-secondary);
	}

	.health-dot {
		display: inline-block;
		width: 7px;
		height: 7px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.health-text {
		font-family: var(--font-mono);
		font-size: 10px;
		color: var(--text-secondary);
	}

	.single-health.warning .health-text {
		color: var(--accent-orange, #f59e0b);
	}

	.single-health.offline .health-text {
		color: var(--tally-program);
	}

	/* ── Compact mode ── */
	.oa-source-tile.compact {
		width: 150px;
		flex-shrink: 0;
	}

	.oa-source-tile.compact .thumbnail {
		height: 84px;
		aspect-ratio: auto;
	}

	.oa-source-tile.compact .label-row {
		padding: 3px 6px 1px;
	}

	.oa-source-tile.compact .source-label {
		font-size: 10px;
	}

	.oa-source-tile.compact .tally-badge {
		font-size: 8px;
		padding: 0 3px;
	}

	.compact-health {
		padding: 1px 6px 3px;
	}
</style>
