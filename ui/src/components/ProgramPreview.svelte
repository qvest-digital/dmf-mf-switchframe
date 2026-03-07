<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { setupHiDPICanvas } from '$lib/video/canvas-utils';
	import HealthAlarm from './HealthAlarm.svelte';

	interface Props {
		state: ControlRoomState;
		onCanvasReady?: (previewCanvas: HTMLCanvasElement, programCanvas: HTMLCanvasElement) => void;
	}
	let { state, onCanvasReady }: Props = $props();

	let previewCanvas: HTMLCanvasElement;
	let programCanvas: HTMLCanvasElement;

	let programSource = $derived(state.sources[state.programSource]);
	let programHealth = $derived(programSource?.status ?? 'healthy');
	let programLabel = $derived(programSource?.label || state.programSource || '—');
	let previewSource = $derived(state.sources[state.previewSource]);
	let previewHealth = $derived(previewSource?.status ?? 'healthy');
	let previewLabel = $derived(previewSource?.label || state.previewSource || '—');

	$effect(() => {
		if (previewCanvas && programCanvas && onCanvasReady) {
			onCanvasReady(previewCanvas, programCanvas);
		}
	});

	// High-DPI canvas sizing via ResizeObserver
	$effect(() => {
		const canvases = [previewCanvas, programCanvas].filter(Boolean) as HTMLCanvasElement[];
		if (canvases.length === 0) return;

		const observers: ResizeObserver[] = [];

		// Preview canvas
		if (previewCanvas?.parentElement) {
			const obs = new ResizeObserver(([entry]) => {
				const { width, height } = entry.contentRect;
				if (width > 0 && height > 0) setupHiDPICanvas(previewCanvas, width, height);
			});
			obs.observe(previewCanvas.parentElement);
			observers.push(obs);
		}

		// Program canvas
		if (programCanvas?.parentElement) {
			const obs = new ResizeObserver(([entry]) => {
				const { width, height } = entry.contentRect;
				if (width > 0 && height > 0) {
					setupHiDPICanvas(programCanvas, width, height);
				}
			});
			obs.observe(programCanvas.parentElement);
			observers.push(obs);
		}

		return () => observers.forEach((obs) => obs.disconnect());
	});
</script>

<div class="program-preview">
	<div class="monitor preview-monitor">
		<div class="monitor-label preview-label">PREVIEW</div>
		<div class="monitor-viewport">
			<canvas bind:this={previewCanvas}></canvas>
			<div class="source-label">{previewLabel}</div>
			<HealthAlarm health={previewHealth} sourceLabel={previewLabel} variant="warning" label="PREVIEW" />
		</div>
	</div>
	<div class="monitor program-monitor">
		<div class="monitor-label program-label">PROGRAM</div>
		<div class="monitor-viewport">
			<canvas bind:this={programCanvas}></canvas>
			<div class="source-label">{programLabel}</div>
			<HealthAlarm health={programHealth} sourceLabel={programLabel} variant="critical" label="PROGRAM" />
		</div>
	</div>
</div>

<style>
	.program-preview {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 6px;
		padding: 6px;
		height: 100%;
		align-content: center;
	}

	.monitor {
		aspect-ratio: 16 / 9;
		background: #050507;
		border-radius: var(--radius-md);
		overflow: hidden;
		position: relative;
		border: 1px solid var(--border-subtle);
		box-shadow: var(--shadow-inset);
		max-height: 100%;
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

	.source-label {
		position: absolute;
		bottom: 8px;
		left: 8px;
		font-family: var(--font-mono);
		font-size: 0.75rem;
		font-weight: 500;
		color: #fff;
		background: rgba(0, 0, 0, 0.6);
		padding: 2px 8px;
		border-radius: var(--radius-sm);
		pointer-events: none;
		z-index: 2;
		letter-spacing: 0.02em;
	}
</style>
