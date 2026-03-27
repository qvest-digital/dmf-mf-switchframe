<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import AROcclusionTab from './AROcclusionTab.svelte';
	import ARSceneTab from './ARSceneTab.svelte';
	import ARTrackingTab from './ARTrackingTab.svelte';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	let activeSubTab = $state<'occlusion' | 'scene' | 'tracking'>('occlusion');

	const depthLatency = $derived(crState.arOcclusion?.depthLatencyMs ?? 0);
	const samLatency = $derived(crState.arOcclusion?.samLatencyMs ?? 0);
	const depthReady = $derived(crState.arOcclusion?.depthReady ?? false);
	const samReady = $derived(crState.arOcclusion?.samReady ?? false);
</script>

<div class="ar-panel">
	<div class="tab-bar">
		<button
			class="vp-tab"
			class:active={activeSubTab === 'occlusion'}
			onclick={() => activeSubTab = 'occlusion'}
		>Occlusion</button>
		<button
			class="vp-tab"
			class:active={activeSubTab === 'scene'}
			onclick={() => activeSubTab = 'scene'}
		>Scene</button>
		<button
			class="vp-tab"
			class:active={activeSubTab === 'tracking'}
			onclick={() => activeSubTab = 'tracking'}
		>Tracking</button>
		<div class="tab-status">
			{#if depthReady}
				<span class="status-badge status-ok">DEPTH {depthLatency.toFixed(1)}ms</span>
			{:else}
				<span class="status-badge status-off">DEPTH</span>
			{/if}
			{#if samReady}
				<span class="status-badge status-ok">SAM {samLatency.toFixed(1)}ms</span>
			{:else}
				<span class="status-badge status-off">SAM</span>
			{/if}
		</div>
	</div>
	<div class="tab-content">
		{#if activeSubTab === 'occlusion'}
			<AROcclusionTab state={crState} />
		{:else if activeSubTab === 'scene'}
			<ARSceneTab state={crState} />
		{:else}
			<ARTrackingTab state={crState} />
		{/if}
	</div>
</div>

<style>
	.ar-panel {
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
		align-items: center;
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
		height: 100%;
		display: flex;
		align-items: center;
	}

	.vp-tab:hover {
		color: var(--text-secondary, #999);
	}

	.vp-tab.active {
		color: var(--text-primary, #eee);
		border-bottom-color: var(--accent-yellow, #d4a017);
	}

	.tab-status {
		margin-left: auto;
		display: flex;
		gap: 6px;
		padding-right: 8px;
	}

	.status-badge {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		font-weight: 600;
		padding: 1px 5px;
		border-radius: 2px;
		letter-spacing: 0.03em;
	}

	.status-ok {
		background: rgba(34, 197, 94, 0.15);
		color: rgb(34, 197, 94);
		border: 1px solid rgba(34, 197, 94, 0.3);
	}

	.status-off {
		background: rgba(255, 255, 255, 0.04);
		color: var(--text-tertiary, #666);
		border: 1px solid rgba(255, 255, 255, 0.08);
	}

	.tab-content {
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}

</style>
