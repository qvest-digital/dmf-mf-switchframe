<script lang="ts">
	import type { ControlRoomState, SCTE35Active } from '$lib/api/types';
	import type { FastControl } from '$lib/transport/fast-control';
	import { setupHiDPICanvas } from '$lib/video/canvas-utils';
	import { getArPromptState } from '$lib/state/ar-prompt.svelte';
	import HealthAlarm from './HealthAlarm.svelte';
	import LayoutOverlay from './LayoutOverlay.svelte';
	import GraphicsOverlay from './GraphicsOverlay.svelte';

	interface Props {
		state: ControlRoomState;
		showLayoutOverlay?: boolean;
		showGraphicsOverlay?: boolean;
		onCanvasReady?: (previewCanvas: HTMLCanvasElement, programCanvas: HTMLCanvasElement) => void;
		onCaptionElReady?: (el: HTMLDivElement) => void;
		fastControl?: FastControl | null;
		onGraphicsLayerSelect?: (id: number) => void;
	}
	let { state: crState, showLayoutOverlay = false, showGraphicsOverlay = false, onCanvasReady, onCaptionElReady, fastControl = null, onGraphicsLayerSelect }: Props = $props();

	let previewCanvas: HTMLCanvasElement;
	let programCanvas: HTMLCanvasElement;
	let programViewport: HTMLDivElement;
	let captionOverlay: HTMLDivElement;
	let programViewportW = $state(0);
	let programViewportH = $state(0);

	let programSource = $derived(crState.sources[crState.programSource]);
	let programHealth = $derived(programSource?.status ?? 'healthy');
	let programLabel = $derived(programSource?.label || crState.programSource || '—');
	let previewSource = $derived(crState.sources[crState.previewSource]);
	let previewHealth = $derived(previewSource?.status ?? 'healthy');
	let previewLabel = $derived(previewSource?.label || crState.previewSource || '—');

	// SCTE-35 break status
	const scte35Active = $derived(
		Object.values(crState.scte35?.activeEvents ?? {}).filter(e => e.isOut)
	);
	const breakEvent = $derived(scte35Active.length > 0 ? scte35Active[0] : null);
	const isHeld = $derived(breakEvent?.held ?? false);

	// Use server-computed remainingMs directly. State broadcasts from the
	// playout tick loop arrive frequently enough for smooth countdown updates.
	const breakCountdownText = $derived.by(() => {
		const evt = breakEvent;
		if (!evt) return '';
		if (evt.held) return 'HELD';
		if (evt.remainingMs == null) return '';
		const remaining = evt.remainingMs;
		if (remaining <= 0) return '0:00';
		const totalSec = Math.ceil(remaining / 1000);
		const min = Math.floor(totalSec / 60);
		const sec = totalSec % 60;
		return `${min}:${sec.toString().padStart(2, '0')}`;
	});

	$effect(() => {
		if (previewCanvas && programCanvas && onCanvasReady) {
			onCanvasReady(previewCanvas, programCanvas);
		}
	});

	$effect(() => {
		if (captionOverlay && onCaptionElReady) {
			onCaptionElReady(captionOverlay);
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

	// Track program viewport dimensions for layout overlay
	$effect(() => {
		if (!programViewport) return;
		const obs = new ResizeObserver(([entry]) => {
			programViewportW = entry.contentRect.width;
			programViewportH = entry.contentRect.height;
		});
		obs.observe(programViewport);
		return () => obs.disconnect();
	});

	// ── AR Prompt overlay ──
	const arPrompt = getArPromptState();
	const arPromptActive = $derived(arPrompt.mode !== 'off');
	const arPlaceMode = $derived(arPrompt.mode === 'place');

	// Box drag state for shift+drag
	let arBoxDrag = $state<{ startX: number; startY: number; curX: number; curY: number } | null>(null);

	// Pipeline output dimensions for coordinate mapping (SAM prompts etc.)
	const pipeW = $derived(crState.pipelineFormat?.width ?? 1920);
	const pipeH = $derived(crState.pipelineFormat?.height ?? 1080);

	function arPromptClick(e: MouseEvent) {
		if (!programViewport) return;
		const rect = programViewport.getBoundingClientRect();
		const nx = (e.clientX - rect.left) / rect.width;
		const ny = (e.clientY - rect.top) / rect.height;
		const px = Math.round(nx * pipeW);
		const py = Math.round(ny * pipeH);

		// Place mode: call place callback with pixel coords
		if (arPrompt.mode === 'place' && arPrompt.placeCallback) {
			arPrompt.placeCallback(px, py);
			return;
		}

		// Prompt mode: call prompt callback
		if (!arPrompt.callback) return;
		// Shift+click starts/continues box mode — handled by pointer events
		if (e.shiftKey) return;
		arPrompt.callback('point', [[px, py]]);
	}

	function arBoxPointerDown(e: PointerEvent) {
		if (!arPrompt.callback || !programViewport || !e.shiftKey) return;
		e.preventDefault();
		(e.target as HTMLElement).setPointerCapture(e.pointerId);
		const rect = programViewport.getBoundingClientRect();
		const nx = (e.clientX - rect.left) / rect.width;
		const ny = (e.clientY - rect.top) / rect.height;
		arBoxDrag = { startX: nx, startY: ny, curX: nx, curY: ny };
	}

	function arBoxPointerMove(e: MouseEvent) {
		if (!arBoxDrag || !programViewport) return;
		const rect = programViewport.getBoundingClientRect();
		arBoxDrag.curX = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
		arBoxDrag.curY = Math.max(0, Math.min(1, (e.clientY - rect.top) / rect.height));
	}

	function arBoxPointerUp() {
		if (!arBoxDrag || !arPrompt.callback) {
			arBoxDrag = null;
			return;
		}
		const x1 = Math.round(Math.min(arBoxDrag.startX, arBoxDrag.curX) * pipeW);
		const y1 = Math.round(Math.min(arBoxDrag.startY, arBoxDrag.curY) * pipeH);
		const x2 = Math.round(Math.max(arBoxDrag.startX, arBoxDrag.curX) * pipeW);
		const y2 = Math.round(Math.max(arBoxDrag.startY, arBoxDrag.curY) * pipeH);
		if (Math.abs(x2 - x1) > 5 && Math.abs(y2 - y1) > 5) {
			arPrompt.callback('box', [[x1, y1, x2, y2]]);
		}
		arBoxDrag = null;
	}
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
		{#if breakEvent}
			<div class="break-status" class:break-held={isHeld}>
				<span class="break-icon">{isHeld ? '||' : breakEvent.autoReturn ? 'AR' : 'BR'}</span>
				<span class="break-label">{isHeld ? 'HELD' : 'AD BREAK'}</span>
				<span class="break-countdown">{breakCountdownText}</span>
			</div>
		{/if}
		<div class="monitor-viewport" bind:this={programViewport}>
			<canvas bind:this={programCanvas}></canvas>
			<div class="caption-overlay" bind:this={captionOverlay}></div>
			<div class="source-label">{programLabel}</div>
			<HealthAlarm health={programHealth} sourceLabel={programLabel} variant="critical" label="PROGRAM" />
			{#if showLayoutOverlay && crState.layout?.slots?.length}
				<LayoutOverlay state={crState} containerWidth={programViewportW} containerHeight={programViewportH} {fastControl} />
			{/if}
			{#if showGraphicsOverlay && crState.graphics?.layers?.some(l => l.active)}
				<GraphicsOverlay state={crState} containerWidth={programViewportW} containerHeight={programViewportH} {fastControl} onSelect={onGraphicsLayerSelect} />
			{/if}
			{#if arPromptActive}
				<!-- svelte-ignore a11y_no_static_element_interactions a11y_no_noninteractive_element_interactions -->
				<div
					class="ar-prompt-overlay"
					class:place-mode={arPlaceMode}
					role="application"
					tabindex="-1"
					onclick={arPromptClick}
					onkeydown={(e) => { if (e.key === 'Enter') arPromptClick(e as any); }}
					onpointerdown={arBoxPointerDown}
					onpointermove={arBoxPointerMove}
					onpointerup={arBoxPointerUp}
				>
					{#each arPrompt.prompts as marker (marker.id)}
						<div
							class="ar-prompt-marker"
							style="left:{(marker.x / pipeW) * 100}%;top:{(marker.y / pipeH) * 100}%"
							title="{marker.label}"
						></div>
					{/each}
					{#if arBoxDrag}
						{@const left = Math.min(arBoxDrag.startX, arBoxDrag.curX) * 100}
						{@const top = Math.min(arBoxDrag.startY, arBoxDrag.curY) * 100}
						{@const width = Math.abs(arBoxDrag.curX - arBoxDrag.startX) * 100}
						{@const height = Math.abs(arBoxDrag.curY - arBoxDrag.startY) * 100}
						<div
							class="ar-prompt-box"
							style="left:{left}%;top:{top}%;width:{width}%;height:{height}%"
						></div>
					{/if}
					<div class="ar-prompt-hint">
						{#if arPlaceMode}
							Click to place object in scene.
						{:else}
							Click to place point. Shift+drag for box.
						{/if}
					</div>
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	.program-preview {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 3px;
		padding: 3px;
		height: 100%;
		align-content: center;
	}

	.monitor {
		aspect-ratio: 16 / 9;
		background: var(--bg-canvas);
		border-radius: var(--radius-sm);
		overflow: hidden;
		position: relative;
		border: 1px solid var(--border-default);
		box-shadow: var(--shadow-inset);
		max-height: 100%;
	}

	.monitor-label {
		position: absolute;
		top: 6px;
		left: 6px;
		font-family: var(--font-ui);
		font-weight: 700;
		font-size: var(--text-xs);
		letter-spacing: 0.08em;
		padding: 1px 7px;
		border-radius: var(--radius-xs);
		z-index: var(--z-above);
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
		border-color: rgba(22, 163, 74, 0.25);
	}

	.program-monitor {
		border-color: rgba(220, 38, 38, 0.25);
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
		display: block;
	}

	.caption-overlay {
		position: absolute;
		top: 0;
		left: 0;
		width: 100%;
		height: 100%;
		pointer-events: none;
		z-index: var(--z-above);
		font-family: 'Courier New', monospace;
		font-size: clamp(12px, 2.2vw, 22px);
		line-height: 1.3;
		overflow: hidden;
	}

	/* Prism CaptionRenderer injects .cc-line and .cc-region children */
	.caption-overlay :global(.cc-line) {
		padding: 1px 4px;
	}

	.caption-overlay :global(.cc-608-line) {
		text-align: center;
	}

	.caption-overlay :global(.cc-608-line span),
	.caption-overlay :global(.cc-708-region span) {
		background: rgba(0, 0, 0, 0.75);
		color: #fff;
		padding: 0 2px;
	}

	.caption-overlay :global(.cc-region) {
		position: absolute;
		padding: 2px 4px;
	}

	.caption-overlay :global(.cc-flash) {
		animation: cc-blink 1s step-end infinite;
	}

	@keyframes cc-blink {
		50% { opacity: 0; }
	}

	.source-label {
		position: absolute;
		bottom: 6px;
		left: 6px;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		font-weight: 500;
		color: rgba(255, 255, 255, 0.9);
		background: var(--overlay-heavy);
		padding: 2px 7px;
		border-radius: var(--radius-xs);
		pointer-events: none;
		z-index: var(--z-above);
		letter-spacing: 0.02em;
	}

	/* SCTE-35 Break Status */
	.break-status {
		position: absolute;
		top: 8px;
		right: 8px;
		display: flex;
		align-items: center;
		gap: 5px;
		padding: 3px 8px;
		background: rgba(220, 38, 38, 0.85);
		border-radius: var(--radius-sm);
		z-index: var(--z-above);
		animation: break-pulse 1.5s ease-in-out infinite;
	}

	.break-status.break-held {
		background: rgba(245, 158, 11, 0.85);
		animation: none;
	}

	@keyframes break-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.7; }
	}

	.break-icon {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 700;
		color: rgba(255, 255, 255, 0.7);
	}

	.break-label {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		color: #fff;
		letter-spacing: 0.04em;
	}

	.break-countdown {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		font-weight: 700;
		color: #fff;
		min-width: 2.5em;
		text-align: right;
	}

	/* AR Prompt Overlay */
	.ar-prompt-overlay {
		position: absolute;
		top: 0;
		left: 0;
		width: 100%;
		height: 100%;
		cursor: crosshair;
		z-index: calc(var(--z-above) + 1);
		pointer-events: auto;
	}

	.ar-prompt-overlay.place-mode {
		cursor: cell;
	}

	.ar-prompt-marker {
		position: absolute;
		width: 12px;
		height: 12px;
		border: 2px solid var(--accent-purple, #a855f7);
		border-radius: 50%;
		transform: translate(-50%, -50%);
		pointer-events: none;
	}

	.ar-prompt-box {
		position: absolute;
		border: 2px dashed var(--accent-purple, #a855f7);
		background: rgba(168, 85, 247, 0.1);
		pointer-events: none;
	}

	.ar-prompt-hint {
		position: absolute;
		bottom: 24px;
		left: 50%;
		transform: translateX(-50%);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: rgba(168, 85, 247, 0.9);
		background: rgba(0, 0, 0, 0.7);
		padding: 2px 8px;
		border-radius: var(--radius-sm);
		pointer-events: none;
		white-space: nowrap;
	}
</style>
