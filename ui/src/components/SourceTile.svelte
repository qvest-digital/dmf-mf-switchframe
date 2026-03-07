<script lang="ts">
	import type { SourceInfo, TallyStatus } from '$lib/api/types';

	interface Props {
		source: SourceInfo;
		tally: TallyStatus;
		index: number;
		audioLevelDb?: number;
		onclick?: () => void;
		onLabelChange?: (key: string, label: string) => void;
	}

	let { source, tally, index, audioLevelDb = -96, onclick, onLabelChange }: Props = $props();

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
	<span class="tile-status" class:offline={source.status === 'offline'} class:stale={source.status === 'stale'}>
		{#if source.isVirtual}<span class="virtual-badge">RPL</span>{:else if source.status !== 'healthy'}{source.status}{/if}
	</span>

	{#if source.delayMs && source.delayMs > 0}
		<span class="delay-badge">D:{source.delayMs}ms</span>
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
		padding: 4px 10px;
		border: 1.5px solid var(--border-default);
		border-radius: var(--radius-md);
		background: var(--bg-elevated);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		min-width: 72px;
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

	.tile-number {
		font-size: 0.55rem;
		font-family: var(--font-mono);
		font-weight: 500;
		opacity: 0.5;
		line-height: 1;
	}

	.tile-label {
		font-weight: 600;
		font-size: 0.75rem;
		letter-spacing: 0.01em;
		line-height: 1.2;
	}

	.tile-label-input {
		font-weight: 600;
		font-size: 0.75rem;
		letter-spacing: 0.01em;
		line-height: 1.2;
		background: rgba(0, 0, 0, 0.3);
		border: 1px solid rgba(255, 255, 255, 0.3);
		border-radius: 2px;
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
		font-size: 0.6rem;
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

	.delay-badge {
		position: absolute;
		bottom: 2px;
		left: 3px;
		font-size: 0.5rem;
		font-family: var(--font-mono);
		color: var(--accent-orange);
		background: rgba(0, 0, 0, 0.6);
		padding: 0 2px;
		border-radius: 2px;
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
