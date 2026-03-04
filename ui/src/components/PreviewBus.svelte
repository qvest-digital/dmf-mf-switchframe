<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import SourceTile from './SourceTile.svelte';
	import { setPreview, fireAndForget } from '$lib/api/switch-api';

	interface Props { state: ControlRoomState; }
	let { state }: Props = $props();
	let sourceKeys = $derived(Object.keys(state.sources).sort());
</script>

<div class="bus preview-bus">
	<span class="bus-label">PREVIEW</span>
	<div class="bus-buttons">
		{#each sourceKeys as key, i}
			<SourceTile
				source={state.sources[key]}
				tally={state.previewSource === key ? 'preview' : 'idle'}
				index={i}
				onclick={() => fireAndForget(setPreview(key))}
			/>
		{/each}
	</div>
</div>

<style>
	.bus { display: flex; align-items: center; gap: 0.5rem; padding: 0.5rem; }
	.bus-label { font-weight: bold; font-size: 0.8rem; min-width: 70px; color: #00aa00; font-family: monospace; }
	.bus-buttons { display: flex; gap: 0.25rem; flex-wrap: wrap; }
</style>
