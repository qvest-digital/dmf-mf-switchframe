<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import type { GraphicsTemplate } from '$lib/graphics/templates';
	import HealthAlarm from './HealthAlarm.svelte';

	interface Props {
		state: ControlRoomState;
		onCanvasReady?: (previewCanvas: HTMLCanvasElement, programCanvas: HTMLCanvasElement) => void;
		graphicsTemplate?: GraphicsTemplate | null;
		graphicsValues?: Record<string, string>;
	}
	let { state, onCanvasReady, graphicsTemplate = null, graphicsValues = {} }: Props = $props();

	let previewCanvas: HTMLCanvasElement;
	let programCanvas: HTMLCanvasElement;
	let overlayCanvas: HTMLCanvasElement;

	let programSource = $derived(state.sources[state.programSource]);
	let programHealth = $derived(programSource?.status ?? 'healthy');
	let programLabel = $derived(programSource?.label || state.programSource || '—');
	let graphicsActive = $derived(state.graphics?.active ?? false);

	$effect(() => {
		if (previewCanvas && programCanvas && onCanvasReady) {
			onCanvasReady(previewCanvas, programCanvas);
		}
	});

	// Render graphics overlay on the program monitor when active
	$effect(() => {
		if (!overlayCanvas) return;
		const ctx = overlayCanvas.getContext('2d');
		if (!ctx) return;
		ctx.clearRect(0, 0, overlayCanvas.width, overlayCanvas.height);
		if (graphicsActive && graphicsTemplate) {
			graphicsTemplate.render(ctx, overlayCanvas.width, overlayCanvas.height, graphicsValues);
		}
	});
</script>

<div class="program-preview">
	<div class="monitor preview-monitor">
		<div class="monitor-label preview-label">PREVIEW</div>
		<div class="monitor-viewport">
			<canvas bind:this={previewCanvas} width="640" height="360"></canvas>
			<span class="source-name">{state.sources[state.previewSource]?.label || state.previewSource || '—'}</span>
		</div>
	</div>
	<div class="monitor program-monitor">
		<div class="monitor-label program-label">PROGRAM</div>
		<div class="monitor-viewport">
			<canvas bind:this={programCanvas} width="640" height="360"></canvas>
			<canvas bind:this={overlayCanvas} class="graphics-overlay" width="640" height="360"></canvas>
			<span class="source-name">{programLabel}</span>
			<HealthAlarm health={programHealth} sourceLabel={programLabel} />
		</div>
	</div>
</div>

<style>
	.program-preview {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 6px;
		padding: 6px;
	}

	.monitor {
		aspect-ratio: 16 / 9;
		background: #050507;
		border-radius: var(--radius-md);
		overflow: hidden;
		position: relative;
		border: 1px solid var(--border-subtle);
		box-shadow: var(--shadow-inset);
	}

	.monitor-label {
		position: absolute;
		top: 8px;
		left: 8px;
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: 0.65rem;
		letter-spacing: 0.06em;
		padding: 2px 8px;
		border-radius: var(--radius-sm);
		z-index: 2;
		text-transform: uppercase;
	}

	.preview-label {
		background: var(--tally-preview);
		color: var(--text-on-color);
	}

	.program-label {
		background: var(--tally-program);
		color: var(--text-on-color);
	}

	.preview-monitor {
		border-color: rgba(22, 163, 74, 0.2);
	}

	.program-monitor {
		border-color: rgba(220, 38, 38, 0.2);
	}

	.monitor-viewport {
		width: 100%;
		height: 100%;
		display: flex;
		align-items: center;
		justify-content: center;
		position: relative;
	}

	.monitor-viewport canvas {
		position: absolute;
		top: 0;
		left: 0;
		width: 100%;
		height: 100%;
		object-fit: contain;
	}

	.graphics-overlay {
		position: absolute;
		top: 0;
		left: 0;
		width: 100%;
		height: 100%;
		object-fit: contain;
		pointer-events: none;
		z-index: 1;
	}

	.source-name {
		font-family: var(--font-mono);
		font-size: 1.25rem;
		font-weight: 500;
		color: var(--text-tertiary);
		position: relative;
		z-index: 1;
		pointer-events: none;
		letter-spacing: 0.02em;
	}
</style>
