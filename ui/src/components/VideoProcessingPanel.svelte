<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import LooksTab from './videoprocessing/LooksTab.svelte';
	import ColorTab from './videoprocessing/ColorTab.svelte';
	import EffectsTab from './videoprocessing/EffectsTab.svelte';

	let { state: crState }: { state: ControlRoomState } = $props();
	let activeTab = $state<'looks' | 'color' | 'effects'>('looks');
</script>

<div class="video-processing-panel">
	<div class="tab-bar">
		<button
			class="vp-tab"
			class:active={activeTab === 'looks'}
			onclick={() => activeTab = 'looks'}
		>Looks</button>
		<button
			class="vp-tab"
			class:active={activeTab === 'color'}
			onclick={() => activeTab = 'color'}
		>Color</button>
		<button
			class="vp-tab"
			class:active={activeTab === 'effects'}
			onclick={() => activeTab = 'effects'}
		>Effects</button>
	</div>
	<div class="tab-content">
		{#if activeTab === 'looks'}
			<LooksTab state={crState} />
		{:else if activeTab === 'color'}
			<ColorTab state={crState} />
		{:else}
			<EffectsTab state={crState} />
		{/if}
	</div>
</div>

<style>
	.video-processing-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
	}

	.tab-bar {
		display: flex;
		gap: 0;
		background: var(--bg-base, #1a1a1a);
		border-bottom: 1px solid var(--border-default, #333);
		flex-shrink: 0;
		height: 24px;
		padding-left: 8px;
	}

	.vp-tab {
		font-family: var(--font-ui, sans-serif);
		font-size: 11px;
		font-weight: 500;
		color: var(--text-tertiary, #666);
		background: transparent;
		border: none;
		border-bottom: 2px solid transparent;
		padding: 0 10px;
		cursor: pointer;
		transition: color 0.15s, border-color 0.15s;
	}

	.vp-tab:hover {
		color: var(--text-secondary, #999);
	}

	.vp-tab.active {
		color: var(--text-primary, #eee);
		border-bottom-color: var(--accent-yellow, #d4a017);
	}

	.tab-content {
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}

</style>
