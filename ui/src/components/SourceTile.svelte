<script lang="ts">
	import type { SourceInfo, TallyStatus, LayoutSlotState, SourceHealthStatus } from '$lib/api/types';
	import { computeSRTHealth } from '$lib/util/srt-health';
	import { healthDotColor } from '$lib/util/health-dots';
	import { isSourceStalled } from '$lib/transport/source-errors.svelte';
	import SRTHealthDot from './SRTHealthDot.svelte';

	interface Props {
		source: SourceInfo;
		tally: TallyStatus;
		index: number;
		audioLevelDb?: number;
		layoutSlots?: LayoutSlotState[];
		engineSourceHealth?: { A: SourceHealthStatus; B: SourceHealthStatus } | null;
		onclick?: () => void;
		onLabelChange?: (key: string, label: string) => void;
		onSRTClick?: (e: MouseEvent) => void;
	}

	let { source, tally, index, audioLevelDb = -96, layoutSlots, engineSourceHealth, onclick, onLabelChange, onSRTClick }: Props = $props();

	let showDualDots = $derived(
		engineSourceHealth != null && engineSourceHealth.A !== engineSourceHealth.B
	);

	let isPIP = $derived(layoutSlots?.some(s => s.enabled && s.sourceKey === source.key) ?? false);
	let srtHealth = $derived(computeSRTHealth(source.srt));
	let stalled = $derived(isSourceStalled(source.key));

	let editing = $state(false);
	let editValue = $state('');
	let inputEl: HTMLInputElement | undefined = $state();

	function startEditing(e: MouseEvent) {
		e.stopPropagation();
		editing = true;
		editValue = source.label || source.key;
		// Focus the input after Svelte renders it
		queueMicrotask(() => {
			inputEl?.select();
		});
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
		if (e.key === 'Enter') {
			e.preventDefault();
			commitEdit();
		} else if (e.key === 'Escape') {
			e.preventDefault();
			cancelEdit();
		}
	}

	/**
	 * Map a dBFS value to a percentage (0..100) for the audio bar.
	 * Silence (-96 or below) maps to 0%, 0 dBFS maps to 100%.
	 */
	function dbToBarPercent(db: number): number {
		const min = -60;
		const max = 0;
		if (db <= min) return 0;
		if (db >= max) return 100;
		return ((db - min) / (max - min)) * 100;
	}

	/**
	 * Choose bar color based on dBFS level.
	 * Green (< -12), Yellow (-12 to -3), Red (> -3).
	 */
	function barColor(db: number): string {
		if (db > -3) return '#ef4444';
		if (db > -12) return '#eab308';
		return '#22c55e';
	}

	let barPercent = $derived(dbToBarPercent(audioLevelDb));
	let barFill = $derived(barColor(audioLevelDb));
	let showBar = $derived(audioLevelDb > -60);
</script>

<button
	class="source-tile"
	class:program={tally === 'program'}
	class:preview={tally === 'preview'}
	class:pip={isPIP && tally !== 'program' && tally !== 'preview'}
	{onclick}
