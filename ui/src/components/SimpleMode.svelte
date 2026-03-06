<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { setPreview, cut, startTransition, apiCall } from '$lib/api/switch-api';
	import { setupHiDPICanvas } from '$lib/video/canvas-utils';

	interface Props {
		state: ControlRoomState;
		onSwitchLayout?: () => void;
		onCanvasReady?: (previewCanvas: HTMLCanvasElement, programCanvas: HTMLCanvasElement) => void;
		onPreview?: (key: string) => void;
		onCut?: () => void;
		onDissolve?: () => void;
	}

	let { state, onSwitchLayout, onCanvasReady, onPreview, onCut, onDissolve }: Props = $props();

	let previewCanvas: HTMLCanvasElement;
	let programCanvas: HTMLCanvasElement;

	$effect(() => {
		if (previewCanvas && programCanvas && onCanvasReady) {
			onCanvasReady(previewCanvas, programCanvas);
		}
	});

	// High-DPI canvas sizing via ResizeObserver
	$effect(() => {
		if (!previewCanvas || !programCanvas) return;

		const observers: ResizeObserver[] = [];

		// Preview canvas
		if (previewCanvas.parentElement) {
			const obs = new ResizeObserver(([entry]) => {
				const { width, height } = entry.contentRect;
				if (width > 0 && height > 0) setupHiDPICanvas(previewCanvas, width, height);
			});
			obs.observe(previewCanvas.parentElement);
			observers.push(obs);
		}

		// Program canvas
		if (programCanvas.parentElement) {
			const obs = new ResizeObserver(([entry]) => {
				const { width, height } = entry.contentRect;
				if (width > 0 && height > 0) setupHiDPICanvas(programCanvas, width, height);
			});
			obs.observe(programCanvas.parentElement);
			observers.push(obs);
		}

		return () => observers.forEach((obs) => obs.disconnect());
	});

	let sourceKeys = $derived(Object.keys(state.sources).sort());
	let previewLabel = $derived(
		state.previewSource && state.sources[state.previewSource]
			? state.sources[state.previewSource].label || state.previewSource
			: '—',
	);
	let programLabel = $derived(
		state.programSource && state.sources[state.programSource]
			? state.sources[state.programSource].label || state.programSource
			: '—',
	);
	let canTransition = $derived(
		state.previewSource !== '' && !state.inTransition && !state.ftbActive,
	);
	let canCut = $derived(state.previewSource !== '' && !state.inTransition);

	function handleSourceClick(key: string) {
		if (onPreview) {
			onPreview(key);
		} else {
			apiCall(setPreview(key), 'Preview failed');
		}
	}

	function handleCut() {
		if (!canCut) return;
		if (onCut) {
			onCut();
		} else {
			apiCall(cut(state.previewSource), 'Cut failed');
		}
	}

	function handleDissolve() {
		if (!canTransition) return;
		if (onDissolve) {
			onDissolve();
		} else {
			apiCall(startTransition(state.previewSource, 'mix', 1000), 'Dissolve failed');
		}
	}

	function tallyClass(key: string): string {
		const tally = state.tallyState[key];
		if (tally === 'program') return 'tally-program';
		if (tally === 'preview') return 'tally-preview';
		return '';
	}
</script>

