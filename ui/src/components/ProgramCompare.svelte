<script lang="ts">
	interface EngineMeta {
		rtt: number;
		seq: number;
		healthy: boolean;
	}

	interface Props {
		engineAMeta?: EngineMeta;
		engineBMeta?: EngineMeta;
		/** Pipeline format width (e.g. 1920 for 1080p). Defaults to 960 (half 1080p). */
		formatWidth?: number;
		/** Pipeline format height (e.g. 1080 for 1080p). Defaults to 540 (half 1080p). */
		formatHeight?: number;
		onExit?: () => void;
		onCanvasAReady?: (canvas: HTMLCanvasElement) => void;
		onCanvasBReady?: (canvas: HTMLCanvasElement) => void;
	}

	let { engineAMeta, engineBMeta, formatWidth, formatHeight, onExit, onCanvasAReady, onCanvasBReady }: Props = $props();

	// Canvas backing store uses half the pipeline format resolution to save decode/render resources
	// while still matching the correct aspect ratio. CSS object-fit:contain handles display scaling.
	const canvasWidth = $derived(formatWidth ? Math.round(formatWidth / 2) : 960);
	const canvasHeight = $derived(formatHeight ? Math.round(formatHeight / 2) : 540);

	let canvasA: HTMLCanvasElement;
	let canvasB: HTMLCanvasElement;

	$effect(() => {
		if (canvasA) onCanvasAReady?.(canvasA);
	});
	$effect(() => {
		if (canvasB) onCanvasBReady?.(canvasB);
	});

	const rttA = $derived(engineAMeta?.rtt != null ? `${engineAMeta.rtt}ms` : '--');
	const rttB = $derived(engineBMeta?.rtt != null ? `${engineBMeta.rtt}ms` : '--');
	const seqA = $derived(engineAMeta?.seq != null ? `${engineAMeta.seq}` : '--');
	const seqB = $derived(engineBMeta?.seq != null ? `${engineBMeta.seq}` : '--');
	const healthyA = $derived(engineAMeta?.healthy ?? false);
	const healthyB = $derived(engineBMeta?.healthy ?? false);
</script>

<div class="program-compare">
	<div class="compare-columns">
		<div class="engine-column">
			<div class="engine-label">ENGINE A</div>
			<div class="video-placeholder">
				<canvas
					class="compare-canvas"
					width={canvasWidth} height={canvasHeight}
					bind:this={canvasA}
				></canvas>
			</div>
			<div class="engine-meta">
				<span class="meta-row">
					<span class="meta-label">RTT:</span>
					<span class="meta-value">{rttA}</span>
				</span>
				<span class="meta-row">
					<span class="meta-label">Seq:</span>
					<span class="meta-value">{seqA}</span>
				</span>
				<span class="meta-row">
					<span class="health-dot" class:healthy={healthyA} class:unhealthy={!healthyA}></span>
					<span class="meta-value">{healthyA ? 'healthy' : 'unhealthy'}</span>
				</span>
			</div>
		</div>
		<div class="engine-column">
			<div class="engine-label">ENGINE B</div>
			<div class="video-placeholder">
				<canvas
					class="compare-canvas"
					width={canvasWidth} height={canvasHeight}
					bind:this={canvasB}
				></canvas>
			</div>
			<div class="engine-meta">
				<span class="meta-row">
					<span class="meta-label">RTT:</span>
					<span class="meta-value">{rttB}</span>
				</span>
				<span class="meta-row">
					<span class="meta-label">Seq:</span>
					<span class="meta-value">{seqB}</span>
				</span>
				<span class="meta-row">
					<span class="health-dot" class:healthy={healthyB} class:unhealthy={!healthyB}></span>
					<span class="meta-value">{healthyB ? 'healthy' : 'unhealthy'}</span>
				</span>
			</div>
		</div>
	</div>
	<div class="compare-footer">
		<button class="exit-btn" onclick={() => onExit?.()}>Exit Compare</button>
	</div>
</div>

<style>
	.program-compare {
		width: 100%;
		height: 100%;
		display: flex;
		flex-direction: column;
		background: var(--bg-canvas);
		border-radius: var(--radius-sm);
		overflow: hidden;
	}

	.compare-columns {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 2px;
		flex: 1;
		padding: 3px;
		min-height: 0;
	}

	.engine-column {
		display: flex;
		flex-direction: column;
		background: var(--bg-surface);
		border-radius: var(--radius-sm);
		overflow: hidden;
	}

	.engine-label {
		padding: 4px 8px;
		font-family: var(--font-ui);
		font-weight: 700;
		font-size: var(--text-xs);
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--text-primary);
		background: var(--bg-recessed, #1a1a1a);
	}

	.video-placeholder {
		position: relative;
		width: 100%;
		aspect-ratio: 16 / 9;
		background: var(--bg-canvas);
		flex-shrink: 0;
	}

	.compare-canvas {
		position: absolute;
		top: 0;
		left: 0;
		width: 100%;
		height: 100%;
		object-fit: contain;
	}

	.engine-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
		padding: 4px 8px;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-dim, #888);
		background: var(--bg-recessed, #1a1a1a);
	}

	.meta-row {
		display: inline-flex;
		align-items: center;
		gap: 4px;
	}

	.meta-label {
		color: var(--text-dim, #666);
	}

	.meta-value {
		color: var(--text-secondary);
	}

	.health-dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		display: inline-block;
		flex-shrink: 0;
	}

	.health-dot.healthy {
		background: #22c55e;
	}

	.health-dot.unhealthy {
		background: #ef4444;
	}

	.compare-footer {
		display: flex;
		justify-content: center;
		padding: 6px;
	}

	.exit-btn {
		padding: 4px 16px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 600;
		cursor: pointer;
		letter-spacing: 0.02em;
	}

	.exit-btn:hover {
		background: var(--bg-hover);
	}
</style>
