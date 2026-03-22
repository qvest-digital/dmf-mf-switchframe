<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import LayoutPanel from './LayoutPanel.svelte';
	import GraphicsPanel from './GraphicsPanel.svelte';
	import KeyPanel from './KeyPanel.svelte';

	let { state, activeSubTab = $bindable('layout'), externalSelectedLayerId = null }: {
		state: ControlRoomState;
		activeSubTab?: 'layout' | 'graphics' | 'keys';
		externalSelectedLayerId?: number | null;
	} = $props();
</script>

<div class="composition-panel">
	<div class="tab-bar">
		<button
			class="vp-tab"
			class:active={activeSubTab === 'layout'}
			onclick={() => activeSubTab = 'layout'}
		>Layout</button>
		<button
			class="vp-tab"
			class:active={activeSubTab === 'graphics'}
			onclick={() => activeSubTab = 'graphics'}
		>Graphics</button>
		<button
			class="vp-tab"
			class:active={activeSubTab === 'keys'}
			onclick={() => activeSubTab = 'keys'}
		>Keys</button>
	</div>
	<div class="tab-content">
		{#if activeSubTab === 'layout'}
			<LayoutPanel {state} />
		{:else if activeSubTab === 'graphics'}
			<GraphicsPanel {state} {externalSelectedLayerId} />
		{:else}
			<KeyPanel {state} />
		{/if}
	</div>
</div>

<style>
	.composition-panel {
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
