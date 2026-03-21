<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import SourceTile from './SourceTile.svelte';
	import SRTStatsPopover from './SRTStatsPopover.svelte';
	import { setPreview, apiCall } from '$lib/api/switch-api';
	import { sortedSourceKeys } from '$lib/util/sort-sources';

	interface Props {
		state: ControlRoomState;
		onPreview?: (key: string) => void;
	}
	let { state: crState, onPreview }: Props = $props();
	let sourceKeys = $derived(sortedSourceKeys(crState.sources));
	let srtPopoverKey: string | null = $state(null);
	let srtPopoverPos = $state({ x: 0, y: 0 });

	function handleSRTClick(key: string, e: MouseEvent) {
		if (srtPopoverKey === key) {
			srtPopoverKey = null;
		} else {
			srtPopoverKey = key;
			srtPopoverPos = { x: e.clientX, y: e.clientY - 10 };
		}
	}
</script>

<div class="bus preview-bus">
	<span class="bus-label">PVW</span>
	<div class="bus-buttons">
		{#each sourceKeys as key, i}
			<SourceTile
				source={crState.sources[key]}
				tally={crState.previewSource === key ? 'preview' : 'idle'}
				index={i}
				audioLevelDb={crState.audioChannels?.[key] ? Math.max(crState.audioChannels[key].peakL, crState.audioChannels[key].peakR) : undefined}
				layoutSlots={crState.layout?.slots}
				onclick={() => onPreview ? onPreview(key) : apiCall(setPreview(key), 'Preview failed')}
				onSRTClick={crState.sources[key]?.srt ? (e) => handleSRTClick(key, e) : undefined}
			/>
		{/each}
	</div>
</div>

{#if srtPopoverKey && crState.sources[srtPopoverKey]?.srt}
	<SRTStatsPopover
		srt={crState.sources[srtPopoverKey].srt!}
		sourceLabel={crState.sources[srtPopoverKey].label || srtPopoverKey}
		x={srtPopoverPos.x}
		y={srtPopoverPos.y}
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
		color: var(--tally-preview);
		opacity: 0.9;
	}

	.bus-buttons {
		display: flex;
		gap: 2px;
		flex-wrap: wrap;
	}
</style>
