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
		padding: 0.5rem 1rem;
		border: 2px solid var(--tally-idle);
		border-radius: 4px;
		background: #1a1a1a;
		color: #ccc;
		cursor: pointer;
		font-family: monospace;
		min-width: 80px;
		transition: border-color 0.1s;
	}
	.source-tile.program { border-color: var(--tally-program); background: #2a0000; color: white; }
	.source-tile.preview { border-color: var(--tally-preview); background: #002a00; color: white; }
	.tile-number { font-size: 0.7rem; opacity: 0.6; }
	.tile-label { font-weight: bold; font-size: 0.9rem; }
	.tile-status { font-size: 0.65rem; text-transform: uppercase; }
	.tile-status.offline { color: #cc0000; }
	.tile-status.stale { color: #cc8822; }
</style>
