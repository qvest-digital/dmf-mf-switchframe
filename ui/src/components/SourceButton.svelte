<script lang="ts">
	import type { SourceInfo, TallyStatus, LayoutSlotState } from '$lib/api/types';
	import { setupHiDPICanvas } from '$lib/video/canvas-utils';
	import { computeSRTHealth } from '$lib/util/srt-health';
	import { isSourceStalled } from '$lib/transport/source-errors.svelte';
	import SRTHealthDot from './SRTHealthDot.svelte';
	import SRTStatsPopover from './SRTStatsPopover.svelte';

	interface Props {
		source: SourceInfo;
		tally: TallyStatus;
		index: number;
		audioLevelDb?: number;
		layoutSlots?: LayoutSlotState[];
		onPreview?: () => void;
		onCut?: () => void;
		onLabelChange?: (key: string, label: string) => void;
		onSRTClick?: (e: MouseEvent) => void;
	}

	let { source, tally, index, audioLevelDb = -96, layoutSlots, onPreview, onCut, onLabelChange, onSRTClick }: Props = $props();

	let isPIP = $derived(layoutSlots?.some(s => s.enabled && s.sourceKey === source.key) ?? false);
	let srtHealth = $derived(computeSRTHealth(source.srt));
	let stalled = $derived(isSourceStalled(source.key));

	let canvasEl: HTMLCanvasElement;
	let wrapEl: HTMLDivElement;

	$effect(() => {
		if (!canvasEl || !wrapEl) return;
		const obs = new ResizeObserver(([entry]) => {
			const { width, height } = entry.contentRect;
			if (width > 0 && height > 0) setupHiDPICanvas(canvasEl, width, height);
		});
		obs.observe(wrapEl);
		return () => obs.disconnect();
	});

	let editing = $state(false);
	let editValue = $state('');
	let inputEl: HTMLInputElement | undefined = $state();

	function startEditing(e: MouseEvent) {
		e.stopPropagation();
		e.preventDefault();
		editing = true;
		editValue = source.label || source.key;
		queueMicrotask(() => inputEl?.select());
	}

	function commitEdit() {
		if (!editing) return;
		editing = false;
		const trimmed = editValue.trim();
		if (trimmed && trimmed !== (source.label || source.key)) {
			onLabelChange?.(source.key, trimmed);
		}
	}

	function cancelEdit() {
		editing = false;
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') { e.preventDefault(); commitEdit(); }
		else if (e.key === 'Escape') { e.preventDefault(); cancelEdit(); }
	}

	function dbToBarPercent(db: number): number {
		const min = -60, max = 0;
		if (db <= min) return 0;
		if (db >= max) return 100;
		return ((db - min) / (max - min)) * 100;
	}

	function barColor(db: number): string {
		if (db > -3) return '#ef4444';
		if (db > -12) return '#eab308';
		return '#22c55e';
	}

	let barPercent = $derived(dbToBarPercent(audioLevelDb));
	let barFill = $derived(barColor(audioLevelDb));
	let showBar = $derived(audioLevelDb > -60);

	let clickTimer: ReturnType<typeof setTimeout> | null = null;
	function handleClick() {
		if (editing) return;
		if (clickTimer) {
			clearTimeout(clickTimer);
			clickTimer = null;
			onCut?.();
		} else {
			clickTimer = setTimeout(() => {
				clickTimer = null;
				onPreview?.();
			}, 250);
		}
	}

	let srtPopoverKey: string | null = $state(null);
	let srtPopoverPos = $state({ x: 0, y: 0 });

	function handleSRTClick(e: MouseEvent) {
		e.stopPropagation();
		if (srtPopoverKey) {
			srtPopoverKey = null;
		} else {
			srtPopoverKey = source.key;
			srtPopoverPos = { x: e.clientX, y: e.clientY - 10 };
		}
	}
</script>

<button
	class="source-btn"
	class:program={tally === 'program' || tally === 'key_fill'}
	class:preview={tally === 'preview'}
	class:key-alpha={tally === 'key_alpha'}
	class:pip={isPIP && tally !== 'program' && tally !== 'preview' && tally !== 'key_fill' && tally !== 'key_alpha'}
	onclick={handleClick}
	title={tally === 'program' ? `${source.label || source.key} (PGM)` : tally === 'key_fill' ? `${source.label || source.key} (KEY FILL — on air)` : tally === 'preview' ? `${source.label || source.key} (PVW)` : tally === 'key_alpha' ? `${source.label || source.key} (KEY ALPHA)` : `Click: Preview · Dbl-click: Cut`}
