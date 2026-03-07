<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import SourceTile from './SourceTile.svelte';
	import { cut, apiCall } from '$lib/api/switch-api';
	import { sortedSourceKeys } from '$lib/util/sort-sources';

	interface Props {
		state: ControlRoomState;
		onCut?: (key: string) => void;
	}
	let { state, onCut }: Props = $props();
	let sourceKeys = $derived(sortedSourceKeys(state.sources));
</script>

<div class="bus program-bus">
	<span class="bus-label">PGM</span>
	<div class="bus-buttons">
		{#each sourceKeys as key, i}
			<SourceTile
				source={state.sources[key]}
				tally={state.programSource === key ? 'program' : 'idle'}
				index={i}
				audioLevelDb={state.audioChannels?.[key] ? Math.max(state.audioChannels[key].peakL, state.audioChannels[key].peakR) : undefined}
				onclick={() => onCut ? onCut(key) : apiCall(cut(key), 'Cut failed')}
			/>
		{/each}
	</div>
</div>

<style>
	.bus {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 4px 10px;
	}

	.bus-label {
		font-family: var(--font-ui);
		font-weight: 700;
		font-size: 0.6rem;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		min-width: 32px;
		color: var(--tally-program);
	}

	.bus-buttons {
		display: flex;
		gap: 3px;
		flex-wrap: wrap;
	}
</style>
