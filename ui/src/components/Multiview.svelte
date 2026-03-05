<script lang="ts">
	import type { ControlRoomState, TallyStatus } from '$lib/api/types';
	import { setPreview, fireAndForget } from '$lib/api/switch-api';

	interface Props { state: ControlRoomState; }
	let { state }: Props = $props();
	let sourceKeys = $derived(Object.keys(state.sources).sort());

	function getTally(key: string): TallyStatus {
		return state.tallyState[key] || 'idle';
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
				<span class="tile-name">{state.sources[key].label || key}</span>
				{#if state.sources[key].status !== 'healthy'}
					<span class="tile-health">{state.sources[key].status}</span>
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

	.tile-health {
		color: var(--accent-orange);
		text-transform: uppercase;
		font-size: 0.55rem;
		font-weight: 600;
		letter-spacing: 0.05em;
	}
</style>
