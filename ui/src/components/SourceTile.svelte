<script lang="ts">
	import type { SourceInfo, TallyStatus } from '$lib/api/types';

	interface Props {
		source: SourceInfo;
		tally: TallyStatus;
		index: number;
		onclick?: () => void;
	}

	let { source, tally, index, onclick }: Props = $props();
</script>

<button
	class="source-tile"
	class:program={tally === 'program'}
	class:preview={tally === 'preview'}
	{onclick}
>
	<span class="tile-number">{index + 1}</span>
	<span class="tile-label">{source.label || source.key}</span>
	<span class="tile-status" class:offline={source.status === 'offline'} class:stale={source.status === 'stale'}>
		{#if source.status !== 'healthy'}{source.status}{/if}
	</span>
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
</style>