>
	<span class="tile-number">{index + 1}</span>
	{#if editing}
		<!-- svelte-ignore a11y_autofocus -->
		<input
			class="tile-label-input"
			bind:this={inputEl}
			bind:value={editValue}
			onkeydown={handleKeydown}
			onblur={commitEdit}
			onclick={(e) => e.stopPropagation()}
			autofocus
		/>
	{:else}
		<span
			class="tile-label"
			ondblclick={onLabelChange ? startEditing : undefined}
			role={onLabelChange ? 'button' : undefined}
		>{source.label || source.key}</span>
	{/if}
	{#if showDualDots}
		<span class="engine-health-dots" aria-label="Engine health: A {engineSourceHealth!.A}, B {engineSourceHealth!.B}">
			<span class="engine-dot-a"><span class="engine-label">A</span><span class="engine-dot" style="background: {healthDotColor(engineSourceHealth!.A)}"></span></span>
			<span class="engine-dot-b"><span class="engine-label">B</span><span class="engine-dot" style="background: {healthDotColor(engineSourceHealth!.B)}"></span></span>
		</span>
	{:else}
		<span class="tile-status" class:offline={source.status === 'offline'} class:stale={source.status === 'stale' || stalled}>
			{#if stalled}<span class="stall-badge">STALL</span>{:else if source.isVirtual}<span class="virtual-badge">RPL</span>{:else if source.status !== 'healthy'}{source.status}{/if}
		</span>
	{/if}

	{#if source.delayMs && source.delayMs > 0}
		<span class="delay-badge">D:{source.delayMs}ms</span>
	{/if}

	{#if srtHealth}
		<SRTHealthDot
			level={srtHealth}
			onclick={onSRTClick ? (e) => { e.stopPropagation(); onSRTClick(e); } : undefined}
		/>
	{/if}

	<!-- Audio level bar (right edge) -->
	{#if showBar}
		<div class="audio-bar" aria-hidden="true">
			<div class="audio-bar-fill" style="height: {barPercent}%; background: {barFill}"></div>
		</div>
	{/if}
</button>

<style>
	.source-tile {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		padding: 3px 8px;
		border: 1.5px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		min-width: 64px;
		position: relative;
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast),
			box-shadow var(--transition-normal),
			color var(--transition-fast);
	}

	.source-tile:hover {
		border-color: var(--border-strong);
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.source-tile:active {
		transform: scale(0.97);
	}

	.source-tile.program {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
		color: var(--text-on-color);
		box-shadow: var(--tally-program-glow);
	}

	.source-tile.preview {
		border-color: var(--tally-preview);
		background: var(--tally-preview-dim);
		color: var(--text-on-color);
		box-shadow: var(--tally-preview-glow);
	}

	.source-tile.pip {
		border-color: var(--accent-gold);
		background: var(--accent-gold-dim);
		color: var(--text-on-color);
		box-shadow: 0 0 6px var(--accent-gold-medium);
	}

	.tile-number {
		font-size: var(--text-xs);
		font-family: var(--font-mono);
		font-weight: 500;
		opacity: 0.5;
		line-height: 1;
	}

	.tile-label {
		font-weight: 600;
		font-size: var(--text-sm);
		letter-spacing: 0.01em;
		line-height: 1.15;
		white-space: nowrap;
	}

	.tile-label-input {
		font-weight: 600;
		font-size: var(--text-sm);
		letter-spacing: 0.01em;
		line-height: 1.2;
		background: var(--overlay-light);
		border: 1px solid rgba(255, 255, 255, 0.3);
		border-radius: var(--radius-xs);
		color: inherit;
		font-family: inherit;
		text-align: center;
		padding: 0 2px;
		width: 100%;
		max-width: 80px;
		outline: none;
	}

	.tile-label-input:focus {
		border-color: rgba(255, 255, 255, 0.6);
	}

	.tile-status {
		font-size: var(--text-xs);
		text-transform: uppercase;
		font-weight: 500;
		letter-spacing: 0.04em;
	}

	.tile-status.offline {
		color: var(--tally-program);
	}

	.tile-status.stale {
		color: var(--accent-orange);
	}

	.virtual-badge {
		color: var(--accent-purple, #a78bfa);
		font-weight: 700;
		letter-spacing: 0.06em;
	}

	.stall-badge {
		color: var(--tally-program, #ef4444);
		font-weight: 700;
		letter-spacing: 0.06em;
		animation: pulse-stall-text 1.5s ease-in-out infinite;
	}

	@keyframes pulse-stall-text {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.5; }
	}

	.delay-badge {
		position: absolute;
		bottom: 2px;
		left: 3px;
		font-size: var(--text-2xs);
		font-family: var(--font-mono);
		color: var(--accent-orange);
		background: var(--overlay-heavy);
		padding: 0 2px;
		border-radius: var(--radius-xs);
	}

	/* Dual engine health dots */
	.engine-health-dots {
		display: flex;
		align-items: center;
		gap: 4px;
		font-size: 9px;
		line-height: 1;
	}

	.engine-dot-a,
	.engine-dot-b {
		display: flex;
		align-items: center;
		gap: 1px;
	}

	.engine-label {
		font-family: var(--font-mono);
		font-weight: 600;
		font-size: 8px;
		opacity: 0.7;
	}

	.engine-dot {
		display: inline-block;
		width: 6px;
		height: 6px;
		border-radius: 50%;
	}

	.source-tile :global(.srt-dot) {
		position: absolute;
		top: 3px;
		left: 3px;
	}

	/* Audio level bar - right edge of tile */
	.audio-bar {
		position: absolute;
		right: 0;
		top: 0;
		bottom: 0;
		width: 4px;
		border-radius: 0 var(--radius-md) var(--radius-md) 0;
		overflow: hidden;
		pointer-events: none;
	}

	.audio-bar-fill {
		position: absolute;
		bottom: 0;
		left: 0;
		right: 0;
		transition: height 0.06s linear;
		border-radius: 0 0 var(--radius-md) 0;
	}
</style>
