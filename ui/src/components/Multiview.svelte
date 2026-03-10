<script lang="ts">
	import type { ControlRoomState, TallyStatus } from '$lib/api/types';
	import { setPreview, apiCall } from '$lib/api/switch-api';
	import { setupHiDPICanvas } from '$lib/video/canvas-utils';
	import { getSourceError } from '$lib/transport/source-errors.svelte';
	import { sortedSourceKeys } from '$lib/util/sort-sources';

	interface Props {
		state: ControlRoomState;
		onLabelChange?: (key: string, label: string) => void;
	}
	let { state: crState, onLabelChange }: Props = $props();
	let sourceKeys = $derived(sortedSourceKeys(crState.sources));
	let multiviewEl: HTMLDivElement;

	// High-DPI canvas sizing for all tile canvases
	$effect(() => {
		if (!multiviewEl) return;
		const observer = new ResizeObserver(() => {
			const canvases = multiviewEl.querySelectorAll('.tile-video') as NodeListOf<HTMLCanvasElement>;
			canvases.forEach((canvas) => {
				const tile = canvas.closest('.tile');
				if (tile) {
					const rect = tile.getBoundingClientRect();
					if (rect.width > 0 && rect.height > 0) {
						setupHiDPICanvas(canvas, rect.width, rect.height);
					}
				}
			});
		});
		observer.observe(multiviewEl);
		return () => observer.disconnect();
	});

	// Track which tile is being edited
	let editingKey = $state<string | null>(null);
	let editValue = $state('');
	let inputEl: HTMLInputElement | undefined = $state();

	function startEditing(key: string, e: MouseEvent) {
		e.stopPropagation();
		editingKey = key;
		editValue = crState.sources[key].label || key;
		queueMicrotask(() => {
			inputEl?.select();
		});
	}

	function commitEdit() {
		if (!editingKey) return;
		const key = editingKey;
		const trimmed = editValue.trim();
		editingKey = null;
		if (trimmed && trimmed !== (crState.sources[key]?.label || key)) {
			onLabelChange?.(key, trimmed);
		}
	}

	function cancelEdit() {
		editingKey = null;
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

	function getTally(key: string): TallyStatus {
		return crState.tallyState[key] || 'idle';
	}
</script>

<div class="multiview" bind:this={multiviewEl}>
	{#each sourceKeys as key, i}
		<button
			class="tile"
			class:tally-program={getTally(key) === 'program'}
			class:tally-preview={getTally(key) === 'preview'}
			onclick={() => apiCall(setPreview(key), 'Preview failed')}
		>
			<canvas class="tile-video" id="tile-{key}"></canvas>
			{#if crState.sources[key].status === 'stale'}
				<div class="health-overlay stale"></div>
			{:else if crState.sources[key].status === 'no_signal'}
				<div class="health-overlay no-signal">
					<span class="health-text">NO SIGNAL</span>
				</div>
			{:else if crState.sources[key].status === 'offline'}
				<div class="health-overlay offline">
					<span class="health-text">OFFLINE</span>
				</div>
			{/if}
			{#if getSourceError(key)}
				<span class="tile-error" title={getSourceError(key)}>!</span>
			{/if}
			<div class="tile-bar">
				<span class="tile-num">{i + 1}</span>
				{#if editingKey === key}
					<!-- svelte-ignore a11y_autofocus -->
					<input
						class="tile-name-input"
						bind:this={inputEl}
						bind:value={editValue}
						onkeydown={handleKeydown}
						onblur={commitEdit}
						onclick={(e) => e.stopPropagation()}
						autofocus
					/>
				{:else}
					<span
						class="tile-name"
						role={onLabelChange ? "button" : undefined}
						ondblclick={onLabelChange ? (e: MouseEvent) => startEditing(key, e) : undefined}
					>{crState.sources[key].label || key}</span>
				{/if}
				{#if crState.sources[key].status !== 'healthy'}
					<span class="tile-health">{crState.sources[key].status}</span>
				{/if}
			</div>
		</button>
	{/each}
</div>

<style>
	.multiview {
		display: flex;
		gap: 3px;
		padding: 2px 4px;
		background: var(--bg-base);
		height: 100%;
		overflow-x: auto;
		overflow-y: hidden;
	}

	.tile {
		aspect-ratio: 16 / 9;
		background: var(--bg-canvas);
		border: 2px solid transparent;
		border-radius: var(--radius-sm);
		cursor: pointer;
		position: relative;
		overflow: hidden;
		padding: 0;
		height: 100%;
		flex-shrink: 0;
		transition:
			border-color var(--transition-fast),
			box-shadow var(--transition-normal);
	}

	.tile:hover {
		border-color: var(--border-strong);
	}

	.tile.tally-program {
		border-color: var(--tally-program);
		box-shadow: var(--tally-program-glow);
	}

	.tile.tally-preview {
		border-color: var(--tally-preview);
		box-shadow: var(--tally-preview-glow);
	}

	.tile-video {
		width: 100%;
		height: 100%;
		display: block;
	}

	.tile-bar {
		position: absolute;
		bottom: 0;
		left: 0;
		right: 0;
		background: linear-gradient(transparent, var(--overlay-opaque));
		padding: 8px 5px 2px;
		display: flex;
		align-items: center;
		gap: 4px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--text-primary);
	}

	.tile-num {
		background: var(--bg-control);
		padding: 1px 4px;
		border-radius: var(--radius-sm);
		font-size: var(--text-2xs);
		font-family: var(--font-mono);
		font-weight: 700;
		color: var(--text-secondary);
	}

	.tile-name {
		flex: 1;
		letter-spacing: 0.01em;
	}

	.tile-name-input {
		flex: 1;
		font-size: var(--text-sm);
		font-weight: 500;
		font-family: inherit;
		letter-spacing: 0.01em;
		color: var(--text-primary);
		background: var(--overlay-medium);
		border: 1px solid rgba(255, 255, 255, 0.3);
		border-radius: var(--radius-xs);
		padding: 0 4px;
		outline: none;
		min-width: 0;
	}

	.tile-name-input:focus {
		border-color: rgba(255, 255, 255, 0.6);
	}

	.tile-health {
		color: var(--accent-orange);
		text-transform: uppercase;
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.05em;
	}

	.health-overlay {
		position: absolute;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		pointer-events: none;
		z-index: var(--z-above);
	}

	.health-overlay.stale {
		border: 2px solid var(--color-warning);
		animation: pulse-stale 2s ease-in-out infinite;
	}

	.health-overlay.no-signal {
		background: rgba(200, 30, 30, 0.4);
	}

	.health-overlay.offline {
		background: var(--overlay-heavy);
	}

	.health-text {
		font-family: var(--font-ui);
		font-size: var(--text-md);
		font-weight: 700;
		letter-spacing: 0.1em;
		color: rgba(255, 255, 255, 0.8);
		text-transform: uppercase;
	}

	.tile-error {
		position: absolute;
		top: 6px;
		right: 6px;
		width: 18px;
		height: 18px;
		background: var(--tally-program);
		color: #fff;
		border-radius: 50%;
		display: flex;
		align-items: center;
		justify-content: center;
		font-weight: 700;
		font-size: var(--text-sm);
		z-index: var(--z-above);
		cursor: help;
	}

	@keyframes pulse-stale {
		0%, 100% { border-color: var(--color-warning); box-shadow: none; }
		50% { border-color: #ffaa33; box-shadow: 0 0 8px rgba(204, 136, 34, 0.4); }
	}
</style>
