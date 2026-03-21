<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import SourceTile from './SourceTile.svelte';
	import SRTStatsPopover from './SRTStatsPopover.svelte';
	import { cut, apiCall } from '$lib/api/switch-api';
	import { sortedSourceKeys } from '$lib/util/sort-sources';

	interface Props {
		state: ControlRoomState;
		onCut?: (key: string) => void;
	}
	let { state: crState, onCut }: Props = $props();
	let sourceKeys = $derived(sortedSourceKeys(crState.sources));
	let srtPopoverKey: string | null = $state(null);

	function handleSRTClick(key: string) {
		srtPopoverKey = srtPopoverKey === key ? null : key;
	}
</script>

<div class="bus program-bus">
	<span class="bus-sep"></span>
	<span class="bus-label">PGM</span>
	<div class="bus-buttons">
		{#each sourceKeys as key, i}
			<SourceTile
				source={crState.sources[key]}
				tally={crState.programSource === key ? 'program' : 'idle'}
				index={i}
				audioLevelDb={crState.audioChannels?.[key] ? Math.max(crState.audioChannels[key].peakL, crState.audioChannels[key].peakR) : undefined}
				layoutSlots={crState.layout?.slots}
				onclick={() => onCut ? onCut(key) : apiCall(cut(key), 'Cut failed')}
				onSRTClick={crState.sources[key]?.srt ? () => handleSRTClick(key) : undefined}
			/>
		{/each}
	</div>
</div>

{#if srtPopoverKey && crState.sources[srtPopoverKey]?.srt}
	<SRTStatsPopover
		srt={crState.sources[srtPopoverKey].srt!}
		sourceLabel={crState.sources[srtPopoverKey].label || srtPopoverKey}
		onclose={() => srtPopoverKey = null}
	/>
{/if}

<style>
	.bus {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.bus-label {
		font-family: var(--font-ui);
		font-weight: 700;
		font-size: var(--text-xs);
		letter-spacing: 0.08em;
		text-transform: uppercase;
		min-width: 28px;
		color: var(--tally-program);
		opacity: 0.9;
	}

	.bus-sep {
		width: 1px;
		height: 20px;
		background: var(--border-default);
		flex-shrink: 0;
	}

	.bus-buttons {
		display: flex;
		gap: 2px;
		flex-wrap: wrap;
	}
</style>