<div class="simple-mode">
	<header class="simple-header">
		<button class="gear-btn" onclick={onSwitchLayout} title="Switch to traditional mode">
			&#9881;
		</button>
		<span class="brand">SwitchFrame</span>
	</header>

	<section class="monitors">
		<div class="monitor preview-mon">
			<div class="monitor-label preview-label">PREVIEW</div>
			<canvas bind:this={previewCanvas}></canvas>
			<div class="monitor-source">{previewLabel}</div>
		</div>
		<div class="monitor program-mon">
			<div class="monitor-label program-label">PROGRAM</div>
			<canvas bind:this={programCanvas}></canvas>
			<div class="monitor-source">{programLabel}</div>
		</div>
	</section>

	<section class="source-buttons">
		{#each sourceKeys as key, i}
			<button
				class="source-btn {tallyClass(key)}"
				onclick={() => handleSourceClick(key)}
			>
				<span class="source-number">{i + 1}</span>
				{state.sources[key].label || key}
			</button>
		{/each}
	</section>

	<section class="action-buttons">
		<button class="action-btn cut-btn" onclick={handleCut} disabled={!canCut}>
			CUT
		</button>
		<button
			class="action-btn dissolve-btn"
			onclick={handleDissolve}
			disabled={!canTransition}
		>
			DISSOLVE
		</button>
	</section>
</div>

<style>
	.simple-mode {
		display: flex;
		flex-direction: column;
		height: 100vh;
		background: var(--bg-base);
		color: var(--text-primary);
		font-family: var(--font-ui);
	}

	.simple-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 16px;
		border-bottom: 1px solid var(--border-subtle);
		background: var(--bg-surface);
	}

	.gear-btn {
		background: none;
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		font-size: 1.2rem;
		cursor: pointer;
		padding: 4px 8px;
		border-radius: var(--radius-md);
		transition:
			color var(--transition-fast),
			border-color var(--transition-fast);
	}

	.gear-btn:hover {
		color: var(--text-primary);
		border-color: var(--border-strong);
	}

	.brand {
		font-size: 0.85rem;
		font-weight: 600;
		color: var(--text-tertiary);
		letter-spacing: 0.03em;
	}

	.monitors {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px;
		padding: 12px;
		flex: 1;
		min-height: 0;
	}

	.monitor {
		position: relative;
		background: #050507;
		border-radius: var(--radius-md);
		overflow: hidden;
		border: 1px solid var(--border-subtle);
		box-shadow: var(--shadow-inset);
	}

	.preview-mon {
		border-color: rgba(22, 163, 74, 0.15);
	}

	.program-mon {
		border-color: rgba(220, 38, 38, 0.15);
	}

	.monitor canvas {
		width: 100%;
		height: 100%;
		object-fit: contain;
	}

	.monitor-label {
		position: absolute;
		top: 8px;
		left: 8px;
		padding: 2px 8px;
		font-size: 0.65rem;
		font-weight: 600;
		letter-spacing: 0.06em;
		border-radius: var(--radius-sm);
		z-index: 1;
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

	.monitor-source {
		position: absolute;
		bottom: 8px;
		left: 8px;
		font-size: 0.75rem;
		font-weight: 500;
		color: var(--text-secondary);
		font-family: var(--font-mono);
	}

	.source-buttons {
		display: flex;
		gap: 6px;
		padding: 10px 12px;
		border-top: 1px solid var(--border-subtle);
		flex-wrap: wrap;
		background: var(--bg-surface);
	}

	.source-btn {
		flex: 1;
		min-width: 100px;
		min-height: 44px;
		padding: 10px 12px;
		background: var(--bg-elevated);
		color: var(--text-primary);
		border: 1.5px solid var(--border-default);
		border-radius: var(--radius-md);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: 0.85rem;
		font-weight: 500;
		text-align: center;
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast),
			box-shadow var(--transition-normal);
	}

	.source-btn:hover {
		border-color: var(--border-strong);
		background: var(--bg-hover);
	}

	.source-btn:active {
		transform: scale(0.97);
	}

	.source-btn .source-number {
		font-weight: 700;
		margin-right: 6px;
		color: var(--text-tertiary);
		font-family: var(--font-mono);
	}

	.tally-preview {
		border-color: var(--tally-preview);
		background: var(--tally-preview-dim);
		box-shadow: var(--tally-preview-glow);
	}

	.tally-program {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
		box-shadow: var(--tally-program-glow);
	}

	.action-buttons {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px;
		padding: 10px 12px 14px;
		border-top: 1px solid var(--border-subtle);
		background: var(--bg-surface);
	}

	.action-btn {
		padding: clamp(12px, 3vw, 20px);
		min-height: 44px;
		font-family: var(--font-ui);
		font-size: clamp(0.85rem, 2.5vw, 1.1rem);
		font-weight: 700;
		letter-spacing: 0.06em;
		border: 1.5px solid;
		border-radius: var(--radius-md);
		cursor: pointer;
		transition:
			background var(--transition-fast),
			box-shadow var(--transition-normal),
			transform var(--transition-fast);
	}

	.action-btn:active:not(:disabled) {
		transform: scale(0.97);
	}

	.action-btn:disabled {
		opacity: 0.3;
		cursor: not-allowed;
	}

	.cut-btn {
		background: var(--tally-program);
		color: var(--text-on-color);
		border-color: #ef4444;
	}

	.cut-btn:hover:not(:disabled) {
		background: #ef4444;
		box-shadow: 0 0 16px rgba(220, 38, 38, 0.3);
	}

	.dissolve-btn {
		background: rgba(59, 130, 246, 0.2);
		color: var(--text-on-color);
		border-color: var(--accent-blue);
	}

	.dissolve-btn:hover:not(:disabled) {
		background: rgba(59, 130, 246, 0.3);
		box-shadow: 0 0 16px rgba(59, 130, 246, 0.25);
	}

	/* Stack monitors vertically on narrow viewports */
	@media (max-width: 767px) {
		.monitors {
			grid-template-columns: 1fr;
			gap: 4px;
			padding: 6px;
		}

		.source-buttons {
			gap: 4px;
			padding: 6px;
		}

		.action-buttons {
			gap: 6px;
			padding: 6px 8px 10px;
		}
	}

	/* Touch: disable hover states on touch devices */
	@media (pointer: coarse) {
		.source-btn:hover {
			border-color: var(--border-default);
			background: var(--bg-elevated);
		}

		.tally-preview:hover {
			border-color: var(--tally-preview);
			background: var(--tally-preview-dim);
		}

		.tally-program:hover {
			border-color: var(--tally-program);
			background: var(--tally-program-dim);
		}
	}
</style>
