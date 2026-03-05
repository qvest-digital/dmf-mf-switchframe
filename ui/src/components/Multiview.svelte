<script lang="ts">
	import type { ControlRoomState, TallyStatus } from '$lib/api/types';
	import { setPreview, fireAndForget } from '$lib/api/switch-api';

	interface Props {
		state: ControlRoomState;
		onLabelChange?: (key: string, label: string) => void;
	}
	let { state: crState, onLabelChange }: Props = $props();
	let sourceKeys = $derived(Object.keys(crState.sources).sort());

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

<div class="multiview">
	{#each sourceKeys as key, i}
		<button
			class="tile"
			class:tally-program={getTally(key) === 'program'}
			class:tally-preview={getTally(key) === 'preview'}
			onclick={() => fireAndForget(setPreview(key))}
		>
			<canvas class="tile-video" id="tile-{key}" width="320" height="180"></canvas>
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
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
		gap: 4px;
		padding: 6px;
		background: var(--bg-base);
		height: 100%;
		align-content: start;
		overflow-y: auto;
	}

	.tile {
		aspect-ratio: 16 / 9;
		background: #050507;
		border: 2px solid transparent;
		border-radius: var(--radius-md);
		cursor: pointer;
		position: relative;
		overflow: hidden;
		padding: 0;
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
		background: linear-gradient(transparent, rgba(0, 0, 0, 0.85));
		padding: 16px 8px 5px;
		display: flex;
		align-items: center;
		gap: 6px;
		font-family: var(--font-ui);
		font-size: 0.7rem;
		font-weight: 500;
		color: var(--text-primary);
	}

	.tile-num {
		background: var(--bg-control);
		padding: 1px 5px;
		border-radius: var(--radius-sm);
		font-size: 0.6rem;
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
		font-size: 0.7rem;
		font-weight: 500;
		font-family: inherit;
		letter-spacing: 0.01em;
		color: var(--text-primary);
		background: rgba(0, 0, 0, 0.5);
		border: 1px solid rgba(255, 255, 255, 0.3);
		border-radius: 2px;
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
		font-size: 0.55rem;
		font-weight: 600;
		letter-spacing: 0.05em;
	}
</style>