>
	<div class="thumb-wrap" bind:this={wrapEl}>
		<canvas class="thumb-canvas" id="tile-{source.key}" bind:this={canvasEl}></canvas>
		{#if stalled}
			<div class="health-overlay frame-stall"><span class="health-text">STALL</span></div>
		{:else if source.status === 'stale'}
			<div class="health-overlay stale"></div>
		{:else if source.status === 'no_signal'}
			<div class="health-overlay no-signal"><span class="health-text">NO SIG</span></div>
		{:else if source.status === 'offline'}
			<div class="health-overlay offline"><span class="health-text">OFF</span></div>
		{/if}
		{#if srtHealth}
			<span class="srt-dot-wrap">
				<SRTHealthDot level={srtHealth} onclick={source.srt ? handleSRTClick : undefined} />
			</span>
		{/if}
		{#if source.delayMs && source.delayMs > 0}
			<span class="delay-badge">D</span>
		{/if}
	</div>
	<div class="label-row">
		<span class="tile-num">{index + 1}</span>
		{#if editing}
			<!-- svelte-ignore a11y_autofocus -->
			<input
				class="label-input"
				bind:this={inputEl}
				bind:value={editValue}
				onkeydown={handleKeydown}
				onblur={commitEdit}
				onclick={(e) => e.stopPropagation()}
				autofocus
			/>
		{:else}
			<span
				class="label-text"
				ondblclick={onLabelChange ? startEditing : undefined}
				role={onLabelChange ? 'button' : undefined}
			>{source.label || source.key}</span>
		{/if}
		{#if source.isVirtual}<span class="virtual-badge">RPL</span>{/if}
	</div>
	{#if showBar}
		<div class="audio-bar" aria-hidden="true">
			<div class="audio-bar-fill" style="height: {barPercent}%; background: {barFill}"></div>
		</div>
	{/if}
</button>

{#if srtPopoverKey && source.srt}
	<SRTStatsPopover
		srt={source.srt}
		sourceLabel={source.label || source.key}
		x={srtPopoverPos.x}
		y={srtPopoverPos.y}
		onclose={() => srtPopoverKey = null}
	/>
{/if}

<style>
	.source-btn {
		display: flex;
		flex-direction: column;
		border: 2px solid transparent;
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		cursor: pointer;
		padding: 2px;
		gap: 1px;
		height: 100%;
		flex-shrink: 0;
		position: relative;
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

	.source-btn.program {
		border-color: var(--tally-program);
		box-shadow: var(--tally-program-glow);
	}

	.source-btn.preview {
		border-color: var(--tally-preview);
		box-shadow: var(--tally-preview-glow);
	}

	.source-btn.pip {
		border-color: var(--accent-gold);
		box-shadow: 0 0 6px var(--accent-gold-medium);
	}

	.source-btn.key-alpha {
		border-color: var(--accent-orange, #f59e0b);
		box-shadow: 0 0 6px rgba(245, 158, 11, 0.45);
	}

	.thumb-wrap {
		width: 100px;
		aspect-ratio: 16 / 9;
		border-radius: var(--radius-xs);
		overflow: hidden;
		position: relative;
		background: var(--bg-canvas);
		flex-shrink: 0;
	}

	.thumb-canvas {
		width: 100%;
		height: 100%;
		display: block;
	}

	.health-overlay {
		position: absolute;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		pointer-events: none;
	}

	.health-overlay.frame-stall {
		background: rgba(200, 30, 30, 0.45);
		border: 1px solid var(--tally-program);
		animation: pulse-stall 1.5s ease-in-out infinite;
	}

	.health-overlay.stale {
		border: 1px solid var(--color-warning);
		animation: pulse-stale 2s ease-in-out infinite;
	}

	.health-overlay.no-signal {
		background: rgba(200, 30, 30, 0.5);
	}

	.health-overlay.offline {
		background: var(--overlay-heavy);
	}

	.health-text {
		font-family: var(--font-ui);
		font-size: 8px;
		font-weight: 700;
		letter-spacing: 0.08em;
		color: rgba(255, 255, 255, 0.85);
		text-transform: uppercase;
	}

	.srt-dot-wrap {
		position: absolute;
		top: 2px;
		left: 2px;
	}

	.srt-dot-wrap :global(.srt-dot) {
		position: static;
	}

	.delay-badge {
		position: absolute;
		bottom: 1px;
		right: 1px;
		font-size: 7px;
		font-family: var(--font-mono);
		font-weight: 700;
		color: var(--accent-orange);
		background: var(--overlay-heavy);
		padding: 0 2px;
		border-radius: var(--radius-xs);
		line-height: 1.4;
	}

	.label-row {
		display: flex;
		align-items: center;
		gap: 2px;
		padding: 0 1px;
		min-width: 0;
		width: 100px;
	}

	.tile-num {
		font-size: 9px;
		font-family: var(--font-mono);
		font-weight: 700;
		opacity: 0.4;
		line-height: 1;
		flex-shrink: 0;
	}

	.label-text {
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: 10px;
		letter-spacing: 0.01em;
		line-height: 1.2;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--text-primary);
		min-width: 0;
	}

	.label-input {
		font-size: 10px;
		font-weight: 600;
		font-family: inherit;
		color: var(--text-primary);
		background: var(--overlay-medium);
		border: 1px solid rgba(255, 255, 255, 0.3);
		border-radius: var(--radius-xs);
		padding: 0 2px;
		outline: none;
		min-width: 0;
		flex: 1;
	}

	.label-input:focus {
		border-color: rgba(255, 255, 255, 0.6);
	}

	.virtual-badge {
		font-size: 7px;
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--accent-purple, #a78bfa);
		flex-shrink: 0;
	}

	.audio-bar {
		position: absolute;
		right: 0;
		top: 0;
		bottom: 0;
		width: 3px;
		border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
		overflow: hidden;
		pointer-events: none;
	}

	.audio-bar-fill {
		position: absolute;
		bottom: 0;
		left: 0;
		right: 0;
		transition: height 0.06s linear;
	}

	@keyframes pulse-stale {
		0%, 100% { border-color: var(--color-warning); box-shadow: none; }
		50% { border-color: #ffaa33; box-shadow: 0 0 4px rgba(204, 136, 34, 0.4); }
	}

	@keyframes pulse-stall {
		0%, 100% { background: rgba(200, 30, 30, 0.45); }
		50% { background: rgba(200, 30, 30, 0.25); }
	}
</style>
