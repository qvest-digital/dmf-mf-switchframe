<script lang="ts">
	interface Props {
		programSource: string;
		isDualEngine?: boolean;
		activeEngine?: 'A' | 'B';
		authoritativeEngine?: 'A' | 'B';
		onEngineToggle?: (engine: 'A' | 'B') => void;
		onCanvasReady?: (canvas: HTMLCanvasElement) => void;
	}

	let {
		programSource,
		isDualEngine = false,
		activeEngine = 'A',
		authoritativeEngine = 'A',
		onEngineToggle,
		onCanvasReady,
	}: Props = $props();

	let canvasEl: HTMLCanvasElement;

	$effect(() => {
		if (canvasEl && onCanvasReady) {
			onCanvasReady(canvasEl);
		}
	});
</script>

<div class="oa-program-wrap">
	<div class="oa-canvas-container">
		<canvas
			bind:this={canvasEl}
			class="oa-program-canvas"
			width="1920"
			height="1080"
		></canvas>

		<div class="source-label">{programSource}</div>

		{#if isDualEngine}
			<div class="engine-toggle">
				<button
					class="engine-btn"
					class:active={activeEngine === 'A'}
					onclick={() => onEngineToggle?.('A')}
				>{authoritativeEngine === 'A' ? '\u2605 ' : ''}A</button>
				<button
					class="engine-btn"
					class:active={activeEngine === 'B'}
					onclick={() => onEngineToggle?.('B')}
				>{authoritativeEngine === 'B' ? '\u2605 ' : ''}B</button>
			</div>
		{/if}
	</div>
</div>

<style>
	.oa-program-wrap {
		position: relative;
		flex: 1;
		display: flex;
		align-items: center;
		justify-content: center;
		background: var(--bg-canvas);
		overflow: hidden;
		min-height: 0;
	}

	.oa-canvas-container {
		position: relative;
		width: 100%;
		max-height: 100%;
		aspect-ratio: 16 / 9;
	}

	.oa-program-canvas {
		width: 100%;
		height: 100%;
		display: block;
		border: 2px solid var(--tally-program);
		box-shadow: var(--tally-program-glow);
	}

	/* ── Source label pill ── */
	.source-label {
		position: absolute;
		bottom: 8px;
		left: 8px;
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 600;
		color: var(--text-on-color);
		background: rgba(0, 0, 0, 0.55);
		padding: 2px 8px;
		border-radius: var(--radius-xs);
		pointer-events: none;
		z-index: var(--z-above);
		letter-spacing: 0.02em;
	}

	/* ── Engine A/B toggle ── */
	.engine-toggle {
		position: absolute;
		bottom: 8px;
		right: 8px;
		display: inline-flex;
		gap: 1px;
		background: var(--bg-recessed);
		border-radius: var(--radius-sm);
		padding: 1px;
		z-index: var(--z-above);
	}

	.engine-btn {
		padding: 2px 8px;
		border: none;
		border-radius: var(--radius-xs, 2px);
		background: transparent;
		color: var(--text-dim);
		font-family: var(--font-mono);
		font-size: 11px;
		font-weight: 600;
		cursor: pointer;
		line-height: 1.2;
		transition:
			background var(--transition-fast),
			color var(--transition-fast);
	}

	.engine-btn:hover {
		color: var(--text-primary);
	}

	.engine-btn.active {
		background: var(--bg-elevated);
		color: var(--text-primary);
	}
</style>
