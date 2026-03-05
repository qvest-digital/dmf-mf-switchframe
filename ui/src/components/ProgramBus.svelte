<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import SourceTile from './SourceTile.svelte';
	import { cut, fireAndForget } from '$lib/api/switch-api';

	interface Props { state: ControlRoomState; }
	let { state }: Props = $props();
	let sourceKeys = $derived(Object.keys(state.sources).sort());
</script>

<div class="bus program-bus">
	<span class="bus-label">PGM</span>
	<div class="bus-buttons">
		{#each sourceKeys as key, i}
			<SourceTile
				source={state.sources[key]}
				tally={state.programSource === key ? 'program' : 'idle'}
				index={i}
				onclick={() => fireAndForget(cut(key))}
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
