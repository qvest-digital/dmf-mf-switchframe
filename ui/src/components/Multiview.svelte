<script lang="ts">
	import type { ControlRoomState, TallyStatus } from '$lib/api/types';
	import { setPreview } from '$lib/api/switch-api';

	interface Props { state: ControlRoomState; }
	let { state }: Props = $props();
	let sourceKeys = $derived(Object.keys(state.sources).sort());

	function getTally(key: string): TallyStatus {
		return state.tallyState[key] || 'idle';
	}
	function tallyColor(tally: TallyStatus): string {
		switch (tally) {
			case 'program': return 'var(--tally-program)';
			case 'preview': return 'var(--tally-preview)';
			default: return 'transparent';
		}
	}
</script>

<div class="multiview">
	{#each sourceKeys as key, i}
		<button class="tile" style:outline-color={tallyColor(getTally(key))} onclick={() => setPreview(key)}>
			<div class="tile-video" id="tile-{i}"></div>
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
	.multiview { display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: 4px; padding: 0.5rem; background: #111; }
	.tile { aspect-ratio: 16/9; background: #0a0a0a; border: none; border-radius: 2px; outline: 3px solid transparent; outline-offset: -3px; cursor: pointer; position: relative; overflow: hidden; padding: 0; transition: outline-color 0.1s; }
	.tile:hover { outline-color: rgba(255,255,255,0.3); }
	.tile-video { width: 100%; height: 100%; }
	.tile-bar { position: absolute; bottom: 0; left: 0; right: 0; background: rgba(0,0,0,0.7); padding: 0.2rem 0.4rem; display: flex; align-items: center; gap: 0.4rem; font-family: monospace; font-size: 0.7rem; color: #ccc; }
	.tile-num { background: #333; padding: 0 0.3rem; border-radius: 2px; font-size: 0.6rem; }
	.tile-name { flex: 1; }
	.tile-health { color: #cc8822; text-transform: uppercase; font-size: 0.6rem; }
</style>
